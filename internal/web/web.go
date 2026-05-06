// Package web embeds the SPA build output and exposes the
// fallback handler that serves index.html for any path the API
// router didn't match. The dist/ subdirectory is populated by the
// Dockerfile's frontend stage; in dev it may be empty (the SPA is
// served by Vite, not the Go binary).
package web

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// FS returns the embedded filesystem rooted at dist/. Callers MUST
// pass it through fs.Sub if they want to drop the leading "dist/"
// prefix.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: sub dist: %w", err)
	}
	return sub, nil
}

// Handler returns an http.Handler that serves files from the
// embedded dist/ directory. Requests for paths that don't match a
// file fall back to dist/index.html (the SPA shell). When dist/ is
// empty (the dev case where Vite owns the frontend), the handler
// returns 404.
func Handler() http.Handler {
	sub, err := FS()
	if err != nil {
		// fs.Sub on an embed.FS rooted at a static directory should
		// never fail in practice. Surface as a 500 to keep the
		// constructor non-fallible.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web assets unavailable", http.StatusInternalServerError)
		})
	}
	files := http.FS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := sub.Open(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "web read error", http.StatusInternalServerError)
				return
			}
			// SPA fallback: serve index.html for any unmatched path.
			idx, err := sub.Open("index.html")
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_ = idx.Close()
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/index.html"
			http.FileServer(files).ServeHTTP(w, r2)
			return
		}
		_ = f.Close()
		http.FileServer(files).ServeHTTP(w, r)
	})
}
