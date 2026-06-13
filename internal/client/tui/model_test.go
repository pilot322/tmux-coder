package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

type fakeAPI struct {
	projects          []httpclient.Project
	sessions          []httpclient.Session
	agents            []httpclient.Agent
	listErr           error
	deleted           int
	deletedSession    int
	deletedAgent      int
	deleteForce       bool
	created           []httpclient.CreateSessionInput
	createdSession    httpclient.Session
	createErr         error
	listProjectsCalls int
	listSessionsCalls int
	listAgentsCalls   int
}

func (a *fakeAPI) ListProjects(context.Context) ([]httpclient.Project, error) {
	a.listProjectsCalls++
	return a.projects, a.listErr
}

func (a *fakeAPI) ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error) {
	a.listSessionsCalls++
	return a.sessions, nil
}

func (a *fakeAPI) ListAgents(context.Context, httpclient.ListAgentsInput) ([]httpclient.Agent, error) {
	a.listAgentsCalls++
	return a.agents, nil
}

func (a *fakeAPI) CreateSession(_ context.Context, in httpclient.CreateSessionInput) (httpclient.Session, error) {
	a.created = append(a.created, in)
	return a.createdSession, a.createErr
}

func (a *fakeAPI) DeleteProject(_ context.Context, id int) error {
	a.deleted = id
	return nil
}

func (a *fakeAPI) DeleteSession(_ context.Context, id int, force bool) error {
	a.deletedSession = id
	a.deleteForce = force
	return nil
}

func (a *fakeAPI) DeleteAgent(_ context.Context, id int) error {
	a.deletedAgent = id
	return nil
}

func TestModelEnterSelectsProjectMainSessionAndQuits(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api.main", MainTmuxSessionName: "api_main"}},
		sessions: []httpclient.Session{{ProjectID: 1, SessionName: "api.main", TmuxName: "api_main", Type: "main"}},
	})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.attach.SessionName != "api_main" || m.attach.PaneID != "" {
		t.Fatalf("attach = %+v", m.attach)
	}
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestModelViewUsesProjectTitle(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}}})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Backend API") {
		t.Fatalf("view does not contain project title: %q", view)
	}
}

func TestModelStartsWithSessionsShown(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	if !m.showSessions {
		t.Fatal("expected sessions to be shown by default")
	}
	if m.showAgents {
		t.Fatal("expected agents to be hidden by default")
	}
}

func TestModelTogglesAgents(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)

	if !m.showAgents {
		t.Fatal("expected agents to be shown after toggle")
	}
}

func TestModelInitialSessionSelectsMatchingSession(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{}, "web-feature")
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{
			{ID: 1, MainSessionName: "api-main"},
			{ID: 2, MainSessionName: "web-main"},
		},
		sessions: []httpclient.Session{
			{ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ProjectID: 2, SessionName: "web-main", Type: "main"},
			{ProjectID: 2, SessionName: "web-feature", Type: "worktree"},
		},
	})
	m = updated.(Model)

	if m.selectedSession != 2 || m.initialSession != "" {
		t.Fatalf("selectedSession=%d initialSession=%q", m.selectedSession, m.initialSession)
	}
}

func TestModelInitialSessionIgnoresMissingSession(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{}, "missing")
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ProjectID: 1, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)

	if m.selectedSession != 0 || m.initialSession != "" {
		t.Fatalf("selectedSession=%d initialSession=%q", m.selectedSession, m.initialSession)
	}
}

func TestModelToggleSessionsSelectsCurrentProjectMainSession(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	projects := []httpclient.Project{
		{ID: 1, Title: "API", MainSessionName: "api-main"},
		{ID: 2, Title: "Web", MainSessionName: "web-main"},
	}
	sessions := []httpclient.Session{
		{ProjectID: 1, SessionName: "api-main", Type: "main"},
		{ProjectID: 1, SessionName: "api-work", Type: "worktree", Branch: "feature/api"},
		{ProjectID: 2, SessionName: "web-work", Type: "worktree", Branch: "feature/web"},
		{ProjectID: 2, SessionName: "web-main", Type: "main"},
	}
	updated, _ := m.Update(listMsg{projects: projects, sessions: sessions})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)

	if !m.showSessions || m.selectedSession != 2 {
		t.Fatalf("showSessions=%v selectedSession=%d", m.showSessions, m.selectedSession)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.attach.SessionName != "web-main" || cmd == nil {
		t.Fatalf("attach=%+v cmd nil=%v", m.attach, cmd == nil)
	}
}

