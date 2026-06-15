package daemonaddr_test

import (
	"testing"

	"github.com/pilot322/tmux-coder/internal/daemonaddr"
)

func TestAddressUsesEnvPortOrDefault(t *testing.T) {
	if got := daemonaddr.Address(func(string) string { return "" }); got != "http://127.0.0.1:64357" {
		t.Fatalf("default address = %q", got)
	}
	if got := daemonaddr.Address(func(string) string { return "7000" }); got != "http://127.0.0.1:7000" {
		t.Fatalf("env address = %q", got)
	}
}

func TestDefaultAddressIgnoresEnv(t *testing.T) {
	if got := daemonaddr.DefaultAddress(); got != "http://127.0.0.1:64357" {
		t.Fatalf("default address = %q", got)
	}
}
