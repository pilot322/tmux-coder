package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

type fakeAgentGateway struct {
	createdWindows []string
	commands       []string
	paneIDCounter  int
	panes          map[string]bool
	killErr        error
}

type fakeProcessGateway struct {
	pgid    int
	timeout time.Duration
	err     error
}

func (g *fakeProcessGateway) TerminateProcessGroup(ctx context.Context, pgid int, timeout time.Duration) error {
	g.pgid = pgid
	g.timeout = timeout
	return g.err
}

func (g *fakeAgentGateway) NewWindow(ctx context.Context, sessionName, workingDir, command string, env []string) (string, error) {
	g.paneIDCounter++
	paneID := "%" + itoa(g.paneIDCounter)
	g.panes[paneID] = true
	g.createdWindows = append(g.createdWindows, sessionName)
	g.commands = append(g.commands, command)
	return paneID, nil
}

func (g *fakeAgentGateway) PaneExists(ctx context.Context, paneID string) (bool, error) {
	return g.panes[paneID], nil
}

func (g *fakeAgentGateway) KillPane(ctx context.Context, paneID string) error {
	delete(g.panes, paneID)
	return g.killErr
}

func (g *fakeAgentGateway) ListPanes(ctx context.Context, sessionName string) ([]string, error) {
	var result []string
	for id, exists := range g.panes {
		if exists {
			result = append(result, id)
		}
	}
	return result, nil
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func agentFixture() (*usecase.CreateAgent, *memory.MemoryAgentRepository, *memory.MemoryProjectRepository, *memory.MemorySessionRepository, *fakeAgentGateway, *spyLock) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	gw := &fakeAgentGateway{panes: make(map[string]bool)}
	uc := usecase.NewCreateAgent(agents, projects, sessions, gw, lock)
	return uc, agents, projects, sessions, gw, lock
}

func seedProjectAndSession(projects *memory.MemoryProjectRepository, sessions *memory.MemorySessionRepository) (*domain.Project, *domain.Session) {
	ctx := context.Background()
	p, _ := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
	s, _ := sessions.Create(ctx, domain.NewSession(0, -1, p.ID(), "api.main", domain.MainSession))
	return p, s
}

func TestCreateAgent_OwnedPane(t *testing.T) {
	uc, _, projects, sessions, gw, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Agent.ID() == 0 {
		t.Fatal("want nonzero agent ID")
	}
	if result.Agent.Kind() != "opencode" {
		t.Fatalf("Kind = %q, want opencode", result.Agent.Kind())
	}
	if result.Agent.Status() != domain.AgentStarting {
		t.Fatalf("Status = %q, want starting", result.Agent.Status())
	}
	if !result.Agent.PaneOwned() {
		t.Fatal("PaneOwned = false, want true for daemon-owned pane")
	}
	if result.Agent.TmuxPaneID() == "" {
		t.Fatal("want non-empty tmuxPaneID after NewWindow")
	}
	if len(gw.createdWindows) != 1 {
		t.Fatalf("want 1 NewWindow call, got %d", len(gw.createdWindows))
	}
	if result.Agent.DisplayName() == "" {
		t.Fatal("want non-empty default display name")
	}
	if len(gw.commands) != 1 || !strings.Contains(gw.commands[0], "agent-wrapper") {
		t.Fatalf("commands = %v, want agent-wrapper subcommand", gw.commands)
	}
}

func TestCreateAgent_BorrowedPane(t *testing.T) {
	uc, _, projects, sessions, gw, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	paneID := "%12"
	gw.panes[paneID] = true
	result, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "claude",
		TmuxPaneID: &paneID,
		DaemonAddr: "127.0.0.1:64357",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Agent.PaneOwned() {
		t.Fatal("PaneOwned = true, want false for borrowed pane")
	}
	if result.Agent.TmuxPaneID() != paneID {
		t.Fatalf("TmuxPaneID = %q, want %q", result.Agent.TmuxPaneID(), paneID)
	}
	if len(gw.createdWindows) != 0 {
		t.Fatalf("want 0 NewWindow calls for borrowed pane, got %d", len(gw.createdWindows))
	}
}

func TestCreateAgent_BorrowedPaneMustBelongToSession(t *testing.T) {
	uc, _, projects, sessions, _, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	paneID := "%12"
	_, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "claude",
		TmuxPaneID: &paneID,
		DaemonAddr: "127.0.0.1:64357",
	})
	if err == nil {
		t.Fatal("want error when borrowed pane is not in the session")
	}
}

func TestCreateAgent_RejectsUnstablePaneID(t *testing.T) {
	uc, _, projects, sessions, _, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	paneID := "1"
	_, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "claude",
		TmuxPaneID: &paneID,
		DaemonAddr: "127.0.0.1:64357",
	})
	if err == nil {
		t.Fatal("want error for non-stable pane id")
	}
}

func TestCreateAgent_CustomDisplayName(t *testing.T) {
	uc, _, projects, sessions, _, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	name := "my-opencode"
	result, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:   p.ID(),
		SessionID:   s.ID(),
		Kind:        "opencode",
		DisplayName: &name,
		DaemonAddr:  "127.0.0.1:64357",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Agent.DisplayName() != "my-opencode" {
		t.Fatalf("DisplayName = %q, want my-opencode", result.Agent.DisplayName())
	}
}

