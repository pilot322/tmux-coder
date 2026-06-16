package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

type fakeAgentGateway struct {
	createdWindows []string
	windowNames    []string
	renamedWindows []renamedWindow
	workingDirs    []string
	commands       []string
	paneIDCounter  int
	panes          map[string]bool
	renameErr      error
	killErr        error
}

type renamedWindow struct {
	paneID string
	name   string
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

func (g *fakeAgentGateway) NewWindow(ctx context.Context, sessionName, windowName, workingDir, command string, env []string) (string, error) {
	g.paneIDCounter++
	paneID := "%" + itoa(g.paneIDCounter)
	g.panes[paneID] = true
	g.createdWindows = append(g.createdWindows, sessionName)
	g.windowNames = append(g.windowNames, windowName)
	g.workingDirs = append(g.workingDirs, workingDir)
	g.commands = append(g.commands, command)
	return paneID, nil
}

func (g *fakeAgentGateway) PaneExists(ctx context.Context, paneID string) (bool, error) {
	return g.panes[paneID], nil
}

func (g *fakeAgentGateway) RenameWindow(ctx context.Context, paneID, name string) error {
	g.renamedWindows = append(g.renamedWindows, renamedWindow{paneID: paneID, name: name})
	return g.renameErr
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
	uc := usecase.NewCreateAgent(agents, projects, sessions, gw, lock, obs.Nop())
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
	if result.Agent.StatusChangedAt().IsZero() {
		t.Fatal("want non-zero status changed timestamp for initial starting status")
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
	if len(gw.windowNames) != 1 || gw.windowNames[0] != result.Agent.DisplayName() {
		t.Fatalf("windowNames = %v, want %q", gw.windowNames, result.Agent.DisplayName())
	}
	if len(gw.commands) != 1 || !strings.Contains(gw.commands[0], "agent-wrapper") {
		t.Fatalf("commands = %v, want agent-wrapper subcommand", gw.commands)
	}
	if len(gw.workingDirs) != 1 || gw.workingDirs[0] != p.FullPath() {
		t.Fatalf("workingDirs = %v, want project root %q", gw.workingDirs, p.FullPath())
	}
}

func TestCreateAgentOwnedPaneUsesWorktreeDirectory(t *testing.T) {
	uc, _, projects, sessions, gw, _ := agentFixture()
	ctx := context.Background()
	p, _ := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
	s, _ := sessions.Create(ctx, domain.NewWorktreeSession(0, -1, p.ID(), "api.feature", "feature", "/work/api.feature"))

	if _, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(gw.workingDirs) != 1 || gw.workingDirs[0] != "/work/api.feature" {
		t.Fatalf("workingDirs = %v, want worktree root", gw.workingDirs)
	}
}

func TestCreateAgentOwnedPaneUsesWorktreeRootForSecondaryDirectory(t *testing.T) {
	uc, _, projects, sessions, gw, _ := agentFixture()
	ctx := context.Background()
	p, _ := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
	wt, _ := sessions.Create(ctx, domain.NewWorktreeSession(0, -1, p.ID(), "api.feature", "feature", "/work/api.feature"))
	s, _ := sessions.Create(ctx, domain.NewSecondarySessionWithTmuxName(0, wt.ID(), p.ID(), "server", "api_feature_server", "services/server", "cascade"))

	if _, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(gw.workingDirs) != 1 || gw.workingDirs[0] != "/work/api.feature/services/server" {
		t.Fatalf("workingDirs = %v, want worktree-rooted secondary dir", gw.workingDirs)
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
	if len(gw.renamedWindows) != 1 || gw.renamedWindows[0].paneID != paneID || gw.renamedWindows[0].name != result.Agent.DisplayName() {
		t.Fatalf("renamedWindows = %#v, want pane %q name %q", gw.renamedWindows, paneID, result.Agent.DisplayName())
	}
}

func TestCreateAgent_BorrowedPaneIgnoresWindowRenameFailure(t *testing.T) {
	uc, _, projects, sessions, gw, _ := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	paneID := "%12"
	gw.panes[paneID] = true
	gw.renameErr = errors.New("tmux rename failed")

	if _, err := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "claude",
		TmuxPaneID: &paneID,
		DaemonAddr: "127.0.0.1:64357",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(gw.renamedWindows) != 1 {
		t.Fatalf("renamedWindows = %#v, want one best-effort attempt", gw.renamedWindows)
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

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
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

func TestAgentEvent_BusySetsStatus(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "busy"}); err != nil {
		t.Fatalf("busy event: %v", err)
	}
	agent, _ := agents.GetByID(ctx, result.Agent.ID())
	if agent.Status() != domain.AgentBusy {
		t.Fatalf("Status = %q, want busy", agent.Status())
	}
}

func TestAgentEvent_IdleSetsStatus(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "idle"}); err != nil {
		t.Fatalf("idle event: %v", err)
	}
	agent, _ := agents.GetByID(ctx, result.Agent.ID())
	if agent.Status() != domain.AgentIdle {
		t.Fatalf("Status = %q, want idle", agent.Status())
	}
}

func TestAgentEvent_WaitingSetsStatus(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "waiting"}); err != nil {
		t.Fatalf("waiting event: %v", err)
	}
	agent, _ := agents.GetByID(ctx, result.Agent.ID())
	if agent.Status() != domain.AgentWaiting {
		t.Fatalf("Status = %q, want waiting", agent.Status())
	}
}

