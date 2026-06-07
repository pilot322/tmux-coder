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

func newServer() *http.ServeMux {
	state := memory.NewDaemonState()
	gw := &stubGateway{exists: make(map[string]bool)}

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gw, state)
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), gw, state)

	return httpapi.NewRouter(httpapi.NewProjectController(create, list, del))
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
		ID              int    `json:"id"`
		FullPath        string `json:"fullPath"`
		MainSessionName string `json:"mainSessionName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID == 0 || created.MainSessionName != "api-main" || created.FullPath != "/work/api" {
		t.Errorf("unexpected body: %+v", created)
	}

	// Second POST for the same path is idempotent: 200, not 201.
	rec = do(t, mux, "POST", "/projects", `{"fullPath":"/work/api"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second POST status = %d, want 200", rec.Code)
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
			MainSessionName string `json:"mainSessionName"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(resp.Projects))
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
