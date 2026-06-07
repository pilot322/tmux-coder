package httpapi

import "net/http"

// NewRouter wires the project routes using the stdlib ServeMux method+wildcard
// patterns introduced in Go 1.22.
func NewRouter(pc *ProjectController) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /projects", pc.Create)
	mux.HandleFunc("GET /projects", pc.List)
	mux.HandleFunc("DELETE /projects/{id}", pc.Delete)
	return mux
}
