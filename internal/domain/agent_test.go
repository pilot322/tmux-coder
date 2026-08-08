package domain_test

import (
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/domain"
)

func TestNewAgent_SetsFields(t *testing.T) {
	a := domain.NewAgent(1, 10, 20, "opencode", "my-agent", "%5", true, domain.AgentStarting)
	if a.ID() != 1 {
		t.Errorf("ID = %d, want 1", a.ID())
	}
	if a.ProjectID() != 10 {
		t.Errorf("ProjectID = %d, want 10", a.ProjectID())
	}
	if a.SessionID() != 20 {
		t.Errorf("SessionID = %d, want 20", a.SessionID())
	}
	if a.Kind() != "opencode" {
		t.Errorf("Kind = %q, want opencode", a.Kind())
	}
	if a.DisplayName() != "my-agent" {
		t.Errorf("DisplayName = %q, want my-agent", a.DisplayName())
	}
	if a.TmuxPaneID() != "%5" {
		t.Errorf("TmuxPaneID = %q, want %%5", a.TmuxPaneID())
	}
	if !a.PaneOwned() {
		t.Error("PaneOwned = false, want true")
	}
	if a.Status() != domain.AgentStarting {
		t.Errorf("Status = %q, want starting", a.Status())
	}
}

func TestAgentModelSurvivesImmutableUpdates(t *testing.T) {
	a := domain.NewAgent(1, 10, 20, "opencode", "agent", "%1", true, domain.AgentStarting).WithModel("anthropic/claude-haiku").WithVariant("high")
	updated := a.WithStatus(domain.AgentIdle).WithTmuxPaneID("%2").WithDisplayName("renamed").WithChildProcessGroupID(42).WithDiscordNotificationArmed(true)
	if updated.Model() != "anthropic/claude-haiku" {
		t.Fatalf("Model = %q", updated.Model())
	}
	if updated.Variant() != "high" {
		t.Fatalf("Variant = %q", updated.Variant())
	}
}

func TestWithStatus_ReturnsNewAgent(t *testing.T) {
	a := domain.NewAgent(1, 10, 20, "opencode", "test", "%5", true, domain.AgentStarting)
	b := a.WithStatus(domain.AgentRunning)
	if b.Status() != domain.AgentRunning {
		t.Errorf("Status = %q, want running", b.Status())
	}
	if a.Status() != domain.AgentStarting {
		t.Errorf("original agent status changed to %q", a.Status())
	}
}

func TestAgentStatusChangedAtTracksOnlyStatusChanges(t *testing.T) {
	createdAt := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	runningAt := createdAt.Add(time.Minute)
	a := domain.NewAgent(1, 10, 20, "opencode", "test", "%5", true, domain.AgentStarting, createdAt)

	if !a.StatusChangedAt().Equal(createdAt) {
		t.Fatalf("StatusChangedAt = %v, want %v", a.StatusChangedAt(), createdAt)
	}
	if renamed := a.WithDisplayName("new-name"); !renamed.StatusChangedAt().Equal(createdAt) {
		t.Fatalf("display name update moved StatusChangedAt to %v", renamed.StatusChangedAt())
	}
	if sameStatus := a.WithStatus(domain.AgentStarting, runningAt); !sameStatus.StatusChangedAt().Equal(createdAt) {
		t.Fatalf("same status update moved StatusChangedAt to %v", sameStatus.StatusChangedAt())
	}
	if running := a.WithStatus(domain.AgentRunning, runningAt); !running.StatusChangedAt().Equal(runningAt) {
		t.Fatalf("status change StatusChangedAt = %v, want %v", running.StatusChangedAt(), runningAt)
	}
}

func TestWithTmuxPaneID_ReturnsNewAgent(t *testing.T) {
	a := domain.NewAgent(1, 10, 20, "opencode", "test", "", true, domain.AgentStarting)
	b := a.WithTmuxPaneID("%42")
	if b.TmuxPaneID() != "%42" {
		t.Errorf("TmuxPaneID = %q, want %%42", b.TmuxPaneID())
	}
	if a.TmuxPaneID() != "" {
		t.Errorf("original agent pane ID changed to %q", a.TmuxPaneID())
	}
}

