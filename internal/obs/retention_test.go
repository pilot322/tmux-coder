package obs

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestSweepRemovesFilesOlderThanMaxAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

	// Both file shapes the layout produces: per-pid (rotated) and per-day.
	old := map[string]time.Time{
		"4242.log":       now.Add(-15 * 24 * time.Hour),
		"4242.log.gz":    now.Add(-20 * 24 * time.Hour),
		"2026-05-01.log": now.Add(-46 * 24 * time.Hour),
		"boot-old.log":   now.Add(-30 * 24 * time.Hour),
	}
	fresh := map[string]time.Time{
		"9999.log":       now.Add(-1 * time.Hour),
		"2026-06-16.log": now,
		"boot-new.log":   now.Add(-13 * 24 * time.Hour),
	}
	for name, mtime := range old {
		writeWithMtime(t, dir, name, mtime)
	}
	for name, mtime := range fresh {
		writeWithMtime(t, dir, name, mtime)
	}

	if err := sweep(dir, 14*24*time.Hour, now); err != nil {
		t.Fatal(err)
	}

	got := dirNames(t, dir)
	want := dirNames(t, dir)[:0]
	for name := range fresh {
		want = append(want, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("after sweep dir has %v, want only fresh %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("after sweep dir has %v, want only fresh %v", got, want)
		}
	}
}

func TestSweepIgnoresMissingDir(t *testing.T) {
	if err := sweep(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour, time.Now()); err != nil {
		t.Fatalf("sweep of missing dir should be a no-op, got %v", err)
	}
}

func writeWithMtime(t *testing.T, dir, name string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
