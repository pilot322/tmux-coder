package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLoggerEmitsJSONWithBaseAndCallFields(t *testing.T) {
	var buf bytes.Buffer
	log := newWithWriter(&buf, RoleDaemon).With("component", "create-project")

	log.Info(context.Background(), "project created", "project_id", 7)

	rec := decodeLines(t, &buf)
	if len(rec) != 1 {
		t.Fatalf("got %d lines, want 1", len(rec))
	}
	line := rec[0]
	want := map[string]any{
		"msg":        "project created",
		"level":      "INFO",
		"role":       "daemon",
		"component":  "create-project",
		"project_id": float64(7),
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("line[%q] = %v, want %v", k, line[k], v)
		}
	}
	if pid, ok := line["pid"].(float64); !ok || int(pid) != os.Getpid() {
		t.Errorf("line[pid] = %v, want %d", line["pid"], os.Getpid())
	}
	if _, ok := line["time"]; !ok {
		t.Error("line missing time")
	}
}

func TestLoggerAddSourcePointsAtCaller(t *testing.T) {
	var buf bytes.Buffer
	log := newWithWriter(&buf, RoleDaemon)

	log.Debug(context.Background(), "here")

	line := decodeLines(t, &buf)[0]
	src, ok := line["source"].(map[string]any)
	if !ok {
		t.Fatalf("line[source] = %v, want object (AddSource enabled)", line["source"])
	}
	file, _ := src["file"].(string)
	if !strings.HasSuffix(file, "logger_test.go") {
		t.Errorf("source.file = %q, want the caller's file (logger_test.go), not the wrapper", file)
	}
}

func TestLoggerEmitsDebugBecauseLevelIsDebug(t *testing.T) {
	var buf bytes.Buffer
	log := newWithWriter(&buf, RoleDaemon)

	log.Debug(context.Background(), "verbose")

	if got := len(decodeLines(t, &buf)); got != 1 {
		t.Fatalf("got %d debug lines, want 1 (level must be DEBUG)", got)
	}
}

func TestNopDoesNothingAndChains(t *testing.T) {
	log := Nop()
	// A no-op logger must never panic and With must keep returning a Logger.
	log.Debug(context.Background(), "x")
	log.With("k", "v").Error(context.Background(), "y", "a", 1)
	if log.With() == nil {
		t.Fatal("Nop().With() returned nil")
	}
}

// decodeLines parses every JSON Lines record written to buf.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", raw, err)
		}
		out = append(out, m)
	}
	return out
}
