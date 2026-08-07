// Package agentwrapper implements the long-running agent babysitter that tmux
// runs inside a pane. It starts an external agent process in its own process
// group, reports lifecycle events to the daemon, and forwards signals.
package agentwrapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/pilot322/tmux-coder/internal/daemonaddr"
)

// AgentEventClient is the small subset of the daemon HTTP client needed by the
// wrapper to prepare and report an agent process.
type AgentEventClient interface {
	SendAgentStarted(ctx context.Context, id int, pgid int) error
	SendAgentEvent(ctx context.Context, id int, event string) error
	EnsureOpenCodeServer(ctx context.Context) (string, error)
	WaitAgentSetup(ctx context.Context, id int) error
	SendAgentSetupFailed(ctx context.Context, id int, message string) error
	SendAgentSetupState(ctx context.Context, id int, statePath string) error
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
	model := configValue(cfg.Getenv, env, "TMUX_CODER_AGENT_MODEL")
	if kind == "opencode" && model != "" {
		var stateRoot string
		env, stateRoot, err = prepareOpenCodeState(env, model)
		if err != nil {
			reportSetupFailure(api, agentID, fmt.Sprintf("prepare isolated OpenCode state: %v", err))
			fmt.Fprintf(cfg.Stderr, "failed to prepare isolated OpenCode state: %v\n", err)
			return 1
		}
		defer os.RemoveAll(stateRoot)
		statePath := filepath.Join(stateRoot, "opencode")
		env = WithEnv(env, "TMUX_CODER_OPENCODE_STATE_PATH="+statePath)
		stateCtx, stateCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = api.SendAgentSetupState(stateCtx, agentID, statePath)
		stateCancel()
		if err != nil {
			reportSetupFailure(api, agentID, fmt.Sprintf("register isolated OpenCode state: %v", err))
			fmt.Fprintf(cfg.Stderr, "failed to register isolated OpenCode state: %v\n", err)
			return 1
		}
	}

	commandArgs := []string{}
	if kind == "opencode" {
		serverURL := configValue(cfg.Getenv, env, "TMUX_CODER_OPENCODE_SERVER_URL")
		if serverURL == "" {
			serverCtx, serverCancel := context.WithTimeout(context.Background(), 15*time.Second)
			serverURL, err = api.EnsureOpenCodeServer(serverCtx)
			serverCancel()
			if err != nil {
				if setupRequested {
					reportSetupFailure(api, agentID, fmt.Sprintf("start shared OpenCode server: %v", err))
				}
				fmt.Fprintf(cfg.Stderr, "failed to start shared OpenCode server: %v\n", err)
				return 1
			}
		}
		commandArgs = append(commandArgs, "attach", serverURL)
		if workingDir, err := os.Getwd(); err == nil {
			commandArgs = append(commandArgs, "--dir", workingDir)
		}
	}

	cmd := cfg.CommandContext(context.Background(), kind, commandArgs...)
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

func prepareOpenCodeState(env []string, model string) ([]string, string, error) {
	root, err := os.MkdirTemp("", "tmux-coder-opencode-state-")
	if err != nil {
		return nil, "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, "", err
	}

	sourceRoot := configValue(nil, env, "XDG_STATE_HOME")
	if sourceRoot == "" {
		home := configValue(nil, env, "HOME")
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				_ = os.RemoveAll(root)
				return nil, "", err
			}
		}
		sourceRoot = filepath.Join(home, ".local", "state")
	}
	source := filepath.Join(sourceRoot, "opencode")
	target := filepath.Join(root, "opencode")
	if err := copyStateDirectory(source, target); err != nil {
		_ = os.RemoveAll(root)
		return nil, "", err
	}
	if err := seedOpenCodeModelState(filepath.Join(target, "model.json"), model); err != nil {
		_ = os.RemoveAll(root)
		return nil, "", err
	}
	return WithEnv(env, "XDG_STATE_HOME="+root), root, nil
}

func copyStateDirectory(source, target string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || rel == "." {
			return err
		}
		destination := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in OpenCode state: %s", rel)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		return errors.Join(copyErr, closeErr)
	})
}

func seedOpenCodeModelState(path, canonical string) error {
	providerID, modelID, ok := strings.Cut(canonical, "/")
	if !ok || providerID == "" || modelID == "" {
		return fmt.Errorf("invalid canonical model %q", canonical)
	}
	state := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode existing model state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	recent, _ := state["recent"].([]any)
	filtered := make([]any, 0, len(recent))
	for _, item := range recent {
		entry, _ := item.(map[string]any)
		if entry["providerID"] == providerID && entry["modelID"] == modelID {
			continue
		}
		filtered = append(filtered, item)
	}
	state["recent"] = filtered
	variants, _ := state["variant"].(map[string]any)
	if variants == nil {
		variants = make(map[string]any)
	}
	delete(variants, canonical)
	state["variant"] = variants

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmux-coder"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
