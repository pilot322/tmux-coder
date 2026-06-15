package usecase_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// A worktree session whose worktree directory has been removed (e.g. by a
// delete that crashed before pruning the record) must not be listed, so the
// user never sees a session they cannot attach to.
func TestGetSessionsOmitsWorktreeWithMissingWorktreePath(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	worktreePath := filepath.Join(t.TempDir(), "api.feature")
	var main, worktree *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		worktree, err = sessions.Create(ctx, domain.NewWorktreeSession(0, project.ID(), "api.feature", "feature", worktreePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	// paths is empty: the worktree directory no longer exists on disk.
	git := &fakeWorktreeGit{paths: map[string]bool{}, events: &events}
	uc := usecase.NewGetSessions(projects, sessions, git, lock)
	views, err := uc.Execute(ctx, usecase.GetSessionsInput{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	ids := make(map[int]bool, len(views))
	for _, v := range views {
		ids[v.Session.ID()] = true
	}
	if !ids[main.ID()] {
		t.Fatalf("main session %d missing from views %v", main.ID(), ids)
	}
	if ids[worktree.ID()] {
		t.Fatalf("worktree session %d with missing worktree should be omitted; views %v", worktree.ID(), ids)
	}
}
