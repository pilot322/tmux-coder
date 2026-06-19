package tmux

import (
	"context"
	"os"
	"path/filepath"
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
