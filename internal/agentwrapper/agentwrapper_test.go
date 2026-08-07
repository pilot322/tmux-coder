package agentwrapper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/agentwrapper"
)

type fakeClient struct {
	started     chan int
	events      []string
	serverURL   string
	serverErr   error
	ensureCalls int
}

func (c *fakeClient) SendAgentStarted(ctx context.Context, id int, pgid int) error {
	c.started <- pgid
	return nil
}

func (c *fakeClient) SendAgentEvent(ctx context.Context, id int, event string) error {
	c.events = append(c.events, event)
	return nil
}

func (c *fakeClient) EnsureOpenCodeServer(ctx context.Context) (string, error) {
	c.ensureCalls++
	return c.serverURL, c.serverErr
}

func (c *fakeClient) WaitAgentSetup(context.Context, int) error { return nil }

func (c *fakeClient) SendAgentSetupFailed(context.Context, int, string) error { return nil }

func (c *fakeClient) SendAgentSetupState(context.Context, int, string) error { return nil }

func TestRunOpencodeAttachesSharedServer(t *testing.T) {
	script := writeExecutable(t, "opencode", "#!/bin/sh\nexit 0\n")
	client := &fakeClient{started: make(chan int, 1), serverURL: "http://127.0.0.1:4567"}
	var name string
	var args []string

	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args:   []string{"7", "opencode"},
		Getenv: func(string) string { return "" },
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		CommandContext: func(ctx context.Context, command string, commandArgs ...string) *exec.Cmd {
			name = command
			args = append([]string{}, commandArgs...)
			return exec.CommandContext(ctx, script)
		},
		NewClient: func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if name != "opencode" {
		t.Fatalf("command = %q, want opencode", name)
	}
	if len(args) != 4 || args[0] != "attach" || args[1] != client.serverURL || args[2] != "--dir" || args[3] == "" {
		t.Fatalf("args = %#v, want attach URL and working directory", args)
	}
	if client.ensureCalls != 1 {
		t.Fatalf("EnsureOpenCodeServer calls = %d, want 1", client.ensureCalls)
	}
}

func TestRunOpencodeUsesConfiguredServerURL(t *testing.T) {
	script := writeExecutable(t, "opencode", "#!/bin/sh\nexit 0\n")
	client := &fakeClient{started: make(chan int, 1)}
	var args []string

	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args:   []string{"7", "opencode"},
		Env:    []string{"TMUX_CODER_OPENCODE_SERVER_URL=http://127.0.0.1:9876"},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		CommandContext: func(ctx context.Context, _ string, commandArgs ...string) *exec.Cmd {
			args = append([]string{}, commandArgs...)
			return exec.CommandContext(ctx, script)
		},
		NewClient: func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if len(args) < 2 || args[1] != "http://127.0.0.1:9876" {
		t.Fatalf("args = %#v, want configured URL", args)
	}
	if client.ensureCalls != 0 {
		t.Fatalf("EnsureOpenCodeServer calls = %d, want 0", client.ensureCalls)
	}
}

