package binresolve_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/binresolve"
)

func TestResolveSiblingThenPathPrefersSibling(t *testing.T) {
	dir := t.TempDir()
	client := filepath.Join(dir, "tmux-coder")
	if err := os.WriteFile(client, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := binresolve.ResolveSiblingThenPath(filepath.Join(dir, "tmux-coderd"), "tmux-coder", func(string) (string, error) {
		return "/path/tmux-coder", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != client {
		t.Fatalf("binary = %q, want %q", got, client)
	}
}

func TestResolveSiblingThenPathFallsBackToPath(t *testing.T) {
	got, err := binresolve.ResolveSiblingThenPath("/missing/tmux-coderd", "tmux-coder", func(name string) (string, error) {
		if name != "tmux-coder" {
			t.Fatalf("name = %q", name)
		}
		return "/bin/tmux-coder", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/bin/tmux-coder" {
		t.Fatalf("binary = %q", got)
	}
}

func TestResolveSiblingThenPathReturnsLookPathError(t *testing.T) {
	_, err := binresolve.ResolveSiblingThenPath("/missing/tmux-coderd", "tmux-coder", func(string) (string, error) {
		return "", errors.New("nope")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
