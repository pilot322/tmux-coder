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

func TestDefaultAgentDisplayName(t *testing.T) {
	name := domain.DefaultAgentDisplayName(7, "opencode")
	if name != "agent-7-opencode" {
		t.Errorf("DefaultAgentDisplayName(7, opencode) = %q, want agent-7-opencode", name)
	}
}
