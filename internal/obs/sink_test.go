package obs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAgentEventSinkConcurrentAppendersStayIntact opens the shared per-day file
// several times over independent descriptors — standing in for the several
// short-lived agent-event processes that fire on every hook — and writes from
// all of them at once. The O_APPEND open is what keeps their small records from
// interleaving, since each writer has its own slog mutex.
func TestAgentEventSinkConcurrentAppendersStayIntact(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	const writers, perWriter = 8, 200

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		sink, err := openSink(RoleAgentEvent, dir, now)
		if err != nil {
			t.Fatal(err)
		}
		log := newWithWriter(sink, RoleAgentEvent)
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer sink.Close()
			for i := 0; i < perWriter; i++ {
				log.Info(context.Background(), "status", "writer", id, "seq", i)
			}
		}(w)
	}
	wg.Wait()

	entries := dirNames(t, dir)
	if len(entries) != 1 || entries[0] != "2026-06-16.log" {
		t.Fatalf("agent-event sink wrote %v, want a single 2026-06-16.log", entries)
	}

	data, err := os.ReadFile(filepath.Join(dir, "2026-06-16.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := splitNonEmpty(string(data))
	if len(lines) != writers*perWriter {
		t.Fatalf("got %d lines, want %d (interleaved/lost writes corrupt the file)", len(lines), writers*perWriter)
	}
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("corrupt line %q: %v", line, err)
		}
	}
}

func TestPidSinkNamesFileByPid(t *testing.T) {
	dir := t.TempDir()
	sink, err := openSink(RoleDaemon, dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if _, err := sink.Write([]byte("hi\n")); err != nil {
		t.Fatal(err)
	}

	got := dirNames(t, dir)
	want := strconv.Itoa(os.Getpid()) + ".log"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("pid sink created %v, want %q", got, want)
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
