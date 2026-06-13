package tmuxserver_test

import (
	"testing"

	"github.com/pilot322/tmux-coder/internal/tmuxserver"
)

func TestLabelDefaultsWhenUnset(t *testing.T) {
	got := tmuxserver.Label(func(string) string { return "" })
	if got != "tmux-coder" {
		t.Fatalf("Label() = %q, want %q", got, "tmux-coder")
	}
}

func TestLabelUsesEnvironment(t *testing.T) {
	got := tmuxserver.Label(func(key string) string {
		if key != tmuxserver.EnvName {
			t.Fatalf("key = %q, want %q", key, tmuxserver.EnvName)
		}
		return "tmux-coder-test-1"
	})
	if got != "tmux-coder-test-1" {
		t.Fatalf("Label() = %q, want %q", got, "tmux-coder-test-1")
	}
}
