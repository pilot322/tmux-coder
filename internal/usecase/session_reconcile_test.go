package usecase_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// When a Worktree Session's directory vanishes out-of-band, the next write op
// reconciles it away. Its Worktree children are independent checkouts that
// survive and reparent to the nearest surviving ancestor; its Secondary children
// cascade (ADR-0010). Here reconcile is driven by an unrelated worktree create.
func TestReconcilePruneReparentsWorktreeChildrenAndCascadesSecondaries(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	projectRoot := filepath.Join(base, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	var events []string
	feat1Path := filepath.Join(base, "api.feat1") // deleted out-of-band: absent from git.paths
	backendPath := filepath.Join(base, "api.feat1-backend")
	frontendPath := filepath.Join(base, "api.feat1-frontend")
	git := &fakeWorktreeGit{paths: map[string]bool{backendPath: true, frontendPath: true}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, &fakeWorktreeHookRunner{events: &events}, memory.NewMemoryResourceLeaseRepository(), obs.Nop())

	var projectID int
	var feat1, backend, frontend, secondary *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		if err != nil {
			return err
		}
		projectID = project.ID()
		_, err = sessions.Create(ctx, domain.NewSession(0, -1, projectID, "api.main", domain.MainSession))
		if err != nil {
			return err
		}
		feat1, err = sessions.Create(ctx, domain.NewWorktreeSession(0, -1, projectID, "api.feat1", "feat1", feat1Path))
		if err != nil {
			return err
		}
		backend, err = sessions.Create(ctx, domain.NewWorktreeSession(0, feat1.ID(), projectID, "api.feat1-backend", "feat1-backend", backendPath))
		if err != nil {
			return err
		}
		frontend, err = sessions.Create(ctx, domain.NewWorktreeSession(0, feat1.ID(), projectID, "api.feat1-frontend", "feat1-frontend", frontendPath))
		if err != nil {
			return err
		}
		secondary, err = sessions.Create(ctx, domain.NewSecondarySession(0, feat1.ID(), projectID, "logs", "logs", "cascade"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Any write op reconciles first; create an unrelated worktree to drive it.
	if _, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: projectID, Type: domain.WorktreeSession, Branch: "newbranch", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := getSession(t, lock, sessions, feat1.ID()); got != nil {
		t.Errorf("vanished feat1 should be pruned, still present")
	}
	for _, child := range []*domain.Session{backend, frontend} {
		got := getSession(t, lock, sessions, child.ID())
		if got == nil {
			t.Fatalf("worktree child %q should survive a prune", child.Name())
		}
		if got.Parent() != -1 {
			t.Errorf("child %q parent = %d, want -1 (parentless after prune-reparent)", got.Name(), got.Parent())
		}
	}
	if got := getSession(t, lock, sessions, secondary.ID()); got != nil {
		t.Errorf("secondary under the pruned worktree should cascade, still present")
	}
}
