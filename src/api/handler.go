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

// RegisterRoutes registers all API routes
func (h *Handler) RegisterRoutes(r *mux.Router) {
	// Health check
	r.HandleFunc("/health", h.HealthCheck).Methods("GET")
	r.HandleFunc("/ready", h.ReadinessCheck).Methods("GET")

	// Prometheus metrics
	gatherer := h.promRegistry
	if gatherer == nil {
		gatherer = prometheus.DefaultGatherer
	}
	r.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{})).Methods("GET")

	// Cluster endpoints
	r.HandleFunc("/api/v1/clusters", h.ListClusters).Methods("GET")
	r.HandleFunc("/api/v1/clusters/{id}", h.GetCluster).Methods("GET")
	r.HandleFunc("/api/v1/clusters/{id}/metrics", h.GetClusterMetrics).Methods("GET")
	r.HandleFunc("/api/v1/clusters/{id}/health", h.GetClusterHealth).Methods("GET")

	// Query analysis endpoints
	r.HandleFunc("/api/v1/analyze", h.AnalyzeQuery).Methods("POST")
	r.HandleFunc("/api/v1/clusters/{id}/queries", h.GetSlowQueries).Methods("GET")

	// Metrics endpoints
	r.HandleFunc("/api/v1/clusters/{id}/tables", h.GetTableMetrics).Methods("GET")
	r.HandleFunc("/api/v1/clusters/{id}/alerts", h.GetAlerts).Methods("GET")
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
		h.respondError(w, http.StatusInternalServerError, err.Error())
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
		h.respondError(w, http.StatusInternalServerError, err.Error())
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
		h.respondError(w, http.StatusInternalServerError, err.Error())
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
		h.respondError(w, http.StatusInternalServerError, err.Error())
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
		h.respondError(w, http.StatusInternalServerError, err.Error())
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
		h.respondError(w, http.StatusInternalServerError, err.Error())
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

// respondError sends an error response
func (h *Handler) respondError(w http.ResponseWriter, statusCode int, message string) {
	response := map[string]string{
		"error": message,
	}
	h.respondJSON(w, statusCode, response)
}
