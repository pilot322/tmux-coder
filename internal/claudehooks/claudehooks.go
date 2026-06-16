// Package claudehooks merges tmux-coder's activity-reporting hooks into a Claude
// Code settings.json. Claude Code reports no activity on its own; instead it runs
// external commands on lifecycle hooks. We bind each relevant hook to a
// `tmux-coder agent-event <status>` invocation so a wrapped Claude agent reports
// the same canonical busy/idle/waiting vocabulary the OpenCode plugin does.
//
// The mapping is stateless by design: every hook fires an independent process,
// so there is no shared "blocked" flag as in the OpenCode plugin. waiting is
// released naturally by the next busy/idle the agent emits (an approved tool
// fires PreToolUse=busy; a finished turn fires Stop=idle).
//
// Merge is a pure function over the settings bytes so the file IO stays in the
// command layer and the merge policy stays unit-testable.
package claudehooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// binding maps one Claude Code hook event to the activity status a fired hook
// should report. SubagentStop is deliberately absent: a finished subagent does
// not mean the main agent is idle, so binding it would clobber busy mid-turn.
type binding struct {
	event  string
	status string
}

var bindings = []binding{
	{"SessionStart", "idle"},
	{"UserPromptSubmit", "busy"},
	{"PreToolUse", "busy"},
	{"PostToolUse", "busy"},
	{"Notification", "waiting"},
	{"Stop", "idle"},
}

// markers identify a hook command as one we own, so re-running the installer
// replaces our prior entries (e.g. after the binary path changes) instead of
// appending duplicates. Both must be present; together they are unique to our
// `tmux-coder agent-event` commands.
const (
	markerBinary  = "tmux-coder"
	markerCommand = "agent-event"
)

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookEntry struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

// Merge returns settings with tmux-coder's hooks installed, preserving every
// other key and every foreign hook entry verbatim. binaryPath is the absolute
// path to the tmux-coder binary the hooks should invoke. Re-running over its own
// output is a no-op beyond reformatting: our prior entries are recognised and
// replaced rather than duplicated. existing may be nil or empty for a fresh file.
func Merge(existing []byte, binaryPath string) ([]byte, error) {
	root := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	}

	// Foreign hook entries are kept as raw JSON so fields we do not model survive
	// untouched; only our own entries are reconstructed.
	hooks := map[string][]json.RawMessage{}
	if raw, ok := root["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse hooks: %w", err)
		}
	}

	for _, b := range bindings {
		kept := make([]json.RawMessage, 0, len(hooks[b.event])+1)
		for _, entry := range hooks[b.event] {
			if !isOurs(entry) {
				kept = append(kept, entry)
			}
		}
		fresh, err := json.Marshal(hookEntry{
			Hooks: []hookCommand{{Type: "command", Command: command(binaryPath, b.status)}},
		})
		if err != nil {
			return nil, err
		}
		hooks[b.event] = append(kept, json.RawMessage(fresh))
	}

	hooksRaw, err := json.Marshal(hooks)
	if err != nil {
		return nil, err
	}
	root["hooks"] = hooksRaw

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// command renders the shell command for a binding. Hooks run through a shell, so
// the binary path is double-quoted to tolerate spaces.
func command(binaryPath, status string) string {
	return `"` + binaryPath + `" agent-event ` + status
}

// isOurs reports whether a hook entry was installed by tmux-coder.
func isOurs(entry json.RawMessage) bool {
	var e hookEntry
	if err := json.Unmarshal(entry, &e); err != nil {
		return false
	}
	for _, h := range e.Hooks {
		if strings.Contains(h.Command, markerBinary) && strings.Contains(h.Command, markerCommand) {
			return true
		}
	}
	return false
}
