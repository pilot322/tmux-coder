package httpclient_test

import (
	"context"
	"encoding/json"
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
			_, _ = w.Write([]byte(`{"projects":[{"id":1,"title":"API","fullPath":"/work/api","mainSessionName":"api-main"}]}`))
		case "POST /projects":
			var req struct {
				Title *string `json:"title"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Title == nil || *req.Title != "Web" {
				t.Fatalf("request title = %v, want Web", req.Title)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2,"title":"Web","fullPath":"/work/web","mainSessionName":"web-main"}`))
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
	if len(projects) != 1 || projects[0].Title != "API" || projects[0].MainSessionName != "api-main" {
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
