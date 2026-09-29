// Package web serves the warmbox dashboard (a Vite + React + Tailwind SPA)
// from the daemon binary. The built assets live in ./dist and are embedded at
// compile time, so `warmbox daemon` serves a UI with no extra files to ship.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the built SPA filesystem (the contents of web/dist).
func Assets() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}

// Handler serves the SPA: hashed assets get long-lived caching, index.html is
// revalidated, and unknown paths fall back to index.html so client-side routes
// resolve on a hard refresh.
func Handler() http.Handler {
	sub, err := Assets()
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// SPA fallback: let the client router handle it.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, sub, "index.html")
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
