package api

import (
	"bytes"
	"encoding/json"
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
