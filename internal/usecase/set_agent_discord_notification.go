package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
)

const DiscordConfigurationGuidance = "configure ~/.tmux-coder/config.yaml with discord_webhook_notify and restart the daemon"

type SetAgentDiscordNotificationInput struct {
	AgentID int
	Enabled bool
}

type SetAgentDiscordNotification struct {
	agents   IAgentRepository
	projects IProjectRepository
	sessions ISessionRepository
	config   domain.DaemonConfig
	lock     StateLock
}

func NewSetAgentDiscordNotification(a IAgentRepository, p IProjectRepository, s ISessionRepository, config domain.DaemonConfig, l StateLock) *SetAgentDiscordNotification {
	return &SetAgentDiscordNotification{agents: a, projects: p, sessions: s, config: config, lock: l}
}

func (uc *SetAgentDiscordNotification) Execute(ctx context.Context, in SetAgentDiscordNotificationInput) (AgentView, error) {
	if in.AgentID == 0 {
		return AgentView{}, fmt.Errorf("%w: agentId is required", ErrValidation)
	}
	if in.Enabled && !uc.config.DiscordWebhookConfigured() {
		return AgentView{}, fmt.Errorf("%w: %s", ErrValidation, DiscordConfigurationGuidance)
	}

	var view AgentView
	err := uc.lock.WithWrite(func() error {
		agent, err := uc.agents.GetByID(ctx, in.AgentID)
		if err != nil {
			return err
		}
		agent, err = uc.agents.Update(ctx, agent.WithDiscordNotificationArmed(in.Enabled))
		if err != nil {
			return err
		}
		project, err := uc.projects.GetByID(ctx, agent.ProjectID())
		if err != nil {
			return err
		}
		session, err := uc.sessions.GetByID(ctx, agent.SessionID())
		if err != nil {
			return err
		}
		allSessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		view = AgentView{Agent: agent, Project: project, Session: session}
		for _, candidate := range allSessions {
			if candidate.ProjectID() == project.ID() && candidate.Type() == domain.MainSession {
				view.MainSessionName = candidate.Name()
				view.MainTmuxSessionName = candidate.TmuxName()
				break
			}
		}
		return nil
	})
	return view, err
}
