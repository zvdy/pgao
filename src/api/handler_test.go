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
	return newTestHandlerWithSecurity(SecurityOptions{})
}

func newTestHandlerWithSecurity(sec SecurityOptions) (*Handler, *mux.Router) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	pool := db.NewConnectionPool(log)

	qa := analyzer.NewQueryAnalyzer()
	pa := analyzer.NewPerformanceAnalyzer()
	mc := collector.NewMetricsCollector(pool, log, 0)
	cc := collector.NewClusterCollector(pool, log, 0)

	h := NewHandler(pool, qa, pa, mc, cc, log).WithSecurity(sec)
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

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"100", 100},
		{"0", 0},
		{"abc", 0},
		{"-5", 0},
		{"10000", 10000}, // caller applies upper cap
	}
	for _, c := range cases {
		if got := parseLimit(c.in); got != c.want {
			t.Errorf("parseLimit(%q)=%d, want %d", c.in, got, c.want)
		}
	}
}

func TestGetSlowQueriesReturns500WhenClusterUnknown(t *testing.T) {
	// With no registered cluster the collector surfaces "cluster not found",
	// which is not a precondition failure — we want 500, not 412, so operators
	// can distinguish missing-cluster from missing-extension.
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/unknown/queries", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for unknown cluster, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestGetTableMetricsReturns500WhenClusterUnknown(t *testing.T) {
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/unknown/tables?limit=50", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for unknown cluster, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestAuthEnabledRejectsApiV1WithoutToken(t *testing.T) {
	_, r := newTestHandlerWithSecurity(SecurityOptions{APIKey: "sekret"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", w.Code)
	}
}

func TestAuthEnabledLetsThroughWithBearerToken(t *testing.T) {
	_, r := newTestHandlerWithSecurity(SecurityOptions{APIKey: "sekret"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with valid token, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestAuthDoesNotGateHealthReadyOrMetrics(t *testing.T) {
	_, r := newTestHandlerWithSecurity(SecurityOptions{APIKey: "sekret"})

	for _, path := range []string{"/health", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusUnauthorized {
			t.Errorf("%s must stay un-authenticated, got 401", path)
		}
	}
}

func TestAnalyzeQueryRejectsOversizedBody(t *testing.T) {
	_, r := newTestHandlerWithSecurity(SecurityOptions{MaxBodyBytes: 16})

	oversized := strings.Repeat("A", 1024)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// JSON decoder returns 400 once it tries to read past the limit, plus
	// MaxBytesReader may set 413 directly. Both mean the body was rejected.
	if w.Code != http.StatusRequestEntityTooLarge && w.Code != http.StatusBadRequest {
		t.Errorf("expected 413 or 400 for oversized body, got %d", w.Code)
	}
}

func TestInternalErrorsDoNotLeakRawDetails(t *testing.T) {
	// /api/v1/clusters/nonexistent/metrics triggers an internal error (no
	// cluster registered). We want "internal server error", NOT the raw
	// collector error.
	_, r := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/nonexistent/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "internal server error") {
		t.Errorf("expected generic error, got %s", body)
	}
	if strings.Contains(body, "failed to collect") || strings.Contains(body, "connection") {
		t.Errorf("response leaked internal error detail: %s", body)
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
