// Package static serves the embedded SPA bundle; the committed dist
// directory is a placeholder the container build overwrites with real Vite
// output before compiling. Each behavior has a CDN equivalent in docs/production-paths.md (SPA delivery).
package static

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the SPA: content-hashed assets cache forever, index.html
// never caches (deploys take effect next navigation), extensionless unknowns fall back to the app shell.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: "dist" is a compile-time embedded directory.
		panic(err)
	}
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "index.html" {
			serveIndex(w, sub)
			return
		}
		info, err := fs.Stat(sub, p)
		if err != nil {
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, sub) // client-side route
			return
		}
		if info.IsDir() {
			// Never serve a directory listing: it would leak the bundle's file
			// inventory and build hash. A CDN answers 403 here; 404 is the in-cluster equivalent.
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			// Vite output under assets/ is content-hashed.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// serveIndex writes index.html directly (http.ServeFileFS would 301 it to ./, looping through this handler).
func serveIndex(w http.ResponseWriter, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.Error(w, "bundle missing index.html", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
