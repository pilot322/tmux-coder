package httpclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

func TestClientListCreateDeleteProjects(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /projects":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"projects":[{"id":1,"title":"API","fullPath":"/work/api","mainSessionName":"api.main","mainTmuxSessionName":"api_main"}]}`))
		case "POST /projects":
			var req struct {
				Title *string `json:"title"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Title == nil || *req.Title != "Web" {
				t.Fatalf("request title = %v, want Web", req.Title)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2,"title":"Web","fullPath":"/work/web","mainSessionName":"web.main","mainTmuxSessionName":"web_main"}`))
		case "DELETE /projects/2":
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := httpclient.New(server.URL, server.Client())
	projects, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 1 || projects[0].Title != "API" || projects[0].MainSessionName != "api.main" || projects[0].MainTmuxSessionName != "api_main" {
		t.Fatalf("unexpected projects: %+v", projects)
	}
	created, err := c.CreateProject(context.Background(), "/work/web", "Web")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != 2 || created.Title != "Web" || created.FullPath != "/work/web" {
		t.Fatalf("unexpected created project: %+v", created)
	}
	if err := c.DeleteProject(context.Background(), 2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

func TestClientReturnsAPIErrorMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"fullPath is required"}`))
	}))
	defer server.Close()

	c := httpclient.New(server.URL, server.Client())
	_, err := c.CreateProject(context.Background(), "")
	if err == nil || err.Error() != "400 Bad Request: fullPath is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestClientListCreateDeleteSessions(t *testing.T) {
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /sessions":
			if r.URL.Query().Get("type") != "worktree" || r.URL.Query().Get("projectId") != "7" {
				t.Fatalf("query = %q, want type=worktree&projectId=7", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"sessions":[{"id":3,"projectId":7,"name":"api.feature","sessionName":"api.feature","tmuxSessionName":"api_feature","type":"worktree","branch":"feature","worktreePath":"/work/api.feature","project":{"id":7,"title":"API","fullPath":"/work/api"}}]}`))
		case "POST /sessions":
			var req httpclient.CreateSessionInput
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.ProjectID != 7 || req.Type != "worktree" || req.Branch != "feature" || !req.CreateWorktree || !req.CreateBranch || req.BaseBranch != "main" {
				t.Fatalf("request = %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":4,"projectId":7,"name":"api.feature","sessionName":"api.feature","tmuxSessionName":"api_feature","type":"worktree","branch":"feature","worktreePath":"/work/api.feature","project":{"id":7,"title":"API","fullPath":"/work/api"}}`))
		case "DELETE /sessions/4":
			if r.URL.Query().Get("force") != "true" {
				t.Fatalf("force query = %q, want true", r.URL.RawQuery)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := httpclient.New(server.URL, server.Client())
	projectID := 7
	sessions, err := c.ListSessions(context.Background(), httpclient.ListSessionsInput{Type: "worktree", ProjectID: &projectID})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionName != "api.feature" || sessions[0].TmuxName != "api_feature" || sessions[0].Project.Title != "API" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	created, err := c.CreateSession(context.Background(), httpclient.CreateSessionInput{ProjectID: 7, Type: "worktree", Branch: "feature", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.ID != 4 || created.Worktree != "/work/api.feature" || created.TmuxName != "api_feature" {
		t.Fatalf("unexpected created session: %+v", created)
	}
	if err := c.DeleteSession(context.Background(), 4, true); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

func TestClientCreateSessionReturnsAPIErrorWithCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"branch already exists","code":"branch_exists"}`))
	}))
	defer server.Close()

	c := httpclient.New(server.URL, server.Client())
	_, err := c.CreateSession(context.Background(), httpclient.CreateSessionInput{ProjectID: 1, Type: "worktree", Branch: "feature/login", CreateWorktree: true, CreateBranch: true})
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *httpclient.APIError", err)
	}
	if apiErr.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", apiErr.Status)
	}
	if apiErr.Code != httpclient.CodeBranchExists {
		t.Fatalf("code = %q, want %q", apiErr.Code, httpclient.CodeBranchExists)
	}
	if apiErr.Message == "" {
		t.Fatalf("message empty, want the server error text")
	}
}

func TestClientAcquirePort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/resources/ports/acquire" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req httpclient.AcquirePortInput
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.HookToken != "hook-token" || req.Key != "web" || req.Start != 8000 || req.End != 8002 {
			t.Fatalf("request = %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"port":8001}`))
	}))
	defer server.Close()

	c := httpclient.New(server.URL, server.Client())
	port, err := c.AcquirePort(context.Background(), httpclient.AcquirePortInput{HookToken: "hook-token", Key: "web", Start: 8000, End: 8002})
	if err != nil {
		t.Fatalf("AcquirePort: %v", err)
	}
	if port != 8001 {
		t.Fatalf("port = %d, want 8001", port)
	}
}
