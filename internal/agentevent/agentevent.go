// Package agentevent implements the `tmux-coder agent-event` subcommand: a
// short-lived emitter that an agent kind's hook integration invokes to report a
// canonical activity status (busy/idle/waiting) for the agent it runs inside. It
// is the hook-driven counterpart to the OpenCode plugin's POST — the path Claude
// Code uses, since Claude Code reports activity through external hook commands
// rather than an in-process plugin.
//
// It identifies the agent solely from TMUX_CODER_AGENT_ID; when that is unset it
// is inert and exits 0, so the hooks can be installed globally and do nothing
// outside a tmux-coder-managed pane. Reporting is best-effort with a short
// timeout and every error is swallowed: a daemon that is down, restarting, or no
// longer recognises this agent id must never make a hook fail and stall the
// agent that triggered it.
//
// The status word a hook asks for is refined using the JSON payload Claude Code
// passes on stdin, because Claude's Notification hook is overloaded: it fires
// both when Claude is blocked on the user (a permission or question prompt =
// waiting) and as an idle nudge ~60s after a finished turn (idle_prompt). The
// latter must not flip a genuinely idle agent back to waiting. See
// resolveStatus.
package agentevent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/pilot322/tmux-coder/internal/agentwrapper"
)

// EventClient is the subset of the daemon HTTP client the emitter needs.
type EventClient interface {
	SendAgentEvent(ctx context.Context, id int, event string) error
}

// RunConfig parameterises a single emitter invocation. Getenv, Stderr and
// NewClient are required; Timeout defaults to one second when unset. Stdin is
// the hook payload Claude Code passes on stdin; it may be nil (then no payload
// refinement happens). The caller must pass nil when stdin is a terminal so a
// manual invocation never blocks reading it.
type RunConfig struct {
	Args      []string
	Getenv    func(string) string
	Stdin     io.Reader
	Stderr    io.Writer
	NewClient func(baseURL string, hc *http.Client) EventClient
	Timeout   time.Duration
}

// activityEvents is the closed set of statuses a hook integration may report.
// Lifecycle events (started/exited) are the wrapper's to send from OS facts, so
// a hook is never allowed to forge them.
var activityEvents = map[string]bool{"busy": true, "idle": true, "waiting": true}

// hookPayload is the subset of Claude Code's hook stdin JSON we read. Every hook
// carries hook_event_name; Notification additionally carries notification_type.
type hookPayload struct {
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
}

// Run reports the activity status named by args[0] for the agent identified by
// TMUX_CODER_AGENT_ID and returns a process exit code. The code is 0 in every
// case except a usage error, so a misfiring or absent daemon never breaks the
// hook that called it.
func Run(cfg RunConfig) int {
	if len(cfg.Args) < 1 {
		fmt.Fprintln(cfg.Stderr, "usage: tmux-coder agent-event <busy|idle|waiting>")
		return 2
	}
	event := cfg.Args[0]
	if !activityEvents[event] {
		fmt.Fprintf(cfg.Stderr, "agent-event: unsupported status %q\n", event)
		return 2
	}

	hook := readHookPayload(cfg.Stdin)
	status := resolveStatus(event, hook)
	trace(cfg, hook, status)

	rawID := cfg.Getenv("TMUX_CODER_AGENT_ID")
	if rawID == "" {
		return 0 // not wrapped by tmux-coder; stay inert
	}
	agentID, err := strconv.Atoi(rawID)
	if err != nil {
		return 0 // unusable id; stay inert rather than fail the hook
	}

	baseURL := agentwrapper.DaemonBaseURL(cfg.Getenv("TMUX_CODERD_ADDR"))
	api := cfg.NewClient(baseURL, nil)

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_ = api.SendAgentEvent(ctx, agentID, status) // best-effort, swallow errors
	return 0
}

// readHookPayload parses the hook JSON Claude Code passes on stdin. It is
// best-effort: a nil reader, a read error, or non-JSON input yields the zero
// payload, leaving the hook's requested status unrefined.
func readHookPayload(stdin io.Reader) hookPayload {
	var p hookPayload
	if stdin == nil {
		return p
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, 1<<16))
	if err != nil {
		return p
	}
	_ = json.Unmarshal(payload, &p)
	return p
}

// resolveStatus refines the status a Claude hook asked for using the hook
// payload. Claude's Notification hook is bound to waiting because it fires when
// Claude needs the user (a permission or question prompt). The same hook also
// fires an idle_prompt notification ~60s after a finished turn — a nudge that
// Claude is idle, not a request for attention — so that one reports idle and the
// rest stay as bound. Hooks without a payload are returned unchanged.
func resolveStatus(event string, hook hookPayload) string {
	if hook.HookEventName == "Notification" && hook.NotificationType == "idle_prompt" {
		return "idle"
	}
	return event
}

// trace appends one diagnostic line per invocation when
// TMUX_CODER_AGENT_EVENT_DEBUG names a file: the Claude hook that fired, its
// notification_type when present, and the status it ultimately reported. It is
// the hook-side counterpart to the OpenCode plugin's TMUX_CODER_PLUGIN_DEBUG and
// the only way to tell apart the several hooks that all report busy. It is
// best-effort: every error is swallowed so it cannot disturb the always-exit-0
// emitter path.
func trace(cfg RunConfig, hook hookPayload, status string) {
	path := cfg.Getenv("TMUX_CODER_AGENT_EVENT_DEBUG")
	if path == "" {
		return
	}

	hookEvent := hook.HookEventName
	if hookEvent == "" {
		hookEvent = "unknown"
	}
	agentID := cfg.Getenv("TMUX_CODER_AGENT_ID")
	if agentID == "" {
		agentID = "none"
	}

	line := time.Now().Format(time.RFC3339Nano) + " hook=" + hookEvent
	if hook.NotificationType != "" {
		line += " notification_type=" + hook.NotificationType
	}
	line += " status=" + status + " agent=" + agentID

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}
