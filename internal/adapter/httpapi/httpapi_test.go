package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/adapter/httpapi"
	"github.com/pilot322/tmux-coder/internal/infra/desktopnotify"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tmux-coder-httpapi-test-*")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "tmux-coder")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		panic(err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	code := m.Run()
	_ = os.Setenv("PATH", oldPath)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

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
func (g *stubGateway) SwitchClients(ctx context.Context, from, to string) error {
	return nil
}

type stubGit struct {
	paths     map[string]bool
	worktrees []usecase.WorktreeRef
}

func (g *stubGit) ValidateBranchName(ctx context.Context, branch string) error   { return nil }
func (g *stubGit) IsWorktreeRoot(ctx context.Context, path string) (bool, error) { return true, nil }
func (g *stubGit) LocalBranchExists(ctx context.Context, repoPath, branch string) (bool, error) {
	return false, nil
}
func (g *stubGit) ResolveCommit(ctx context.Context, repoPath, ref string) (bool, error) {
	return true, nil
}
func (g *stubGit) WorktreePathExists(ctx context.Context, path string) (bool, error) {
	return g.paths[path], nil
}
func (g *stubGit) ListWorktrees(ctx context.Context, repoPath string) ([]usecase.WorktreeRef, error) {
	return g.worktrees, nil
}
func (g *stubGit) AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, createBranch bool) error {
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

type stubAgentGateway struct {
	panes   map[string]bool
	created []stubNewWindowCall
}

type stubPortAvailability struct {
	occupied map[int]bool
}

func (p *stubPortAvailability) Available(ctx context.Context, port int) bool {
	return !p.occupied[port]
}

type stubNewWindowCall struct {
	sessionName string
	windowName  string
	workingDir  string
	command     string
	env         []string
}

func (g *stubAgentGateway) NewWindow(ctx context.Context, sessionName, windowName, workingDir, command string, env []string) (string, error) {
	paneID := fmt.Sprintf("%%%d", len(g.created)+1)
	g.created = append(g.created, stubNewWindowCall{sessionName, windowName, workingDir, command, env})
	g.panes[paneID] = true
	return paneID, nil
}

func (g *stubAgentGateway) RenameWindow(ctx context.Context, paneID, name string) error {
	return nil
}

func (g *stubAgentGateway) PaneExists(ctx context.Context, paneID string) (bool, error) {
	return g.panes[paneID], nil
}

func (g *stubAgentGateway) KillPane(ctx context.Context, paneID string) error {
	delete(g.panes, paneID)
	return nil
}

func (g *stubAgentGateway) ListPanes(ctx context.Context, sessionName string) ([]string, error) {
	var result []string
	for id, exists := range g.panes {
		if exists {
			result = append(result, id)
		}
	}
	return result, nil
}

func newServer() *http.ServeMux {
	return newServerWithGit(&stubGit{paths: make(map[string]bool)})
}

func newServerWithGit(git *stubGit) *http.ServeMux {
	state := memory.NewDaemonState()
	gw := &stubGateway{exists: make(map[string]bool)}
	agentGw := &stubAgentGateway{panes: make(map[string]bool)}
	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gw, git, state, state.Config(), obs.Nop())
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state, obs.Nop())
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), state.Agents(), gw, state, obs.Nop())
	createSession := usecase.NewCreateSession(state.Projects(), state.Sessions(), gw, git, state, obs.Nop())
	listSessions := usecase.NewGetSessions(state.Projects(), state.Sessions(), git, state, obs.Nop())
	deleteSession := usecase.NewDeleteSession(state.Sessions(), state.Agents(), gw, git, state, obs.Nop())
	createAgent := usecase.NewCreateAgent(state.Agents(), state.Projects(), state.Sessions(), agentGw, state, obs.Nop())
	listAgents := usecase.NewGetAgents(state.Agents(), state.Projects(), state.Sessions(), agentGw, state, obs.Nop())
	renameAgent := usecase.NewRenameAgent(state.Agents(), state.Projects(), state.Sessions(), agentGw, state, obs.Nop())
	agentEvent := usecase.NewAgentEvent(state.Agents(), state.Projects(), state.Sessions(), desktopnotify.NoopNotifier{}, state, obs.Nop())
	deleteAgent := usecase.NewDeleteAgent(state.Agents(), agentGw, nil, state, obs.Nop())

	return httpapi.NewRouter(
		httpapi.NewProjectController(create, list, del),
		httpapi.NewSessionController(createSession, listSessions, deleteSession),
		httpapi.NewAgentController(createAgent, listAgents, renameAgent, agentEvent, deleteAgent),
	)
}

