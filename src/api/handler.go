package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/analyzer"
	"github.com/zvdy/pgao/src/collector"
	"github.com/zvdy/pgao/src/db"
)

// clusterPinger is the minimal surface the readiness check needs. Kept as an
// unexported interface so handlers can be unit-tested with a fake while the
// real implementation stays *db.ConnectionPool.
type clusterPinger interface {
	GetAllClusters() []string
	PingAll(ctx context.Context) map[string]error
}

// Handler handles API requests
type Handler struct {
	pool                *db.ConnectionPool
	pinger              clusterPinger
	queryAnalyzer       *analyzer.QueryAnalyzer
	performanceAnalyzer *analyzer.PerformanceAnalyzer
	metricsCollector    *collector.MetricsCollector
	clusterCollector    *collector.ClusterCollector
	log                 *logrus.Logger
	promRegistry        prometheus.Gatherer
	sec                 SecurityOptions
	httpInstr           HTTPInstrumentationProvider
}

// SecurityOptions controls the middleware applied to /api/v1/*. Zero value
// is safe: no auth, no rate limit, 1 MiB body cap, 5s request timeout.
// Non-zero fields override the defaults.
type SecurityOptions struct {
	APIKey         string
	RequestTimeout time.Duration
	MaxBodyBytes   int64
	RateLimitRPS   float64
	RateLimitBurst int
}

// readinessPingTimeout caps how long the readiness handler will wait on any
// single cluster. Kubernetes probes have their own deadline; we want to answer
// quickly with per-cluster errors rather than stall the whole probe.
const readinessPingTimeout = 2 * time.Second

// NewHandler creates a new API handler
func NewHandler(
	pool *db.ConnectionPool,
	queryAnalyzer *analyzer.QueryAnalyzer,
	performanceAnalyzer *analyzer.PerformanceAnalyzer,
	metricsCollector *collector.MetricsCollector,
	clusterCollector *collector.ClusterCollector,
	log *logrus.Logger,
) *Handler {
	return &Handler{
		pool:                pool,
		pinger:              pool,
		queryAnalyzer:       queryAnalyzer,
		performanceAnalyzer: performanceAnalyzer,
		metricsCollector:    metricsCollector,
		clusterCollector:    clusterCollector,
		log:                 log,
	}
}

// WithPinger overrides the readiness pinger. Used by tests to inject a fake
// so handler behaviour can be asserted without a real PostgreSQL cluster.
func (h *Handler) WithPinger(p clusterPinger) *Handler {
	h.pinger = p
	return h
}

// WithPromRegistry attaches a Prometheus gatherer; when set, /metrics is
// registered. Defaults to the global registry if nil.
func (h *Handler) WithPromRegistry(g prometheus.Gatherer) *Handler {
	h.promRegistry = g
	return h
}

// WithSecurity configures the middleware applied to /api/v1/*. Must be
// called before RegisterRoutes; later calls have no effect.
func (h *Handler) WithSecurity(opts SecurityOptions) *Handler {
	h.sec = opts
	return h
}

// HTTPInstrumentationProvider supplies a mux middleware that records HTTP
// request count + latency. metrics.HTTPInstrumentation satisfies it.
type HTTPInstrumentationProvider interface {
	Middleware() mux.MiddlewareFunc
}

// WithHTTPInstrumentation wires per-request metrics. Must be called before
// RegisterRoutes; later calls have no effect.
func (h *Handler) WithHTTPInstrumentation(p HTTPInstrumentationProvider) *Handler {
	h.httpInstr = p
	return h
}

