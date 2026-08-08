package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestReportOpenCodeSessionRejectsUnknownAgent(t *testing.T) {
	uc := usecase.NewReportOpenCodeSession(memory.NewMemoryAgentRepository(), &spyLock{})
	sessionID := "ses_current"
	err := uc.Execute(context.Background(), usecase.ReportOpenCodeSessionInput{
		AgentID:       999,
		TmuxPaneID:    "%5",
		SessionID:     &sessionID,
		ReporterEpoch: 7,
		Sequence:      1,
	})

	if !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("Execute error = %v, want ErrAgentNotFound", err)
	}
}

func TestReportOpenCodeSessionAssociatesSessionWithAgent(t *testing.T) {
	ctx := context.Background()
	agents := memory.NewMemoryAgentRepository()
	agent, err := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%5", true, domain.AgentRunning))
	if err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewReportOpenCodeSession(agents, &spyLock{})
	sessionID := "ses_current"

	err = uc.Execute(ctx, usecase.ReportOpenCodeSessionInput{
		AgentID:       agent.ID(),
		TmuxPaneID:    "%5",
		SessionID:     &sessionID,
		ReporterEpoch: 7,
		Sequence:      1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stored, err := agents.GetByID(ctx, agent.ID())
	if err != nil {
		t.Fatal(err)
	}
	got := stored.OpenCodeSessionID()
	if got == nil || *got != sessionID {
		t.Fatalf("OpenCodeSessionID = %v, want %q", got, sessionID)
	}
	if stored.OpenCodeSessionReporterEpoch() != 7 || stored.OpenCodeSessionSequence() != 1 {
		t.Fatalf("OpenCode session order = (%d, %d), want (7, 1)", stored.OpenCodeSessionReporterEpoch(), stored.OpenCodeSessionSequence())
	}
}

func TestReportOpenCodeSessionRejectsMalformedReport(t *testing.T) {
	ctx := context.Background()
	agents := memory.NewMemoryAgentRepository()
	agent, err := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%5", true, domain.AgentRunning))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_current"
	valid := usecase.ReportOpenCodeSessionInput{
		AgentID:       agent.ID(),
		TmuxPaneID:    "%5",
		SessionID:     &sessionID,
		ReporterEpoch: 7,
		Sequence:      1,
	}
	invalidSessionID := "ses invalid"
	tests := []struct {
		name   string
		mutate func(*usecase.ReportOpenCodeSessionInput)
	}{
		{"missing Agent ID", func(in *usecase.ReportOpenCodeSessionInput) { in.AgentID = 0 }},
		{"invalid pane ID", func(in *usecase.ReportOpenCodeSessionInput) { in.TmuxPaneID = "5" }},
		{"invalid session ID", func(in *usecase.ReportOpenCodeSessionInput) { in.SessionID = &invalidSessionID }},
		{"missing reporter epoch", func(in *usecase.ReportOpenCodeSessionInput) { in.ReporterEpoch = 0 }},
		{"missing sequence", func(in *usecase.ReportOpenCodeSessionInput) { in.Sequence = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := valid
			tt.mutate(&in)
			err := usecase.NewReportOpenCodeSession(agents, &spyLock{}).Execute(ctx, in)
			if !errors.Is(err, usecase.ErrValidation) {
				t.Fatalf("Execute error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestReportOpenCodeSessionRequiresMatchingOpenCodePane(t *testing.T) {
	tests := []struct {
		name       string
		agentKind  string
		agentPane  string
		reportPane string
	}{
		{"non-OpenCode Agent", "claude", "%5", "%5"},
		{"different pane", "opencode", "%5", "%6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			agents := memory.NewMemoryAgentRepository()
			agent, err := agents.Create(ctx, domain.NewAgent(0, 1, 2, tt.agentKind, "test", tt.agentPane, true, domain.AgentRunning))
			if err != nil {
				t.Fatal(err)
			}
			sessionID := "ses_current"
			err = usecase.NewReportOpenCodeSession(agents, &spyLock{}).Execute(ctx, usecase.ReportOpenCodeSessionInput{
				AgentID:       agent.ID(),
				TmuxPaneID:    tt.reportPane,
				SessionID:     &sessionID,
				ReporterEpoch: 7,
				Sequence:      1,
			})
			if !errors.Is(err, usecase.ErrValidation) {
				t.Fatalf("Execute error = %v, want ErrValidation", err)
			}
			stored, getErr := agents.GetByID(ctx, agent.ID())
			if getErr != nil {
				t.Fatal(getErr)
			}
			if stored.OpenCodeSessionID() != nil {
				t.Fatal("rejected report changed the Agent's OpenCode session")
			}
		})
	}
}

func TestReportOpenCodeSessionIgnoresDuplicateAndStaleReports(t *testing.T) {
	ctx := context.Background()
	agents := memory.NewMemoryAgentRepository()
	agent, err := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%5", true, domain.AgentRunning))
	if err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewReportOpenCodeSession(agents, &spyLock{})
	report := func(sessionID string, epoch, sequence uint64) {
		t.Helper()
		if err := uc.Execute(ctx, usecase.ReportOpenCodeSessionInput{
			AgentID:       agent.ID(),
			TmuxPaneID:    "%5",
			SessionID:     &sessionID,
			ReporterEpoch: epoch,
			Sequence:      sequence,
		}); err != nil {
			t.Fatalf("report (%d, %d): %v", epoch, sequence, err)
		}
	}
	assertCurrent := func() {
		t.Helper()
		stored, getErr := agents.GetByID(ctx, agent.ID())
		if getErr != nil {
			t.Fatal(getErr)
		}
		got := stored.OpenCodeSessionID()
		if got == nil || *got != "ses_new" {
			t.Fatalf("OpenCodeSessionID = %v, want ses_new", got)
		}
		if stored.OpenCodeSessionReporterEpoch() != 7 || stored.OpenCodeSessionSequence() != 11 {
			t.Fatalf("OpenCode session order = (%d, %d), want (7, 11)", stored.OpenCodeSessionReporterEpoch(), stored.OpenCodeSessionSequence())
		}
	}

	report("ses_old", 7, 10)
	report("ses_new", 7, 11)
	assertCurrent()
	for _, stale := range []struct {
		epoch    uint64
		sequence uint64
	}{
		{7, 11},  // duplicate
		{7, 10},  // stale sequence
		{6, 999}, // stale reporter
	} {
		report("ses_rollback", stale.epoch, stale.sequence)
		assertCurrent()
	}
}

func TestReportOpenCodeSessionNewerReporterEpochSupersedesOld(t *testing.T) {
	ctx := context.Background()
	agents := memory.NewMemoryAgentRepository()
	oldSessionID := "ses_old"
	agent, err := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%5", true, domain.AgentRunning).
		WithOpenCodeSession(&oldSessionID, 7, 99))
	if err != nil {
		t.Fatal(err)
	}
	newSessionID := "ses_new"

	err = usecase.NewReportOpenCodeSession(agents, &spyLock{}).Execute(ctx, usecase.ReportOpenCodeSessionInput{
		AgentID:       agent.ID(),
		TmuxPaneID:    "%5",
		SessionID:     &newSessionID,
		ReporterEpoch: 8,
		Sequence:      1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	stored, err := agents.GetByID(ctx, agent.ID())
	if err != nil {
		t.Fatal(err)
	}
	got := stored.OpenCodeSessionID()
	if got == nil || *got != newSessionID {
		t.Fatalf("OpenCodeSessionID = %v, want %q", got, newSessionID)
	}
	if stored.OpenCodeSessionReporterEpoch() != 8 || stored.OpenCodeSessionSequence() != 1 {
		t.Fatalf("OpenCode session order = (%d, %d), want (8, 1)", stored.OpenCodeSessionReporterEpoch(), stored.OpenCodeSessionSequence())
	}
}

func TestReportOpenCodeSessionNullOrEmptyClearsCurrentSession(t *testing.T) {
	empty := ""
	for _, tt := range []struct {
		name      string
		sessionID *string
	}{
		{"null", nil},
		{"empty", &empty},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			agents := memory.NewMemoryAgentRepository()
			currentSessionID := "ses_current"
			agent, err := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "test", "%5", true, domain.AgentRunning).
				WithOpenCodeSession(&currentSessionID, 7, 1))
			if err != nil {
				t.Fatal(err)
			}

			err = usecase.NewReportOpenCodeSession(agents, &spyLock{}).Execute(ctx, usecase.ReportOpenCodeSessionInput{
				AgentID:       agent.ID(),
				TmuxPaneID:    "%5",
				SessionID:     tt.sessionID,
				ReporterEpoch: 7,
				Sequence:      2,
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			stored, err := agents.GetByID(ctx, agent.ID())
			if err != nil {
				t.Fatal(err)
			}
			if stored.OpenCodeSessionID() != nil {
				t.Fatalf("OpenCodeSessionID = %v, want nil", stored.OpenCodeSessionID())
			}
			if stored.OpenCodeSessionReporterEpoch() != 7 || stored.OpenCodeSessionSequence() != 2 {
				t.Fatalf("OpenCode session order = (%d, %d), want (7, 2)", stored.OpenCodeSessionReporterEpoch(), stored.OpenCodeSessionSequence())
			}
		})
	}
}