func newResourceServer(ports *stubPortAvailability) (*http.ServeMux, *memory.MemoryResourceLeaseRepository) {
	state := memory.NewDaemonState()
	gw := &stubGateway{exists: make(map[string]bool)}
	git := &stubGit{paths: make(map[string]bool)}
	agentGw := &stubAgentGateway{panes: make(map[string]bool)}
	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gw, git, state, state.Config(), obs.Nop())
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state, obs.Nop())
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), state.Agents(), gw, state, obs.Nop())
	createSession := usecase.NewCreateSession(state.Projects(), state.Sessions(), gw, git, state, obs.Nop())
	listSessions := usecase.NewGetSessions(state.Projects(), state.Sessions(), git, state, obs.Nop())
	deleteSession := usecase.NewDeleteSession(state.Sessions(), state.Agents(), gw, git, state, obs.Nop())
	createAgent := usecase.NewCreateAgent(state.Agents(), state.Projects(), state.Sessions(), agentGw, state, obs.Nop())
	listAgents := usecase.NewGetAgents(state.Agents(), state.Projects(), state.Sessions(), agentGw, state, obs.Nop())
	renameAgent := usecase.NewRenameAgent(state.Agents(), state.Projects(), state.Sessions(), agentGw, state, obs.Nop())
	agentEvent := usecase.NewAgentEvent(state.Agents(), state.Projects(), state.Sessions(), desktopnotify.NoopNotifier{}, state, obs.Nop())
	deleteAgent := usecase.NewDeleteAgent(state.Agents(), agentGw, nil, state, obs.Nop())
	acquirePort := usecase.NewAcquirePort(state.Sessions(), state.Leases(), ports, state, obs.Nop())

	return httpapi.NewRouter(
		httpapi.NewProjectController(create, list, del),
		httpapi.NewSessionController(createSession, listSessions, deleteSession),
		httpapi.NewAgentController(createAgent, listAgents, renameAgent, agentEvent, deleteAgent),
		httpapi.NewResourceController(acquirePort),
	), state.Leases()
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

func TestPostProjects_WorktreesDetectedReturnsPreconditionRequired(t *testing.T) {
	mux := newServerWithGit(&stubGit{
		paths: map[string]bool{"/work/api.feature": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.feature", Branch: "feature"},
		},
	})

	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("POST status = %d, want 428 (body: %s)", rec.Code, rec.Body)
	}
	var resp struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Worktrees []struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
		} `json:"worktrees"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != usecase.CodeWorktreesDetected {
		t.Fatalf("code = %q, want %q", resp.Code, usecase.CodeWorktreesDetected)
	}
	if len(resp.Worktrees) != 1 || resp.Worktrees[0].Path != "/work/api.feature" || resp.Worktrees[0].Branch != "feature" {
		t.Fatalf("worktrees = %+v, want feature worktree", resp.Worktrees)
	}
}

func TestPostProjects_ExplicitFalseSkipsWorktreeAdoptionPrompt(t *testing.T) {
	mux := newServerWithGit(&stubGit{
		paths: map[string]bool{"/work/api.feature": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.feature", Branch: "feature"},
		},
	})

	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api","createWorktreeSessions":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
}

func TestPostAcquirePortWithHookToken(t *testing.T) {
	mux, leases := newResourceServer(&stubPortAvailability{occupied: map[int]bool{8000: true}})
	if err := leases.BeginHook(context.Background(), "hook-token", usecase.HookLeaseOwner{ProjectID: 7}); err != nil {
		t.Fatal(err)
	}

	rec := do(t, mux, "POST", "/resources/ports/acquire", `{"hookToken":"hook-token","key":"web","start":8000,"end":8002}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var resp struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Port != 8001 {
		t.Fatalf("port = %d, want 8001", resp.Port)
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

	body := `{"projectId":` + strconv.Itoa(project.ID) + `,"type":"worktree","branch":"feature/login","createWorktree":true,"createBranch":true}`
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

func TestPostSessions_StateConflictCarriesCode(t *testing.T) {
	git := &stubGit{
		paths:     make(map[string]bool),
		worktrees: []usecase.WorktreeRef{{Path: "/work/api.feature-login", Branch: "feature/login"}},
	}
	mux := newServerWithGit(git)
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)

	body := `{"projectId":` + strconv.Itoa(project.ID) + `,"type":"worktree","branch":"feature/login","createWorktree":true,"createBranch":true}`
	rec = do(t, mux, "POST", "/sessions", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body)
	}
	var resp struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != usecase.CodeWorktreeExists {
		t.Fatalf("code = %q, want %q (body: %s)", resp.Code, usecase.CodeWorktreeExists, rec.Body)
	}
	if resp.Error == "" {
		t.Fatalf("error message missing (body: %s)", rec.Body)
	}
}

