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

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/pilot322/tmux-coder/internal/daemonaddr"
)

// AgentEventClient is the small subset of the daemon HTTP client needed by the
// wrapper to prepare and report an agent process.
type AgentEventClient interface {
	SendAgentStarted(ctx context.Context, id int, pgid int) error
	SendAgentEvent(ctx context.Context, id int, event string) error
	EnsureOpenCodeServer(ctx context.Context) (httpclient.OpenCodeConnection, error)
	WaitAgentSetup(ctx context.Context, id int) error
	SendAgentSetupFailed(ctx context.Context, id int, message string) error
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
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	api := cfg.NewClient(daemonAddr, nil)
	setupRequested := configValue(cfg.Getenv, env, "TMUX_CODER_AGENT_SETUP") == "1"
	commandArgs := []string{}
	if kind == "opencode" {
		serverCtx, serverCancel := context.WithTimeout(context.Background(), 15*time.Second)
		connection, serverErr := api.EnsureOpenCodeServer(serverCtx)
		serverCancel()
		if serverErr != nil || connection.URL == "" {
			message := fmt.Sprintf("connect to OpenCode v2 server: %v", serverErr)
			if serverErr == nil {
				message = "OpenCode server returned an empty URL"
			}
			if setupRequested {
				reportSetupFailure(api, agentID, message)
			}
			fmt.Fprintln(cfg.Stderr, message)
			return 1
		}
		password := connection.Password
		if configValue(cfg.Getenv, env, "TMUX_CODER_OPENCODE_SERVER_URL") != "" {
			if supplied := configValue(cfg.Getenv, env, "OPENCODE_PASSWORD"); supplied != "" {
				password = supplied
			} else if supplied := configValue(cfg.Getenv, env, "OPENCODE_SERVER_PASSWORD"); supplied != "" {
				password = supplied
			}
		}
		env = WithEnv(env, "OPENCODE_PASSWORD="+password)
		workingDir, cwdErr := os.Getwd()
		if cwdErr != nil {
			if setupRequested {
				reportSetupFailure(api, agentID, "resolve OpenCode pane working directory")
			}
			fmt.Fprintf(cfg.Stderr, "resolve OpenCode pane working directory: %v\n", cwdErr)
			return 1
		}
		commandArgs = append(commandArgs, "--server", connection.URL, workingDir)
	}

	binary := kind
	if kind == "opencode" {
		if selected := configValue(cfg.Getenv, env, "TMUX_CODER_OPENCODE_BINARY"); selected != "" {
			binary = selected
		}
	}
	cmd := cfg.CommandContext(context.Background(), binary, commandArgs...)
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.Env = WithEnv(env,
		"TMUX_CODER_PANE_ID="+paneID,
		"TMUX_CODER_AGENT_ID="+strconv.Itoa(agentID),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		if setupRequested {
			reportSetupFailure(api, agentID, fmt.Sprintf("start %s: %v", kind, err))
		}
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
	var setupCh chan error
	if setupRequested && configValue(cfg.Getenv, env, "TMUX_CODER_AGENT_SETUP_OWNER") != "daemon" {
		setupCh = make(chan error, 1)
		go func() {
			setupCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			setupCh <- api.WaitAgentSetup(setupCtx, agentID)
		}()
	}

	var waitErr error
	var setupErr error
wait:
	for {
		select {
		case waitErr = <-waitCh:
			break wait
		case sig := <-sigCh:
			_ = syscall.Kill(-pgid, sig.(syscall.Signal))
			waitErr = <-waitCh
			break wait
		case setupErr = <-setupCh:
			setupCh = nil
			if setupErr == nil {
				continue
			}
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			select {
			case waitErr = <-waitCh:
			case <-time.After(2 * time.Second):
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				waitErr = <-waitCh
			}
			break wait
		}
	}
	restoreTerminal()

	eventCtx, eventCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer eventCancel()
	_ = api.SendAgentEvent(eventCtx, agentID, "exited")
	if setupErr != nil {
		fmt.Fprintf(cfg.Stderr, "OpenCode startup setup failed: %v\n", setupErr)
		return 1
	}

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
		return daemonaddr.DefaultAddress()
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

func reportSetupFailure(api AgentEventClient, agentID int, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = api.SendAgentSetupFailed(ctx, agentID, message)
	_ = api.SendAgentEvent(ctx, agentID, "exited")
}
