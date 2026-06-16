package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// deleteFixture seeds one project and returns a DeleteProject wired to the same
// repos, gateway and lock, plus the created project's id and tmux session name.
func deleteFixture(ctx context.Context) (*usecase.DeleteProject, *memory.MemoryProjectRepository, *memory.MemoryAgentRepository, *fakeGateway, int, string) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	gw := newFakeGateway(lock)

	create := usecase.NewCreateProject(projects, sessions, gw, lock, domain.DefaultDaemonConfig(), obs.Nop())
	res, _ := create.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})

	del := usecase.NewDeleteProject(projects, sessions, agents, gw, lock, obs.Nop())
	return del, projects, agents, gw, res.Project.ID(), res.MainTmuxSessionName
}

func TestDeleteProject_KillsSessionsAndRemovesRecords(t *testing.T) {
	ctx := context.Background()
	del, projects, _, gw, id, mainName := deleteFixture(ctx)

	if err := del.Execute(ctx, id); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(gw.killed) != 1 || gw.killed[0] != mainName {
		t.Errorf("gateway.Kill calls = %v, want [%s]", gw.killed, mainName)
	}
	if _, err := projects.GetByID(ctx, id); !errors.Is(err, usecase.ErrProjectNotFound) {
		t.Errorf("project should be gone, got %v", err)
	}
}

func TestDeleteProject_UnknownIDReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	del, _, _, _, _, _ := deleteFixture(ctx)

	if err := del.Execute(ctx, 9999); !errors.Is(err, usecase.ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestDeleteProject_GatewayKillFailureKeepsRecords(t *testing.T) {
	ctx := context.Background()
	del, projects, _, gw, id, _ := deleteFixture(ctx)
	gw.killErr = errors.New("tmux refused")

	if err := del.Execute(ctx, id); !errors.Is(err, usecase.ErrGateway) {
		t.Fatalf("want ErrGateway, got %v", err)
	}
	if _, err := projects.GetByID(ctx, id); err != nil {
		t.Errorf("records should be kept on kill failure, got %v", err)
	}
}

func TestDeleteProject_RemovesAgentRecords(t *testing.T) {
	ctx := context.Background()
	del, _, agents, _, id, _ := deleteFixture(ctx)
	_, _ = agents.Create(ctx, domain.NewAgent(0, id, 1, "opencode", "", "%1", true, domain.AgentStarting))

	if err := del.Execute(ctx, id); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	all, _ := agents.GetAll(ctx)
	if len(all) != 0 {
		t.Fatalf("agents should be gone after project delete: %+v", all)
	}
}
