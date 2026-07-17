package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestSetAgentDiscordNotification(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	agent := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")
	config := domain.DefaultDaemonConfig()
	config.DiscordWebhookNotify = "https://discord.com/api/webhooks/1/token"
	uc := usecase.NewSetAgentDiscordNotification(agents, projects, sessions, config, lock)

	for _, enabled := range []bool{true, true, false, false} {
		view, err := uc.Execute(context.Background(), usecase.SetAgentDiscordNotificationInput{AgentID: agent.ID(), Enabled: enabled})
		if err != nil {
			t.Fatalf("set enabled=%v: %v", enabled, err)
		}
		if view.Agent.DiscordNotificationArmed() != enabled {
			t.Fatalf("enabled=%v, armed=%v", enabled, view.Agent.DiscordNotificationArmed())
		}
		if view.Project.ID() != p.ID() || view.Session.ID() != s.ID() {
			t.Fatalf("returned incomplete AgentView: %+v", view)
		}
	}
}

func TestSetAgentDiscordNotificationRejectsUnconfiguredEnableButAllowsDisable(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	agent := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")
	uc := usecase.NewSetAgentDiscordNotification(agents, projects, sessions, domain.DefaultDaemonConfig(), lock)

	_, err := uc.Execute(context.Background(), usecase.SetAgentDiscordNotificationInput{AgentID: agent.ID(), Enabled: true})
	if !errors.Is(err, usecase.ErrValidation) || !strings.Contains(err.Error(), "~/.tmux-coder/config.yaml") || !strings.Contains(err.Error(), "discord_webhook_notify") {
		t.Fatalf("unconfigured error = %v", err)
	}
	stored, _ := agents.GetByID(context.Background(), agent.ID())
	if stored.DiscordNotificationArmed() {
		t.Fatal("unconfigured enable armed the agent")
	}
	if _, err := uc.Execute(context.Background(), usecase.SetAgentDiscordNotificationInput{AgentID: agent.ID(), Enabled: false}); err != nil {
		t.Fatalf("unconfigured disable: %v", err)
	}
}

func TestSetAgentDiscordNotificationMissingAgent(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	config := domain.DefaultDaemonConfig()
	config.DiscordWebhookNotify = "https://discord.com/api/webhooks/1/token"
	uc := usecase.NewSetAgentDiscordNotification(agents, projects, sessions, config, lock)
	_, err := uc.Execute(context.Background(), usecase.SetAgentDiscordNotificationInput{AgentID: 999, Enabled: true})
	if !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("error = %v, want ErrAgentNotFound", err)
	}
}
