package daemon_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/client/daemon"
)

func TestResolveBinaryPrefersSibling(t *testing.T) {
	dir := t.TempDir()
	sibling := filepath.Join(dir, "tmux-coderd")
	if err := os.WriteFile(sibling, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := daemon.ResolveBinary(filepath.Join(dir, "tmux-coder"), func(string) (string, error) {
		return "/path/tmux-coderd", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != sibling {
		t.Fatalf("binary = %q, want %q", got, sibling)
	}
}

func TestResolveBinaryFallsBackToPath(t *testing.T) {
	got, err := daemon.ResolveBinary("/missing/tmux-coder", func(name string) (string, error) {
		if name != "tmux-coderd" {
			t.Fatalf("lookpath name = %q", name)
		}
		return "/bin/tmux-coderd", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/bin/tmux-coderd" {
		t.Fatalf("binary = %q", got)
	}
}

func TestResolveBinaryReturnsLookPathError(t *testing.T) {
	_, err := daemon.ResolveBinary("/missing/tmux-coder", func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
