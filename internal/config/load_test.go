package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestLoadErrorsIncludeSourcePath(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "unknown = true\n")
	_, err := config.Load(root)
	if err == nil || !errors.Is(err, config.ErrValidation) {
		t.Fatalf("Load error = %v", err)
	}
	if path := config.ProjectPath(root); !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not include %q", err, path)
	}
}

func TestLoadActionFileMissingAndInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.toml")
	actions, err := config.LoadActionFile(path)
	if err != nil || len(actions) != 0 {
		t.Fatalf("missing action file = %+v, %v", actions, err)
	}
	if err := os.WriteFile(path, []byte("[[menu-actions]]\nname = \"Bad\"\nscript = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = config.LoadActionFile(path)
	if err == nil || !errors.Is(err, config.ErrValidation) || !strings.Contains(err.Error(), path) {
		t.Fatalf("invalid action file error = %v", err)
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
