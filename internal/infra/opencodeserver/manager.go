package opencodeserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	mu       sync.Mutex
	getenv   func(string) string
	command  commandRunner
	version  func(context.Context, string) (string, error)
	log      obs.Logger
	port     int
	url      string
	password string
	cmd      *exec.Cmd
	result   *processResult
}

func NewManager(log obs.Logger, port int) *Manager {
	return &Manager{
		getenv:  os.Getenv,
		command: exec.Command,
		version: binaryVersion,
		log:     log.With("component", "opencode-server"),
		port:    port,
	}
}

func (m *Manager) Ensure(ctx context.Context) (usecase.OpenCodeConnection, error) {
	binary := m.getenv("TMUX_CODER_OPENCODE_BINARY")
	if binary == "" {
		binary = "opencode"
	}
	version, err := m.version(ctx, binary)
	if err != nil {
		return usecase.OpenCodeConnection{}, fmt.Errorf("OpenCode v2 binary: %w", err)
	}
	if configured := m.getenv("TMUX_CODER_OPENCODE_SERVER_URL"); configured != "" {
		password := m.getenv("OPENCODE_PASSWORD")
		if password == "" {
			password = m.getenv("OPENCODE_SERVER_PASSWORD")
		}
		if err := checkInfo(ctx, configured, password, version); err != nil {
			return usecase.OpenCodeConnection{}, fmt.Errorf("external OpenCode server: %w", err)
		}
		return usecase.OpenCodeConnection{URL: configured, Password: password}, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		select {
		case <-m.result.done:
			m.log.Warn(ctx, "OpenCode server exited", "err", errorString(m.result.err))
			m.clear()
		default:
			return usecase.OpenCodeConnection{URL: m.url, Password: m.password}, nil
		}
	}

	listener, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(m.port)))
	if err != nil {
		return usecase.OpenCodeConnection{}, fmt.Errorf("reserve OpenCode server port %d: %w", m.port, err)
	}
	if err := listener.Close(); err != nil {
		return usecase.OpenCodeConnection{}, fmt.Errorf("release listen port: %w", err)
	}
	password := m.getenv("OPENCODE_PASSWORD")
	if password == "" {
		password = m.getenv("OPENCODE_SERVER_PASSWORD")
	}
	if password == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return usecase.OpenCodeConnection{}, fmt.Errorf("generate OpenCode password: %w", err)
		}
		password = base64.RawURLEncoding.EncodeToString(secret)
	}

	cmd := m.command(binary, "serve", "--hostname", "0.0.0.0", "--port", strconv.Itoa(m.port))
	cmd.Env = append(serverEnv(os.Environ()), "OPENCODE_PASSWORD="+password)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err := cmd.Start(); err != nil {
		return usecase.OpenCodeConnection{}, fmt.Errorf("start opencode serve: %w", err)
	}

	m.cmd = cmd
	m.url = fmt.Sprintf("http://127.0.0.1:%d", m.port)
	m.password = password
	m.result = &processResult{done: make(chan struct{})}
	result := m.result
	go func() {
		result.err = cmd.Wait()
		close(result.done)
	}()

	readyCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := waitUntilReady(readyCtx, m.url, password, version, result); err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-result.done:
		case <-time.After(time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-result.done
		}
		m.clear()
		return usecase.OpenCodeConnection{}, err
	}

	m.log.Info(ctx, "OpenCode server started", "url", m.url, "pid", cmd.Process.Pid)
	return usecase.OpenCodeConnection{URL: m.url, Password: password}, nil
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
	m.password = ""
	m.cmd = nil
	m.result = nil
}

func waitUntilReady(ctx context.Context, url, password, version string, result *processResult) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		checkCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		err := checkInfo(checkCtx, url, password, version)
		cancel()
		if err == nil {
			return nil
		}
		if _, ok := err.(readinessError); ok {
			return err
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

type readinessError string

func (e readinessError) Error() string { return string(e) }

func checkInfo(ctx context.Context, serverURL, password, version string) error {
	parsed, err := url.Parse(serverURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return readinessError("OpenCode server URL must be an HTTP(S) root origin without credentials")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/api/info", nil)
	if err != nil {
		return readinessError("invalid OpenCode server URL")
	}
	if password != "" {
		req.SetBasicAuth("opencode", password)
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to OpenCode v2 server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return readinessError("OpenCode server rejected password; set OPENCODE_PASSWORD")
	}
	if resp.StatusCode != http.StatusOK {
		return readinessError(fmt.Sprintf("OpenCode v2 /api/info returned HTTP %d", resp.StatusCode))
	}
	var info struct {
		Version string          `json:"version"`
		PID     int             `json:"pid"`
		URLs    json.RawMessage `json:"urls"`
		Paths   json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16384)).Decode(&info); err != nil {
		return readinessError("OpenCode server returned invalid /api/info JSON")
	}
	if !strings.HasPrefix(info.Version, "2.") || info.PID <= 0 || len(info.URLs) == 0 || len(info.Paths) == 0 {
		return readinessError("OpenCode server did not provide a compatible v2 /api/info response")
	}
	if info.Version != version {
		return readinessError(fmt.Sprintf("OpenCode server version %s does not match pane binary version %s", info.Version, version))
	}
	return nil
}

func binaryVersion(ctx context.Context, binary string) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, binary, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run --version: %w", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(output)), "opencode v")
	if !strings.HasPrefix(version, "2.") {
		return "", fmt.Errorf("expected OpenCode v2, got %q", strings.TrimSpace(string(output)))
	}
	return version, nil
}

func serverEnv(env []string) []string {
	blocked := map[string]bool{
		"OPENCODE_PASSWORD":        true,
		"OPENCODE_SERVER_PASSWORD": true,
		"TMUX_CODER_AGENT_ID":      true,
		"TMUX_CODER_AGENT_KIND":    true,
		"TMUX_CODER_PANE_ID":       true,
		"TMUX_CODER_PROJECT_ID":    true,
		"TMUX_CODER_SESSION_ID":    true,
	}
	out := make([]string, 0, len(env))
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		if !blocked[key] {
			out = append(out, value)
		}
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}
