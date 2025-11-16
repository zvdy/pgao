package api

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/analyzer"
	"github.com/zvdy/pgao/src/collector"
	"github.com/zvdy/pgao/src/db"
	"github.com/zvdy/pgao/src/models"
)

// Handler handles API requests
type Handler struct {
	pool                *db.ConnectionPool
	queryAnalyzer       *analyzer.QueryAnalyzer
	performanceAnalyzer *analyzer.PerformanceAnalyzer
	metricsCollector    *collector.MetricsCollector
	clusterCollector    *collector.ClusterCollector
	log                 *logrus.Logger
}

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
		queryAnalyzer:       queryAnalyzer,
		performanceAnalyzer: performanceAnalyzer,
		metricsCollector:    metricsCollector,
		clusterCollector:    clusterCollector,
		log:                 log,
	}
}

// RegisterRoutes registers all API routes
func (h *Handler) RegisterRoutes(r *mux.Router) {
	// Health check
	r.HandleFunc("/health", h.HealthCheck).Methods("GET")
	r.HandleFunc("/ready", h.ReadinessCheck).Methods("GET")

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

// ReadinessCheck checks if the service is ready
func (h *Handler) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	clusters := h.pool.GetAllClusters()

	ready := len(clusters) > 0
	status := "ready"
	if !ready {
		status = "not_ready"
	}

	response := map[string]interface{}{
		"status":   status,
		"clusters": len(clusters),
	}

	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}

	h.respondJSON(w, statusCode, response)
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

// GetSlowQueries returns slow queries for a cluster
func (h *Handler) GetSlowQueries(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	// This would typically query the database for slow query logs
	_ = clusterID

	slowQueries := make([]*models.SlowQuery, 0)
	h.respondJSON(w, http.StatusOK, slowQueries)
}

// GetTableMetrics returns table metrics for a cluster
func (h *Handler) GetTableMetrics(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterID := vars["id"]

	tableMetrics, err := h.metricsCollector.CollectTableMetrics(r.Context(), clusterID, "")
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.respondJSON(w, http.StatusOK, tableMetrics)
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