func TestCreateAgent_ValidationErrors(t *testing.T) {
	uc, _, projects, sessions, _, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	_, err := uc.Execute(ctx, usecase.CreateAgentInput{Kind: "opencode"})
	if err == nil {
		t.Fatal("want error for missing projectId")
	}

	_, err = uc.Execute(ctx, usecase.CreateAgentInput{ProjectID: p.ID(), Kind: "opencode"})
	if err == nil {
		t.Fatal("want error for missing sessionId")
	}

	_, err = uc.Execute(ctx, usecase.CreateAgentInput{ProjectID: p.ID(), SessionID: s.ID()})
	if err == nil {
		t.Fatal("want error for missing kind")
	}
}

func TestCreateAgent_SessionNotBelongingToProject(t *testing.T) {
	uc, _, projects, sessions, _, _ := agentFixture()
	ctx := context.Background()
	p1, _ := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
	p2, _ := projects.Create(ctx, domain.NewProject(0, "/work/web", "web"))
	s2, _ := sessions.Create(ctx, domain.NewSession(0, -1, p2.ID(), "web.main", domain.MainSession))

	_, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p1.ID(),
		SessionID:  s2.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})
	if err == nil {
		t.Fatal("want error when session does not belong to project")
	}
}

func TestAgentEvent_StartedAndExited(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, lock)
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "started"}); err != nil {
		t.Fatalf("started event: %v", err)
	}
	agent, _ := agents.GetByID(ctx, result.Agent.ID())
	if agent.Status() != domain.AgentRunning {
		t.Fatalf("Status = %q, want running", agent.Status())
	}

	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "exited"}); err != nil {
		t.Fatalf("exited event: %v", err)
	}
	_, err := agents.GetByID(ctx, result.Agent.ID())
	if err == nil {
		t.Fatal("want agent to be removed after exited event")
	}
}

func TestAgentEvent_InvalidEvent(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, lock)
	err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "unknown"})
	if err == nil {
		t.Fatal("want error for unknown event type")
	}
}

func TestGetAgents_PrunesMissingPanesAndFilters(t *testing.T) {
	_, agents, projects, sessions, gw, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	keepPane := "%10"
	missingPane := "%11"
	gw.panes[keepPane] = true
	kept, _ := agents.Create(ctx, domain.NewAgent(0, p.ID(), s.ID(), "opencode", "", keepPane, true, domain.AgentRunning))
	pruned, _ := agents.Create(ctx, domain.NewAgent(0, p.ID(), s.ID(), "claude", "", missingPane, true, domain.AgentRunning))
	otherProject, _ := projects.Create(ctx, domain.NewProject(0, "/work/web", "web"))
	otherSession, _ := sessions.Create(ctx, domain.NewSession(0, -1, otherProject.ID(), "web.main", domain.MainSession))
	gw.panes["%12"] = true
	_, _ = agents.Create(ctx, domain.NewAgent(0, otherProject.ID(), otherSession.ID(), "opencode", "", "%12", true, domain.AgentRunning))

	views, err := usecase.NewGetAgents(agents, projects, sessions, gw, lock).Execute(ctx, usecase.GetAgentsInput{ProjectID: ptrInt(p.ID())})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(views) != 1 || views[0].Agent.ID() != kept.ID() {
		t.Fatalf("views = %#v, want only kept agent %d", views, kept.ID())
	}
	if _, err := agents.GetByID(ctx, pruned.ID()); !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("pruned agent lookup error = %v, want ErrAgentNotFound", err)
	}
	if views[0].MainSessionName != s.Name() || views[0].MainTmuxSessionName != s.TmuxName() {
		t.Fatalf("main session = %q/%q", views[0].MainSessionName, views[0].MainTmuxSessionName)
	}
}

func TestDeleteAgent_KillsOwnedPaneAndDeletesRecord(t *testing.T) {
	_, agents, _, _, gw, lock := agentFixture()
	ctx := context.Background()
	gw.panes["%10"] = true
	agent, _ := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "", "%10", true, domain.AgentRunning))

	if err := usecase.NewDeleteAgent(agents, gw, nil, lock).Execute(ctx, agent.ID()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gw.panes["%10"] {
		t.Fatal("pane still exists after delete")
	}
	if _, err := agents.GetByID(ctx, agent.ID()); !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("lookup error = %v, want ErrAgentNotFound", err)
	}
}

func TestDeleteAgent_TerminatesBorrowedPaneProcessGroup(t *testing.T) {
	_, agents, _, _, gw, lock := agentFixture()
	ctx := context.Background()
	agent, _ := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "", "%10", false, domain.AgentRunning))
	agent, _ = agents.Update(ctx, agent.WithChildProcessGroupID(4242))
	process := &fakeProcessGateway{}

	if err := usecase.NewDeleteAgent(agents, gw, process, lock).Execute(ctx, agent.ID()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if process.pgid != 4242 {
		t.Fatalf("pgid = %d, want 4242", process.pgid)
	}
	if _, err := agents.GetByID(ctx, agent.ID()); !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("lookup error = %v, want ErrAgentNotFound", err)
	}
}

func ptrInt(v int) *int { return &v }
