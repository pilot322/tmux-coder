package opencodeserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/pilot322/tmux-coder/internal/obs"
)

func TestManagerStartsOneServerAndReusesItsURL(t *testing.T) {
	t.Setenv("GO_WANT_OPENCODE_SERVER_HELPER", "1")
	port := freeTCPPort(t)
	m := NewManager(obs.Nop(), port)
	m.version = func(context.Context, string) (string, error) { return "2.0.16", nil }
	starts := 0
	var startedArgs []string
	m.command = func(_ string, args ...string) *exec.Cmd {
		starts++
		startedArgs = append([]string(nil), args...)
		helperArgs := append([]string{"-test.run=TestOpenCodeServerHelper", "--"}, args...)
		return exec.Command(os.Args[0], helperArgs...)
	}
	t.Cleanup(m.Close)

	first, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	second, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if first.URL == "" || first.Password == "" || second != first {
		t.Fatal("expected one stable authenticated connection")
	}
	if starts != 1 {
		t.Fatalf("server starts = %d, want 1", starts)
	}
	if len(startedArgs) != 5 || startedArgs[0] != "serve" || startedArgs[1] != "--hostname" || startedArgs[2] != "0.0.0.0" || startedArgs[3] != "--port" || startedArgs[4] != strconv.Itoa(port) {
		t.Fatalf("server args = %#v, want serve bound to 0.0.0.0:%d", startedArgs, port)
	}
}

// Run with TMUX_CODER_TEST_OPENCODE_V2_BINARY to exercise the real v2 executable
// without changing the OpenCode installation used by the current terminal.
func TestManagerRealV2(t *testing.T) {
	binary := os.Getenv("TMUX_CODER_TEST_OPENCODE_V2_BINARY")
	if binary == "" {
		t.Skip("set TMUX_CODER_TEST_OPENCODE_V2_BINARY for isolated v2 integration")
	}
	t.Setenv("TMUX_CODER_OPENCODE_BINARY", binary)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("OPENCODE_DISABLE_MODELS_FETCH", "1")
	t.Setenv("OPENCODE_CONFIG_PROJECT_DISABLE", "1")
	t.Setenv("OPENCODE_PASSWORD", "")
	m := NewManager(obs.Nop(), freeTCPPort(t))
	t.Cleanup(m.Close)
	connection, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if connection.Password == "" {
		t.Fatal("managed server has no password")
	}
	resp, err := http.Get(connection.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/info: %d", resp.StatusCode)
	}
	second, err := m.Ensure(context.Background())
	if err != nil || second != connection {
		t.Fatalf("shared connection changed: %v", err)
	}
}

func TestManagerUsesConfiguredServerWithoutStartingProcess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/info" {
			t.Errorf("path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"version":"2.0.16","pid":123,"urls":{},"paths":{}}`))
	}))
	defer server.Close()
	m := NewManager(obs.Nop(), freeTCPPort(t))
	m.version = func(context.Context, string) (string, error) { return "2.0.16", nil }
	m.getenv = func(key string) string {
		if key == "TMUX_CODER_OPENCODE_SERVER_URL" {
			return server.URL
		}
		return ""
	}
	m.command = func(string, ...string) *exec.Cmd {
		t.Fatal("configured server should not start a process")
		return nil
	}

	url, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if url.URL != server.URL {
		t.Fatalf("url = %q", url.URL)
	}
}

func TestManagerRestartsServerAfterItExits(t *testing.T) {
	t.Setenv("GO_WANT_OPENCODE_SERVER_HELPER", "1")
	m := NewManager(obs.Nop(), freeTCPPort(t))
	m.version = func(context.Context, string) (string, error) { return "2.0.16", nil }
	starts := 0
	m.command = func(_ string, args ...string) *exec.Cmd {
		starts++
		helperArgs := append([]string{"-test.run=TestOpenCodeServerHelper", "--"}, args...)
		return exec.Command(os.Args[0], helperArgs...)
	}
	t.Cleanup(m.Close)

	first, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if err := syscall.Kill(-m.cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatalf("stop first server: %v", err)
	}
	<-m.result.done

	second, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if starts != 2 {
		t.Fatalf("server starts = %d, want 2", starts)
	}
	if first.URL == "" || second.URL == "" {
		t.Fatal("missing server URL")
	}
}

func TestExternalServerRejectsWrongPasswordAndVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || password != "expected" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"version":"2.0.15","pid":123,"urls":{},"paths":{}}`))
	}))
	defer server.Close()
	m := NewManager(obs.Nop(), freeTCPPort(t))
	m.version = func(context.Context, string) (string, error) { return "2.0.16", nil }
	m.getenv = func(key string) string {
		if key == "TMUX_CODER_OPENCODE_SERVER_URL" {
			return server.URL
		}
		if key == "OPENCODE_PASSWORD" {
			return "wrong"
		}
		return ""
	}
	if _, err := m.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "rejected password") {
		t.Fatalf("auth error: %v", err)
	}
	m.getenv = func(key string) string {
		if key == "TMUX_CODER_OPENCODE_SERVER_URL" {
			return server.URL
		}
		if key == "OPENCODE_PASSWORD" {
			return "expected"
		}
		return ""
	}
	if _, err := m.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("version error: %v", err)
	}
}

func TestExternalServerRejectsInvalidInfoAndCredentialsInURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":`))
	}))
	defer server.Close()
	m := NewManager(obs.Nop(), freeTCPPort(t))
	m.version = func(context.Context, string) (string, error) { return "2.0.16", nil }
	configured := server.URL
	m.getenv = func(key string) string {
		if key == "TMUX_CODER_OPENCODE_SERVER_URL" {
			return configured
		}
		return ""
	}
	if _, err := m.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid /api/info JSON") {
		t.Fatalf("invalid response: %v", err)
	}
	configured = strings.Replace(server.URL, "//", "//secret:secret@", 1)
	if _, err := m.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "without credentials") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("credential URL error: %v", err)
	}
}

func TestManagerRejectsV1BinaryBeforeStartingServer(t *testing.T) {
	m := NewManager(obs.Nop(), freeTCPPort(t))
	m.version = func(context.Context, string) (string, error) { return "", fmt.Errorf("expected OpenCode v2") }
	m.command = func(string, ...string) *exec.Cmd { t.Fatal("v1 binary started server"); return nil }
	if _, err := m.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "expected OpenCode v2") {
		t.Fatalf("v1 error: %v", err)
	}
}

func TestServerEnvRemovesAgentIdentity(t *testing.T) {
	env := serverEnv([]string{
		"PATH=/bin",
		"OPENCODE_PASSWORD=old",
		"TMUX_CODER_AGENT_ID=7",
		"TMUX_CODER_PANE_ID=%1",
		"TMUX_CODERD_ADDR=127.0.0.1:64357",
	})
	want := []string{"PATH=/bin", "TMUX_CODERD_ADDR=127.0.0.1:64357"}
	if len(env) != len(want) {
		t.Fatalf("env = %#v", env)
	}
	for i := range want {
		if env[i] != want[i] {
			t.Fatalf("env = %#v, want %#v", env, want)
		}
	}
}

func TestOpenCodeServerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_OPENCODE_SERVER_HELPER") != "1" {
		return
	}
	port := ""
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
			break
		}
	}
	if port == "" {
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		os.Exit(3)
	}
	_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, ok := r.BasicAuth()
		if !ok || password != os.Getenv("OPENCODE_PASSWORD") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"2.0.16","pid":123,"urls":{},"paths":{}}`))
	}))
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("allocate test port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release test port: %v", err)
	}
	return port
}