// RegisterRoutes registers all API routes. /health, /ready, and /metrics are
// never wrapped in auth/timeout/rate-limit middleware so k8s probes and
// Prometheus scrapes keep working even if the caller has no API key.
func (h *Handler) RegisterRoutes(r *mux.Router) {
	// Health + probes: recoverer only, so a panic doesn't leak the pod.
	rec := recoverer(h.log)
	reqLog := requestLogger(h.log)
	r.Handle("/health", chain(http.HandlerFunc(h.HealthCheck), rec, reqLog)).Methods("GET")
	r.Handle("/ready", chain(http.HandlerFunc(h.ReadinessCheck), rec, reqLog)).Methods("GET")

	// Prometheus metrics
	gatherer := h.promRegistry
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}
	r.Handle("/metrics", chain(
		promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}),
		rec, reqLog,
	)).Methods("GET")

	// /api/v1/* shares a subrouter so the middleware chain is applied once.
	reqTimeout := h.sec.RequestTimeout
	if reqTimeout <= 0 {
		reqTimeout = 5 * time.Second
	}
	maxBytes := h.sec.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}

	api := r.PathPrefix("/api/v1").Subrouter()
	mws := []mux.MiddlewareFunc{
		muxMiddleware(rec),
		muxMiddleware(reqLog),
	}
	if h.httpInstr != nil {
		mws = append(mws, h.httpInstr.Middleware())
	}
	mws = append(mws,
		muxMiddleware(rateLimit(h.sec.RateLimitRPS, h.sec.RateLimitBurst)),
		muxMiddleware(timeout(reqTimeout)),
		muxMiddleware(maxBody(maxBytes)),
		muxMiddleware(apiKeyAuth(h.sec.APIKey)),
	)
	api.Use(mws...)
	api.HandleFunc("/clusters", h.ListClusters).Methods("GET")
	api.HandleFunc("/clusters/{id}", h.GetCluster).Methods("GET")
	api.HandleFunc("/clusters/{id}/metrics", h.GetClusterMetrics).Methods("GET")
	api.HandleFunc("/clusters/{id}/health", h.GetClusterHealth).Methods("GET")
	api.HandleFunc("/analyze", h.AnalyzeQuery).Methods("POST")
	api.HandleFunc("/clusters/{id}/queries", h.GetSlowQueries).Methods("GET")
	api.HandleFunc("/clusters/{id}/tables", h.GetTableMetrics).Methods("GET")
	api.HandleFunc("/clusters/{id}/alerts", h.GetAlerts).Methods("GET")
}

// muxMiddleware adapts a plain http middleware to the mux.MiddlewareFunc
// signature. Both are `func(http.Handler) http.Handler` — the adapter only
// exists to keep the type assertion local.
func muxMiddleware(mw func(http.Handler) http.Handler) mux.MiddlewareFunc {
	return mw
}

// HealthCheck returns the health status
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"status": "ok",
	}
	h.respondJSON(w, http.StatusOK, response)
}

// ReadinessCheck reports ready only when at least one registered cluster
// responds to a ping. Per-cluster status is included so operators can see
// which clusters are down without tailing logs.
func (h *Handler) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	clusters := h.pinger.GetAllClusters()
	if len(clusters) == 0 {
		h.respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":   "not_ready",
			"reason":   "no clusters registered",
			"clusters": map[string]string{},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), readinessPingTimeout)
	defer cancel()

	results := h.pinger.PingAll(ctx)

	perCluster := make(map[string]string, len(results))
	healthy := 0
	for _, id := range clusters {
		if err, ok := results[id]; ok && err != nil {
			perCluster[id] = "unhealthy: " + err.Error()
			continue
		}
		perCluster[id] = "ok"
		healthy++
	}

	status := "ready"
	code := http.StatusOK
	if healthy == 0 {
		status = "not_ready"
		code = http.StatusServiceUnavailable
	}

	h.respondJSON(w, code, map[string]interface{}{
		"status":   status,
		"healthy":  healthy,
		"total":    len(clusters),
		"clusters": perCluster,
	})
}

// ListClusters returns list of all clusters
func (h *Handler) ListClusters(w http.ResponseWriter, r *http.Request) {
	clusters := h.clusterCollector.GetAllClusters()
	h.respondJSON(w, http.StatusOK, clusters)
}

// GetCluster returns information about a specific cluster
func (h *Handler) GetCluster(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	cluster, err := h.clusterCollector.GetCluster(clusterID)
	if err != nil {
		h.respondError(w, http.StatusNotFound, "Cluster not found")
		return
	}

	h.respondJSON(w, http.StatusOK, cluster)
}

// GetClusterMetrics returns metrics for a specific cluster
func (h *Handler) GetClusterMetrics(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	metrics, err := h.metricsCollector.GetMetricsSnapshot(r.Context(), clusterID)
	if err != nil {
		h.respondInternal(w, r, "GetClusterMetrics", err)
		return
	}

	h.respondJSON(w, http.StatusOK, metrics)
}

