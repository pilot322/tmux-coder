package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
)

const maxDashboardBodyBytes int64 = 1 << 20

// NewRouter wires the API routes using the stdlib ServeMux method+wildcard
// patterns introduced in Go 1.22.
func NewRouter(pc *ProjectController, sc *SessionController, ac *AgentController, resources ...*ResourceController) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /projects", pc.Create)
	mux.HandleFunc("GET /projects", pc.List)
	mux.HandleFunc("DELETE /projects/{id}", pc.Delete)
	mux.HandleFunc("GET /sessions", sc.List)
	mux.HandleFunc("POST /sessions", sc.Create)
	mux.HandleFunc("DELETE /sessions/{id}", sc.Delete)
	mux.HandleFunc("GET /agents", ac.List)
	mux.HandleFunc("POST /agents", ac.Create)
	mux.HandleFunc("PATCH /agents/{id}", ac.Update)
	mux.HandleFunc("PUT /agents/{id}/discord-notification", ac.SetDiscordNotification)
	mux.HandleFunc("POST /agents/{id}/event", ac.Event)
	mux.HandleFunc("PUT /agents/{id}/opencode-session", ac.ReportOpenCodeSession)
	mux.HandleFunc("POST /agents/{id}/opencode-setup/ready", ac.OpenCodeSetupReady)
	mux.HandleFunc("POST /agents/{id}/opencode-setup/state", ac.SetOpenCodeSetupState)
	mux.HandleFunc("POST /agents/{id}/opencode-setup/opened", ac.OpenCodeSetupOpened)
	mux.HandleFunc("GET /agents/{id}/opencode-setup", ac.WaitOpenCodeSetup)
	mux.HandleFunc("DELETE /agents/{id}", ac.Delete)
	if len(resources) > 0 && resources[0] != nil {
		mux.HandleFunc("POST /resources/ports/acquire", resources[0].AcquirePort)
		if resources[0].ensureOpenCodeServer != nil {
			mux.HandleFunc("POST /resources/opencode-server", resources[0].EnsureOpenCodeServer)
		}
	}
	return mux
}

// NewDashboardRouter serves the browser dashboard and only the management API
// routes it needs. Internal agent-integration and resource routes stay on
// NewRouter.
func NewDashboardRouter(pc *ProjectController, sc *SessionController, ac *AgentController, dashboard http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/projects", dashboardMutation(pc.Create))
	mux.HandleFunc("GET /api/projects", pc.List)
	mux.HandleFunc("DELETE /api/projects/{id}", pc.Delete)
	mux.HandleFunc("GET /api/sessions", sc.List)
	mux.HandleFunc("POST /api/sessions", dashboardMutation(sc.Create))
	mux.HandleFunc("DELETE /api/sessions/{id}", sc.Delete)
	mux.HandleFunc("GET /api/agents", ac.List)
	mux.HandleFunc("POST /api/agents", dashboardMutation(ac.Create))
	mux.HandleFunc("PATCH /api/agents/{id}", dashboardMutation(ac.Update))
	mux.HandleFunc("PUT /api/agents/{id}/discord-notification", dashboardMutation(ac.SetDiscordNotification))
	mux.HandleFunc("DELETE /api/agents/{id}", ac.Delete)
	mux.HandleFunc("/api", http.NotFound)
	mux.HandleFunc("/api/", http.NotFound)
	mux.Handle("/", dashboard)
	return mux
}

func dashboardMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		if r.ContentLength > maxDashboardBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
			return
		}
		bodyReader := http.MaxBytesReader(w, r.Body, maxDashboardBodyBytes)
		body, err := io.ReadAll(bodyReader)
		_ = bodyReader.Close()
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
			} else {
				writeError(w, http.StatusBadRequest, "invalid request body")
			}
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next(w, r)
	}
}
