package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/zvdy/pgao/src/analyzer"
	"github.com/zvdy/pgao/src/collector"
	"github.com/zvdy/pgao/src/db"
)

// fakePinger lets us drive ReadinessCheck without touching a real pool.
type fakePinger struct {
	clusters []string
	errs     map[string]error
}

func (f *fakePinger) GetAllClusters() []string                   { return f.clusters }
func (f *fakePinger) PingAll(_ context.Context) map[string]error { return f.errs }

func newTestHandler() (*Handler, *mux.Router) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	pool := db.NewConnectionPool(log)

	qa := analyzer.NewQueryAnalyzer()
	pa := analyzer.NewPerformanceAnalyzer()
	mc := collector.NewMetricsCollector(pool, log, 0)
	cc := collector.NewClusterCollector(pool, log, 0)

	h := NewHandler(pool, qa, pa, mc, cc, log)
	r := mux.NewRouter()
	h.RegisterRoutes(r)
	return h, r
}

func TestHealthCheckReturnsOK(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %+v", body)
	}
}

func TestReadinessReturns503WhenNoClusters(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when no clusters, got %d", w.Code)
	}
}

func TestReadinessReturns503WhenAllClustersDown(t *testing.T) {
	h, _ := newTestHandler()
	h.WithPinger(&fakePinger{
		clusters: []string{"c1", "c2"},
		errs: map[string]error{
			"c1": errors.New("connection refused"),
			"c2": errors.New("dial timeout"),
		},
	})
	r := mux.NewRouter()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when all clusters unhealthy, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Errorf("status mismatch: %+v", body)
	}
	if body["healthy"].(float64) != 0 {
		t.Errorf("healthy count mismatch: %+v", body)
	}
	clusters, _ := body["clusters"].(map[string]interface{})
	if !strings.Contains(clusters["c1"].(string), "connection refused") {
		t.Errorf("c1 status should surface the error: %+v", clusters)
	}
}

func TestReadinessReturns200WhenAtLeastOneClusterHealthy(t *testing.T) {
	h, _ := newTestHandler()
	h.WithPinger(&fakePinger{
		clusters: []string{"c1", "c2"},
		errs: map[string]error{
			"c1": nil,
			"c2": errors.New("boom"),
		},
	})
	r := mux.NewRouter()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when one cluster healthy, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["healthy"].(float64) != 1 {
		t.Errorf("expected healthy=1, got %+v", body["healthy"])
	}
	if body["status"] != "ready" {
		t.Errorf("expected status=ready, got %+v", body)
	}
}

func TestAnalyzeQueryRejectsEmptyBody(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty query, got %d", w.Code)
	}
}

func TestAnalyzeQueryRejectsMalformedJSON(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestAnalyzeQueryReturnsAnalysis(t *testing.T) {
	_, r := newTestHandler()

	body := map[string]string{"query": "SELECT id FROM users WHERE id = 1"}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["query_type"] != "SELECT" {
		t.Errorf("expected query_type=SELECT, got %v", got["query_type"])
	}
}

func TestGetClusterReturns404WhenMissing(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/does-not-exist", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestListClustersReturnsEmptyWhenNoneRegistered(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %+v", got)
	}
}

func TestMetricsEndpointServesPromText(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") && !strings.Contains(ct, "application/openmetrics-text") {
		t.Errorf("unexpected content-type: %s", ct)
	}
}
