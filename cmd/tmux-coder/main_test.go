package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

func TestIsAgentWrapperMode(t *testing.T) {
	if !isAgentWrapperMode([]string{"tmux-coder", "agent-wrapper", "7", "opencode"}) {
		t.Fatal("expected agent-wrapper mode")
	}
	if isAgentWrapperMode([]string{"tmux-coder", "new"}) {
		t.Fatal("did not expect agent-wrapper mode for `new`")
	}
	if isAgentWrapperMode([]string{"tmux-coder"}) {
		t.Fatal("did not expect agent-wrapper mode with no subcommand")
	}
}

type fakeAgentAPI struct {
	sessions []httpclient.Session
	created  httpclient.CreateAgentInput
}

func (a *fakeAgentAPI) ListSessions(ctx context.Context, in httpclient.ListSessionsInput) ([]httpclient.Session, error) {
	return a.sessions, nil
}

func (a *fakeAgentAPI) CreateAgent(ctx context.Context, in httpclient.CreateAgentInput) (httpclient.Agent, error) {
	a.created = in
	return httpclient.Agent{ID: 12, ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: in.Kind, DisplayName: "agent", Status: "starting"}, nil
}

func TestRunNewParsesExplicitIDsAndKind(t *testing.T) {
	api := &fakeAgentAPI{}
	name := "reviewer"
	err := runNew(context.Background(), []string{"claude", "--name", name, "--project-id", "3", "--session-id", "4"}, func(string) string { return "" }, api, "http://daemon")
	if err != nil {
		t.Fatalf("runNew: %v", err)
	}
	if api.created.ProjectID != 3 || api.created.SessionID != 4 || api.created.Kind != "claude" {
		t.Fatalf("created = %#v", api.created)
	}
	if api.created.DisplayName == nil || *api.created.DisplayName != name {
		t.Fatalf("display name = %#v", api.created.DisplayName)
	}
}

func TestRunNewMapsCurrentTmuxSession(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "tmux"), "#!/bin/sh\ncase \"$3\" in '#S') printf tc-api-main ;; '#{pane_id}') exit 1 ;; esac\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	api := &fakeAgentAPI{sessions: []httpclient.Session{{ID: 8, ProjectID: 9, SessionName: "api.main", TmuxName: "tc-api-main"}}}
	err := runNew(context.Background(), []string{"opencode"}, func(key string) string {
		if key == "TMUX" {
			return "/tmp/tmux"
		}
		return ""
	}, api, "http://daemon")
	if err != nil {
		t.Fatalf("runNew: %v", err)
	}
	if api.created.ProjectID != 9 || api.created.SessionID != 8 {
		t.Fatalf("created = %#v", api.created)
	}
}

func TestRunNewRejectsBadIntegerArgs(t *testing.T) {
	err := runNew(context.Background(), []string{"--project-id", "nope"}, func(string) string { return "" }, &fakeAgentAPI{}, "http://daemon")
	if err == nil {
		t.Fatal("expected error")
	}
}

func writeTestExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
