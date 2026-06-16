// Package httpapi is the HTTP adapter: it translates requests into usecase
// calls and usecase results into JSON responses. The DTOs here keep the wire
// format out of the usecase layer.
package httpapi

import "time"

type createProjectRequest struct {
	FullPath               string  `json:"fullPath"`
	Title                  *string `json:"title"`
	CreateWorktreeSessions *bool   `json:"createWorktreeSessions,omitempty"`
}

type worktreeRef struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

type projectResponse struct {
	ID                  int    `json:"id"`
	Title               string `json:"title"`
	FullPath            string `json:"fullPath"`
	MainSessionName     string `json:"mainSessionName"`
	MainTmuxSessionName string `json:"mainTmuxSessionName"`
}

type projectsResponse struct {
	Projects []projectResponse `json:"projects"`
}

type createSessionRequest struct {
	ProjectID                int    `json:"projectId"`
	Type                     string `json:"type"`
	Branch                   string `json:"branch"`
	CreateWorktree           bool   `json:"createWorktree"`
	CreateBranch             bool   `json:"createBranch"`
	BaseBranch               string `json:"baseBranch"`
	ParentSessionID          int    `json:"parentSessionId"`
	RelativeWorkingDirectory string `json:"relativeWorkingDirectory"`
	PreferredName            string `json:"preferredName"`
	OnDelete                 string `json:"onDelete"`
}

type sessionResponse struct {
	ID                       int             `json:"id"`
	Parent                   int             `json:"parent"`
	ParentSessionID          int             `json:"parentSessionId,omitempty"`
	ProjectID                int             `json:"projectId"`
	Name                     string          `json:"name"`
	SessionName              string          `json:"sessionName"`
	TmuxName                 string          `json:"tmuxSessionName"`
	Type                     string          `json:"type"`
	Branch                   string          `json:"branch,omitempty"`
	Worktree                 string          `json:"worktreePath,omitempty"`
	RelativeWorkingDirectory string          `json:"relativeWorkingDirectory,omitempty"`
	OnDelete                 string          `json:"onDelete,omitempty"`
	Project                  projectResponse `json:"project"`
}

type sessionsResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

type errorResponse struct {
	Error     string        `json:"error"`
	Code      string        `json:"code,omitempty"`
	Worktrees []worktreeRef `json:"worktrees,omitempty"`
}

type acquirePortRequest struct {
	Key       string `json:"key"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	HookToken string `json:"hookToken,omitempty"`
	ProjectID int    `json:"projectId,omitempty"`
	SessionID int    `json:"sessionId,omitempty"`
}

type acquirePortResponse struct {
	Port int `json:"port"`
}

type createAgentRequest struct {
	ProjectID   int     `json:"projectId"`
	SessionID   int     `json:"sessionId"`
	Kind        string  `json:"kind"`
	DisplayName *string `json:"displayName"`
	TmuxPaneID  *string `json:"tmuxPaneId"`
}

type updateAgentRequest struct {
	DisplayName *string `json:"displayName"`
}

type agentResponse struct {
	ID                  int             `json:"id"`
	ProjectID           int             `json:"projectId"`
	SessionID           int             `json:"sessionId"`
	Kind                string          `json:"kind"`
	DisplayName         string          `json:"displayName"`
	TmuxPaneID          string          `json:"tmuxPaneId"`
	PaneOwned           bool            `json:"paneOwned"`
	Status              string          `json:"status"`
	StatusChangedAt     time.Time       `json:"statusChangedAt"`
	ChildProcessGroupID int             `json:"childProcessGroupId,omitempty"`
	Project             projectResponse `json:"project"`
	Session             sessionResponse `json:"session"`
}

type agentsResponse struct {
	Agents []agentResponse `json:"agents"`
}

type agentEventRequest struct {
	Event               string `json:"event"`
	ChildProcessGroupID *int   `json:"childProcessGroupId,omitempty"`
}
