package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/config"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	file, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if file.Worktree.OnCreateTimeout != config.DefaultWorktreeHookTimeout {
		t.Errorf("timeout = %v, want default", file.Worktree.OnCreateTimeout)
	}
	if len(file.Secondaries) != 0 {
		t.Errorf("secondaries = %d, want 0", len(file.Secondaries))
	}
}

func TestLoadReadsAndValidates(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "[worktree]\non-create-timeout = \"45s\"\n\n[[secondary-sessions]]\nsubdir = \"backend\"\n")

	file, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if file.Worktree.OnCreateTimeout != 45*time.Second {
		t.Errorf("timeout = %v, want 45s", file.Worktree.OnCreateTimeout)
	}
	if len(file.Secondaries) != 1 || file.Secondaries[0].Subdir != "backend" {
		t.Errorf("secondaries = %+v", file.Secondaries)
	}
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".tmux-coder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmux-coder.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
