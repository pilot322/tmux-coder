// Package httpapi is the HTTP adapter: it translates requests into usecase
// calls and usecase results into JSON responses. The DTOs here keep the wire
// format out of the usecase layer.
package httpapi

type createProjectRequest struct {
	FullPath string  `json:"fullPath"`
	Title    *string `json:"title"`
}

type projectResponse struct {
	ID              int    `json:"id"`
	Title           string `json:"title"`
	FullPath        string `json:"fullPath"`
	MainSessionName string `json:"mainSessionName"`
}

type projectsResponse struct {
	Projects []projectResponse `json:"projects"`
}

type createSessionRequest struct {
	ProjectID  int    `json:"projectId"`
	Type       string `json:"type"`
	Branch     string `json:"branch"`
	Create     bool   `json:"create"`
	BaseBranch string `json:"baseBranch"`
}

type sessionResponse struct {
	ID          int             `json:"id"`
	Parent      int             `json:"parent"`
	ProjectID   int             `json:"projectId"`
	Name        string          `json:"name"`
	SessionName string          `json:"sessionName"`
	Type        string          `json:"type"`
	Branch      string          `json:"branch,omitempty"`
	Worktree    string          `json:"worktreePath,omitempty"`
	Project     projectResponse `json:"project"`
}

type sessionsResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

type errorResponse struct {
	Error string `json:"error"`
}
