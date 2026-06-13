package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// deleteFixture seeds one project and returns a DeleteProject wired to the same
// repos, gateway and lock, plus the created project's id and tmux session name.
func deleteFixture(ctx context.Context) (*usecase.DeleteProject, *memory.MemoryProjectRepository, *fakeGateway, int, string) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	gw := newFakeGateway(lock)

	create := usecase.NewCreateProject(projects, sessions, gw, lock, domain.DefaultDaemonConfig())
	res, _ := create.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})

	del := usecase.NewDeleteProject(projects, sessions, gw, lock)
	return del, projects, gw, res.Project.ID(), res.MainTmuxSessionName
}

func TestDeleteProject_KillsSessionsAndRemovesRecords(t *testing.T) {
	ctx := context.Background()
	del, projects, gw, id, mainName := deleteFixture(ctx)

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
	del, _, _, _, _ := deleteFixture(ctx)

	if err := del.Execute(ctx, 9999); !errors.Is(err, usecase.ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestDeleteProject_GatewayKillFailureKeepsRecords(t *testing.T) {
	ctx := context.Background()
	del, projects, gw, id, _ := deleteFixture(ctx)
	gw.killErr = errors.New("tmux refused")

	if err := del.Execute(ctx, id); !errors.Is(err, usecase.ErrGateway) {
		t.Fatalf("want ErrGateway, got %v", err)
	}
	if _, err := projects.GetByID(ctx, id); err != nil {
		t.Errorf("records should be kept on kill failure, got %v", err)
	}
}
