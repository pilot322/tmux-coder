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
	wrapper := filepath.Join(dir, "tmux-coderd-wrapper")
	if err := os.WriteFile(wrapper, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := binresolve.ResolveSiblingThenPath(filepath.Join(dir, "tmux-coderd"), "tmux-coderd-wrapper", func(string) (string, error) {
		return "/path/tmux-coderd-wrapper", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != wrapper {
		t.Fatalf("binary = %q, want %q", got, wrapper)
	}
}

func TestResolveSiblingThenPathFallsBackToPath(t *testing.T) {
	got, err := binresolve.ResolveSiblingThenPath("/missing/tmux-coderd", "tmux-coderd-wrapper", func(name string) (string, error) {
		if name != "tmux-coderd-wrapper" {
			t.Fatalf("name = %q", name)
		}
		return "/bin/tmux-coderd-wrapper", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/bin/tmux-coderd-wrapper" {
		t.Fatalf("binary = %q", got)
	}
}

func TestResolveSiblingThenPathReturnsLookPathError(t *testing.T) {
	_, err := binresolve.ResolveSiblingThenPath("/missing/tmux-coderd", "tmux-coderd-wrapper", func(string) (string, error) {
		return "", errors.New("nope")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
