package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Project struct {
	ID                  int    `json:"id"`
	Title               string `json:"title"`
	FullPath            string `json:"fullPath"`
	MainSessionName     string `json:"mainSessionName"`
	MainTmuxSessionName string `json:"mainTmuxSessionName"`
}

type Session struct {
	ID                       int     `json:"id"`
	Parent                   int     `json:"parent"`
	ParentSessionID          int     `json:"parentSessionId,omitempty"`
	ProjectID                int     `json:"projectId"`
	Name                     string  `json:"name"`
	SessionName              string  `json:"sessionName"`
	TmuxName                 string  `json:"tmuxSessionName"`
	Type                     string  `json:"type"`
	Branch                   string  `json:"branch,omitempty"`
	Worktree                 string  `json:"worktreePath,omitempty"`
	RelativeWorkingDirectory string  `json:"relativeWorkingDirectory,omitempty"`
	OnDelete                 string  `json:"onDelete,omitempty"`
	Project                  Project `json:"project"`
}

type Agent struct {
	ID                  int     `json:"id"`
	ProjectID           int     `json:"projectId"`
	SessionID           int     `json:"sessionId"`
	Kind                string  `json:"kind"`
	DisplayName         string  `json:"displayName"`
	TmuxPaneID          string  `json:"tmuxPaneId"`
	PaneOwned           bool    `json:"paneOwned"`
	Status              string  `json:"status"`
	ChildProcessGroupID int     `json:"childProcessGroupId,omitempty"`
	Project             Project `json:"project"`
	Session             Session `json:"session"`
}

type CreateAgentInput struct {
	ProjectID   int     `json:"projectId"`
	SessionID   int     `json:"sessionId"`
	Kind        string  `json:"kind"`
	DisplayName *string `json:"displayName,omitempty"`
	TmuxPaneID  *string `json:"tmuxPaneId,omitempty"`
}

type ListAgentsInput struct {
	ProjectID *int
	SessionID *int
}

type CreateSessionInput struct {
	ProjectID                int    `json:"projectId,omitempty"`
	Type                     string `json:"type"`
	Branch                   string `json:"branch,omitempty"`
	CreateWorktree           bool   `json:"createWorktree,omitempty"`
	CreateBranch             bool   `json:"createBranch,omitempty"`
	BaseBranch               string `json:"baseBranch,omitempty"`
	ParentSessionID          int    `json:"parentSessionId,omitempty"`
	RelativeWorkingDirectory string `json:"relativeWorkingDirectory,omitempty"`
	PreferredName            string `json:"preferredName,omitempty"`
	OnDelete                 string `json:"onDelete,omitempty"`
}

type ListSessionsInput struct {
	Type      string
	ProjectID *int
}

type AcquirePortInput struct {
	Key       string `json:"key"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	HookToken string `json:"hookToken,omitempty"`
	ProjectID int    `json:"projectId,omitempty"`
	SessionID int    `json:"sessionId,omitempty"`
}

// Conflict codes the Daemon may carry on a 409. They are the wire contract and
// must equal the usecase.Code* strings (ADR-0009); the TUI matches on them to
// offer the right follow-up prompt.
const (
	CodeSessionExists  = "session_exists"
	CodeWorktreeExists = "worktree_exists"
	CodePathBlocked    = "path_blocked"
	CodeBranchExists   = "branch_exists"
)

// APIError is a non-2xx response from the Daemon. Code is populated when the
// body carried a machine-readable conflict code; callers match on it with
// errors.As to react without parsing Message.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	status := fmt.Sprintf("%d %s", e.Status, http.StatusText(e.Status))
	if e.Message != "" {
		return status + ": " + e.Message
	}
	return status
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: hc}
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/projects", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := c.doJSON(req, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

func (c *Client) CreateProject(ctx context.Context, fullPath string, title ...string) (Project, error) {
	var projectTitle *string
	if len(title) > 0 {
		projectTitle = &title[0]
	}
	body, err := json.Marshal(struct {
		FullPath string  `json:"fullPath"`
		Title    *string `json:"title,omitempty"`
	}{FullPath: fullPath, Title: projectTitle})
	if err != nil {
		return Project{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/projects", bytes.NewReader(body))
	if err != nil {
		return Project{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var project Project
	if err := c.doJSON(req, 0, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) DeleteProject(ctx context.Context, id int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/projects/%d", c.baseURL, id), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, http.StatusNoContent, nil)
}

func (c *Client) ListSessions(ctx context.Context, in ListSessionsInput) ([]Session, error) {
	values := url.Values{}
	if in.Type != "" {
		values.Set("type", in.Type)
	}
	if in.ProjectID != nil {
		values.Set("projectId", strconv.Itoa(*in.ProjectID))
	}
	endpoint := c.baseURL + "/sessions"
	if encoded := values.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.doJSON(req, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return resp.Sessions, nil
}

func (c *Client) CreateSession(ctx context.Context, in CreateSessionInput) (Session, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return Session{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions", bytes.NewReader(body))
	if err != nil {
		return Session{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var session Session
	if err := c.doJSON(req, http.StatusCreated, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (c *Client) DeleteSession(ctx context.Context, id int, force bool) error {
	endpoint := fmt.Sprintf("%s/sessions/%d", c.baseURL, id)
	if force {
		endpoint += "?force=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, http.StatusNoContent, nil)
}

func (c *Client) AcquirePort(ctx context.Context, in AcquirePortInput) (int, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/resources/ports/acquire", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		Port int `json:"port"`
	}
	if err := c.doJSON(req, http.StatusOK, &resp); err != nil {
		return 0, err
	}
	return resp.Port, nil
}

func (c *Client) ListAgents(ctx context.Context, in ListAgentsInput) ([]Agent, error) {
	values := url.Values{}
	if in.ProjectID != nil {
		values.Set("projectId", strconv.Itoa(*in.ProjectID))
	}
	if in.SessionID != nil {
		values.Set("sessionId", strconv.Itoa(*in.SessionID))
	}
	endpoint := c.baseURL + "/agents"
	if encoded := values.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Agents []Agent `json:"agents"`
	}
	if err := c.doJSON(req, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

func (c *Client) CreateAgent(ctx context.Context, in CreateAgentInput) (Agent, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return Agent{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/agents", bytes.NewReader(body))
	if err != nil {
		return Agent{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var agent Agent
	if err := c.doJSON(req, http.StatusCreated, &agent); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func (c *Client) SendAgentEvent(ctx context.Context, id int, event string) error {
	return c.sendAgentEvent(ctx, id, event, nil)
}

func (c *Client) SendAgentStarted(ctx context.Context, id int, childProcessGroupID int) error {
	return c.sendAgentEvent(ctx, id, "started", &childProcessGroupID)
}

func (c *Client) sendAgentEvent(ctx context.Context, id int, event string, childProcessGroupID *int) error {
	body, err := json.Marshal(struct {
		Event               string `json:"event"`
		ChildProcessGroupID *int   `json:"childProcessGroupId,omitempty"`
	}{Event: event, ChildProcessGroupID: childProcessGroupID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/agents/%d/event", c.baseURL, id), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doJSON(req, http.StatusNoContent, nil)
}

func (c *Client) DeleteAgent(ctx context.Context, id int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/agents/%d", c.baseURL, id), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, http.StatusNoContent, nil)
}

func (c *Client) doJSON(req *http.Request, want int, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if want != 0 && resp.StatusCode != want || want == 0 && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return statusError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	return &APIError{Status: resp.StatusCode, Code: e.Code, Message: e.Error}
}