func TestModelToggleSessionsOffSelectsOwningProject(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	projects := []httpclient.Project{
		{ID: 1, MainSessionName: "api-main"},
		{ID: 2, MainSessionName: "web-main"},
	}
	sessions := []httpclient.Session{
		{ProjectID: 1, SessionName: "api-main", Type: "main"},
		{ProjectID: 2, SessionName: "web-main", Type: "main"},
		{ProjectID: 2, SessionName: "web-work", Type: "worktree"},
	}
	updated, _ := m.Update(listMsg{projects: projects, sessions: sessions})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)

	if m.showSessions || m.selected != 1 {
		t.Fatalf("showSessions=%v selected=%d", m.showSessions, m.selected)
	}
}

func TestModelExpandedViewRendersSessionsUnderProject(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{
			{ProjectID: 1, SessionName: "api-work", Type: "worktree", Branch: "feature/api"},
			{ProjectID: 1, SessionName: "api-main", Type: "main"},
		},
	})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Backend API") || !strings.Contains(view, "- api-main") || !strings.Contains(view, "- api-work (feature/api)") {
		t.Fatalf("view missing expanded rows: %q", view)
	}
}

func TestModelAgentViewRendersAgentsUnderSessions(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", TmuxName: "api_main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "- api-main") || !strings.Contains(view, "- reviewer [running]") {
		t.Fatalf("view missing session agent rows: %q", view)
	}
}

func TestModelAgentViewRendersAgentsUnderProjects(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Backend API") || !strings.Contains(view, "- reviewer [running]") {
		t.Fatalf("view missing project agent rows: %q", view)
	}
}

func TestModelEnterSelectsAgentPane(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", TmuxName: "api_main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.attach.SessionName != "api_main" || m.attach.PaneID != "%7" || cmd == nil {
		t.Fatalf("attach=%+v cmd nil=%v", m.attach, cmd == nil)
	}
}

func TestModelListCommandFetchesProjectsAndSessions(t *testing.T) {
	api := &fakeAPI{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ProjectID: 1, SessionName: "api-main", Type: "main"}},
	}
	m := NewModel(context.Background(), api)
	msg := m.listCmd()().(listMsg)

	if msg.err != nil || len(msg.projects) != 1 || len(msg.sessions) != 1 {
		t.Fatalf("msg = %+v", msg)
	}
	if api.listProjectsCalls != 1 || api.listSessionsCalls != 1 || api.listAgentsCalls != 1 {
		t.Fatalf("listProjectsCalls=%d listSessionsCalls=%d listAgentsCalls=%d", api.listProjectsCalls, api.listSessionsCalls, api.listAgentsCalls)
	}
}

func TestModelWorktreePromptCreatesSessionForSelectedProject(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = updated.(Model)
	if !m.creatingWorktree || m.worktreeProjectID != 7 {
		t.Fatalf("creatingWorktree=%v worktreeProjectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("feature/login")})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("loading=%v cmd nil=%v", m.loading, cmd == nil)
	}

	msg := cmd().(createSessionMsg)
	if msg.err != nil {
		t.Fatalf("create err = %v", msg.err)
	}
	if len(api.created) != 1 {
		t.Fatalf("created calls = %d", len(api.created))
	}
	in := api.created[0]
	if in.ProjectID != 7 || in.Type != "worktree" || in.Branch != "feature/login" || !in.Create || in.BaseBranch != "" {
		t.Fatalf("create input = %+v", in)
	}
}

func TestModelWorktreeCreateRefetchesAndSelectsNewSession(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}}, sessions: []httpclient.Session{{ProjectID: 7, SessionName: "api-main", Type: "main"}}})
	m = updated.(Model)

	updated, cmd := m.Update(createSessionMsg{session: api.createdSession})
	m = updated.(Model)
	if !m.loading || !m.showSessions || m.creatingWorktree || cmd == nil {
		t.Fatalf("loading=%v showSessions=%v creatingWorktree=%v cmd nil=%v", m.loading, m.showSessions, m.creatingWorktree, cmd == nil)
	}

	api.projects = []httpclient.Project{{ID: 7, MainSessionName: "api-main"}}
	api.sessions = []httpclient.Session{
		{ProjectID: 7, SessionName: "api-main", Type: "main"},
		{ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"},
	}
	msg := cmd().(listMsg)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.selectedSession != 1 || m.pendingSelectSession != "" {
		t.Fatalf("selectedSession=%d pendingSelectSession=%q", m.selectedSession, m.pendingSelectSession)
	}
}

