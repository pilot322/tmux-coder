package hookexec_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/infra/hookexec"
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

	result, err := hookexec.NewRunner().Run(context.Background(), usecase.WorktreeHookRequest{
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
