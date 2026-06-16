package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	acquired httpclient.AcquirePortInput
	port     int
}

type fakeOpenAPI struct {
	calls     []openCall
	firstErr  error
	calledErr bool
}

type openCall struct {
	fullPath string
	decision *bool
}

func (a *fakeOpenAPI) CreateProject(ctx context.Context, fullPath string, createWorktreeSessions *bool, title ...string) (httpclient.Project, error) {
	a.calls = append(a.calls, openCall{fullPath: fullPath, decision: createWorktreeSessions})
	if a.firstErr != nil && !a.calledErr {
		a.calledErr = true
		return httpclient.Project{}, a.firstErr
	}
	return httpclient.Project{ID: 1, FullPath: fullPath, MainTmuxSessionName: "api_main"}, nil
}

func (a *fakeAgentAPI) ListSessions(ctx context.Context, in httpclient.ListSessionsInput) ([]httpclient.Session, error) {
	return a.sessions, nil
}

func (a *fakeAgentAPI) CreateAgent(ctx context.Context, in httpclient.CreateAgentInput) (httpclient.Agent, error) {
	a.created = in
	return httpclient.Agent{ID: 12, ProjectID: in.ProjectID, SessionID: in.SessionID, Kind: in.Kind, DisplayName: "agent", Status: "starting"}, nil
}

func (a *fakeAgentAPI) AcquirePort(ctx context.Context, in httpclient.AcquirePortInput) (int, error) {
	a.acquired = in
	if a.port == 0 {
		return 8001, nil
	}
	return a.port, nil
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

func TestRunAcquirePortUsesHookTokenAndPrintsOnlyPort(t *testing.T) {
	api := &fakeAgentAPI{port: 8123}
	var out bytes.Buffer
	err := runAcquirePort(context.Background(), []string{"web", "--start", "8000", "--end", "8002"}, func(key string) string {
		if key == "TMUX_CODER_HOOK_TOKEN" {
			return "hook-token"
		}
		return ""
	}, api, &out)
	if err != nil {
		t.Fatalf("runAcquirePort: %v", err)
	}
	if api.acquired.HookToken != "hook-token" || api.acquired.Key != "web" || api.acquired.Start != 8000 || api.acquired.End != 8002 {
		t.Fatalf("acquired = %#v", api.acquired)
	}
	if out.String() != "8123\n" {
		t.Fatalf("stdout = %q, want only port number", out.String())
	}
}

func TestRunAcquirePortMapsCurrentTmuxSession(t *testing.T) {
	dir := t.TempDir()
	writeTestExecutable(t, filepath.Join(dir, "tmux"), "#!/bin/sh\ncase \"$3\" in '#S') printf tc-api-feature ;; '#{pane_id}') exit 1 ;; esac\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	api := &fakeAgentAPI{port: 8124, sessions: []httpclient.Session{{ID: 8, ProjectID: 9, SessionName: "api.feature", TmuxName: "tc-api-feature"}}}
	var out bytes.Buffer
	err := runAcquirePort(context.Background(), []string{"web", "--start", "8000", "--end", "8002"}, func(key string) string {
		if key == "TMUX" {
			return "/tmp/tmux"
		}
		return ""
	}, api, &out)
	if err != nil {
		t.Fatalf("runAcquirePort: %v", err)
	}
	if api.acquired.ProjectID != 9 || api.acquired.SessionID != 8 || api.acquired.HookToken != "" {
		t.Fatalf("acquired = %#v", api.acquired)
	}
	if out.String() != "8124\n" {
		t.Fatalf("stdout = %q, want only port number", out.String())
	}
}

func TestRunOpenFlagBypassesWorktreePrompt(t *testing.T) {
	api := &fakeOpenAPI{}
	var out bytes.Buffer

	_, err := runOpen(context.Background(), []string{"--no-create-worktree-sessions"}, func() (string, error) {
		return "/work/api", nil
	}, api, strings.NewReader(""), &out, true)
	if err != nil {
		t.Fatalf("runOpen: %v", err)
	}
	if len(api.calls) != 1 || api.calls[0].decision == nil || *api.calls[0].decision {
		t.Fatalf("calls = %#v, want one explicit false decision", api.calls)
	}
	if out.Len() != 0 {
		t.Fatalf("prompt output = %q, want none", out.String())
	}
}

func TestRunOpenPromptsAndReissuesYesOnEnter(t *testing.T) {
	api := &fakeOpenAPI{firstErr: &httpclient.APIError{Status: 428, Code: httpclient.CodeWorktreesDetected, Worktrees: []httpclient.WorktreeRef{{Path: "/work/api.feature", Branch: "feature"}}}}
	var out bytes.Buffer

	_, err := runOpen(context.Background(), nil, func() (string, error) {
		return "/work/api", nil
	}, api, strings.NewReader("\n"), &out, true)
	if err != nil {
		t.Fatalf("runOpen: %v", err)
	}
	if len(api.calls) != 2 || api.calls[0].decision != nil || api.calls[1].decision == nil || !*api.calls[1].decision {
		t.Fatalf("calls = %#v, want nil then true decision", api.calls)
	}
	if !strings.Contains(out.String(), "Create Worktree Sessions") {
		t.Fatalf("prompt output = %q", out.String())
	}
}

func TestRunOpenNonInteractiveReissuesNo(t *testing.T) {
	api := &fakeOpenAPI{firstErr: &httpclient.APIError{Status: 428, Code: httpclient.CodeWorktreesDetected}}

	_, err := runOpen(context.Background(), nil, func() (string, error) {
		return "/work/api", nil
	}, api, io.Reader(strings.NewReader("")), io.Discard, false)
	if err != nil {
		t.Fatalf("runOpen: %v", err)
	}
	if len(api.calls) != 2 || api.calls[1].decision == nil || *api.calls[1].decision {
		t.Fatalf("calls = %#v, want second explicit false decision", api.calls)
	}
}

func TestRunOpenReturnsOtherErrors(t *testing.T) {
	want := errors.New("boom")
	api := &fakeOpenAPI{firstErr: want}
	_, err := runOpen(context.Background(), nil, func() (string, error) { return "/work/api", nil }, api, strings.NewReader(""), io.Discard, true)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func writeTestExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
