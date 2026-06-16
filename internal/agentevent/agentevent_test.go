package agentevent_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/agentevent"
)

type fakeClient struct {
	baseURL string
	id      int
	event   string
	calls   int
}

func (c *fakeClient) SendAgentEvent(ctx context.Context, id int, event string) error {
	c.calls++
	c.id = id
	c.event = event
	return nil
}

func newRun(client *fakeClient, env map[string]string, args ...string) (int, *bytes.Buffer) {
	return newRunStdin(client, env, nil, args...)
}

func newRunStdin(client *fakeClient, env map[string]string, stdin io.Reader, args ...string) (int, *bytes.Buffer) {
	stderr := &bytes.Buffer{}
	code := agentevent.Run(agentevent.RunConfig{
		Args:   args,
		Getenv: func(key string) string { return env[key] },
		Stdin:  stdin,
		Stderr: stderr,
		NewClient: func(baseURL string, _ *http.Client) agentevent.EventClient {
			client.baseURL = baseURL
			return client
		},
	})
	return code, stderr
}

func TestReportsActivityForWrappedAgent(t *testing.T) {
	client := &fakeClient{}
	code, _ := newRun(client, map[string]string{
		"TMUX_CODER_AGENT_ID": "7",
		"TMUX_CODERD_ADDR":    "127.0.0.1:7000",
	}, "busy")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if client.calls != 1 || client.id != 7 || client.event != "busy" {
		t.Fatalf("client got calls=%d id=%d event=%q", client.calls, client.id, client.event)
	}
	if client.baseURL != "http://127.0.0.1:7000" {
		t.Fatalf("baseURL = %q", client.baseURL)
	}
}

func TestInertWithoutAgentID(t *testing.T) {
	client := &fakeClient{}
	code, _ := newRun(client, map[string]string{}, "idle")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if client.calls != 0 {
		t.Fatalf("client called %d times, want 0 when unwrapped", client.calls)
	}
}

func TestInertWithUnparsableAgentID(t *testing.T) {
	client := &fakeClient{}
	code, _ := newRun(client, map[string]string{"TMUX_CODER_AGENT_ID": "not-a-number"}, "idle")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if client.calls != 0 {
		t.Fatalf("client called %d times, want 0 for unusable id", client.calls)
	}
}

func TestRejectsLifecycleAndUnknownStatuses(t *testing.T) {
	for _, status := range []string{"started", "exited", "running", "nonsense", ""} {
		client := &fakeClient{}
		code, stderr := newRun(client, map[string]string{"TMUX_CODER_AGENT_ID": "7"}, status)
		if code != 2 {
			t.Fatalf("status %q: exit code = %d, want 2", status, code)
		}
		if client.calls != 0 {
			t.Fatalf("status %q: client called %d times, want 0", status, client.calls)
		}
		if stderr.Len() == 0 {
			t.Fatalf("status %q: expected an error on stderr", status)
		}
	}
}

// Claude's Notification hook is bound to waiting because it fires when Claude
// needs the user, but it also fires an idle_prompt nudge ~60s after a finished
// turn. That one must report idle so a finished agent does not flip to waiting.
func TestNotificationIdlePromptReportsIdle(t *testing.T) {
	client := &fakeClient{}
	payload := strings.NewReader(`{"hook_event_name":"Notification","notification_type":"idle_prompt","message":"Claude is waiting for your input"}`)
	code, _ := newRunStdin(client, map[string]string{"TMUX_CODER_AGENT_ID": "7"}, payload, "waiting")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if client.event != "idle" {
		t.Fatalf("idle_prompt notification reported %q, want idle", client.event)
	}
}

// A permission/question notification is the genuine waiting case and must be
// left untouched.
func TestNotificationPermissionPromptReportsWaiting(t *testing.T) {
	client := &fakeClient{}
	payload := strings.NewReader(`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}`)
	code, _ := newRunStdin(client, map[string]string{"TMUX_CODER_AGENT_ID": "7"}, payload, "waiting")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if client.event != "waiting" {
		t.Fatalf("permission_prompt notification reported %q, want waiting", client.event)
	}
}

// Without a payload (nil stdin, a hand-run hook, or non-JSON) the requested
// status is reported verbatim.
func TestStatusUnchangedWithoutPayload(t *testing.T) {
	client := &fakeClient{}
	code, _ := newRunStdin(client, map[string]string{"TMUX_CODER_AGENT_ID": "7"}, nil, "waiting")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if client.event != "waiting" {
		t.Fatalf("reported %q, want waiting", client.event)
	}
}

// The debug trace records the firing hook, its notification_type, and the
// reported status when TMUX_CODER_AGENT_EVENT_DEBUG names a file.
func TestDebugTraceRecordsHookAndStatus(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.log")
	client := &fakeClient{}
	payload := strings.NewReader(`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`)
	code, _ := newRunStdin(client, map[string]string{
		"TMUX_CODER_AGENT_ID":          "7",
		"TMUX_CODER_AGENT_EVENT_DEBUG": tracePath,
	}, payload, "waiting")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("trace file not written: %v", err)
	}
	line := string(data)
	for _, want := range []string{"hook=Notification", "notification_type=idle_prompt", "status=idle", "agent=7"} {
		if !strings.Contains(line, want) {
			t.Fatalf("trace %q missing %q", line, want)
		}
	}
}

// With no debug file set, nothing is traced.
func TestDebugTraceOffByDefault(t *testing.T) {
	client := &fakeClient{}
	payload := strings.NewReader(`{"hook_event_name":"Stop"}`)
	if _, _ = newRunStdin(client, map[string]string{"TMUX_CODER_AGENT_ID": "7"}, payload, "idle"); client.event != "idle" {
		t.Fatalf("reported %q, want idle", client.event)
	}
}

func TestUsageWithoutArgs(t *testing.T) {
	client := &fakeClient{}
	stderr := &bytes.Buffer{}
	code := agentevent.Run(agentevent.RunConfig{
		Args:   nil,
		Getenv: func(string) string { return "" },
		Stderr: stderr,
		NewClient: func(string, *http.Client) agentevent.EventClient {
			t.Fatal("client must not be constructed on usage error")
			return client
		},
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
