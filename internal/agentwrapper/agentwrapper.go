// Package agentwrapper implements the long-running agent babysitter that tmux
// runs inside a pane. It starts an external agent process in its own process
// group, reports lifecycle events to the daemon, and forwards signals.
package agentwrapper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// AgentEventClient is the small subset of the daemon HTTP client needed by the
// wrapper to report lifecycle events.
type AgentEventClient interface {
	SendAgentStarted(ctx context.Context, id int, pgid int) error
	SendAgentEvent(ctx context.Context, id int, event string) error
}

// CommandRunner matches exec.CommandContext so tests can substitute process
// creation.
type CommandRunner func(ctx context.Context, name string, arg ...string) *exec.Cmd

// RunConfig parameterises a single wrapper invocation. All fields are required
// except Env, which defaults to os.Environ() when nil.
type RunConfig struct {
	Args           []string
	Getenv         func(string) string
	Env            []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	CommandContext CommandRunner
	NewClient      func(baseURL string, hc *http.Client) AgentEventClient
}

// Run starts the agent identified by args[0]=agentID and args[1]=kind, waits for
// it to finish, and returns the agent's exit code. It reports started/exited
// events to the daemon and forwards INT/TERM to the agent process group.
func Run(cfg RunConfig) int {
	if len(cfg.Args) < 2 {
		fmt.Fprintln(cfg.Stderr, "usage: tmux-coder agent-wrapper <agentID> <kind>")
		return 1
	}

	agentID, err := strconv.Atoi(cfg.Args[0])
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "invalid agent ID: %v\n", err)
		return 1
	}
	kind := cfg.Args[1]

	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}

	daemonAddr := DaemonBaseURL(configValue(cfg.Getenv, env, "TMUX_CODERD_ADDR"))
	paneID := configValue(cfg.Getenv, env, "TMUX_CODER_PANE_ID")
	if paneID == "" {
		paneID = CurrentPaneID(context.Background())
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	api := cfg.NewClient(daemonAddr, nil)

	cmd := cfg.CommandContext(context.Background(), kind)
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.Env = WithEnv(env, "TMUX_CODER_PANE_ID="+paneID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(cfg.Stderr, "failed to start %s: %v\n", kind, err)
		return 1
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}
	restoreTerminal, err := foregroundProcessGroup(cfg.Stdin, pgid)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "failed to give terminal to %s: %v\n", kind, err)
	} else {
		_ = syscall.Kill(-pgid, syscall.SIGCONT)
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	_ = api.SendAgentStarted(notifyCtx, agentID, pgid)

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case sig := <-sigCh:
		_ = syscall.Kill(-pgid, sig.(syscall.Signal))
		waitErr = <-waitCh
	}
	restoreTerminal()

	eventCtx, eventCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer eventCancel()
	_ = api.SendAgentEvent(eventCtx, agentID, "exited")

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(cfg.Stderr, "agent %s exited with error: %v\n", kind, waitErr)
		return 1
	}
	return 0
}

// DaemonBaseURL normalises a daemon address into a full URL.
func DaemonBaseURL(raw string) string {
	if raw == "" {
		return "http://127.0.0.1:64357"
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "http://" + raw
}

// CurrentPaneID asks tmux for the current pane id. It returns an empty string
// when tmux is unavailable.
func CurrentPaneID(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// WithEnv returns env with values added or replaced.
func WithEnv(env []string, values ...string) []string {
	out := append([]string{}, env...)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		replaced := false
		for i, existing := range out {
			if strings.HasPrefix(existing, key+"=") {
				out[i] = value
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, value)
		}
	}
	return out
}

func foregroundProcessGroup(stdin io.Reader, pgid int) (func(), error) {
	file, ok := stdin.(*os.File)
	if !ok || file == nil {
		return func() {}, nil
	}
	fd := file.Fd()
	original, err := terminalProcessGroup(fd)
	if err != nil {
		if errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.EINVAL) {
			return func() {}, nil
		}
		return func() {}, err
	}
	if err := setTerminalProcessGroup(fd, pgid); err != nil {
		return func() {}, err
	}
	return func() {
		ignoreSignalDuring(syscall.SIGTTOU, func() {
			_ = setTerminalProcessGroup(fd, original)
		})
	}, nil
}

func terminalProcessGroup(fd uintptr) (int, error) {
	var pgid int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&pgid)))
	if errno != 0 {
		return 0, errno
	}
	return int(pgid), nil
}

func setTerminalProcessGroup(fd uintptr, pgid int) error {
	v := int32(pgid)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&v)))
	if errno != 0 {
		return errno
	}
	return nil
}

func ignoreSignalDuring(sig os.Signal, fn func()) {
	signal.Ignore(sig)
	defer signal.Reset(sig)
	fn()
}

func configValue(getenv func(string) string, env []string, key string) string {
	if getenv != nil {
		if value := getenv(key); value != "" {
			return value
		}
	}
	for _, value := range env {
		name, v, ok := strings.Cut(value, "=")
		if ok && name == key {
			return v
		}
	}
	return ""
}
