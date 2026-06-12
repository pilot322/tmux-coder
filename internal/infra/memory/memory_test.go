package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestMemoryProjectRepository_AssignsIncrementingIDs(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryProjectRepository()

	p1, _ := r.Create(ctx, domain.NewProject(0, "/work/a", "a"))
	p2, _ := r.Create(ctx, domain.NewProject(0, "/work/b", "b"))

	if p1.ID() == 0 || p2.ID() == 0 || p1.ID() == p2.ID() {
		t.Fatalf("want distinct nonzero ids, got %d and %d", p1.ID(), p2.ID())
	}
}

func TestMemoryProjectRepository_GetByIDNotFound(t *testing.T) {
	_, err := memory.NewMemoryProjectRepository().GetByID(context.Background(), 999)
	if !errors.Is(err, usecase.ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestMemoryProjectRepository_GetByFullPath(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryProjectRepository()
	created, _ := r.Create(ctx, domain.NewProject(0, "/work/api", "API"))

	got, err := r.GetByFullPath(ctx, "/work/api")
	if err != nil || got.ID() != created.ID() {
		t.Fatalf("GetByFullPath(/work/api) = (%v, %v), want id %d", got, err, created.ID())
	}
	if got.Title() != "API" {
		t.Fatalf("Title = %q, want API", got.Title())
	}

	if _, err := r.GetByFullPath(ctx, "/nope"); !errors.Is(err, usecase.ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound for missing path, got %v", err)
	}
}

func TestMemoryProjectRepository_DeleteRemoves(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryProjectRepository()
	p, _ := r.Create(ctx, domain.NewProject(0, "/work/a", "a"))

	if err := r.Delete(ctx, p.ID()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.GetByID(ctx, p.ID()); !errors.Is(err, usecase.ErrProjectNotFound) {
		t.Fatalf("deleted project should be gone, got %v", err)
	}
}

func TestMemorySessionRepository_GetByProjectIDFilters(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemorySessionRepository()
	_, _ = r.Create(ctx, domain.NewSession(0, -1, 1, "a-main", domain.MainSession))
	_, _ = r.Create(ctx, domain.NewSession(0, -1, 2, "b-main", domain.MainSession))
	_, _ = r.Create(ctx, domain.NewSession(0, -1, 1, "a-sec", domain.SecondarySession))

	got, err := r.GetByProjectID(ctx, 1)
	if err != nil {
		t.Fatalf("GetByProjectID: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions for project 1, got %d", len(got))
	}
}

func TestDaemonState_WithWritePropagatesError(t *testing.T) {
	state := memory.NewDaemonState()
	sentinel := errors.New("boom")

	err := state.WithWrite(func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithWrite should return fn's error, got %v", err)
	}
}
