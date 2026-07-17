package opencodeserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

const startupTimeout = 10 * time.Second

var _ usecase.OpenCodeServerGateway = (*Manager)(nil)

type commandRunner func(name string, args ...string) *exec.Cmd

type processResult struct {
	done chan struct{}
	err  error
}

type Manager struct {
	mu      sync.Mutex
	getenv  func(string) string
	command commandRunner
	log     obs.Logger
	port    int
	url     string
	cmd     *exec.Cmd
	result  *processResult
}

func NewManager(log obs.Logger, port int) *Manager {
	return &Manager{
		getenv:  os.Getenv,
		command: exec.Command,
		log:     log.With("component", "opencode-server"),
		port:    port,
	}
}

func (m *Manager) Ensure(ctx context.Context) (string, error) {
	if configured := m.getenv("TMUX_CODER_OPENCODE_SERVER_URL"); configured != "" {
		return configured, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		select {
		case <-m.result.done:
			m.log.Warn(ctx, "OpenCode server exited", "err", errorString(m.result.err))
			m.clear()
		default:
			return m.url, nil
		}
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(m.port)))
	if err != nil {
		return "", fmt.Errorf("reserve OpenCode server port %d: %w", m.port, err)
	}
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release listen port: %w", err)
	}

	binary := m.getenv("TMUX_CODER_OPENCODE_BINARY")
	if binary == "" {
		binary = "opencode"
	}
	cmd := m.command(binary, "serve", "--hostname", "0.0.0.0", "--port", strconv.Itoa(m.port))
	cmd.Env = serverEnv(os.Environ())
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start opencode serve: %w", err)
	}

	m.cmd = cmd
	m.url = fmt.Sprintf("http://127.0.0.1:%d", m.port)
	m.result = &processResult{done: make(chan struct{})}
	result := m.result
	go func() {
		result.err = cmd.Wait()
		close(result.done)
	}()

	readyCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := waitUntilReady(readyCtx, m.port, result); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-result.done:
		case <-time.After(time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-result.done
		}
		m.clear()
		return "", err
	}

	m.log.Info(ctx, "OpenCode server started", "url", m.url, "pid", cmd.Process.Pid)
	return m.url, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return
	}
	_ = syscall.Kill(-m.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-m.result.done:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-m.cmd.Process.Pid, syscall.SIGKILL)
		<-m.result.done
	}
	m.clear()
}

func (m *Manager) clear() {
	m.url = ""
	m.cmd = nil
	m.result = nil
}

func waitUntilReady(ctx context.Context, port int, result *processResult) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-result.done:
			return fmt.Errorf("opencode serve exited before becoming ready: %s", errorString(result.err))
		case <-ctx.Done():
			return fmt.Errorf("wait for opencode serve: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func serverEnv(env []string) []string {
	blocked := map[string]bool{
		"OPENCODE_DISABLE_AUTOUPDATE": true,
		"TMUX_CODER_AGENT_ID":         true,
		"TMUX_CODER_AGENT_KIND":       true,
		"TMUX_CODER_PANE_ID":          true,
		"TMUX_CODER_PROJECT_ID":       true,
		"TMUX_CODER_SESSION_ID":       true,
	}
	out := make([]string, 0, len(env)+1)
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		if !blocked[key] {
			out = append(out, value)
		}
	}
	return append(out, "OPENCODE_DISABLE_AUTOUPDATE=true")
}

func errorString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}
