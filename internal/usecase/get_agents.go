package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
)

type GetAgentsInput struct {
	ProjectID *int
	SessionID *int
}

type AgentView struct {
	Agent               *domain.Agent
	Project             *domain.Project
	Session             *domain.Session
	MainSessionName     string
	MainTmuxSessionName string
}

type GetAgents struct {
	agents   IAgentRepository
	projects IProjectRepository
	sessions ISessionRepository
	tmux     AgentTmuxGateway
	lock     StateLock
	log      obs.Logger
}

func NewGetAgents(a IAgentRepository, p IProjectRepository, s ISessionRepository, tmux AgentTmuxGateway, l StateLock, log obs.Logger) *GetAgents {
	return &GetAgents{agents: a, projects: p, sessions: s, tmux: tmux, lock: l, log: log.With("component", "get-agents")}
}

func (uc *GetAgents) Execute(ctx context.Context, in GetAgentsInput) ([]AgentView, error) {
	if in.ProjectID != nil && in.SessionID != nil {
		var project *domain.Project
		var session *domain.Session
		if err := uc.lock.WithRead(func() error {
			p, err := uc.projects.GetByID(ctx, *in.ProjectID)
			if err != nil {
				return err
			}
			project = p
			s, err := uc.sessions.GetByID(ctx, *in.SessionID)
			if err != nil {
				return err
			}
			session = s
			return nil
		}); err != nil {
			return nil, err
		}
		if session.ProjectID() != project.ID() {
			return nil, fmt.Errorf("%w: session does not belong to project", ErrValidation)
		}
	}

	var agents []*domain.Agent
	var projects []*domain.Project
	var sessions []*domain.Session

	if err := uc.lock.WithRead(func() error {
		all, err := uc.agents.GetAll(ctx)
		if err != nil {
			return err
		}
		agents = all

		projs, err := uc.projects.GetAll(ctx)
		if err != nil {
			return err
		}
		projects = projs

		sess, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		sessions = sess
		return nil
	}); err != nil {
		return nil, err
	}

	projectsByID := make(map[int]*domain.Project, len(projects))
	for _, p := range projects {
		projectsByID[p.ID()] = p
	}
	sessionsByID := make(map[int]*domain.Session, len(sessions))
	for _, s := range sessions {
		sessionsByID[s.ID()] = s
	}
	mainByProject := make(map[int]*domain.Session)
	for _, s := range sessions {
		if s.Type() == domain.MainSession {
			mainByProject[s.ProjectID()] = s
		}
	}

	var pruned []int
	for _, a := range agents {
		if a.TmuxPaneID() != "" {
			exists, err := uc.tmux.PaneExists(ctx, a.TmuxPaneID())
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrGateway, err)
			}
			if !exists {
				pruned = append(pruned, a.ID())
			}
		}
	}

	if len(pruned) > 0 {
		uc.log.Info(ctx, "pruning agents whose panes are gone", "count", len(pruned), "agent_ids", pruned)
		if err := uc.lock.WithWrite(func() error {
			for _, id := range pruned {
				if err := uc.agents.Delete(ctx, id); err != nil {
					return err
				}
			}
			all, err := uc.agents.GetAll(ctx)
			if err != nil {
				return err
			}
			agents = all
			return nil
		}); err != nil {
			return nil, err
		}
	}

	views := make([]AgentView, 0, len(agents))
	for _, a := range agents {
		if in.ProjectID != nil && a.ProjectID() != *in.ProjectID {
			continue
		}
		if in.SessionID != nil && a.SessionID() != *in.SessionID {
			continue
		}
		p := projectsByID[a.ProjectID()]
		s := sessionsByID[a.SessionID()]
		if p == nil || s == nil {
			continue
		}
		view := AgentView{Agent: a, Project: p, Session: s}
		if main := mainByProject[p.ID()]; main != nil {
			view.MainSessionName = main.Name()
			view.MainTmuxSessionName = main.TmuxName()
		}
		views = append(views, view)
	}

	return views, nil
}
