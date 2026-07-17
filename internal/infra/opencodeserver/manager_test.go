package opencodeserver

import (
	"context"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/pilot322/tmux-coder/internal/obs"
)

func TestManagerStartsOneServerAndReusesItsURL(t *testing.T) {
	t.Setenv("GO_WANT_OPENCODE_SERVER_HELPER", "1")
	m := NewManager(obs.Nop())
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
	second, err := m.Ensure(context.Background())
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if first == "" || second != first {
		t.Fatalf("URLs = %q and %q, want one stable URL", first, second)
	}
	if starts != 1 {
		t.Fatalf("server starts = %d, want 1", starts)
	}
}

func TestManagerUsesConfiguredServerWithoutStartingProcess(t *testing.T) {
	m := NewManager(obs.Nop())
	m.getenv = func(key string) string {
		if key == "TMUX_CODER_OPENCODE_SERVER_URL" {
			return "http://127.0.0.1:9876"
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
	if url != "http://127.0.0.1:9876" {
		t.Fatalf("url = %q", url)
	}
}

func TestManagerRestartsServerAfterItExits(t *testing.T) {
	t.Setenv("GO_WANT_OPENCODE_SERVER_HELPER", "1")
	m := NewManager(obs.Nop())
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
	if first == "" || second == "" {
		t.Fatalf("URLs = %q and %q", first, second)
	}
}

func TestServerEnvRemovesAgentIdentity(t *testing.T) {
	env := serverEnv([]string{
		"PATH=/bin",
		"OPENCODE_DISABLE_AUTOUPDATE=false",
		"TMUX_CODER_AGENT_ID=7",
		"TMUX_CODER_PANE_ID=%1",
		"TMUX_CODERD_ADDR=127.0.0.1:64357",
	})
	want := []string{"PATH=/bin", "TMUX_CODERD_ADDR=127.0.0.1:64357", "OPENCODE_DISABLE_AUTOUPDATE=true"}
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
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			os.Exit(0)
		}
		_ = conn.Close()
	}
}
