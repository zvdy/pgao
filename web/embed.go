// Package webui embeds the production build of pgao's web UI and serves
// it from the same listener as the HTTP API.
//
// The production bundle is written to web/dist by `make ui` (and by the
// release Dockerfile's node stage). The repository ships a minimal
// fallback web/dist/index.html so this package compiles even on a fresh
// clone before `make ui` runs — operators who never run `make ui` land
// on a "UI not built" page instead of a 500.
//
// Living inside web/ rather than src/ is a deliberate trade-off: //go:embed
// patterns cannot reach across directories with .. so the Go file has to
// sit next to the dist/ tree it embeds. The .ts/.tsx source coexists
// peacefully with this single .go file because Go's build only consumes
// *.go.
package webui

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the read-only filesystem rooted at the bundle.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// Handler returns an http.Handler that serves the embedded SPA. Unknown
// paths fall back to index.html so client-side routing (react-router)
// works on full reloads of /clusters/some-id deep links.
//
// Routes that exist in the bundle (index.html, /assets/*) are served
// verbatim. Paths that look like static asset requests but miss
// (e.g. /favicon.png) return 404 — they don't fall through to index,
// otherwise broken <img> requests would download the entire SPA.
func Handler() (http.Handler, error) {
	root, err := FS()
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))

		if clean != "/" {
			f, openErr := root.Open(strings.TrimPrefix(clean, "/"))
			if openErr == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
			if !errors.Is(openErr, fs.ErrNotExist) {
				http.Error(w, "ui filesystem error", http.StatusInternalServerError)
				return
			}
			if looksLikeAsset(clean) {
				http.NotFound(w, r)
				return
			}
		}

		serveIndex(w, root)
	}), nil
}

func serveIndex(w http.ResponseWriter, root fs.FS) {
	idx, err := root.Open("index.html")
	if err != nil {
		http.Error(w, "ui index missing", http.StatusInternalServerError)
		return
	}
	defer func() { _ = idx.Close() }()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html is short and uncacheable so the asset hashes inside
	// it stay fresh after a deploy. Assets themselves are content-hashed
	// so the file server's default caching behaviour is fine for them.
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := io.Copy(w, idx); err != nil {
		// Header already sent; nothing useful to do.
		return
	}
}

// looksLikeAsset returns true for paths that should never fall back to
// index.html. The check is conservative: anything with a typical static
// asset extension is treated as a hard 404 on miss.
func looksLikeAsset(p string) bool {
	dot := strings.LastIndex(p, ".")
	if dot == -1 || dot == len(p)-1 {
		return false
	}
	ext := strings.ToLower(p[dot+1:])
	switch ext {
	case "js", "mjs", "cjs", "css", "map", "json",
		"png", "jpg", "jpeg", "gif", "svg", "webp", "ico",
		"woff", "woff2", "ttf", "otf", "eot",
		"txt", "xml":
		return true
	}
	return false
}
