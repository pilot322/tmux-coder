package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestRemoveWorktreeUsesOwningRepoFromGitFile(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "initial")

	worktree := filepath.Join(t.TempDir(), "repo.feature")
	runGit(t, repo, "worktree", "add", "-b", "feature", worktree)

	g := NewGateway(obs.Nop())
	if err := g.RemoveWorktree(context.Background(), worktree, true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree stat after removal = %v, want not exist", err)
	}
}

func TestRemoveWorktreeForceRemovesOrphanedDirectory(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")

	worktree := filepath.Join(t.TempDir(), "repo.feature")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "repo.feature")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "left-behind.txt"), []byte("orphaned checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGateway(obs.Nop())
	if err := g.RemoveWorktree(context.Background(), worktree, true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree stat after forced orphan removal = %v, want not exist", err)
	}
}

func TestRemoveWorktreeWithoutForceKeepsOrphanedDirectory(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")

	worktree := filepath.Join(t.TempDir(), "repo.feature")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "repo.feature")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGateway(obs.Nop())
	err := g.RemoveWorktree(context.Background(), worktree, false)
	if !errors.Is(err, usecase.ErrConflict) {
		t.Fatalf("RemoveWorktree error = %v, want ErrConflict", err)
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("worktree stat after non-forced orphan removal = %v, want still present", statErr)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	full := append([]string{"-C", repo}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", full, err, out)
	}
}
