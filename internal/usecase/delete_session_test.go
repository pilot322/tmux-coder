package usecase_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestDeleteSessionReleasesOwnedPortLeases(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	leases := memory.NewMemoryResourceLeaseRepository()
	lock := &spyLock{}
	worktreePath := filepath.Join(t.TempDir(), "api.feature")
	var session *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		session, err = sessions.Create(ctx, domain.NewWorktreeSession(0, project.ID(), "api.feature", "feature", worktreePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{ProjectID: session.ProjectID(), OwnerKind: usecase.ResourceLeaseOwnerSession, SessionID: session.ID(), Key: "web", Start: 8000, End: 8000}, func(int) bool { return true }); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{worktreePath: true}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{session.TmuxName(): true}}
	uc := usecase.NewDeleteSessionWithLeases(sessions, agents, tmux, git, lock, leases)
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: session.ID(), Force: true}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if err := leases.BeginHook(ctx, "hook-token", usecase.HookLeaseOwner{ProjectID: session.ProjectID()}); err != nil {
		t.Fatal(err)
	}
	port, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{OwnerKind: usecase.ResourceLeaseOwnerHook, HookToken: "hook-token", Key: "web", Start: 8000, End: 8000}, func(int) bool { return true })
	if err != nil {
		t.Fatalf("AcquirePort after delete: %v", err)
	}
	if port != 8000 {
		t.Fatalf("port after delete = %d, want released port 8000", port)
	}
}
