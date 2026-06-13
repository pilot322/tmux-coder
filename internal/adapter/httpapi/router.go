package httpapi

import "net/http"

// NewRouter wires the API routes using the stdlib ServeMux method+wildcard
// patterns introduced in Go 1.22.
func NewRouter(pc *ProjectController, sc *SessionController) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /projects", pc.Create)
	mux.HandleFunc("GET /projects", pc.List)
	mux.HandleFunc("DELETE /projects/{id}", pc.Delete)
	mux.HandleFunc("GET /sessions", sc.List)
	mux.HandleFunc("POST /sessions", sc.Create)
	mux.HandleFunc("DELETE /sessions/{id}", sc.Delete)
	return mux
}
