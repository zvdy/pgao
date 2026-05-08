package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAtRoot(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html, got %q", ct)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "<html") {
		t.Errorf("response body did not look like HTML: %s", body)
	}
}

func TestHandlerSPAFallback(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	// /clusters/foo is not in the bundle but should serve the SPA so
	// client-side routing works on full reloads.
	req := httptest.NewRequest(http.MethodGet, "/clusters/foo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("expected index.html body for SPA fallback")
	}
}

func TestHandler404sMissingAssets(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	for _, p := range []string{"/missing.js", "/assets/nope.css", "/favicon.png"} {
		t.Run(p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound {
				t.Errorf("expected 404 for %s, got %d", p, w.Code)
			}
		})
	}
}

func TestLooksLikeAsset(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/assets/index.js", true},
		{"/foo.css", true},
		{"/favicon.png", true},
		{"/clusters/abc", false},
		{"/", false},
		{"/no-ext-trailing.", false},
	}
	for _, c := range cases {
		if got := looksLikeAsset(c.path); got != c.want {
			t.Errorf("looksLikeAsset(%q)=%v, want %v", c.path, got, c.want)
		}
	}
}
