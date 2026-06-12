package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileSetsDaemonPort(t *testing.T) {
	unsetEnv(t, "TMUX_CODERD_PORT")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("TMUX_CODERD_PORT=7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}

	if got := daemonPort(); got != "7777" {
		t.Fatalf("daemonPort() = %q, want %q", got, "7777")
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

	if got := daemonPort(); got != "8888" {
		t.Fatalf("daemonPort() = %q, want %q", got, "8888")
	}
}

func TestDaemonPortDefaultsWhenUnset(t *testing.T) {
	unsetEnv(t, "TMUX_CODERD_PORT")

	if got := daemonPort(); got != "64357" {
		t.Fatalf("daemonPort() = %q, want %q", got, "64357")
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
