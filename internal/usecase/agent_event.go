package usecase

import (
	"context"
	"fmt"

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
	lock     StateLock
	log      obs.Logger
}

func NewAgentEvent(a IAgentRepository, p IProjectRepository, s ISessionRepository, n Notifier, l StateLock, log obs.Logger) *AgentEvent {
	return &AgentEvent{agents: a, projects: p, sessions: s, notifier: n, lock: l, log: log.With("component", "agent-event")}
}

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
// last-write-wins, then raises a Desktop Notification on the transitions that
// want the user's attention.
func (uc *AgentEvent) handleActivity(ctx context.Context, agentID int, status domain.AgentStatus) error {
	agent, err := uc.readAgent(ctx, agentID)
	if err != nil {
		return err
	}

	old := agent.Status()
	if err := uc.lock.WithWrite(func() error {
		_, err := uc.agents.Update(ctx, agent.WithStatus(status))
		return err
	}); err != nil {
		return err
	}

	// Notify only when an agent leaves busy for a state the user cares about.
	// Composing the body needs the project and session, looked up outside the
	// write lock; delivery is best-effort and never affects event processing
	// (ADR 0008).
	if old == domain.AgentBusy && (status == domain.AgentWaiting || status == domain.AgentIdle) {
		project, session := uc.lookupContext(ctx, agent)
		if n, ok := notificationFor(old, status, agentName(agent), project, session); ok {
			_ = uc.notifier.Notify(ctx, n)
		}
	}

	return nil
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

// notificationFor maps a busy departure to its Desktop Notification, mirroring
// the TUI's visual semantics: waiting needs the user (critical), idle is done
// (normal). It is the single source of truth for which transitions notify, so
// any other (old, new) pair yields ok=false.
func notificationFor(old, new domain.AgentStatus, name, project, session string) (Notification, bool) {
	if old != domain.AgentBusy {
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
