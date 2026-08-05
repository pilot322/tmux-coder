package hookexec_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/infra/hookexec"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestRunnerRealTmuxFailureWaitsForEnter(t *testing.T) {
	if os.Getenv("TMUX_CODER_E2E") == "" {
		t.Skip("set TMUX_CODER_E2E=1 to run real tmux integration tests")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	label := fmt.Sprintf("tmux-coder-hook-e2e-%d", os.Getpid())
	t.Setenv("TMUX_CODER_TMUX_SERVER", label)
	defer exec.Command("tmux", "-L", label, "kill-server").Run()

	worktree := t.TempDir()
	scriptPath := filepath.Join(worktree, "setup.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'visible failure output\\n'\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("tmux", "-L", label, "new-session", "-d", "-s", "api_feature", "-c", worktree).CombinedOutput(); err != nil {
		t.Fatalf("create tmux session: %v: %s", err, output)
	}

	execution, err := hookexec.NewRunner(obs.Nop()).Start(context.Background(), usecase.WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktree,
		Timeout:    time.Second,
		Env: map[string]string{
			"TMUX_CODER_TMUX_SESSION_NAME": "api_feature",
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := execution.Wait(context.Background()); err == nil {
		t.Fatal("Wait succeeded, want hook failure")
	}

	pane, err := exec.Command("tmux", "-L", label, "capture-pane", "-p", "-t", "api_feature:worktree-setup").CombinedOutput()
	if err != nil {
		t.Fatalf("capture setup pane: %v: %s", err, pane)
	}
	for _, want := range []string{"visible failure output", "Press Enter to remove this Worktree Session"} {
		if !strings.Contains(string(pane), want) {
			t.Fatalf("setup pane missing %q:\n%s", want, pane)
		}
	}

	acknowledged := make(chan error, 1)
	go func() { acknowledged <- execution.WaitForAcknowledgement(context.Background()) }()
	select {
	case err := <-acknowledged:
		t.Fatalf("acknowledgement returned before Enter: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if output, err := exec.Command("tmux", "-L", label, "send-keys", "-t", "api_feature:worktree-setup", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send Enter: %v: %s", err, output)
	}
	select {
	case err := <-acknowledged:
		if err != nil {
			t.Fatalf("WaitForAcknowledgement: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("acknowledgement did not complete after Enter")
	}

	if output, err := exec.Command("tmux", "-L", label, "set-option", "-g", "remain-on-exit", "on").CombinedOutput(); err != nil {
		t.Fatalf("enable remain-on-exit: %v: %s", err, output)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'successful setup\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	execution, err = hookexec.NewRunner(obs.Nop()).Start(context.Background(), usecase.WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktree,
		Timeout:    time.Second,
		Env: map[string]string{
			"TMUX_CODER_TMUX_SESSION_NAME": "api_feature",
		},
	})
	if err != nil {
		t.Fatalf("Start successful hook: %v", err)
	}
	if _, err := execution.Wait(context.Background()); err != nil {
		t.Fatalf("Wait successful hook: %v", err)
	}
	windows, err := exec.Command("tmux", "-L", label, "list-windows", "-t", "api_feature", "-F", "#{window_name}").CombinedOutput()
	if err != nil {
		t.Fatalf("list windows after success: %v: %s", err, windows)
	}
	if strings.Contains(string(windows), "worktree-setup") {
		t.Fatalf("successful setup window remained with remain-on-exit enabled: %s", windows)
	}
}
