package claudehooks_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/claudehooks"
)

type settings struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func parse(t *testing.T, raw []byte) settings {
	t.Helper()
	var s settings
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}
	return s
}

// commandsFor returns every hook command bound to an event.
func commandsFor(s settings, event string) []string {
	var cmds []string
	for _, entry := range s.Hooks[event] {
		for _, h := range entry.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

func TestMergeFromEmptyInstallsAllBindings(t *testing.T) {
	out, err := claudehooks.Merge(nil, "/usr/local/bin/tmux-coder")
	if err != nil {
		t.Fatal(err)
	}
	s := parse(t, out)

	want := map[string]string{
		"SessionStart":     "idle",
		"UserPromptSubmit": "busy",
		"PreToolUse":       "busy",
		"PostToolUse":      "busy",
		"Notification":     "waiting",
		"Stop":             "idle",
	}
	if len(s.Hooks) != len(want) {
		t.Fatalf("got %d hook events, want %d: %v", len(s.Hooks), len(want), s.Hooks)
	}
	for event, status := range want {
		cmds := commandsFor(s, event)
		if len(cmds) != 1 {
			t.Fatalf("%s: got %d commands, want 1", event, len(cmds))
		}
		wantCmd := `"/usr/local/bin/tmux-coder" agent-event ` + status
		if cmds[0] != wantCmd {
			t.Fatalf("%s: command = %q, want %q", event, cmds[0], wantCmd)
		}
	}
}

func TestMergePreservesForeignKeysAndHooks(t *testing.T) {
	existing := []byte(`{
	  "model": "opus",
	  "hooks": {
	    "Stop": [
	      { "hooks": [ { "type": "command", "command": "/home/me/notify.sh" } ] }
	    ],
	    "PreCompact": [
	      { "hooks": [ { "type": "command", "command": "/home/me/save.sh" } ] }
	    ]
	  }
	}`)

	out, err := claudehooks.Merge(existing, "/bin/tmux-coder")
	if err != nil {
		t.Fatal(err)
	}

	// Foreign top-level key survives.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(out, &root); err != nil {
		t.Fatal(err)
	}
	if string(root["model"]) != `"opus"` {
		t.Fatalf("model key = %s, want \"opus\"", root["model"])
	}

	s := parse(t, out)
	// Foreign PreCompact hook is untouched.
	if got := commandsFor(s, "PreCompact"); len(got) != 1 || got[0] != "/home/me/save.sh" {
		t.Fatalf("PreCompact commands = %v, want the foreign save.sh", got)
	}
	// Our Stop hook is added alongside the user's existing Stop hook.
	stop := commandsFor(s, "Stop")
	if len(stop) != 2 {
		t.Fatalf("Stop commands = %v, want foreign + ours", stop)
	}
	if !contains(stop, "/home/me/notify.sh") {
		t.Fatalf("Stop lost the foreign notify.sh: %v", stop)
	}
	if !contains(stop, `"/bin/tmux-coder" agent-event idle`) {
		t.Fatalf("Stop missing our command: %v", stop)
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	once, err := claudehooks.Merge(nil, "/bin/tmux-coder")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := claudehooks.Merge(once, "/bin/tmux-coder")
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Fatalf("re-merge changed output:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
	s := parse(t, twice)
	if got := commandsFor(s, "Stop"); len(got) != 1 {
		t.Fatalf("Stop has %d commands after re-merge, want 1 (no duplicates)", len(got))
	}
}

func TestMergeReplacesStaleBinaryPath(t *testing.T) {
	old, err := claudehooks.Merge(nil, "/old/path/tmux-coder")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := claudehooks.Merge(old, "/new/path/tmux-coder")
	if err != nil {
		t.Fatal(err)
	}
	s := parse(t, updated)
	stop := commandsFor(s, "Stop")
	if len(stop) != 1 {
		t.Fatalf("Stop has %d commands, want 1 after path update: %v", len(stop), stop)
	}
	if !strings.Contains(stop[0], "/new/path/tmux-coder") {
		t.Fatalf("Stop command not updated to new path: %q", stop[0])
	}
}

func TestMergeRejectsInvalidJSON(t *testing.T) {
	if _, err := claudehooks.Merge([]byte("{not json"), "/bin/tmux-coder"); err == nil {
		t.Fatal("expected an error for malformed settings")
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
