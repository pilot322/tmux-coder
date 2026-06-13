package process_test

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/infra/process"
)

func TestTerminateProcessGroupIgnoresInvalidPGID(t *testing.T) {
	if err := process.NewProcessGateway().TerminateProcessGroup(context.Background(), 0, time.Millisecond); err != nil {
		t.Fatalf("TerminateProcessGroup: %v", err)
	}
}

func TestTerminateProcessGroupTerminatesChildGroup(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}

	if err := process.NewProcessGateway().TerminateProcessGroup(context.Background(), pgid, 50*time.Millisecond); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("TerminateProcessGroup: %v", err)
	}
	_ = cmd.Wait()
	if err := syscall.Kill(-pgid, 0); err == nil {
		t.Fatal("process group still exists")
	}
}
