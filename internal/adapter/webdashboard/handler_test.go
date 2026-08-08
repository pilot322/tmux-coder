package webdashboard_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/adapter/webdashboard"
)

func TestHandlerServesEmbeddedDashboard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html; charset=utf-8", contains: "<title>tmux-coder control room</title>"},
		{path: "/index.html", contentType: "text/html; charset=utf-8", contains: "id=\"dashboard\""},
		{path: "/app.js", contentType: "text/javascript; charset=utf-8", contains: "API_PREFIX"},
		{path: "/styles.css", contentType: "text/css; charset=utf-8", contains: ":root"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			rec := request(t, http.MethodGet, tt.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", tt.path, rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.contentType {
				t.Fatalf("GET %s Content-Type = %q, want %q", tt.path, got, tt.contentType)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Fatalf("GET %s body does not contain %q", tt.path, tt.contains)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("GET %s X-Content-Type-Options = %q, want nosniff", tt.path, got)
			}
			if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "connect-src 'self'") {
				t.Fatalf("GET %s Content-Security-Policy = %q, want same-origin connections", tt.path, got)
			}
		})
	}
}

func TestHandlerFallsBackToIndexForBrowserRoutes(t *testing.T) {
	t.Parallel()

	rec := request(t, http.MethodGet, "/projects/17/sessions?focus=agent")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want HTML", got)
	}
	if !strings.Contains(rec.Body.String(), "id=\"dashboard\"") {
		t.Fatal("SPA fallback did not serve index.html")
	}
}

func TestHandlerNeverFallsBackForAPIMisses(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/api", "/api/projects", "/api/agents/42"} {
		rec := request(t, http.MethodGet, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "id=\"dashboard\"") {
			t.Fatalf("GET %s returned dashboard HTML", path)
		}
	}
}

func TestHandlerDoesNotTurnMissingAssetsIntoHTML(t *testing.T) {
	t.Parallel()

	rec := request(t, http.MethodGet, "/missing.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "id=\"dashboard\"") {
		t.Fatal("missing JavaScript returned dashboard HTML")
	}
}

func TestHandlerSupportsHeadAndRejectsMutatingMethods(t *testing.T) {
	t.Parallel()

	rec := request(t, http.MethodHead, "/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD body length = %d, want 0", rec.Body.Len())
	}

	rec = request(t, http.MethodPost, "/projects")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want GET, HEAD", got)
	}
}

func request(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	webdashboard.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHandlerAssetsAreNonEmpty(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(webdashboard.Handler())
	defer server.Close()
	for _, path := range []string{"/", "/app.js", "/styles.css"} {
		resp, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if len(body) == 0 {
			t.Fatalf("GET %s returned an empty body", path)
		}
	}
}
