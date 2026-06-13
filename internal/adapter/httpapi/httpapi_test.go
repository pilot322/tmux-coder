package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/adapter/httpapi"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// stubGateway is a tmux stand-in that always succeeds and tracks presence.
type stubGateway struct {
	exists map[string]bool
}

func (g *stubGateway) Create(ctx context.Context, name, dir string) error {
	g.exists[name] = true
	return nil
}
func (g *stubGateway) Kill(ctx context.Context, name string) error {
	delete(g.exists, name)
	return nil
}
func (g *stubGateway) Exists(ctx context.Context, name string) (bool, error) {
	return g.exists[name], nil
}

type stubGit struct {
	paths map[string]bool
}

func (g *stubGit) ValidateBranchName(ctx context.Context, branch string) error   { return nil }
func (g *stubGit) IsWorktreeRoot(ctx context.Context, path string) (bool, error) { return true, nil }
func (g *stubGit) LocalBranchExists(ctx context.Context, repoPath, branch string) (bool, error) {
	return true, nil
}
func (g *stubGit) ResolveCommit(ctx context.Context, repoPath, ref string) (bool, error) {
	return true, nil
}
func (g *stubGit) WorktreePathExists(ctx context.Context, path string) (bool, error) {
	return g.paths[path], nil
}
func (g *stubGit) AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, create bool) error {
	g.paths[worktreePath] = true
	return nil
}
func (g *stubGit) RemoveWorktree(ctx context.Context, worktreePath string, force bool) error {
	delete(g.paths, worktreePath)
	return nil
}
func (g *stubGit) DeleteBranch(ctx context.Context, repoPath, branch string) error { return nil }

func (g *stubGit) CurrentBranch(ctx context.Context, repoPath string) (string, error) {
	return "main", nil
}

func newServer() *http.ServeMux {
	state := memory.NewDaemonState()
	gw := &stubGateway{exists: make(map[string]bool)}
	git := &stubGit{paths: make(map[string]bool)}

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gw, state, state.Config())
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), gw, state)
	createSession := usecase.NewCreateSession(state.Projects(), state.Sessions(), gw, git, state)
	listSessions := usecase.NewGetSessions(state.Projects(), state.Sessions(), git, state)
	deleteSession := usecase.NewDeleteSession(state.Sessions(), gw, git, state)

	return httpapi.NewRouter(
		httpapi.NewProjectController(create, list, del),
		httpapi.NewSessionController(createSession, listSessions, deleteSession),
	)
}

