package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type AgentEventInput struct {
	AgentID             int
	Event               string
	ChildProcessGroupID *int
}

type AgentEvent struct {
	agents IAgentRepository
	lock   StateLock
}

func NewAgentEvent(a IAgentRepository, l StateLock) *AgentEvent {
	return &AgentEvent{agents: a, lock: l}
}

func (uc *AgentEvent) Execute(ctx context.Context, in AgentEventInput) error {
	switch in.Event {
	case "started":
		return uc.handleStarted(ctx, in.AgentID, in.ChildProcessGroupID)
	case "exited":
		return uc.handleExited(ctx, in.AgentID)
	default:
		return fmt.Errorf("%w: unsupported event type %q", ErrValidation, in.Event)
	}
}

func (uc *AgentEvent) handleStarted(ctx context.Context, agentID int, childProcessGroupID *int) error {
	var agent *domain.Agent
	if err := uc.lock.WithRead(func() error {
		a, err := uc.agents.GetByID(ctx, agentID)
		if err != nil {
			return err
		}
		agent = a
		return nil
	}); err != nil {
		return err
	}

	return uc.lock.WithWrite(func() error {
		updated := agent.WithStatus(domain.AgentRunning)
		if childProcessGroupID != nil {
			updated = updated.WithChildProcessGroupID(*childProcessGroupID)
		}
		_, err := uc.agents.Update(ctx, updated)
		return err
	})
}

func (uc *AgentEvent) handleExited(ctx context.Context, agentID int) error {
	return uc.lock.WithWrite(func() error {
		return uc.agents.Delete(ctx, agentID)
	})
}
