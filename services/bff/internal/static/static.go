// Package static serves the embedded SPA bundle. The committed dist
// directory is a placeholder; the container build overwrites it with
// the real Vite output before compiling, so the binary ships the app.
// Each behavior here has a CDN equivalent documented in the README's
// production-paths section (response-headers policy, 403/404 fallback
// mapping, immutable asset sync).
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

// Handler serves the SPA: content-hashed assets cache forever,
// index.html never caches (deploys take effect on the next
// navigation), and extensionless unknown paths fall back to the app
// shell for client-side routing.
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
			// Never serve a directory listing: it would leak the
			// bundle's file inventory (and the build hash). A CDN
			// answers 403 here; 404 is the in-cluster equivalent.
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

// serveIndex writes index.html directly (http.ServeFileFS would 301
// the literal index.html path to ./, which loops through this handler).
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
