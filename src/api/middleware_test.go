package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func silentLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func TestRecovererConvertsPanicToGeneric500(t *testing.T) {
	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	h := recoverer(silentLogger())(panicking)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from panic recovery, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "boom") {
		t.Errorf("panic message leaked into response: %s", w.Body.String())
	}
}

func TestTimeoutSetsDeadline(t *testing.T) {
	slow := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// block until context deadline fires
		<-r.Context().Done()
	})
	h := timeout(20 * time.Millisecond)(slow)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analyze", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 from timeout handler, got %d", w.Code)
	}
}

func TestMaxBodyRejectsOversizedRequest(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := maxBody(10)(read)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader("way more than ten bytes"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestAPIKeyAuthNoOpWhenKeyEmpty(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := apiKeyAuth("")(ok)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("empty key should disable middleware, got %d", w.Code)
	}
}

func TestAPIKeyAuthRejectsMissingAndWrongToken(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := apiKeyAuth("sekret")(ok)

	cases := []struct {
		name       string
		mutate     func(*http.Request)
		wantStatus int
	}{
		{"no header", func(*http.Request) {}, http.StatusUnauthorized},
		{"wrong bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"right bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer sekret") }, http.StatusOK},
		{"right x-api-key", func(r *http.Request) { r.Header.Set("X-API-Key", "sekret") }, http.StatusOK},
		{"wrong x-api-key", func(r *http.Request) { r.Header.Set("X-API-Key", "wrong") }, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
			c.mutate(req)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != c.wantStatus {
				t.Errorf("got %d, want %d", w.Code, c.wantStatus)
			}
		})
	}
}

func TestRateLimitReturns429OnceBurstExceeded(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	// 1 rps, burst of 1 — second immediate request must be rejected.
	h := rateLimit(1, 1)(ok)

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request should pass the limiter, got %d", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be rate-limited, got %d", second.Code)
	}
}

func TestChainAppliesMiddlewareInOrder(t *testing.T) {
	var order []string
	mk := func(label string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "enter:"+label)
				next.ServeHTTP(w, r)
				order = append(order, "exit:"+label)
			})
		}
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "final")
		w.WriteHeader(http.StatusOK)
	})

	h := chain(final, mk("outer"), mk("inner"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	got := strings.Join(order, ",")
	want := "enter:outer,enter:inner,final,exit:inner,exit:outer"
	if got != want {
		t.Errorf("chain order wrong:\n got: %s\nwant: %s", got, want)
	}
}