func TestModelWorktreePromptUsesSelectedSessionOwner(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}, {ID: 2, MainSessionName: "web-main"}},
		sessions: []httpclient.Session{
			{ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ProjectID: 2, SessionName: "web-main", Type: "main"},
			{ProjectID: 2, SessionName: "web-feature", Type: "worktree"},
		},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = updated.(Model)

	if !m.creatingWorktree || m.worktreeProjectID != 2 {
		t.Fatalf("creatingWorktree=%v worktreeProjectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
}

func TestModelWorktreePromptEscCancels(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.creatingWorktree || m.worktreeBranch != "" || m.worktreeProjectID != 0 || cmd != nil || len(api.created) != 0 {
		t.Fatalf("creatingWorktree=%v branch=%q projectID=%d cmd=%v created=%d", m.creatingWorktree, m.worktreeBranch, m.worktreeProjectID, cmd, len(api.created))
	}
}

func TestModelDeleteConfirmationDeletesSelectedProject(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("expected delete command and loading state")
	}
	msg := cmd().(deleteMsg)
	if msg.err != nil || api.deleted != 7 {
		t.Fatalf("delete msg = %+v deleted=%d", msg, api.deleted)
	}
}

func TestModelDeleteConfirmationDestroysSelectedWorktreeSession(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ProjectID: 7, SessionName: "api.feature-login", Type: "worktree", Branch: "feature/login"},
		},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = updated.(Model)

	if !m.confirm || m.confirmDelete != deleteWorktreeSession || m.confirmDeleteID != 2 {
		t.Fatalf("confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "Destroy worktree session and worktree? y/n") {
		t.Fatalf("missing worktree confirmation: %q", m.View())
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("expected delete command and loading state")
	}
	msg := cmd().(deleteMsg)
	if msg.err != nil || api.deletedSession != 2 || !api.deleteForce || api.deleted != 0 {
		t.Fatalf("delete msg=%+v deletedSession=%d force=%v deletedProject=%d", msg, api.deletedSession, api.deleteForce, api.deleted)
	}
}

func TestModelDeleteConfirmationDeletesSelectedAgent(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 12, ProjectID: 7, SessionID: 1, DisplayName: "reviewer", TmuxPaneID: "%12", Status: "running"}},
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = updated.(Model)

	if !m.confirm || m.confirmDelete != deleteAgent || m.confirmDeleteID != 12 {
		t.Fatalf("confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "Delete agent? y/n") {
		t.Fatalf("missing agent confirmation: %q", m.View())
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("expected delete command and loading state")
	}
	msg := cmd().(deleteMsg)
	if msg.err != nil || api.deletedAgent != 12 || api.deletedSession != 0 || api.deleted != 0 {
		t.Fatalf("delete msg=%+v deletedAgent=%d deletedSession=%d deletedProject=%d", msg, api.deletedAgent, api.deletedSession, api.deleted)
	}
}

func TestModelDeleteOnMainSessionDoesNotDeleteProject(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = updated.(Model)

	if m.confirm || cmd != nil || api.deleted != 0 || api.deletedSession != 0 || m.status != "only worktree sessions can be destroyed" {
		t.Fatalf("confirm=%v cmd=%v deleted=%d deletedSession=%d status=%q", m.confirm, cmd, api.deleted, api.deletedSession, m.status)
	}
}

func TestModelKeepsErrorStatusOnListFailure(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{err: errors.New("daemon unavailable")})
	m = updated.(Model)
	if m.status != "daemon unavailable" || m.loading {
		t.Fatalf("status=%q loading=%v", m.status, m.loading)
	}
}
