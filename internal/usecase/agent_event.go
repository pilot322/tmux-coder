package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
)

type AgentEventInput struct {
	AgentID             int
	Event               string
	ChildProcessGroupID *int
}

type AgentEvent struct {
	agents   IAgentRepository
	projects IProjectRepository
	sessions ISessionRepository
	notifier Notifier
	discord  DiscordNotifier
	lock     StateLock
	log      obs.Logger
}

func NewAgentEvent(a IAgentRepository, p IProjectRepository, s ISessionRepository, n Notifier, l StateLock, log obs.Logger) *AgentEvent {
	return NewAgentEventWithDiscord(a, p, s, n, noopDiscordNotifier{}, l, log)
}

func NewAgentEventWithDiscord(a IAgentRepository, p IProjectRepository, s ISessionRepository, n Notifier, d DiscordNotifier, l StateLock, log obs.Logger) *AgentEvent {
	return &AgentEvent{agents: a, projects: p, sessions: s, notifier: n, discord: d, lock: l, log: log.With("component", "agent-event")}
}

type noopDiscordNotifier struct{}

func (noopDiscordNotifier) Notify(context.Context, DiscordMessage) error { return nil }

func (uc *AgentEvent) Execute(ctx context.Context, in AgentEventInput) error {
	uc.log.Debug(ctx, "agent event received", "agent_id", in.AgentID, "event", in.Event)
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
	return uc.lock.WithWrite(func() error {
		agent, err := uc.agents.GetByID(ctx, agentID)
		if err != nil {
			return err
		}
		updated := agent
		if updated.Status() == domain.AgentStarting {
			updated = updated.WithStatus(domain.AgentRunning)
		}
		if childProcessGroupID != nil {
			updated = updated.WithChildProcessGroupID(*childProcessGroupID)
		}
		_, err = uc.agents.Update(ctx, updated)
		return err
	})
}

// handleActivity applies an agent-reported activity status (busy/idle/waiting)
// last-write-wins, then raises a Desktop Notification on the transitions that
// want the user's attention.
func (uc *AgentEvent) handleActivity(ctx context.Context, agentID int, status domain.AgentStatus) error {
	var agent *domain.Agent
	var old domain.AgentStatus
	var sendDiscord bool
	if err := uc.lock.WithWrite(func() error {
		current, err := uc.agents.GetByID(ctx, agentID)
		if err != nil {
			return err
		}
		agent = current
		old = current.Status()
		updated := current.WithStatus(status)
		sendDiscord = current.DiscordNotificationArmed() && old == domain.AgentBusy &&
			(status == domain.AgentWaiting || status == domain.AgentIdle)
		if current.DiscordNotificationArmed() && status == domain.AgentIdle {
			updated = updated.WithDiscordNotificationArmed(false)
		}
		_, err = uc.agents.Update(ctx, updated)
		return err
	}); err != nil {
		return err
	}

	// Notify when an agent enters a state the user cares about.
	// Composing the body needs the project and session, looked up outside the
	// write lock; delivery is best-effort and never affects event processing
	// (ADR 0008).
	if old != status && (status == domain.AgentWaiting || status == domain.AgentIdle) {
		project, session := uc.lookupContext(ctx, agent)
		if n, ok := notificationFor(old, status, agentName(agent), project, session); ok {
			_ = uc.notifier.Notify(ctx, n)
			if sendDiscord {
				_ = uc.discord.Notify(ctx, discordMessage(n, project, session))
			}
		}
	}

	return nil
}

// Discord embed colors, chosen to mirror notification urgency: critical reading
// (waiting / needs input) glows red, normal reading (idle / done) glows green.
const (
	discordColorCritical = 0xE74C3C
	discordColorNormal   = 0x2ECC71
)

// discordMessage composes the structured Discord webhook payload for a one-shot
// notification. Content is a single-line summary so mobile push notifications
// show the same essential information at a glance; Embed renders a styled card
// with color keyed off urgency and inline Project/Session fields in the Discord
// client.
func discordMessage(n Notification, project, session string) DiscordMessage {
	color := discordColorNormal
	if n.Urgency == UrgencyCritical {
		color = discordColorCritical
	}
	fields := make([]DiscordField, 0, 2)
	if project != "" {
		fields = append(fields, DiscordField{Name: "Project", Value: project, Inline: true})
	}
	if session != "" {
		fields = append(fields, DiscordField{Name: "Session", Value: session, Inline: true})
	}
	// Single-line content for mobile push; the embed repeats the title and adds
	// rich fields so the in-client card is informative.
	line := n.Title
	if project != "" || session != "" {
		parts := make([]string, 0, 3)
		if n.Title != "" {
			parts = append(parts, n.Title)
		}
		if project != "" {
			parts = append(parts, project)
		}
		if session != "" {
			parts = append(parts, session)
		}
		line = strings.Join(parts, " · ")
	}
	return DiscordMessage{
		Content: line,
		Embed: DiscordEmbed{
			Title:       n.Title,
			Description: n.Body,
			Color:       color,
			Fields:      fields,
		},
	}
}

// lookupContext fetches the agent's project title and session name for the
// notification body, tolerating missing lookups by leaving that part empty.
func (uc *AgentEvent) lookupContext(ctx context.Context, agent *domain.Agent) (project, session string) {
	_ = uc.lock.WithRead(func() error {
		if p, err := uc.projects.GetByID(ctx, agent.ProjectID()); err == nil {
			project = p.Title()
		}
		if s, err := uc.sessions.GetByID(ctx, agent.SessionID()); err == nil {
			session = s.Name()
		}
		return nil
	})
	return project, session
}

// agentName is the agent's display name, falling back to agent-{id} — the same
// fallback the TUI's agentRowLabel uses.
func agentName(agent *domain.Agent) string {
	if name := agent.DisplayName(); name != "" {
		return name
	}
	return fmt.Sprintf("agent-%d", agent.ID())
}

// notificationFor maps a transition into a user-relevant state to its Desktop
// Notification, mirroring the TUI's visual semantics: waiting needs the user
// (critical), idle is done (normal). It is the single source of truth for which
// transitions notify, so any other (old, new) pair yields ok=false.
func notificationFor(old, new domain.AgentStatus, name, project, session string) (Notification, bool) {
	if old == new {
		return Notification{}, false
	}
	body := project + " · " + session
	// Both qualifying transitions request a sound; whether one is actually
	// audible is the mechanism's call (player present, sound enabled).
	switch new {
	case domain.AgentWaiting:
		return Notification{Title: name + " needs input", Body: body, Urgency: UrgencyCritical, Sound: true, SoundName: "agent-waiting"}, true
	case domain.AgentIdle:
		return Notification{Title: name + " is idle", Body: body, Urgency: UrgencyNormal, Sound: true, SoundName: "agent-idle"}, true
	default:
		return Notification{}, false
	}
}

func (uc *AgentEvent) handleExited(ctx context.Context, agentID int) error {
	return uc.lock.WithWrite(func() error {
		return uc.agents.Delete(ctx, agentID)
	})
}
