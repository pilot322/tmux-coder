package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/daemonaddr"
	"github.com/pilot322/tmux-coder/internal/daemonconfig"
	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/tmuxserver"
)

func TestNewDashboardServerConfiguresPublicHTTPDefenses(t *testing.T) {
	server := newDashboardServer("127.0.0.1:41000", http.NotFoundHandler())

	if server.Addr != "127.0.0.1:41000" {
		t.Errorf("Addr = %q, want %q", server.Addr, "127.0.0.1:41000")
	}
	if server.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout must be configured")
	}
	if server.ReadTimeout <= 0 {
		t.Error("ReadTimeout must be configured")
	}
	if server.WriteTimeout <= 0 {
		t.Error("WriteTimeout must be configured")
	}
	if server.IdleTimeout <= 0 {
		t.Error("IdleTimeout must be configured")
	}
	if server.MaxHeaderBytes <= 0 {
		t.Error("MaxHeaderBytes must be configured")
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestDashboardDeleteDisablesWriteDeadlineOnlyForSessions(t *testing.T) {
	server := newDashboardServer("127.0.0.1:0", http.NotFoundHandler())
	for _, tc := range []struct {
		method, path string
		wantCleared  bool
	}{
		{"DELETE", "/api/sessions/42?force=true", true},
		{"DELETE", "/api/projects/42", false},
		{"GET", "/api/sessions/42", false},
	} {
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		server.Handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if got := len(w.deadlines) == 1 && w.deadlines[0].IsZero(); got != tc.wantCleared {
			t.Errorf("%s %s clears write deadline = %t, want %t", tc.method, tc.path, got, tc.wantCleared)
		}
	}
}

func TestLoadEnvFileSetsDaemonPort(t *testing.T) {
	unsetEnv(t, "TMUX_CODERD_PORT")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("TMUX_CODERD_PORT=7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}

	if got := daemonaddr.Port(os.Getenv); got != "7777" {
		t.Fatalf("daemonaddr.Port(os.Getenv) = %q, want %q", got, "7777")
	}
}

func TestLoadEnvFileDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("TMUX_CODERD_PORT", "8888")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("TMUX_CODERD_PORT=7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}

	if got := daemonaddr.Port(os.Getenv); got != "8888" {
		t.Fatalf("daemonaddr.Port(os.Getenv) = %q, want %q", got, "8888")
	}
}

func TestLoadDaemonEnvDoesNotReadApplicationEnvFromLaunchDirectory(t *testing.T) {
	const key = "TMUX_CODER_REPRO_VALUE"
	unsetEnv(t, key)
	t.Setenv("HOME", t.TempDir())
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), []byte(key+"=from-project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chdir(t, projectDir)

	if err := loadDaemonEnv(); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	if value, exists := os.LookupEnv(key); exists {
		t.Fatalf("%s = %q, want unset", key, value)
	}
}

func TestLoadDaemonEnvReadsDaemonOwnedFileIndependentOfLaunchDirectory(t *testing.T) {
	const key = "TMUX_CODERD_PORT"
	unsetEnv(t, key)
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemonConfigDir := filepath.Join(home, ".tmux-coder")
	if err := os.MkdirAll(daemonConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daemonConfigDir, ".env"), []byte(key+"=7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chdir(t, t.TempDir())

	if err := loadDaemonEnv(); err != nil {
		t.Fatal(err)
	}

	if got := daemonaddr.Port(os.Getenv); got != "7777" {
		t.Fatalf("daemonaddr.Port(os.Getenv) = %q, want %q", got, "7777")
	}
}

func TestLoadDaemonEnvDoesNotOverrideInheritedEnv(t *testing.T) {
	t.Setenv("TMUX_CODERD_PORT", "8888")
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemonConfigDir := filepath.Join(home, ".tmux-coder")
	if err := os.MkdirAll(daemonConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daemonConfigDir, ".env"), []byte("TMUX_CODERD_PORT=7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadDaemonEnv(); err != nil {
		t.Fatal(err)
	}

	if got := daemonaddr.Port(os.Getenv); got != "8888" {
		t.Fatalf("daemonaddr.Port(os.Getenv) = %q, want %q", got, "8888")
	}
}

func TestDevelopmentBuildDoesNotLoadGlobalNetworkOverrides(t *testing.T) {
	previousDevelopmentBuild := domain.DevelopmentBuild
	domain.DevelopmentBuild = "true"
	t.Cleanup(func() { domain.DevelopmentBuild = previousDevelopmentBuild })
	unsetEnv(t, daemonaddr.EnvName)
	unsetEnv(t, daemonconfig.OpenCodeServerPortEnv)
	unsetEnv(t, daemonconfig.DashboardListenAddressEnv)
	unsetEnv(t, daemonconfig.DashboardPublicURLEnv)
	unsetEnv(t, daemonconfig.OpenCodePublicURLEnv)
	unsetEnv(t, tmuxserver.EnvName)
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemonConfigDir := filepath.Join(home, ".tmux-coder")
	if err := os.MkdirAll(daemonConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(daemonaddr.EnvName + "=41000\n" +
		daemonconfig.OpenCodeServerPortEnv + "=41001\n" +
		daemonconfig.DashboardListenAddressEnv + "=127.0.0.1:41002\n" +
		daemonconfig.DashboardPublicURLEnv + "=https://production-dashboard.example\n" +
		daemonconfig.OpenCodePublicURLEnv + "=https://production-opencode.example\n" +
		tmuxserver.EnvName + "=global\n")
	if err := os.WriteFile(filepath.Join(daemonConfigDir, ".env"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadDaemonEnv(); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		daemonaddr.EnvName,
		daemonconfig.OpenCodeServerPortEnv,
		daemonconfig.DashboardListenAddressEnv,
		daemonconfig.DashboardPublicURLEnv,
		daemonconfig.OpenCodePublicURLEnv,
		tmuxserver.EnvName,
	} {
		if value, exists := os.LookupEnv(key); exists {
			t.Errorf("%s = %q, want unset so baked development default is retained", key, value)
		}
	}
}

func TestDaemonPortDefaultsWhenUnset(t *testing.T) {
	unsetEnv(t, "TMUX_CODERD_PORT")

	if got := daemonaddr.Port(os.Getenv); got != "64357" {
		t.Fatalf("daemonaddr.Port(os.Getenv) = %q, want %q", got, "64357")
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(previous)
	})
}