func TestAgentEvent_StartedDoesNotDowngradeActivityButRecordsPGID(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "busy"}); err != nil {
		t.Fatalf("busy event: %v", err)
	}

	pgid := 4242
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "started", ChildProcessGroupID: &pgid}); err != nil {
		t.Fatalf("started event: %v", err)
	}

	agent, _ := agents.GetByID(ctx, result.Agent.ID())
	if agent.Status() != domain.AgentBusy {
		t.Fatalf("Status = %q, want busy (started must not downgrade activity)", agent.Status())
	}
	if agent.ChildProcessGroupID() != pgid {
		t.Fatalf("ChildProcessGroupID = %d, want %d (started must record pgid)", agent.ChildProcessGroupID(), pgid)
	}
}

func TestAgentEvent_StartedFromStartingPromotesToRunningAndRecordsPGID(t *testing.T) {
	uc, agents, projects, sessions, _, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()

	result, _ := uc.Execute(ctx, usecase.CreateAgentInput{
		ProjectID:  p.ID(),
		SessionID:  s.ID(),
		Kind:       "opencode",
		DaemonAddr: "127.0.0.1:64357",
	})

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
	pgid := 9001
	if err := eventUc.Execute(ctx, usecase.AgentEventInput{AgentID: result.Agent.ID(), Event: "started", ChildProcessGroupID: &pgid}); err != nil {
		t.Fatalf("started event: %v", err)
	}

	agent, _ := agents.GetByID(ctx, result.Agent.ID())
	if agent.Status() != domain.AgentRunning {
		t.Fatalf("Status = %q, want running", agent.Status())
	}
	if agent.ChildProcessGroupID() != pgid {
		t.Fatalf("ChildProcessGroupID = %d, want %d", agent.ChildProcessGroupID(), pgid)
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

	eventUc := usecase.NewAgentEvent(agents, projects, sessions, &fakeNotifier{}, lock, obs.Nop())
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

	views, err := usecase.NewGetAgents(agents, projects, sessions, gw, lock, obs.Nop()).Execute(ctx, usecase.GetAgentsInput{ProjectID: ptrInt(p.ID())})
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

func TestRenameAgent_UpdatesDisplayName(t *testing.T) {
	_, agents, projects, sessions, gw, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	agent, _ := agents.Create(ctx, domain.NewAgent(0, p.ID(), s.ID(), "opencode", "old", "%10", true, domain.AgentRunning))

	view, err := usecase.NewRenameAgent(agents, projects, sessions, gw, lock, obs.Nop()).Execute(ctx, usecase.RenameAgentInput{AgentID: agent.ID(), DisplayName: " new name "})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if view.Agent.DisplayName() != "new name" {
		t.Fatalf("DisplayName = %q, want new name", view.Agent.DisplayName())
	}
	stored, _ := agents.GetByID(ctx, agent.ID())
	if stored.DisplayName() != "new name" {
		t.Fatalf("stored DisplayName = %q, want new name", stored.DisplayName())
	}
	if len(gw.renamedWindows) != 1 || gw.renamedWindows[0].paneID != "%10" || gw.renamedWindows[0].name != "new name" {
		t.Fatalf("renamedWindows = %#v, want pane %%10 name new name", gw.renamedWindows)
	}
	if view.Project.ID() != p.ID() || view.Session.ID() != s.ID() || view.MainSessionName != s.Name() {
		t.Fatalf("view context = project %d session %d main %q", view.Project.ID(), view.Session.ID(), view.MainSessionName)
	}
}

func TestRenameAgent_IgnoresWindowRenameFailure(t *testing.T) {
	_, agents, projects, sessions, gw, lock := agentFixture()
	p, s := seedProjectAndSession(projects, sessions)
	ctx := context.Background()
	agent, _ := agents.Create(ctx, domain.NewAgent(0, p.ID(), s.ID(), "opencode", "old", "%10", true, domain.AgentRunning))
	gw.renameErr = errors.New("tmux rename failed")

	view, err := usecase.NewRenameAgent(agents, projects, sessions, gw, lock, obs.Nop()).Execute(ctx, usecase.RenameAgentInput{AgentID: agent.ID(), DisplayName: "new"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if view.Agent.DisplayName() != "new" {
		t.Fatalf("DisplayName = %q, want new", view.Agent.DisplayName())
	}
	if len(gw.renamedWindows) != 1 {
		t.Fatalf("renamedWindows = %#v, want one best-effort attempt", gw.renamedWindows)
	}
}

func TestRenameAgent_NotFound(t *testing.T) {
	_, agents, projects, sessions, gw, lock := agentFixture()
	_, err := usecase.NewRenameAgent(agents, projects, sessions, gw, lock, obs.Nop()).Execute(context.Background(), usecase.RenameAgentInput{AgentID: 999, DisplayName: "new"})
	if !errors.Is(err, usecase.ErrAgentNotFound) {
		t.Fatalf("Execute error = %v, want ErrAgentNotFound", err)
	}
}

func TestRenameAgent_RejectsEmptyName(t *testing.T) {
	_, agents, projects, sessions, gw, lock := agentFixture()
	_, err := usecase.NewRenameAgent(agents, projects, sessions, gw, lock, obs.Nop()).Execute(context.Background(), usecase.RenameAgentInput{AgentID: 1, DisplayName: "  "})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
}

func TestDeleteAgent_KillsOwnedPaneAndDeletesRecord(t *testing.T) {
	_, agents, _, _, gw, lock := agentFixture()
	ctx := context.Background()
	gw.panes["%10"] = true
	agent, _ := agents.Create(ctx, domain.NewAgent(0, 1, 2, "opencode", "", "%10", true, domain.AgentRunning))

	if err := usecase.NewDeleteAgent(agents, gw, nil, lock, obs.Nop()).Execute(ctx, agent.ID()); err != nil {
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

	if err := usecase.NewDeleteAgent(agents, gw, process, lock, obs.Nop()).Execute(ctx, agent.ID()); err != nil {
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
