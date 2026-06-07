// Package httpapi is the HTTP adapter: it translates requests into usecase
// calls and usecase results into JSON responses. The DTOs here keep the wire
// format out of the usecase layer.
package httpapi

type createProjectRequest struct {
	FullPath string `json:"fullPath"`
}

type projectResponse struct {
	ID              int    `json:"id"`
	FullPath        string `json:"fullPath"`
	MainSessionName string `json:"mainSessionName"`
}

type projectsResponse struct {
	Projects []projectResponse `json:"projects"`
}

type errorResponse struct {
	Error string `json:"error"`
}
