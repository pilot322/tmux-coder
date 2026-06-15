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
	case "busy":
		return uc.handleActivity(ctx, in.AgentID, domain.AgentBusy)
	case "idle":
		return uc.handleActivity(ctx, in.AgentID, domain.AgentIdle)
	case "waiting":
		return uc.handleActivity(ctx, in.AgentID, domain.AgentWaiting)
	case "exited":
		return uc.handleExited(ctx, in.AgentID)
	default:
		return fmt.Errorf("%w: unsupported event type %q", ErrValidation, in.Event)
	}
}

// handleStarted records the agent's process-group id, and promotes status to
// running only from starting. It never downgrades a richer status the agent's
// integration has already reported (see ADR 0008).
func (uc *AgentEvent) handleStarted(ctx context.Context, agentID int, childProcessGroupID *int) error {
	agent, err := uc.readAgent(ctx, agentID)
	if err != nil {
		return err
	}

	return uc.lock.WithWrite(func() error {
		updated := agent
		if updated.Status() == domain.AgentStarting {
			updated = updated.WithStatus(domain.AgentRunning)
		}
		if childProcessGroupID != nil {
			updated = updated.WithChildProcessGroupID(*childProcessGroupID)
		}
		_, err := uc.agents.Update(ctx, updated)
		return err
	})
}

// handleActivity applies an agent-reported activity status (busy/idle/waiting)
// last-write-wins.
func (uc *AgentEvent) handleActivity(ctx context.Context, agentID int, status domain.AgentStatus) error {
	agent, err := uc.readAgent(ctx, agentID)
	if err != nil {
		return err
	}

	return uc.lock.WithWrite(func() error {
		_, err := uc.agents.Update(ctx, agent.WithStatus(status))
		return err
	})
}

func (uc *AgentEvent) readAgent(ctx context.Context, agentID int) (*domain.Agent, error) {
	var agent *domain.Agent
	err := uc.lock.WithRead(func() error {
		a, err := uc.agents.GetByID(ctx, agentID)
		if err != nil {
			return err
		}
		agent = a
		return nil
	})
	return agent, err
}

func (uc *AgentEvent) handleExited(ctx context.Context, agentID int) error {
	return uc.lock.WithWrite(func() error {
		return uc.agents.Delete(ctx, agentID)
	})
}
