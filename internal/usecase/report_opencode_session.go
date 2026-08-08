package usecase

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

type ReportOpenCodeSessionInput struct {
	AgentID       int
	TmuxPaneID    string
	SessionID     *string
	ReporterEpoch uint64
	Sequence      uint64
}

type ReportOpenCodeSession struct {
	agents IAgentRepository
	lock   StateLock
}

func NewReportOpenCodeSession(agents IAgentRepository, lock StateLock) *ReportOpenCodeSession {
	return &ReportOpenCodeSession{agents: agents, lock: lock}
}

func (uc *ReportOpenCodeSession) Execute(ctx context.Context, in ReportOpenCodeSessionInput) error {
	if in.AgentID <= 0 {
		return fmt.Errorf("%w: agentId is required", ErrValidation)
	}
	if !validStablePaneID(in.TmuxPaneID) {
		return fmt.Errorf("%w: tmuxPaneId must be a stable tmux pane id like %%12", ErrValidation)
	}
	if in.SessionID != nil && *in.SessionID != "" && strings.IndexFunc(*in.SessionID, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) != -1 {
		return fmt.Errorf("%w: sessionId must not contain whitespace or control characters", ErrValidation)
	}
	if in.ReporterEpoch == 0 {
		return fmt.Errorf("%w: reporterEpoch must be positive", ErrValidation)
	}
	if in.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be positive", ErrValidation)
	}

	return uc.lock.WithWrite(func() error {
		agent, err := uc.agents.GetByID(ctx, in.AgentID)
		if err != nil {
			return err
		}
		if agent.Kind() != "opencode" {
			return fmt.Errorf("%w: OpenCode session reports require an opencode Agent Kind", ErrValidation)
		}
		if agent.TmuxPaneID() != in.TmuxPaneID {
			return fmt.Errorf("%w: tmuxPaneId does not match Agent", ErrValidation)
		}
		currentEpoch := agent.OpenCodeSessionReporterEpoch()
		if in.ReporterEpoch < currentEpoch ||
			(in.ReporterEpoch == currentEpoch && in.Sequence <= agent.OpenCodeSessionSequence()) {
			return nil
		}
		_, err = uc.agents.Update(ctx, agent.WithOpenCodeSession(in.SessionID, in.ReporterEpoch, in.Sequence))
		return err
	})
}
