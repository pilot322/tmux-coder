// Package webdashboard serves the dependency-free browser dashboard.
package webdashboard

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed index.html app.js styles.css
var assets embed.FS

// Handler returns an HTTP handler for the embedded dashboard. API routes are
// deliberately outside this handler: mount the management API at /api/.
func Handler() http.Handler {
	return http.HandlerFunc(serve)
}

func serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	requestPath := r.URL.Path
	if requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") {
		http.NotFound(w, r)
		return
	}

	name := ""
	switch requestPath {
	case "/", "/index.html":
		name = "index.html"
	case "/app.js":
		name = "app.js"
	case "/styles.css":
		name = "styles.css"
	default:
		if path.Ext(requestPath) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}

	body, err := assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	switch name {
	case "index.html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
	case "app.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
	case "styles.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}
