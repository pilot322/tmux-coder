package usecase_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestDeleteWorktreeSwitchesAttachedClientsToMainBeforeKill(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
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
	git := &fakeWorktreeGit{paths: map[string]bool{worktreePath: true}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{worktree.TmuxName(): true, main.TmuxName(): true}}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock)
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: worktree.ID(), Force: true}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tmux.switched) != 1 || tmux.switched[0] != (switchCall{from: worktree.TmuxName(), to: main.TmuxName()}) {
		t.Fatalf("switched = %+v, want one switch from %q to %q", tmux.switched, worktree.TmuxName(), main.TmuxName())
	}
	switchIdx, killIdx := -1, -1
	for i, e := range events {
		if e == "tmux:switch:"+worktree.TmuxName()+"->"+main.TmuxName() {
			switchIdx = i
		}
		if e == "tmux:kill:"+worktree.TmuxName() {
			killIdx = i
		}
	}
	if switchIdx == -1 || killIdx == -1 || switchIdx > killIdx {
		t.Fatalf("want client switch before kill; events = %v", events)
	}
}

func TestDeleteSecondarySwitchesAttachedClientsToMainBeforeKill(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	var main, secondary *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		secondary, err = sessions.Create(ctx, domain.NewSecondarySession(0, main.ID(), project.ID(), "api.logs", "", ""))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{secondary.TmuxName(): true, main.TmuxName(): true}}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock)
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: secondary.ID()}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tmux.switched) != 1 || tmux.switched[0] != (switchCall{from: secondary.TmuxName(), to: main.TmuxName()}) {
		t.Fatalf("switched = %+v, want one switch from %q to %q", tmux.switched, secondary.TmuxName(), main.TmuxName())
	}
	switchIdx, killIdx := -1, -1
	for i, e := range events {
		if e == "tmux:switch:"+secondary.TmuxName()+"->"+main.TmuxName() {
			switchIdx = i
		}
		if e == "tmux:kill:"+secondary.TmuxName() {
			killIdx = i
		}
	}
	if switchIdx == -1 || killIdx == -1 || switchIdx > killIdx {
		t.Fatalf("want client switch before kill; events = %v", events)
	}
}

func TestDeleteWorktreeRemovesSessionEvenWhenKillFails(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
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
	git := &fakeWorktreeGit{paths: map[string]bool{worktreePath: true}, events: &events}
	// The worktree session is the one the user is attached to, so killing it
	// tears the connection down and the kill shells out non-zero.
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{worktree.TmuxName(): true, main.TmuxName(): true}, killErr: errors.New("server gone")}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock)
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: worktree.ID(), Force: true}); err != nil {
		t.Fatalf("Execute should not surface a best-effort kill failure: %v", err)
	}

	if len(git.removed) != 1 || git.removed[0] != worktreePath {
		t.Fatalf("worktree removed = %v, want [%s]", git.removed, worktreePath)
	}
	if err := lock.WithRead(func() error {
		_, err := sessions.GetByID(ctx, worktree.ID())
		return err
	}); !errors.Is(err, usecase.ErrSessionNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrSessionNotFound (no orphan row)", err)
	}
}

func TestDeleteSecondaryRemovesSessionEvenWhenKillFails(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	var main, secondary *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		secondary, err = sessions.Create(ctx, domain.NewSecondarySession(0, main.ID(), project.ID(), "api.logs", "", ""))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{secondary.TmuxName(): true, main.TmuxName(): true}, killErr: errors.New("server gone")}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock)
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: secondary.ID()}); err != nil {
		t.Fatalf("Execute should not surface a best-effort kill failure: %v", err)
	}

	if err := lock.WithRead(func() error {
		_, err := sessions.GetByID(ctx, secondary.ID())
		return err
	}); !errors.Is(err, usecase.ErrSessionNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrSessionNotFound (no orphan row)", err)
	}
}

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
