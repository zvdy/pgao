// Package metrics — runtime instrumentation surfaces for pgao itself.
//
// The Exporter type in exporter.go publishes per-cluster gauges scraped
// from the latest collection sample. The types below publish *operational*
// metrics about pgao's own behaviour: how long collection rounds take,
// how often they fail, and how the HTTP API is being used.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
)

// CollectionInstrumentation wraps the counters/histograms collectors emit
// to report on their own behaviour. Created by NewCollectionInstrumentation
// and passed to MetricsCollector / ClusterCollector via WithInstrumentation.
type CollectionInstrumentation struct {
	errors   *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewCollectionInstrumentation builds the collection-side metrics and
// registers them with reg. Pass the same registry that the rest of pgao
// uses so /metrics serves them alongside the per-cluster gauges.
func NewCollectionInstrumentation(reg prometheus.Registerer) *CollectionInstrumentation {
	ci := &CollectionInstrumentation{
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgao_collection_errors_total",
			Help: "Total number of failed collection rounds, by cluster + collector kind.",
		}, []string{"cluster", "kind"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pgao_collection_duration_seconds",
			Help:    "Time spent on a single collection round, by cluster + collector kind.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"cluster", "kind"}),
	}
	if reg != nil {
		reg.MustRegister(ci.errors, ci.duration)
	}
	return ci
}

// ObserveCollection records one collection attempt. err is non-nil to
// flag the round as failed; nil means success. Call from each collector
// once per cluster per round.
func (ci *CollectionInstrumentation) ObserveCollection(cluster, kind string, took time.Duration, err error) {
	if ci == nil {
		return
	}
	ci.duration.WithLabelValues(cluster, kind).Observe(took.Seconds())
	if err != nil {
		ci.errors.WithLabelValues(cluster, kind).Inc()
	}
}

// HTTPInstrumentation wraps the request-counter + duration-histogram a
// gorilla/mux subrouter is wrapped with via Middleware().
type HTTPInstrumentation struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewHTTPInstrumentation builds the HTTP-side metrics and registers them.
func NewHTTPInstrumentation(reg prometheus.Registerer) *HTTPInstrumentation {
	hi := &HTTPInstrumentation{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgao_http_requests_total",
			Help: "Total HTTP requests handled, by status code, method, and matched route template.",
		}, []string{"code", "method", "route"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pgao_http_request_duration_seconds",
			Help:    "HTTP handler latency by matched route template.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
	}
	if reg != nil {
		reg.MustRegister(hi.requests, hi.duration)
	}
	return hi
}

// Middleware returns a gorilla/mux MiddlewareFunc that records request
// count + latency. Routes that are not matched (404) are labelled "unknown"
// so high-cardinality scanner traffic doesn't blow up the time series.
func (hi *HTTPInstrumentation) Middleware() mux.MiddlewareFunc {
	if hi == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			route := "unknown"
			if rt := mux.CurrentRoute(r); rt != nil {
				if tpl, err := rt.GetPathTemplate(); err == nil {
					route = tpl
				}
			}
			hi.requests.WithLabelValues(strconv.Itoa(sw.status), r.Method, route).Inc()
			hi.duration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		})
	}
}

// statusRecorder captures the HTTP status code written by the inner
// handler so the middleware can label requests by code.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}
