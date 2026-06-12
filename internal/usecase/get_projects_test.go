package usecase_test

import (
	"context"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestGetProjects_ReturnsProjectsWithMainSessionNames(t *testing.T) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	gw := newFakeGateway(lock)
	ctx := context.Background()

	create := usecase.NewCreateProject(projects, sessions, gw, lock, domain.DefaultDaemonConfig())
	_, _ = create.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	_, _ = create.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/web"})

	get := usecase.NewGetProjects(projects, sessions, lock)
	views, err := get.Execute(ctx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("want 2 projects, got %d", len(views))
	}
	for _, v := range views {
		if v.MainSessionName == "" {
			t.Errorf("project %d has empty main session name", v.Project.ID())
		}
	}
}

func TestGetProjects_EmptyWhenNoProjects(t *testing.T) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}

	views, err := usecase.NewGetProjects(projects, sessions, lock).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("want no projects, got %d", len(views))
	}
}
