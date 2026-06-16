package git

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestGatewayLogsDebugLinePerCommand(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	rec := obs.Recording()
	g := NewGateway(rec)
	if _, err := g.ListWorktrees(context.Background(), repo); err != nil {
		t.Fatal(err)
	}

	var logged bool
	for _, line := range rec.Records() {
		if line["level"] == "DEBUG" && line["msg"] == "git exec" && line["component"] == "git" {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("expected a DEBUG git exec line tagged component=git, got %v", rec.Records())
	}
}

func TestParseWorktreePorcelain(t *testing.T) {
	out := "" +
		"worktree /work/api\n" +
		"HEAD 1111111111111111111111111111111111111111\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /work/api.feature-login\n" +
		"HEAD 2222222222222222222222222222222222222222\n" +
		"branch refs/heads/feature/login\n" +
		"\n" +
		"worktree /work/api.detached\n" +
		"HEAD 3333333333333333333333333333333333333333\n" +
		"detached\n" +
		"\n" +
		"worktree /work/api.bare\n" +
		"bare\n"

	got := parseWorktreePorcelain([]byte(out))
	want := []usecase.WorktreeRef{
		{Path: "/work/api", Branch: "main"},
		{Path: "/work/api.feature-login", Branch: "feature/login"},
		{Path: "/work/api.detached", Detached: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseWorktreePorcelain =\n %+v\nwant\n %+v", got, want)
	}
}
