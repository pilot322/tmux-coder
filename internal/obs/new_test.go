package obs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNewCreatesRoleDirAndWritesValidJSON(t *testing.T) {
	home := t.TempDir()
	getenv := getenvFor(home, "tmux-coder-feat-observability")

	log, err := New(RoleDaemon, getenv)
	if err != nil {
		t.Fatal(err)
	}
	log.With("component", "main").Info(context.Background(), "listening", "addr", "127.0.0.1:64359")

	dir := filepath.Join(home, ".tmux-coder", "logs", "dev-feat-observability", "daemon")
	pidLog := filepath.Join(dir, strconv.Itoa(os.Getpid())+".log")
	data, err := os.ReadFile(pidLog)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", pidLog, err)
	}

	lines := decodeLines(t, bytes.NewBuffer(data))
	if len(lines) != 1 || lines[0]["msg"] != "listening" || lines[0]["component"] != "main" {
		t.Fatalf("unexpected log contents: %v", lines)
	}
}
