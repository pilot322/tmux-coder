package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
)

const defaultSigtermTimeout = 5 * time.Second

type DeleteAgent struct {
	agents  IAgentRepository
	tmux    AgentTmuxGateway
	process AgentProcessGateway
	lock    StateLock
	log     obs.Logger
}

func NewDeleteAgent(a IAgentRepository, tmux AgentTmuxGateway, process AgentProcessGateway, l StateLock, log obs.Logger) *DeleteAgent {
	return &DeleteAgent{agents: a, tmux: tmux, process: process, lock: l, log: log.With("component", "delete-agent")}
}

func (uc *DeleteAgent) Execute(ctx context.Context, id int) error {
	var agent *domain.Agent
	if err := uc.lock.WithRead(func() error {
		a, err := uc.agents.GetByID(ctx, id)
		if err != nil {
			return err
		}
		agent = a
		return nil
	}); err != nil {
		return err
	}

	if agent.PaneOwned() {
		if err := uc.tmux.KillPane(ctx, agent.TmuxPaneID()); err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
	} else {
		if agent.ChildProcessGroupID() == 0 {
			refreshed, err := uc.waitForChildProcessGroup(ctx, id, defaultSigtermTimeout)
			if err != nil {
				return err
			}
			agent = refreshed
		}
		if uc.process == nil {
			return fmt.Errorf("%w: process gateway is not configured", ErrGateway)
		}
		if err := uc.process.TerminateProcessGroup(ctx, agent.ChildProcessGroupID(), defaultSigtermTimeout); err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
	}

	uc.log.Info(ctx, "deleting agent", "agent_id", id, "pane_owned", agent.PaneOwned())
	return uc.lock.WithWrite(func() error {
		return uc.agents.Delete(ctx, id)
	})
}

func (uc *DeleteAgent) waitForChildProcessGroup(ctx context.Context, id int, timeout time.Duration) (*domain.Agent, error) {
	deadline := time.Now().Add(timeout)
	for {
		var agent *domain.Agent
		if err := uc.lock.WithRead(func() error {
			a, err := uc.agents.GetByID(ctx, id)
			if err != nil {
				return err
			}
			agent = a
			return nil
		}); err != nil {
			return nil, err
		}
		if agent.ChildProcessGroupID() != 0 {
			return agent, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: agent child process group is not known yet", ErrConflict)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