// GetClusterHealth returns health status for a cluster
func (h *Handler) GetClusterHealth(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	metrics, err := h.metricsCollector.GetMetricsSnapshot(r.Context(), clusterID)
	if err != nil {
		h.respondInternal(w, r, "GetClusterHealth", err)
		return
	}

	alerts := h.performanceAnalyzer.AnalyzeMetrics(metrics)
	health := h.performanceAnalyzer.GenerateHealthStatus(clusterID, metrics, alerts)

	h.respondJSON(w, http.StatusOK, health)
}

// AnalyzeQueryRequest represents a query analysis request
type AnalyzeQueryRequest struct {
	Query string `json:"query"`
}

// AnalyzeQuery analyzes a SQL query
func (h *Handler) AnalyzeQuery(w http.ResponseWriter, r *http.Request) {
	var req AnalyzeQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Query == "" {
		h.respondError(w, http.StatusBadRequest, "Query is required")
		return
	}

	analysis, err := h.queryAnalyzer.Analyze(req.Query)
	if err != nil {
		h.respondInternal(w, r, "AnalyzeQuery", err)
		return
	}

	h.respondJSON(w, http.StatusOK, analysis)
}

// GetSlowQueries returns slow queries from pg_stat_statements.
// Query params: ?limit (1-500, default 100), ?order_by (mean_exec_time |
// total_exec_time | calls), ?database (filter label only for now).
// Returns 412 Precondition Failed when pg_stat_statements is not installed.
func (h *Handler) GetSlowQueries(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	opts := collector.SlowQueryOptions{
		Limit:    parseLimit(r.URL.Query().Get("limit")),
		OrderBy:  r.URL.Query().Get("order_by"),
		Database: r.URL.Query().Get("database"),
	}

	queries, err := h.metricsCollector.CollectQueryMetrics(r.Context(), clusterID, opts)
	if err != nil {
		if errors.Is(err, collector.ErrPgStatStatementsMissing) {
			h.respondError(w, http.StatusPreconditionFailed,
				"pg_stat_statements extension is not installed on this cluster; run CREATE EXTENSION pg_stat_statements")
			return
		}
		h.respondInternal(w, r, "GetSlowQueries", err)
		return
	}

	h.respondJSON(w, http.StatusOK, queries)
}

// GetTableMetrics returns table metrics from pg_stat_user_tables.
// Query params: ?limit (1-500, default 100), ?database (filter label only).
func (h *Handler) GetTableMetrics(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	opts := collector.TableMetricsOptions{
		Limit:    parseLimit(r.URL.Query().Get("limit")),
		Database: r.URL.Query().Get("database"),
	}

	tableMetrics, err := h.metricsCollector.CollectTableMetrics(r.Context(), clusterID, opts)
	if err != nil {
		h.respondInternal(w, r, "GetTableMetrics", err)
		return
	}

	h.respondJSON(w, http.StatusOK, tableMetrics)
}

// parseLimit parses a user-provided ?limit value. Returns 0 when the value
// is missing or invalid so the collector applies its default.
func parseLimit(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// GetAlerts returns active alerts for a cluster
func (h *Handler) GetAlerts(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	metrics, err := h.metricsCollector.GetMetricsSnapshot(r.Context(), clusterID)
	if err != nil {
		h.respondInternal(w, r, "GetAlerts", err)
		return
	}

	alerts := h.performanceAnalyzer.AnalyzeMetrics(metrics)
	h.respondJSON(w, http.StatusOK, alerts)
}

// respondJSON sends a JSON response
func (h *Handler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.Errorf("Failed to encode JSON response: %v", err)
	}
}

// respondError sends an error response with a caller-supplied safe message.
func (h *Handler) respondError(w http.ResponseWriter, statusCode int, message string) {
	response := map[string]string{
		"error": message,
	}
	h.respondJSON(w, statusCode, response)
}

// respondInternal logs the underlying error with context and returns a
// generic "internal server error" to the client. Use for errors the caller
// has no business seeing (query failures, connection errors, etc).
func (h *Handler) respondInternal(w http.ResponseWriter, r *http.Request, op string, err error) {
	h.log.WithError(err).WithFields(logrus.Fields{
		"op":   op,
		"path": r.URL.Path,
	}).Error("handler error")
	h.respondError(w, http.StatusInternalServerError, "internal server error")
}
