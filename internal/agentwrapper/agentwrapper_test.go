package agentwrapper_test

import (
	"bytes"
	"context"
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
	started chan int
	events  []string
}

func (c *fakeClient) SendAgentStarted(ctx context.Context, id int, pgid int) error {
	c.started <- pgid
	return nil
}

func (c *fakeClient) SendAgentEvent(ctx context.Context, id int, event string) error {
	c.events = append(c.events, event)
	return nil
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
