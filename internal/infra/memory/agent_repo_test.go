package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestMemoryAgentRepository_CreateAssignsID(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryAgentRepository()

	a1, err := r.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "", "%1", true, domain.AgentStarting))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a1.ID() == 0 {
		t.Fatal("want nonzero id after Create")
	}
	a2, err := r.Create(ctx, domain.NewAgent(0, 1, 3, "claude", "", "%2", true, domain.AgentStarting))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a2.ID() == a1.ID() {
		t.Fatal("want distinct ids")
	}
}

func TestMemoryAgentRepository_GetByID(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryAgentRepository()
	_, err := r.GetByID(ctx, 999)
	if !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("want ErrAgentNotFound, got %v", err)
	}
	created, _ := r.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%1", true, domain.AgentStarting))
	got, err := r.GetByID(ctx, created.ID())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID() != created.ID() {
		t.Fatalf("ID = %d, want %d", got.ID(), created.ID())
	}
}

func TestMemoryAgentRepository_GetBySessionID(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryAgentRepository()
	r.Create(ctx, domain.NewAgent(0, 1, 10, "opencode", "a", "%1", true, domain.AgentStarting))
	r.Create(ctx, domain.NewAgent(0, 1, 10, "claude", "b", "%2", true, domain.AgentStarting))
	r.Create(ctx, domain.NewAgent(0, 2, 20, "opencode", "c", "%3", true, domain.AgentStarting))

	agents, err := r.GetBySessionID(ctx, 10)
	if err != nil {
		t.Fatalf("GetBySessionID: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("want 2 agents for session 10, got %d", len(agents))
	}
}

func TestMemoryAgentRepository_Update(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryAgentRepository()
	created, _ := r.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%1", true, domain.AgentStarting))
	updated := created.WithStatus(domain.AgentRunning)
	result, err := r.Update(ctx, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.Status() != domain.AgentRunning {
		t.Fatalf("Status = %q, want running", result.Status())
	}
	got, _ := r.GetByID(ctx, created.ID())
	if got.Status() != domain.AgentRunning {
		t.Fatalf("Status after update = %q, want running", got.Status())
	}
}

func TestMemoryAgentRepository_UpdateNotFound(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryAgentRepository()
	ghost := domain.NewAgent(999, 1, 2, "opencode", "test", "%1", true, domain.AgentRunning)
	_, err := r.Update(ctx, ghost)
	if !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("want ErrAgentNotFound, got %v", err)
	}
}

func TestMemoryAgentRepository_Delete(t *testing.T) {
	ctx := context.Background()
	r := memory.NewMemoryAgentRepository()
	created, _ := r.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%1", true, domain.AgentStarting))
	if err := r.Delete(ctx, created.ID()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := r.GetByID(ctx, created.ID())
	if !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("want ErrAgentNotFound after delete, got %v", err)
	}
}