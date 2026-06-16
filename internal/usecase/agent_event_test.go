package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// fakeNotifier records the Desktop Notifications it is asked to deliver. onNotify,
// when set, runs inside Notify so a test can observe state at delivery time (e.g.
// that the status update is already committed before the notification fires).
type fakeNotifier struct {
	calls    []usecase.Notification
	err      error
	onNotify func()
}

func (n *fakeNotifier) Notify(_ context.Context, msg usecase.Notification) error {
	n.calls = append(n.calls, msg)
	if n.onNotify != nil {
		n.onNotify()
	}
	return n.err
}

// seedBusyAgent stores an already-busy agent with a known display name in the
// fixture's project/session, so a follow-up activity event exercises a departure
// from busy.
func seedBusyAgent(t *testing.T, agents *memory.MemoryAgentRepository, projectID, sessionID int, displayName string) *domain.Agent {
	t.Helper()
	a, err := agents.Create(context.Background(), domain.NewAgent(0, projectID, sessionID, "opencode", displayName, "%10", true, domain.AgentBusy))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAgentEvent_NotifiesOnBusyToWaiting(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	agent := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")

	notifier := &fakeNotifier{}
	eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: agent.ID(), Event: "waiting"}); err != nil {
		t.Fatalf("waiting event: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("expected exactly 1 notification, got %d", len(notifier.calls))
	}
	got := notifier.calls[0]
	if got.Urgency != usecase.UrgencyCritical {
		t.Fatalf("urgency = %v, want critical", got.Urgency)
	}
	if got.Title != "reviewer needs input" {
		t.Fatalf("title = %q, want %q", got.Title, "reviewer needs input")
	}
	if got.Body != "api · api.main" {
		t.Fatalf("body = %q, want %q", got.Body, "api · api.main")
	}
	if !got.Sound {
		t.Fatalf("expected the notification to request a sound")
	}
}

func TestAgentEvent_NotifiesOnBusyToIdle(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	agent := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")

	notifier := &fakeNotifier{}
	eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: agent.ID(), Event: "idle"}); err != nil {
		t.Fatalf("idle event: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("expected exactly 1 notification, got %d", len(notifier.calls))
	}
	got := notifier.calls[0]
	if got.Urgency != usecase.UrgencyNormal {
		t.Fatalf("urgency = %v, want normal", got.Urgency)
	}
	if got.Title != "reviewer is idle" {
		t.Fatalf("title = %q, want %q", got.Title, "reviewer is idle")
	}
	if got.Body != "api · api.main" {
		t.Fatalf("body = %q, want %q", got.Body, "api · api.main")
	}
	if !got.Sound {
		t.Fatalf("expected the notification to request a sound")
	}
}

func TestAgentEvent_NotificationNameFallsBackToAgentID(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	agent := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")
	// The repo defaults blank names on Create, so force a genuinely empty display
	// name to exercise the notification's fallback.
	if _, err := agents.Update(ctx, agent.WithDisplayName("")); err != nil {
		t.Fatal(err)
	}

	notifier := &fakeNotifier{}
	eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: agent.ID(), Event: "waiting"}); err != nil {
		t.Fatalf("waiting event: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("expected exactly 1 notification, got %d", len(notifier.calls))
	}
	want := fmt.Sprintf("agent-%d needs input", agent.ID())
	if notifier.calls[0].Title != want {
		t.Fatalf("title = %q, want %q", notifier.calls[0].Title, want)
	}
}

func TestAgentEvent_DoesNotNotifyOnNonBusyDepartures(t *testing.T) {
	cases := []struct {
		name  string
		start domain.AgentStatus
		event string
	}{
		{"running to waiting", domain.AgentRunning, "waiting"},
		{"idle to waiting", domain.AgentIdle, "waiting"},
		{"starting to idle", domain.AgentStarting, "idle"},
		{"busy to busy", domain.AgentBusy, "busy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, agents, projects, sessions, _, lock := agentFixture()
			p, s := seedProjectAndSession(projects, sessions)
			ctx := context.Background()
			a, _ := agents.Create(ctx, domain.NewAgent(0, p.ID(), s.ID(), "opencode", "reviewer", "%10", true, tc.start))

			notifier := &fakeNotifier{}
			eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
			if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: a.ID(), Event: tc.event}); err != nil {
				t.Fatalf("%s event: %v", tc.event, err)
			}
			if len(notifier.calls) != 0 {
				t.Fatalf("%s must not notify, got %d", tc.name, len(notifier.calls))
			}
		})
	}
}

func TestAgentEvent_StartedAndExitedNeverNotify(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	a := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")

	notifier := &fakeNotifier{}
	eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: a.ID(), Event: "started"}); err != nil {
		t.Fatalf("started: %v", err)
	}
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: a.ID(), Event: "exited"}); err != nil {
		t.Fatalf("exited: %v", err)
	}
	if len(notifier.calls) != 0 {
		t.Fatalf("started/exited must not notify, got %d", len(notifier.calls))
	}
}

func TestAgentEvent_NotifyFailureIsSwallowed(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	a := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")

	notifier := &fakeNotifier{err: errors.New("no session bus")}
	eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: a.ID(), Event: "waiting"}); err != nil {
		t.Fatalf("notify failure must not fail the event: %v", err)
	}
	// The status update still happened despite the notify error.
	got, _ := agents.GetByID(ctx, a.ID())
	if got.Status() != domain.AgentWaiting {
		t.Fatalf("status = %q, want waiting", got.Status())
	}
}

func TestAgentEvent_StatusCommittedBeforeNotifyAndOutsideLock(t *testing.T) {
	_, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	a := seedBusyAgent(t, agents, p.ID(), s.ID(), "reviewer")

	var statusAtNotify domain.AgentStatus
	var inWriteAtNotify bool
	notifier := &fakeNotifier{onNotify: func() {
		inWriteAtNotify = lock.inWrite
		got, _ := agents.GetByID(ctx, a.ID())
		statusAtNotify = got.Status()
	}}
	eventUc := usecase.NewAgentEvent(agents, projects, sessions, notifier, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: a.ID(), Event: "waiting"}); err != nil {
		t.Fatalf("waiting event: %v", err)
	}

	if statusAtNotify != domain.AgentWaiting {
		t.Fatalf("status at notify = %q, want waiting (update must precede notify)", statusAtNotify)
	}
	if inWriteAtNotify {
		t.Fatalf("notify must run outside the write lock")
	}
}
