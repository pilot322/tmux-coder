package obs

import (
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/tmuxserver"
)

func TestLogDirInstalledLabel(t *testing.T) {
	home := t.TempDir()

	got, err := LogDir(RoleDaemon, getenvFor(home, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".tmux-coder", "logs", "daemon")
	if got != want {
		t.Fatalf("LogDir = %q, want %q", got, want)
	}
}

func TestLogDirDevLabelNestsUnderDevWorktree(t *testing.T) {
	home := t.TempDir()

	got, err := LogDir(RoleTUI, getenvFor(home, "tmux-coder-foo"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".tmux-coder", "logs", "dev-foo", "tui")
	if got != want {
		t.Fatalf("LogDir = %q, want %q", got, want)
	}
}

func TestLogDirDevDefaultLabelNestsUnderDevWorktree(t *testing.T) {
	home := t.TempDir()
	original := tmuxserver.DefaultLabel
	tmuxserver.DefaultLabel = "tmux-coder-feature"
	t.Cleanup(func() { tmuxserver.DefaultLabel = original })

	got, err := LogDir(RoleDaemon, getenvFor(home, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".tmux-coder", "logs", "dev-feature", "daemon")
	if got != want {
		t.Fatalf("LogDir = %q, want %q", got, want)
	}
}

func TestLogDirInstalledEnvLabelOverridesDevDefault(t *testing.T) {
	home := t.TempDir()
	original := tmuxserver.DefaultLabel
	tmuxserver.DefaultLabel = "tmux-coder-feature"
	t.Cleanup(func() { tmuxserver.DefaultLabel = original })

	got, err := LogDir(RoleDaemon, getenvFor(home, "tmux-coder"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".tmux-coder", "logs", "daemon")
	if got != want {
		t.Fatalf("LogDir = %q, want %q", got, want)
	}
}

func TestLogDirHonorsServerEnvForEachRole(t *testing.T) {
	home := t.TempDir()
	cases := map[Role]string{
		RoleDaemon:     "daemon",
		RoleTUI:        "tui",
		RoleAgentEvent: "agent-event",
		RoleWrapper:    "wrapper",
	}
	for role, sub := range cases {
		got, err := LogDir(role, getenvFor(home, "tmux-coder-feat-observability"))
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".tmux-coder", "logs", "dev-feat-observability", sub)
		if got != want {
			t.Fatalf("LogDir(%q) = %q, want %q", role, got, want)
		}
	}
}

// getenvFor builds a getenv that reports the given HOME and tmux server label,
// so LogDir can be exercised without mutating the real process environment.
func getenvFor(home, label string) func(string) string {
	return func(key string) string {
		switch key {
		case "HOME":
			return home
		case "TMUX_CODER_TMUX_SERVER":
			return label
		default:
			return ""
		}
	}
}
