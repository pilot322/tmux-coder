package hookexec_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/infra/hookexec"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestRunnerInvokesExecutableWithWorkingDirAndEnv(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "hook.out")
	scriptPath := filepath.Join(root, "hook.sh")
	script := "#!/bin/sh\npwd > \"$OUT\"\nprintf '%s\\n' \"$TMUX_CODER_SESSION_NAME\" >> \"$OUT\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := hookexec.NewRunner(obs.Nop()).Run(context.Background(), usecase.WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktree,
		Timeout:    time.Second,
		Env: map[string]string{
			"OUT":                     outputPath,
			"TMUX_CODER_SESSION_NAME": "api.feature",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v (output: %s)", err, result.Output)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(got) != 2 || got[0] != worktree || got[1] != "api.feature" {
		t.Fatalf("hook output file = %q", contents)
	}
}

func TestRunnerWritesFailureOutputToHookLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMUX_CODER_TMUX_SERVER", "tmux-coder-hook-test")

	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "hook.sh")
	script := "#!/bin/sh\nprintf 'hello stdout\\n'\nprintf 'hello stderr\\n' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := hookexec.NewRunner(obs.Nop()).Run(context.Background(), usecase.WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktree,
		Timeout:    time.Second,
		Env: map[string]string{
			"TMUX_CODER_PROJECT_ID":    "42",
			"TMUX_CODER_BRANCH":        "feature/login",
			"TMUX_CODER_HOOK_TOKEN":    "super-secret-token",
			"TMUX_CODER_PROJECT_ROOT":  root,
			"TMUX_CODER_WORKTREE_ROOT": worktree,
		},
	})
	if err == nil {
		t.Fatal("Run succeeded, want hook failure")
	}
	if result.LogPath == "" {
		t.Fatal("LogPath is empty")
	}
	wantDir := filepath.Join(home, ".tmux-coder", "logs", "dev-hook-test", "daemon", "hooks")
	if filepath.Dir(result.LogPath) != wantDir {
		t.Fatalf("LogPath dir = %q, want %q", filepath.Dir(result.LogPath), wantDir)
	}
	contents, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(contents)
	for _, want := range []string{"hook_kind: worktree-on-create", "project_id: 42", "branch: feature/login", "hook_token_set: true", "hello stdout", "hello stderr"} {
		if !strings.Contains(log, want) {
			t.Fatalf("hook log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "super-secret-token") {
		t.Fatalf("hook log leaked hook token:\n%s", log)
	}
}
