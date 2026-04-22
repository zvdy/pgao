package api

import (
	"crypto/subtle"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// recoverer catches panics from downstream handlers and converts them into a
// generic 500. The real stack trace goes to the log so operators have enough
// to debug without leaking internals to the caller.
func recoverer(log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.WithFields(logrus.Fields{
						"path":  r.URL.Path,
						"panic": rec,
						"stack": string(debug.Stack()),
					}).Error("panic in handler")
					respondGeneric(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger records method, path, status, and duration once the handler
// returns. Kept deliberately small — anything richer belongs in a tracer.
func requestLogger(log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.WithFields(logrus.Fields{
				"method":   r.Method,
				"path":     r.URL.Path,
				"status":   sw.status,
				"duration": time.Since(start).String(),
			}).Info("http request")
		})
	}
}

// timeout attaches a per-request context deadline. Handlers that honour
// r.Context() inherit the cap automatically; slow Postgres queries downstream
// will cancel at d.
func timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, `{"error":"request timeout"}`)
	}
}

// maxBody wraps r.Body in http.MaxBytesReader so a handler's json.Decode
// fails fast on oversized bodies. http.MaxBytesReader also sets status 413
// automatically if the handler writes the error.
func maxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n > 0 && r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// apiKeyAuth gates /api/v1/* on a bearer token or X-API-Key header. When
// key is empty the middleware is a no-op so dev environments keep working.
// /health, /ready, /metrics are excluded by the caller, not here — this
// middleware runs on the authenticated subtree only.
func apiKeyAuth(key string) func(http.Handler) http.Handler {
	if key == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	want := []byte(key)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !constantTimeEqualToken(r, want) {
				respondGeneric(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// constantTimeEqualToken checks the Authorization: Bearer <key> header and
// the X-API-Key header in constant time.
func constantTimeEqualToken(r *http.Request, want []byte) bool {
	if got := r.Header.Get("X-API-Key"); got != "" {
		return subtle.ConstantTimeCompare([]byte(got), want) == 1
	}
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(auth[len(prefix):]), want) == 1
}

// rateLimit applies a process-wide token bucket. Intentionally global rather
// than per-client: pgao sits behind an authenticated boundary and is meant
// to protect Postgres, not to enforce quotas.
func rateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	if rps <= 0 || burst <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	lim := rate.NewLimiter(rate.Limit(rps), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !lim.Allow() {
				respondGeneric(w, http.StatusTooManyRequests, "rate limited")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// chain composes middleware in the order passed; the first middleware is the
// outermost wrapper so its defers run last.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// statusWriter captures the response status so the logger can record it.
// http.ResponseWriter does not surface the status itself.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

// respondGeneric is used by middleware that can't call the Handler helpers
// (those live on *Handler). Kept minimal; no logger wiring.
func respondGeneric(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}
