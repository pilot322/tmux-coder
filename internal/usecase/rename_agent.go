package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
)

type RenameAgentInput struct {
	AgentID     int
	DisplayName string
}

type RenameAgent struct {
	agents   IAgentRepository
	projects IProjectRepository
	sessions ISessionRepository
	tmux     AgentTmuxGateway
	lock     StateLock
	log      obs.Logger
}

func NewRenameAgent(a IAgentRepository, p IProjectRepository, s ISessionRepository, tmux AgentTmuxGateway, l StateLock, log obs.Logger) *RenameAgent {
	return &RenameAgent{agents: a, projects: p, sessions: s, tmux: tmux, lock: l, log: log.With("component", "rename-agent")}
}

func (uc *RenameAgent) Execute(ctx context.Context, in RenameAgentInput) (AgentView, error) {
	name := strings.TrimSpace(in.DisplayName)
	if in.AgentID == 0 {
		return AgentView{}, fmt.Errorf("%w: agentId is required", ErrValidation)
	}
	if name == "" {
		return AgentView{}, fmt.Errorf("%w: displayName is required", ErrValidation)
	}

	var agent *domain.Agent
	if err := uc.lock.WithWrite(func() error {
		current, err := uc.agents.GetByID(ctx, in.AgentID)
		if err != nil {
			return err
		}
		updated, err := uc.agents.Update(ctx, current.WithDisplayName(name))
		if err != nil {
			return err
		}
		agent = updated
		return nil
	}); err != nil {
		return AgentView{}, err
	}
	if err := uc.tmux.RenameWindow(ctx, agent.TmuxPaneID(), agent.DisplayName()); err != nil {
		uc.log.Warn(ctx, "agent window rename failed", "agent_id", agent.ID(), "pane_id", agent.TmuxPaneID(), "display_name", agent.DisplayName(), "err", err.Error())
	}

	var project *domain.Project
	var session *domain.Session
	var sessions []*domain.Session
	if err := uc.lock.WithRead(func() error {
		p, err := uc.projects.GetByID(ctx, agent.ProjectID())
		if err != nil {
			return err
		}
		project = p
		s, err := uc.sessions.GetByID(ctx, agent.SessionID())
		if err != nil {
			return err
		}
		session = s
		allSessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		sessions = allSessions
		return nil
	}); err != nil {
		return AgentView{}, err
	}

	view := AgentView{Agent: agent, Project: project, Session: session}
	for _, s := range sessions {
		if s.ProjectID() == project.ID() && s.Type() == domain.MainSession {
			view.MainSessionName = s.Name()
			view.MainTmuxSessionName = s.TmuxName()
			break
		}
	}
	return view, nil
}