func TestRunOpencodeUsesSeededDisposableModelState(t *testing.T) {
	dir := t.TempDir()
	sourceRoot := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "opencode"), 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `{"recent":[{"providerID":"anthropic","modelID":"claude-haiku"},{"providerID":"other","modelID":"keep"}],"favorite":[{"providerID":"anthropic","modelID":"claude-haiku"}],"variant":{"other/keep":"high"}}`
	if err := os.WriteFile(filepath.Join(sourceRoot, "opencode", "model.json"), []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "captured")
	script := writeExecutable(t, "opencode", "#!/bin/sh\nprintf '%s\\n' \"$XDG_STATE_HOME\" > \"$MARKER\"\ncat \"$XDG_STATE_HOME/opencode/model.json\" >> \"$MARKER\"\n")
	client := &fakeClient{started: make(chan int, 1)}
	env := []string{
		"XDG_STATE_HOME=" + sourceRoot,
		"MARKER=" + marker,
		"TMUX_CODER_OPENCODE_SERVER_URL=http://127.0.0.1:9876",
		"TMUX_CODER_AGENT_SETUP=1",
		"TMUX_CODER_AGENT_SETUP_OWNER=daemon",
		"TMUX_CODER_AGENT_MODEL=anthropic/claude-haiku",
	}
	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args: []string{"7", "opencode"}, Env: env,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, CommandContext: func(context.Context, string, ...string) *exec.Cmd {
			return exec.Command(script)
		},
		NewClient: func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	captured, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	root, stateJSON, ok := strings.Cut(string(captured), "\n")
	if !ok {
		t.Fatalf("captured = %q", captured)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated state root still exists: %v", err)
	}
	var state struct {
		Recent   []map[string]string `json:"recent"`
		Favorite []map[string]string `json:"favorite"`
		Variant  map[string]string   `json:"variant"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Recent) != 1 || state.Recent[0]["modelID"] != "keep" {
		t.Fatalf("recent = %#v", state.Recent)
	}
	if len(state.Favorite) != 1 || state.Variant["other/keep"] != "high" || state.Variant["anthropic/claude-haiku"] != "" {
		t.Fatalf("seeded state = %#v", state)
	}
}

func TestRunNonOpencodeKeepsZeroArgumentCommand(t *testing.T) {
	script := writeExecutable(t, "claude", "#!/bin/sh\nexit 0\n")
	client := &fakeClient{started: make(chan int, 1)}
	var name string
	var args []string

	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args:   []string{"7", "claude"},
		Getenv: func(string) string { return "" },
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		CommandContext: func(ctx context.Context, command string, commandArgs ...string) *exec.Cmd {
			name = command
			args = append([]string{}, commandArgs...)
			return exec.CommandContext(ctx, script)
		},
		NewClient: func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if name != "claude" || len(args) != 0 {
		t.Fatalf("command = %q, args = %#v; want claude with no arguments", name, args)
	}
}

func TestRunInjectsPaneEnvAndDispatchesEvents(t *testing.T) {
	script := writeExecutable(t, "agent", "#!/bin/sh\nprintf '%s' \"$TMUX_CODER_PANE_ID\"\n")
	client := &fakeClient{started: make(chan int, 1)}
	var stdout bytes.Buffer

	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args: []string{"7", script},
		Getenv: func(key string) string {
			if key == "TMUX_CODER_PANE_ID" {
				return "%55"
			}
			return ""
		},
		Stdin:          nil,
		Stdout:         &stdout,
		Stderr:         &bytes.Buffer{},
		CommandContext: exec.CommandContext,
		NewClient:      func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.String() != "%55" {
		t.Fatalf("stdout = %q, want pane id", stdout.String())
	}
	pgid := <-client.started
	if pgid <= 0 {
		t.Fatalf("pgid = %d, want positive", pgid)
	}
	if len(client.events) != 1 || client.events[0] != "exited" {
		t.Fatalf("events = %#v", client.events)
	}
}

func TestRunInjectsAgentIDEnv(t *testing.T) {
	script := writeExecutable(t, "agent", "#!/bin/sh\nprintf '%s' \"$TMUX_CODER_AGENT_ID\"\n")
	client := &fakeClient{started: make(chan int, 1)}
	var stdout bytes.Buffer

	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args:           []string{"7", script},
		Getenv:         func(string) string { return "" },
		Stdin:          nil,
		Stdout:         &stdout,
		Stderr:         &bytes.Buffer{},
		CommandContext: exec.CommandContext,
		NewClient:      func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if stdout.String() != "7" {
		t.Fatalf("stdout = %q, want agent id 7 in child env", stdout.String())
	}
}

func TestRunReturnsChildExitCode(t *testing.T) {
	script := writeExecutable(t, "agent", "#!/bin/sh\nexit 23\n")
	client := &fakeClient{started: make(chan int, 1)}
	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args:           []string{"7", script},
		Getenv:         func(string) string { return "" },
		Stdout:         &bytes.Buffer{},
		Stderr:         &bytes.Buffer{},
		CommandContext: exec.CommandContext,
		NewClient:      func(string, *http.Client) agentwrapper.AgentEventClient { return client },
	})
	if code != 23 {
		t.Fatalf("exit code = %d, want 23", code)
	}
}

func TestRunReadsDaemonAddrFromConfigEnv(t *testing.T) {
	script := writeExecutable(t, "agent", "#!/bin/sh\nexit 0\n")
	client := &fakeClient{started: make(chan int, 1)}
	var baseURL string

	code := agentwrapper.Run(agentwrapper.RunConfig{
		Args:           []string{"7", script},
		Getenv:         func(string) string { return "" },
		Env:            []string{"TMUX_CODERD_ADDR=127.0.0.1:7000"},
		Stdout:         &bytes.Buffer{},
		Stderr:         &bytes.Buffer{},
		CommandContext: exec.CommandContext,
		NewClient: func(url string, _ *http.Client) agentwrapper.AgentEventClient {
			baseURL = url
			return client
		},
	})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if baseURL != "http://127.0.0.1:7000" {
		t.Fatalf("baseURL = %q", baseURL)
	}
}

func TestRunForwardsSignalsToChildProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "term")
	ready := filepath.Join(dir, "ready")
	script := writeExecutable(t, "agent", "#!/bin/sh\ntrap 'printf term > \"$1\"; exit 0' TERM\nprintf ready > \"$2\"\nwhile true; do sleep 1 & wait $!; done\n")
	client := &fakeClient{started: make(chan int, 1)}
	done := make(chan int, 1)
	go func() {
		done <- agentwrapper.Run(agentwrapper.RunConfig{
			Args:           []string{"7", scriptWithArgs(t, script, marker, ready)},
			Getenv:         func(string) string { return "" },
			Stdout:         &bytes.Buffer{},
			Stderr:         &bytes.Buffer{},
			CommandContext: exec.CommandContext,
			NewClient:      func(string, *http.Client) agentwrapper.AgentEventClient { return client },
		})
	}()

	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("child did not start")
	}
	waitForFile(t, ready)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 && code != -1 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not exit after signal")
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "term" {
		t.Fatalf("marker = %q", data)
	}
}

func writeExecutable(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func scriptWithArgs(t *testing.T, script string, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-with-arg")
	body := "#!/bin/sh\nexec " + strconv.Quote(script)
	for _, arg := range args {
		body += " " + strconv.Quote(arg)
	}
	body += "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s was not created", path)
}

func TestDaemonBaseURL(t *testing.T) {
	if got := agentwrapper.DaemonBaseURL(""); got != "http://127.0.0.1:64357" {
		t.Fatalf("default = %q", got)
	}
	if got := agentwrapper.DaemonBaseURL("127.0.0.1:7000"); got != "http://127.0.0.1:7000" {
		t.Fatalf("host = %q", got)
	}
	if got := agentwrapper.DaemonBaseURL("http://localhost:7000"); !strings.HasPrefix(got, "http://") {
		t.Fatalf("url = %q", got)
	}
}
