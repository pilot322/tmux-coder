package tmux

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/obs"
)

func TestPaneExistsReportsTrueWhenTmuxSucceeds(t *testing.T) {
	g := fakeTmuxGateway(t)
	t.Setenv("TMUX_CODER_FAKE_TMUX_MODE", "success")

	exists, err := g.PaneExists(context.Background(), "%10")
	if err != nil {
		t.Fatalf("PaneExists: %v", err)
	}
	if !exists {
		t.Fatal("exists = false, want true")
	}
}

func TestPaneExistsReportsFalseForClearMissingTarget(t *testing.T) {
	g := fakeTmuxGateway(t)
	t.Setenv("TMUX_CODER_FAKE_TMUX_MODE", "missing-pane")

	exists, err := g.PaneExists(context.Background(), "%10")
	if err != nil {
		t.Fatalf("PaneExists: %v", err)
	}
	if exists {
		t.Fatal("exists = true, want false")
	}
}

func TestPaneExistsReturnsErrorForOtherTmuxFailures(t *testing.T) {
	g := fakeTmuxGateway(t)
	t.Setenv("TMUX_CODER_FAKE_TMUX_MODE", "server-error")

	exists, err := g.PaneExists(context.Background(), "%10")
	if err == nil {
		t.Fatal("want error")
	}
	if exists {
		t.Fatal("exists = true, want false")
	}
	if !strings.Contains(err.Error(), "server exited unexpectedly") {
		t.Fatalf("err = %v, want tmux output", err)
	}
}

func TestNewWindowAppliesServerLabelOnce(t *testing.T) {
	g := fakeTmuxGateway(t)
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("TMUX_CODER_FAKE_TMUX_MODE", "new-window")
	t.Setenv("TMUX_CODER_FAKE_TMUX_ARGS", argsPath)

	paneID, err := g.NewWindow(context.Background(), "session-main", "agent-1", "/work/tree", "opencode run", []string{"FOO=bar", "BAZ=qux"})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if paneID != "%42" {
		t.Fatalf("paneID = %q, want %%42", paneID)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake tmux args: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"-L", "test",
		"new-window", "-P", "-F", "#{pane_id}",
		"-t", "session-main",
		"-n", "agent-1",
		"-c", "/work/tree",
		"-e", "FOO=bar",
		"-e", "BAZ=qux",
		"opencode run",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func fakeTmuxGateway(t *testing.T) *TmuxGateway {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	script := `#!/bin/sh
case "$TMUX_CODER_FAKE_TMUX_MODE" in
success)
  exit 0
  ;;
missing-pane)
  printf '%s\n' "can't find pane: %10" >&2
  exit 1
  ;;
server-error)
  printf '%s\n' "server exited unexpectedly" >&2
  exit 1
  ;;
new-window)
  printf '%s\n' "$@" > "$TMUX_CODER_FAKE_TMUX_ARGS"
  printf '%s\n' "%42"
  exit 0
  ;;
*)
  printf '%s\n' "unknown fake tmux mode" >&2
  exit 2
  ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	return &TmuxGateway{binary: path, serverLabel: "test", log: obs.Nop()}
}
