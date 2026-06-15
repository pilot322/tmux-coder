package git

import (
	"reflect"
	"testing"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

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