func TestPostSessions_WorktreeProvenanceParentRoundTrips(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	mainID := getSessionID(t, mux, project.ID)

	// A 'w' creation is a fresh worktree (createWorktree+createBranch) that
	// carries parentSessionId; the controller forwards it and the response
	// reports the stored Provenance parent (ADR-0010).
	body := `{"projectId":` + strconv.Itoa(project.ID) + `,"type":"worktree","branch":"feature/login","createWorktree":true,"createBranch":true,"parentSessionId":` + strconv.Itoa(mainID) + `}`
	rec = do(t, mux, "POST", "/sessions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /sessions status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var session struct {
		ID              int    `json:"id"`
		Parent          int    `json:"parent"`
		ParentSessionID int    `json:"parentSessionId"`
		Type            string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if session.Type != "worktree" || session.Parent != mainID || session.ParentSessionID != mainID {
		t.Fatalf("worktree provenance not wired: %+v (want parent %d)", session, mainID)
	}

	rec = do(t, mux, "GET", "/sessions?type=worktree&projectId="+strconv.Itoa(project.ID), "")
	var listed struct {
		Sessions []struct {
			ID              int `json:"id"`
			ParentSessionID int `json:"parentSessionId"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != session.ID || listed.Sessions[0].ParentSessionID != mainID {
		t.Fatalf("listed worktree provenance = %+v, want parent %d", listed.Sessions, mainID)
	}
}

func TestPostSessions_CreatesSecondarySession(t *testing.T) {
	mux := newServer()
	projectPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectPath, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"`+projectPath+`"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	parentID := getSessionID(t, mux, project.ID)

	body := `{"type":"secondary","parentSessionId":` + strconv.Itoa(parentID) + `,"relativeWorkingDirectory":"pkg/","onDelete":"cascade"}`
	rec = do(t, mux, "POST", "/sessions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("secondary create status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var created struct {
		ID                       int    `json:"id"`
		Parent                   int    `json:"parent"`
		ParentSessionID          int    `json:"parentSessionId"`
		ProjectID                int    `json:"projectId"`
		SessionName              string `json:"sessionName"`
		TmuxName                 string `json:"tmuxSessionName"`
		Type                     string `json:"type"`
		RelativeWorkingDirectory string `json:"relativeWorkingDirectory"`
		OnDelete                 string `json:"onDelete"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == 0 || created.Parent != parentID || created.ParentSessionID != parentID || created.ProjectID != project.ID || created.SessionName != "pkg" || created.TmuxName != filepath.Base(projectPath)+"_main_pkg" || created.Type != "secondary" || created.RelativeWorkingDirectory != "pkg" || created.OnDelete != "cascade" {
		t.Fatalf("unexpected secondary: %+v", created)
	}

	rec = do(t, mux, "GET", "/sessions?type=secondary&projectId="+strconv.Itoa(project.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET secondary status = %d, want 200", rec.Code)
	}
	var listed struct {
		Sessions []struct {
			ID                       int    `json:"id"`
			ParentSessionID          int    `json:"parentSessionId"`
			RelativeWorkingDirectory string `json:"relativeWorkingDirectory"`
			OnDelete                 string `json:"onDelete"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != created.ID || listed.Sessions[0].ParentSessionID != parentID || listed.Sessions[0].RelativeWorkingDirectory != "pkg" || listed.Sessions[0].OnDelete != "cascade" {
		t.Fatalf("unexpected listed secondary: %+v", listed.Sessions)
	}
}

func TestDeleteSessions_DeletesWorktreeSession(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	rec = do(t, mux, "POST", "/sessions", `{"projectId":`+strconv.Itoa(project.ID)+`,"type":"worktree","branch":"feature/login","createWorktree":true,"createBranch":true}`)
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

func TestDeleteSessions_SecondaryCascadeDeletesDescendants(t *testing.T) {
	mux := newServer()
	projectPath := t.TempDir()
	for _, dir := range []string{"pkg", "pkg/internal"} {
		if err := os.MkdirAll(filepath.Join(projectPath, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"`+projectPath+`"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	mainID := getSessionID(t, mux, project.ID)
	parentID := createSecondarySession(t, mux, mainID, "pkg", "")
	childID := createSecondarySession(t, mux, parentID, "pkg/internal", "")

	if rec := do(t, mux, "DELETE", "/sessions/"+strconv.Itoa(parentID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE secondary status = %d, want 204 (body: %s)", rec.Code, rec.Body)
	}
	rec = do(t, mux, "GET", "/sessions?type=secondary&projectId="+strconv.Itoa(project.ID), "")
	var listed struct {
		Sessions []struct {
			ID int `json:"id"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Sessions) != 0 {
		t.Fatalf("expected secondary cascade to delete %d and %d, got %+v", parentID, childID, listed.Sessions)
	}
}

func TestDeleteSessions_SecondaryInheritReparentsDirectChildren(t *testing.T) {
	mux := newServer()
	projectPath := t.TempDir()
	for _, dir := range []string{"pkg", "pkg/internal"} {
		if err := os.MkdirAll(filepath.Join(projectPath, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"`+projectPath+`"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	mainID := getSessionID(t, mux, project.ID)
	parentID := createSecondarySession(t, mux, mainID, "pkg", "inherit")
	childID := createSecondarySession(t, mux, parentID, "pkg/internal", "")

	if rec := do(t, mux, "DELETE", "/sessions/"+strconv.Itoa(parentID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE secondary status = %d, want 204 (body: %s)", rec.Code, rec.Body)
	}
	rec = do(t, mux, "GET", "/sessions?type=secondary&projectId="+strconv.Itoa(project.ID), "")
	var listed struct {
		Sessions []struct {
			ID              int `json:"id"`
			ParentSessionID int `json:"parentSessionId"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listed)
	if len(listed.Sessions) != 1 || listed.Sessions[0].ID != childID || listed.Sessions[0].ParentSessionID != mainID {
		t.Fatalf("expected child %d reparented to %d, got %+v", childID, mainID, listed.Sessions)
	}
}

func TestPostAgents_CreatesAgentInSession(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)

	rec = do(t, mux, "GET", "/sessions", "")
	var sessions struct {
		Sessions []struct {
			ID int `json:"id"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sessions)
	if len(sessions.Sessions) == 0 {
		t.Fatal("expected at least one session after creating project")
	}
	sessionID := sessions.Sessions[0].ID

	body := `{"projectId":` + strconv.Itoa(project.ID) + `,"sessionId":` + strconv.Itoa(sessionID) + `,"kind":"opencode"}`
	rec = do(t, mux, "POST", "/agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /agents status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var agent struct {
		ID              int       `json:"id"`
		ProjectID       int       `json:"projectId"`
		SessionID       int       `json:"sessionId"`
		Kind            string    `json:"kind"`
		DisplayName     string    `json:"displayName"`
		Status          string    `json:"status"`
		StatusChangedAt time.Time `json:"statusChangedAt"`
		PaneOwned       bool      `json:"paneOwned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &agent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if agent.ID == 0 || agent.ProjectID != project.ID || agent.SessionID != sessionID || agent.Kind != "opencode" || agent.Status != "starting" || !agent.PaneOwned {
		t.Fatalf("unexpected agent: %+v", agent)
	}
	if agent.DisplayName == "" {
		t.Fatal("expected non-empty display name")
	}
	if agent.StatusChangedAt.IsZero() {
		t.Fatal("expected non-zero statusChangedAt")
	}
}

func TestGetAgents_ListsAgents(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	sessionID := getSessionID(t, mux, project.ID)

	rec = do(t, mux, "POST", "/agents", `{"projectId":`+strconv.Itoa(project.ID)+`,"sessionId":`+strconv.Itoa(sessionID)+`,"kind":"opencode"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create agent status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}

	rec = do(t, mux, "GET", "/agents", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agents status = %d, want 200", rec.Code)
	}
	var resp struct {
		Agents []struct {
			Kind            string    `json:"kind"`
			Status          string    `json:"status"`
			StatusChangedAt time.Time `json:"statusChangedAt"`
		} `json:"agents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(resp.Agents))
	}
	if resp.Agents[0].Kind != "opencode" {
		t.Fatalf("kind = %q, want opencode", resp.Agents[0].Kind)
	}
	if resp.Agents[0].StatusChangedAt.IsZero() {
		t.Fatal("expected non-zero statusChangedAt")
	}
}

func TestPatchAgent_RenamesAgent(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "PATCH", "/agents/"+strconv.Itoa(agentID), `{"displayName":"reviewer"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /agents/{id} status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var agent struct {
		ID          int    `json:"id"`
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &agent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if agent.ID != agentID || agent.DisplayName != "reviewer" {
		t.Fatalf("agent = %+v, want id %d displayName reviewer", agent, agentID)
	}
}

func TestPatchAgent_RejectsEmptyName(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "PATCH", "/agents/"+strconv.Itoa(agentID), `{"displayName":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH empty displayName status = %d, want 400", rec.Code)
	}
}

func TestPostAgents_EventStarted(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "POST", "/agents/"+strconv.Itoa(agentID)+"/event", `{"event":"started"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST event status = %d, want 204", rec.Code)
	}

	rec = do(t, mux, "GET", "/agents", "")
	var resp struct {
		Agents []struct {
			Status string `json:"status"`
		} `json:"agents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Agents) != 1 || resp.Agents[0].Status != "running" {
		t.Fatalf("want running status, got %+v", resp.Agents)
	}
}

func TestPostAgents_EventBusy(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "POST", "/agents/"+strconv.Itoa(agentID)+"/event", `{"event":"busy"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST event status = %d, want 204", rec.Code)
	}

	rec = do(t, mux, "GET", "/agents", "")
	var resp struct {
		Agents []struct {
			Status string `json:"status"`
		} `json:"agents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Agents) != 1 || resp.Agents[0].Status != "busy" {
		t.Fatalf("want busy status, got %+v", resp.Agents)
	}
}

func TestPostAgents_EventExitedRemovesAgent(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "POST", "/agents/"+strconv.Itoa(agentID)+"/event", `{"event":"exited"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST event status = %d, want 204", rec.Code)
	}

	rec = do(t, mux, "GET", "/agents", "")
	var resp struct {
		Agents []struct{} `json:"agents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Agents) != 0 {
		t.Fatalf("want 0 agents after exit, got %d", len(resp.Agents))
	}
}

func TestPostAgents_InvalidEvent(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "POST", "/agents/"+strconv.Itoa(agentID)+"/event", `{"event":"unknown"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown event status = %d, want 400", rec.Code)
	}
}

func TestDeleteAgent(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	agentID := createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "DELETE", "/agents/"+strconv.Itoa(agentID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE agent status = %d, want 204", rec.Code)
	}
}

func TestDeleteAgent_NotFound(t *testing.T) {
	mux := newServer()
	rec := do(t, mux, "DELETE", "/agents/9999", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE unknown agent status = %d, want 404", rec.Code)
	}
}

func TestGetAgents_FilterByProjectAndSession(t *testing.T) {
	mux := newServer()
	projectID, sessionID := createProjectAndGetSession(t, mux)
	_ = createAgent(t, mux, projectID, sessionID)

	rec := do(t, mux, "GET", "/agents?projectId="+strconv.Itoa(projectID)+"&sessionId="+strconv.Itoa(sessionID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agents with filters status = %d, want 200", rec.Code)
	}
	var resp struct {
		Agents []struct {
			Kind string `json:"kind"`
		} `json:"agents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(resp.Agents))
	}
}

func TestGetAgents_InvalidFilterCombo(t *testing.T) {
	mux := newServer()
	do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/web"}`)
	var project2 struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project2)
	sessionID2 := getSessionID(t, mux, project2.ID)

	rec = do(t, mux, "GET", "/agents?projectId=1&sessionId="+strconv.Itoa(sessionID2), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid filter combo status = %d, want 400", rec.Code)
	}
}

func getSessionID(t *testing.T, mux *http.ServeMux, projectID int) int {
	t.Helper()
	rec := do(t, mux, "GET", "/sessions?projectId="+strconv.Itoa(projectID), "")
	var resp struct {
		Sessions []struct {
			ID int `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(resp.Sessions) == 0 {
		t.Fatal("no sessions found")
	}
	return resp.Sessions[0].ID
}

func createProjectAndGetSession(t *testing.T, mux *http.ServeMux) (int, int) {
	t.Helper()
	rec := do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	var project struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &project)
	sessionID := getSessionID(t, mux, project.ID)
	return project.ID, sessionID
}

func createAgent(t *testing.T, mux *http.ServeMux, projectID, sessionID int) int {
	t.Helper()
	body := `{"projectId":` + strconv.Itoa(projectID) + `,"sessionId":` + strconv.Itoa(sessionID) + `,"kind":"opencode"}`
	rec := do(t, mux, "POST", "/agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create agent status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var agent struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &agent)
	return agent.ID
}

func createSecondarySession(t *testing.T, mux *http.ServeMux, parentID int, relwd, onDelete string) int {
	t.Helper()
	body := `{"type":"secondary","parentSessionId":` + strconv.Itoa(parentID) + `,"relativeWorkingDirectory":"` + relwd + `"`
	if onDelete != "" {
		body += `,"onDelete":"` + onDelete + `"`
	}
	body += `}`
	rec := do(t, mux, "POST", "/sessions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create secondary status = %d, want 201 (body: %s)", rec.Code, rec.Body)
	}
	var session struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &session)
	return session.ID
}
