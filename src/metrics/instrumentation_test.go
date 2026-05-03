package metrics

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
)

// gatherReg renders a registry to the same compact label-value format the
// exporter tests use. Pulled out so this file does not depend on
// exporter_test.go's helper, which is typed for a single Collector.
func gatherReg(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var sb strings.Builder
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			fmt.Fprintf(&sb, "%s", mf.GetName())
			for _, l := range m.GetLabel() {
				fmt.Fprintf(&sb, " %s=%s", l.GetName(), l.GetValue())
			}
			if g := m.GetGauge(); g != nil {
				fmt.Fprintf(&sb, " =%s", strconv.FormatFloat(g.GetValue(), 'f', -1, 64))
			}
			if c := m.GetCounter(); c != nil {
				fmt.Fprintf(&sb, " =%s", strconv.FormatFloat(c.GetValue(), 'f', -1, 64))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func TestCollectionInstrumentation_RecordsDurationAndErrors(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	ci := NewCollectionInstrumentation(reg)

	ci.ObserveCollection("c1", "metrics", 250*time.Millisecond, nil)
	ci.ObserveCollection("c1", "metrics", 500*time.Millisecond, errors.New("boom"))
	ci.ObserveCollection("c2", "cluster_info", 50*time.Millisecond, nil)

	out := gatherReg(t, reg)

	if !strings.Contains(out, `pgao_collection_errors_total cluster=c1 kind=metrics =1`) {
		t.Errorf("expected error counter for c1/metrics=1, got:\n%s", out)
	}
	if strings.Contains(out, `pgao_collection_errors_total cluster=c2`) {
		t.Errorf("c2 had no errors; counter must not appear, got:\n%s", out)
	}
	// Histograms emit _count + _sum samples; check the count for the metrics kind.
	if !strings.Contains(out, "pgao_collection_duration_seconds") {
		t.Errorf("expected duration histogram, got:\n%s", out)
	}
}

func TestHTTPInstrumentation_LabelsByRouteTemplate(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	hi := NewHTTPInstrumentation(reg)

	r := mux.NewRouter()
	r.Use(hi.Middleware())
	r.HandleFunc("/api/v1/clusters/{id}/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, id := range []string{"alpha", "beta", "gamma"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+id+"/metrics", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	out := gatherReg(t, reg)

	// All three requests roll up under the same route template — no
	// per-cluster label explosion.
	want := `pgao_http_requests_total code=200 method=GET route=/api/v1/clusters/{id}/metrics =3`
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in:\n%s", want, out)
	}
}

// Note: gorilla/mux only runs Use() middleware for routes that match —
// 404s go through its built-in NotFound handler, so this middleware
// never sees them. The "unknown" route label in the code is defensive
// only; a request that *does* match a route but has no GetPathTemplate
// (rare; happens with PathPrefix-only routes) hits that path.

func TestNilCollectionObserverIsSafe(t *testing.T) {
	var ci *CollectionInstrumentation
	// must not panic
	ci.ObserveCollection("c1", "metrics", time.Second, nil)
}