func do(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPostProjects_CreatesThenReturnsExisting(t *testing.T) {
	mux := newServer()

	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var created struct {
		ID                  int    `json:"id"`
		Title               string `json:"title"`
		FullPath            string `json:"fullPath"`
		MainSessionName     string `json:"mainSessionName"`
		MainTmuxSessionName string `json:"mainTmuxSessionName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == 0 || created.Title != "api" || created.MainSessionName != "api.main" || created.MainTmuxSessionName != "api_main" || created.FullPath != "/work/api" {
		t.Errorf("unexpected body: %+v", created)
	}

	// Second POST for the same path is idempotent: 200, not 201.
	rec = do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second POST status = %d, want 200", rec.Code)
	}
}

func TestPostProjects_AcceptsOptionalTitle(t *testing.T) {
	mux := newServer()

	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api","title":" Backend API "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var created struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Title != "Backend API" {
		t.Fatalf("Title = %q, want Backend API", created.Title)
	}

	rec = do(t, mux, "POST", "/projects", `{"fullPath":"/work/api","title":"Different"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate POST status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	if created.Title != "Backend API" {
		t.Fatalf("duplicate Title = %q, want Backend API", created.Title)
	}
}

func TestPostProjects_InvalidBody(t *testing.T) {
	mux := newServer()

	if rec := do(t, mux, "POST", "/projects", `not json`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON status = %d, want 400", rec.Code)
	}
	if rec := do(t, mux, "POST", "/projects", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing fullPath status = %d, want 400", rec.Code)
	}
	if rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api","title":"   "}`); rec.Code != http.StatusBadRequest {
		t.Errorf("blank title status = %d, want 400", rec.Code)
	}
	if rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api","title":"Backend  API"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("adjacent spaces title status = %d, want 400", rec.Code)
	}
}

func TestGetProjects_ListsCreated(t *testing.T) {
	mux := newServer()
	do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	do(t, mux, "POST", "/projects", `{"fullPath":"/work/web"}`)

	rec := do(t, mux, "GET", "/projects", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var resp struct {
		Projects []struct {
			ID              int    `json:"id"`
			Title           string `json:"title"`
			MainSessionName string `json:"mainSessionName"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(resp.Projects))
	}
	if resp.Projects[0].Title == "" {
		t.Fatalf("first project title is empty: %+v", resp.Projects[0])
	}
}

func TestDeleteProjects_NoContentThenNotFound(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	if rec := do(t, mux, "DELETE", "/projects/"+strconv.Itoa(created.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", rec.Code)
	}
	if rec := do(t, mux, "DELETE", "/projects/9999", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown status = %d, want 404", rec.Code)
	}
}

func TestGetSessions_ListsMainSessionWithProjectTitle(t *testing.T) {
	mux := newServer()
	do(t, mux, "POST", "/projects", `{"fullPath":"/work/api","title":"Backend API"}`)

	rec := do(t, mux, "GET", "/sessions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sessions status = %d, want 200", rec.Code)
	}
	var resp struct {
		Sessions []struct {
			SessionName string `json:"sessionName"`
			TmuxName    string `json:"tmuxSessionName"`
			Type        string `json:"type"`
			Branch      string `json:"branch"`
			Project     struct {
				Title               string `json:"title"`
				MainSessionName     string `json:"mainSessionName"`
				MainTmuxSessionName string `json:"mainTmuxSessionName"`
			} `json:"project"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionName != "api.main" || resp.Sessions[0].TmuxName != "api_main" || resp.Sessions[0].Type != "main" || resp.Sessions[0].Branch != "main" || resp.Sessions[0].Project.Title != "Backend API" || resp.Sessions[0].Project.MainSessionName != "api.main" || resp.Sessions[0].Project.MainTmuxSessionName != "api_main" {
		t.Fatalf("unexpected sessions: %+v", resp.Sessions)
	}
}

func TestPostSessions_CreatesWorktreeSessionAndRejectsDuplicateBranch(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)

	body := `{"projectId":` + strconv.Itoa(project.ID) + `,"type":"worktree","branch":"feature/login"}`
	rec = do(t, mux, "POST", "/sessions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /sessions status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var session struct {
		ID          int    `json:"id"`
		SessionName string `json:"sessionName"`
		TmuxName    string `json:"tmuxSessionName"`
		Type        string `json:"type"`
		Branch      string `json:"branch"`
		Worktree    string `json:"worktreePath"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if session.ID == 0 || session.SessionName != "api.feature-login" || session.TmuxName != "api_feature-login" || session.Type != "worktree" || session.Branch != "feature/login" || session.Worktree != "/work/api.feature-login" {
		t.Fatalf("unexpected session: %+v", session)
	}

	if rec := do(t, mux, "POST", "/sessions", body); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate branch status = %d, want 409", rec.Code)
	}
}

func TestPostSessions_SecondaryNotImplemented(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/sessions", `{"projectId":1,"type":"secondary"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("secondary create status = %d, want 501", rec.Code)
	}
}

func TestDeleteSessions_DeletesWorktreeSession(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	rec = do(t, mux, "POST", "/sessions", `{"projectId":`+strconv.Itoa(project.ID)+`,"type":"worktree","branch":"feature/login"}`)
	var session struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &session)

	if rec := do(t, mux, "DELETE", "/sessions/"+strconv.Itoa(session.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /sessions status = %d, want 204", rec.Code)
	}
	rec = do(t, mux, "GET", "/sessions?type=worktree&projectId="+strconv.Itoa(project.ID), "")
	var resp struct {
		Sessions []struct{} `json:"sessions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Sessions) != 0 {
		t.Fatalf("worktree session should be gone: %+v", resp.Sessions)
	}
}