func TestWithDisplayName_ReturnsNewAgent(t *testing.T) {
	a := domain.NewAgent(1, 10, 20, "opencode", "", "%5", true, domain.AgentStarting)
	b := a.WithDisplayName("new-name")
	if b.DisplayName() != "new-name" {
		t.Errorf("DisplayName = %q, want new-name", b.DisplayName())
	}
}

func TestDiscordNotificationArmedIsImmutableAndPreserved(t *testing.T) {
	createdAt := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	original := domain.NewAgent(1, 10, 20, "opencode", "test", "%5", true, domain.AgentStarting, createdAt)
	armed := original.WithDiscordNotificationArmed(true)
	if original.DiscordNotificationArmed() || !armed.DiscordNotificationArmed() {
		t.Fatalf("arming mutated original or did not arm copy")
	}
	if !armed.StatusChangedAt().Equal(createdAt) {
		t.Fatalf("arming moved StatusChangedAt to %v", armed.StatusChangedAt())
	}

	copies := []*domain.Agent{
		armed.WithStatus(domain.AgentRunning),
		armed.WithTmuxPaneID("%6"),
		armed.WithDisplayName("renamed"),
		armed.WithChildProcessGroupID(123),
	}
	for i, copy := range copies {
		if !copy.DiscordNotificationArmed() {
			t.Errorf("copy %d lost armed state", i)
		}
	}
}

func TestAgentWithOpenCodeSessionIsImmutable(t *testing.T) {
	a := domain.NewAgent(1, 10, 20, "opencode", "test", "%5", true, domain.AgentRunning)
	sessionID := "ses_current"

	updated := a.WithOpenCodeSession(&sessionID, 7, 11)

	if a.OpenCodeSessionID() != nil {
		t.Fatal("updating OpenCode session mutated the original Agent")
	}
	got := updated.OpenCodeSessionID()
	if got == nil || *got != sessionID {
		t.Fatalf("OpenCodeSessionID = %v, want %q", got, sessionID)
	}
	if updated.OpenCodeSessionReporterEpoch() != 7 || updated.OpenCodeSessionSequence() != 11 {
		t.Fatalf("OpenCode session order = (%d, %d), want (7, 11)", updated.OpenCodeSessionReporterEpoch(), updated.OpenCodeSessionSequence())
	}
}

func TestAgentOpenCodeSessionSurvivesImmutableUpdates(t *testing.T) {
	sessionID := "ses_current"
	a := domain.NewAgent(1, 10, 20, "opencode", "test", "%5", true, domain.AgentStarting).
		WithOpenCodeSession(&sessionID, 7, 11)

	copies := []*domain.Agent{
		a.WithStatus(domain.AgentRunning),
		a.WithTmuxPaneID("%6"),
		a.WithDisplayName("renamed"),
		a.WithModel("anthropic/claude-haiku"),
		a.WithVariant("high"),
		a.WithChildProcessGroupID(123),
		a.WithDiscordNotificationArmed(true),
	}
	for i, copy := range copies {
		got := copy.OpenCodeSessionID()
		if got == nil || *got != sessionID {
			t.Errorf("copy %d OpenCodeSessionID = %v, want %q", i, got, sessionID)
		}
		if copy.OpenCodeSessionReporterEpoch() != 7 || copy.OpenCodeSessionSequence() != 11 {
			t.Errorf("copy %d OpenCode session order = (%d, %d), want (7, 11)", i, copy.OpenCodeSessionReporterEpoch(), copy.OpenCodeSessionSequence())
		}
	}
}

func TestDefaultAgentDisplayName(t *testing.T) {
	name := domain.DefaultAgentDisplayName(7, "opencode")
	if name != "agent-7-opencode" {
		t.Errorf("DefaultAgentDisplayName(7, opencode) = %q, want agent-7-opencode", name)
	}
}
