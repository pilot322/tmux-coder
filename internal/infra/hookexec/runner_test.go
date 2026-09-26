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
	installFakeTmux(t)
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

	execution, err := hookexec.NewRunner(obs.Nop()).Start(context.Background(), usecase.WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktree,
		Timeout:    time.Second,
		Env: map[string]string{
			"OUT":                          outputPath,
			"TMUX_CODER_SESSION_NAME":      "api.feature",
			"TMUX_CODER_TMUX_SESSION_NAME": "api_feature",
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	result, err := execution.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v (output: %s)", err, result.Output)
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
	installFakeTmux(t)
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

	execution, err := hookexec.NewRunner(obs.Nop()).Start(context.Background(), usecase.WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktree,
		Timeout:    time.Second,
		Env: map[string]string{
			"TMUX_CODER_PROJECT_ID":        "42",
			"TMUX_CODER_BRANCH":            "feature/login",
			"TMUX_CODER_HOOK_TOKEN":        "super-secret-token",
			"TMUX_CODER_PROJECT_ROOT":      root,
			"TMUX_CODER_WORKTREE_ROOT":     worktree,
			"TMUX_CODER_TMUX_SESSION_NAME": "api_feature",
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	result, err := execution.Wait(context.Background())
	if err == nil {
		t.Fatal("Wait succeeded, want hook failure")
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

func TestRunDestroyLogsOutputAndBoundsExecution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMUX_CODER_TMUX_SERVER", "tmux-coder-destroy-test")
	t.Setenv("TMUX_CODER_HOOK_TOKEN", "inherited-secret")
	root := t.TempDir()
	script := filepath.Join(root, "destroy.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\npwd\nprintf 'token=%s branch=%s\\n' \"${TMUX_CODER_HOOK_TOKEN-unset}\" \"$TMUX_CODER_BRANCH\"\nprintf 'stderr\\n' >&2\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := hookexec.NewRunner(obs.Nop())
	req := usecase.WorktreeHookRequest{ScriptPath: script, WorkingDir: root, Timeout: time.Second, Env: map[string]string{"TMUX_CODER_BRANCH": "feature"}}
	result, err := runner.RunDestroy(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "status 9") {
		t.Fatalf("RunDestroy error = %v", err)
	}
	contents, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hook_kind: worktree-on-destroy", "hook_token_set: false", "token=unset branch=feature", "stderr", root} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("log missing %q: %s", want, contents)
		}
	}
	if !strings.Contains(filepath.Base(result.LogPath), "worktree-on-destroy") {
		t.Fatalf("log path = %q", result.LogPath)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho before-timeout\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	req.Timeout = 100 * time.Millisecond
	result, err = runner.RunDestroy(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v, output = %q", err, result.Output)
	}
	if !strings.Contains(result.Output, "before-timeout") {
		t.Fatalf("timeout output = %q", result.Output)
	}
}

func installFakeTmux(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	script := `#!/usr/bin/env bash
set -e
cwd=""
for ((i = 1; i < $#; i++)); do
  if [ "${!i}" = "-e" ]; then
    ((i++))
    export "${!i}"
  elif [ "${!i}" = "-c" ]; then
    ((i++))
    cwd="${!i}"
  fi
done
command="${!#}"
(cd "$cwd" && bash -c "$command" </dev/null >/dev/null 2>&1) &
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
