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

// runes is a convenience for sending a printable key press.
func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func loaded(t *testing.T, msg listMsg) Model {
	t.Helper()
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(msg)
	return updated.(Model)
}

func press(m Model, msg tea.KeyMsg) Model {
	updated, _ := m.Update(msg)
	return updated.(Model)
}

// --- tabs & navigation ---------------------------------------------------

func TestModelStartsOnOverviewTab(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	if m.tab != tabOverview {
		t.Fatalf("expected to start on overview tab, got tab=%d", m.tab)
	}
}

func TestModelNumberKeysSwitchTabs(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
	})

	m = press(m, runes("2"))
	if m.tab != tabSessions {
		t.Fatalf("expected tab=%d, got %d", tabSessions, m.tab)
	}
	// Pressing the active tab again is a no-op.
	m = press(m, runes("2"))
	if m.tab != tabSessions {
		t.Fatalf("pressing active tab should be a no-op, got %d", m.tab)
	}
	m = press(m, runes("1"))
	if m.tab != tabProjects {
		t.Fatalf("expected tab=%d, got %d", tabProjects, m.tab)
	}
	m = press(m, runes("3"))
	if m.tab != tabAgents {
		t.Fatalf("expected tab=%d, got %d", tabAgents, m.tab)
	}
	m = press(m, runes("0"))
	if m.tab != tabOverview {
		t.Fatalf("expected tab=%d, got %d", tabOverview, m.tab)
	}
}

func TestModelTabStripRendersAllTabs(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}}})
	view := m.View()
	for _, label := range []string{"0 Overview", "1 Projects", "2 Sessions", "3 Agents"} {
		if !strings.Contains(view, label) {
			t.Fatalf("tab strip missing %q: %q", label, view)
		}
	}
}

func TestModelCtrlNCtrlPNavigate(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"},
			{ID: 11, ProjectID: 1, SessionName: "wt", Type: "worktree", Branch: "b"},
		},
	})

	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if cur, _ := m.cursor(); cur.session.ID != 11 {
		t.Fatalf("ctrl+n should move down to session 11, got %d", cur.session.ID)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if cur, _ := m.cursor(); cur.session.ID != 10 {
		t.Fatalf("ctrl+p should move up to session 10, got %d", cur.session.ID)
	}
}

// --- identity-based selection -------------------------------------------

func TestModelSessionSelectionSharedBetweenOverviewAndSessions(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main", MainTmuxSessionName: "main_t"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "main", TmuxName: "main_t", Type: "main"},
			{ID: 11, ProjectID: 1, SessionName: "wt", TmuxName: "wt_t", Type: "worktree", Branch: "b"},
		},
	})

	// Select the worktree session in the Overview, then jump to Sessions.
	m = press(m, runes("j"))
	m = press(m, runes("2"))
	if cur, _ := m.cursor(); cur.session.ID != 11 {
		t.Fatalf("session selection should carry from overview to sessions, got %d", cur.session.ID)
	}

	// Move back to main in Sessions, then jump to Overview.
	m = press(m, runes("k"))
	m = press(m, runes("0"))
	if cur, _ := m.cursor(); cur.session.ID != 10 {
		t.Fatalf("session selection should carry from sessions back to overview, got %d", cur.session.ID)
	}
}

func TestModelProjectSelectionPersistsAcrossTabSwitch(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{
			{ID: 1, Title: "A", MainSessionName: "a", MainTmuxSessionName: "a_t"},
			{ID: 2, Title: "B", MainSessionName: "b", MainTmuxSessionName: "b_t"},
		},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "a", Type: "main"},
			{ID: 20, ProjectID: 2, SessionName: "b", Type: "main"},
		},
	})

	m = press(m, runes("1"))
	m = press(m, runes("j"))
	if m.projectSel.id != 2 {
		t.Fatalf("expected project 2 selected, got %d", m.projectSel.id)
	}
	m = press(m, runes("0"))
	m = press(m, runes("1"))
	if cur, _ := m.cursor(); cur.project.ID != 2 {
		t.Fatalf("project selection should persist across tab switches, got %d", cur.project.ID)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.attach.SessionName != "b_t" || cmd == nil {
		t.Fatalf("enter in projects should attach main session, attach=%+v", m.attach)
	}
}

func TestModelAgentSelectionPersistsAcrossTabSwitch(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 20, ProjectID: 1, SessionID: 10, Kind: "claude", DisplayName: "a1", Status: "running"},
			{ID: 21, ProjectID: 1, SessionID: 10, Kind: "claude", DisplayName: "a2", Status: "running"},
		},
	})

	m = press(m, runes("3"))
	m = press(m, runes("j"))
	if m.agentSel.id != 21 {
		t.Fatalf("expected agent 21 selected, got %d", m.agentSel.id)
	}
	m = press(m, runes("0"))
	m = press(m, runes("3"))
	if cur, _ := m.cursor(); cur.agent.ID != 21 {
		t.Fatalf("agent selection should persist across tab switches, got %d", cur.agent.ID)
	}
}

func TestModelSelectionClampsToNearestOnRefresh(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"},
			{ID: 11, ProjectID: 1, SessionName: "wt1", Type: "worktree"},
			{ID: 12, ProjectID: 1, SessionName: "wt2", Type: "worktree"},
		},
	})
	m = press(m, runes("2"))
	m = press(m, runes("j")) // select wt1 at index 1
	if m.sessionSel.id != 11 {
		t.Fatalf("expected wt1 selected, got %d", m.sessionSel.id)
	}

	// wt1 disappears underneath us; the cursor should land on its neighbour.
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"},
			{ID: 12, ProjectID: 1, SessionName: "wt2", Type: "worktree"},
		},
	})
	m = updated.(Model)
	if cur, _ := m.cursor(); cur.session.ID != 12 {
		t.Fatalf("selection should clamp to nearest after refresh, got %d", cur.session.ID)
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
			{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ID: 20, ProjectID: 2, SessionName: "web-main", Type: "main"},
			{ID: 21, ProjectID: 2, SessionName: "web-feature", Type: "worktree"},
		},
	})
	m = updated.(Model)

	if cur, _ := m.cursor(); cur.session.ID != 21 {
		t.Fatalf("initial session should be pre-selected, got %d", cur.session.ID)
	}
	if m.initialSession != "" {
		t.Fatalf("initialSession should be cleared, got %q", m.initialSession)
	}
}

func TestModelInitialSessionIgnoresMissingSession(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{}, "missing")
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)

	if cur, _ := m.cursor(); cur.session.ID != 10 {
		t.Fatalf("missing initial session should fall back to first row, got %d", cur.session.ID)
	}
	if m.initialSession != "" {
		t.Fatalf("initialSession should be cleared, got %q", m.initialSession)
	}
}

// --- attach --------------------------------------------------------------

func TestModelEnterSelectsProjectMainSessionAndQuits(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api.main", MainTmuxSessionName: "api_main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api.main", TmuxName: "api_main", Type: "main"}},
	})
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.attach.SessionName != "api_main" || m.attach.PaneID != "" {
		t.Fatalf("attach = %+v", m.attach)
	}
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestModelEnterSelectsAgentPane(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", TmuxName: "api_main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	m = press(m, runes("j"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.attach.SessionName != "api_main" || m.attach.PaneID != "%7" || cmd == nil {
		t.Fatalf("attach=%+v cmd nil=%v", m.attach, cmd == nil)
	}
}

// --- per-view rendering --------------------------------------------------

func TestModelViewUsesProjectTitle(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}}})
	view := m.View()
	if !strings.Contains(view, "Backend API") {
		t.Fatalf("view does not contain project title: %q", view)
	}
}

func TestModelOverviewRendersSessionsUnderProject(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{
			{ID: 1, ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ID: 2, ProjectID: 1, SessionName: "api-work", Type: "worktree", Branch: "feature/api"},
		},
	})
	view := m.View()
	if !strings.Contains(view, "Backend API") || !strings.Contains(view, "- api-main") || !strings.Contains(view, "- api-work (feature/api)") {
		t.Fatalf("view missing overview rows: %q", view)
	}
}

func TestModelOverviewRendersAgentsUnderSessions(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", TmuxName: "api_main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	view := m.View()
	if !strings.Contains(view, "- api-main") || !strings.Contains(view, "- reviewer [running]") {
		t.Fatalf("view missing session agent rows: %q", view)
	}
}

func TestModelOverviewIndentsAgentsUnderSecondarySessions(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "api-main", TmuxName: "api_main", Type: "main"},
			{ID: 11, ParentSessionID: 10, ProjectID: 1, SessionName: "pkg", TmuxName: "api_pkg", Type: "secondary"},
			{ID: 12, ParentSessionID: 11, ProjectID: 1, SessionName: "inner", TmuxName: "api_inner", Type: "secondary"},
		},
		agents: []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 12, DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	view := m.View()
	secondary := strings.Index(view, "    - inner")
	agent := strings.Index(view, "        - reviewer [running]")
	if secondary < 0 || agent < 0 || secondary > agent {
		t.Fatalf("agent under secondary not indented below secondary session: %q", view)
	}
}

func TestModelSessionRowsRenderSecondaryTreeIndented(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, Title: "API", FullPath: "/work/api", MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 3, ParentSessionID: 2, ProjectID: 7, SessionName: "inner", Type: "secondary"},
			{ID: 1, Parent: -1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ParentSessionID: 1, ProjectID: 7, SessionName: "pkg", Type: "secondary"},
		},
	})
	view := m.View()
	main := strings.Index(view, "- api.main")
	parent := strings.Index(view, "  - pkg")
	child := strings.Index(view, "    - inner")
	if main < 0 || parent < 0 || child < 0 || !(main < parent && parent < child) {
		t.Fatalf("secondary tree not rendered in order/indentation: %q", view)
	}
}

func TestModelSessionsViewOmitsAgents(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, Kind: "claude", DisplayName: "reviewer", Status: "running"}},
	})
	m = press(m, runes("2"))
	view := m.View()
	if !strings.Contains(view, "- main") {
		t.Fatalf("sessions view missing session row: %q", view)
	}
	if strings.Contains(view, "reviewer") {
		t.Fatalf("sessions view should omit agents: %q", view)
	}
}

func TestModelProjectsViewRendersTwoLineBlock(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "Backend API", FullPath: "/work/api", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ID: 11, ProjectID: 1, SessionName: "wt", Type: "worktree"},
		},
		agents: []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, Kind: "claude", DisplayName: "r", Status: "running"}},
	})
	m = press(m, runes("1"))
	view := m.View()
	if !strings.Contains(view, "Backend API (2 sessions · 1 agent)") {
		t.Fatalf("projects view missing block header with counts: %q", view)
	}
	if !strings.Contains(view, "- path: /work/api") {
		t.Fatalf("projects view missing path sub-line: %q", view)
	}
}

func TestModelAgentsViewRendersAgentRows(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, Kind: "claude", DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	m = press(m, runes("3"))
	view := m.View()
	for _, want := range []string{"claude · reviewer", "[running]", "API / api-main"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agents view missing %q: %q", want, view)
		}
	}
}

func TestModelAgentsViewEmptyPlaceholder(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
	})
	m = press(m, runes("3"))
	if !strings.Contains(m.View(), "No active agents.") {
		t.Fatalf("agents view missing empty placeholder: %q", m.View())
	}
}

// --- footer --------------------------------------------------------------

func TestModelFooterShowsPerViewKeys(t *testing.T) {
	base := listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
	}

	cases := []struct {
		tab     string
		want    []string
		notWant []string
	}{
		{"0", []string{"w worktree", "X delete"}, []string{"S secondary"}},
		{"1", []string{"w worktree", "X delete"}, []string{"S secondary"}},
		{"2", []string{"w worktree", "S secondary", "X delete"}, nil},
		{"3", []string{"X delete"}, []string{"w worktree", "S secondary"}},
	}
	for _, tc := range cases {
		m := loaded(t, base)
		m = press(m, runes(tc.tab))
		view := m.View()
		for _, w := range tc.want {
			if !strings.Contains(view, w) {
				t.Fatalf("tab %s footer missing %q: %q", tc.tab, w, view)
			}
		}
		for _, nw := range tc.notWant {
			if strings.Contains(view, nw) {
				t.Fatalf("tab %s footer should not contain %q: %q", tc.tab, nw, view)
			}
		}
		// The removed toggle keys must be gone from every view.
		if strings.Contains(view, "s sessions") || strings.Contains(view, "a agents") {
			t.Fatalf("tab %s footer still advertises removed toggles: %q", tc.tab, view)
		}
	}
}

// --- worktree creation (Overview, Projects, Sessions tabs) --------------

func TestModelWorktreePromptCreatesSessionForSelectedProject(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ID: 2, ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("1"))
	m = press(m, runes("w"))
	if !m.creatingWorktree || m.worktreeProjectID != 7 {
		t.Fatalf("creatingWorktree=%v worktreeProjectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
	m = press(m, runes("feature/login"))
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
	api := &fakeAPI{createdSession: httpclient.Session{ID: 2, ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)

	updated, cmd := m.Update(createSessionMsg{session: api.createdSession})
	m = updated.(Model)
	if !m.loading || m.tab != tabSessions || m.creatingWorktree || cmd == nil {
		t.Fatalf("loading=%v tab=%d creatingWorktree=%v cmd nil=%v", m.loading, m.tab, m.creatingWorktree, cmd == nil)
	}

	api.projects = []httpclient.Project{{ID: 7, MainSessionName: "api-main"}}
	api.sessions = []httpclient.Session{
		{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"},
		{ID: 2, ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"},
	}
	msg := cmd().(listMsg)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if cur, _ := m.cursor(); cur.session.ID != 2 {
		t.Fatalf("expected newly created session selected, got %d", cur.session.ID)
	}
	if m.pendingSelectSession != "" {
		t.Fatalf("pendingSelectSession should be cleared, got %q", m.pendingSelectSession)
	}
}

func TestModelWorktreePromptUsesSelectedProject(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}, {ID: 2, MainSessionName: "web-main"}},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ID: 20, ProjectID: 2, SessionName: "web-main", Type: "main"},
		},
	})
	m = press(m, runes("1"))
	m = press(m, runes("G"))
	m = press(m, runes("w"))

	if !m.creatingWorktree || m.worktreeProjectID != 2 {
		t.Fatalf("creatingWorktree=%v worktreeProjectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
}

func TestModelWorktreePromptEscCancels(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("1"))
	m = press(m, runes("w"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.creatingWorktree || m.worktreeBranch != "" || m.worktreeProjectID != 0 || cmd != nil || len(api.created) != 0 {
		t.Fatalf("creatingWorktree=%v branch=%q projectID=%d cmd=%v created=%d", m.creatingWorktree, m.worktreeBranch, m.worktreeProjectID, cmd, len(api.created))
	}
}

func TestModelWorktreeEmptyProjectsShowsHint(t *testing.T) {
	m := loaded(t, listMsg{})
	m = press(m, runes("1")) // projects tab, no projects
	m = press(m, runes("w"))
	if m.creatingWorktree {
		t.Fatalf("should not start worktree with no project selected")
	}
	if m.status != "no project selected" {
		t.Fatalf("expected hint, got status=%q", m.status)
	}
}

func TestModelEnterAttachesAgentPaneFromAgentsTab(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", TmuxName: "api_main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 1, SessionID: 10, Kind: "claude", DisplayName: "reviewer", TmuxPaneID: "%7", Status: "running"}},
	})
	m = press(m, runes("3"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.attach.SessionName != "api_main" || m.attach.PaneID != "%7" || cmd == nil {
		t.Fatalf("agents tab enter should attach pane, attach=%+v cmd nil=%v", m.attach, cmd == nil)
	}
}

func TestModelWorktreeFromOverviewUsesSelectedProject(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	// Overview tab (default): w targets the selected session's project.
	m = press(m, runes("w"))
	if !m.creatingWorktree || m.worktreeProjectID != 7 {
		t.Fatalf("creatingWorktree=%v worktreeProjectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
}

func TestModelWorktreeFromSessionsUsesSelectedProject(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("w"))
	if !m.creatingWorktree || m.worktreeProjectID != 7 {
		t.Fatalf("creatingWorktree=%v worktreeProjectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
}

func TestModelWorktreeIgnoredOnAgentsTab(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 7, SessionID: 1, Kind: "claude"}},
	})
	m = press(m, runes("3")) // agents tab
	m = press(m, runes("w"))
	if m.creatingWorktree {
		t.Fatalf("w should be ignored on the agents tab")
	}
}

// --- secondary creation (Sessions tab) ----------------------------------

func TestModelSecondaryPromptCreatesSessionFromSelectedSession(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ID: 2, ProjectID: 7, SessionName: "pkg", Type: "secondary"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("2"))
	m = press(m, runes("S"))
	if !m.creatingSecondary || m.secondaryParentID != 1 {
		t.Fatalf("creatingSecondary=%v secondaryParentID=%d", m.creatingSecondary, m.secondaryParentID)
	}
	m = press(m, runes("pkg"))
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = press(m, runes("Tools"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("loading=%v cmd nil=%v", m.loading, cmd == nil)
	}
	_ = cmd().(createSessionMsg)
	if len(api.created) != 1 {
		t.Fatalf("created calls = %d", len(api.created))
	}
	in := api.created[0]
	if in.Type != "secondary" || in.ParentSessionID != 1 || in.RelativeWorkingDirectory != "pkg" || in.PreferredName != "Tools" || in.OnDelete != "cascade" {
		t.Fatalf("create input = %+v", in)
	}
}

func TestModelSecondaryIgnoredOutsideSessionsTab(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = press(m, runes("1")) // projects tab
	m = press(m, runes("S"))
	if m.creatingSecondary {
		t.Fatalf("S should be ignored outside the sessions tab")
	}
}

// --- deletion ------------------------------------------------------------

func TestModelDeleteConfirmationDeletesSelectedProject(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("1"))
	m = press(m, runes("X"))
	if !m.confirm || m.confirmDelete != deleteProject || m.confirmDeleteID != 7 {
		t.Fatalf("confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "Delete project? y/n") {
		t.Fatalf("missing project confirmation: %q", m.View())
	}
	updated, cmd := m.Update(runes("y"))
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
	m = press(m, runes("j"))
	m = press(m, runes("X"))

	if !m.confirm || m.confirmDelete != deleteWorktreeSession || m.confirmDeleteID != 2 {
		t.Fatalf("confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "Destroy worktree session and worktree? y/n") {
		t.Fatalf("missing worktree confirmation: %q", m.View())
	}

	updated, cmd := m.Update(runes("y"))
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
	m = press(m, runes("j"))
	m = press(m, runes("X"))

	if !m.confirm || m.confirmDelete != deleteAgent || m.confirmDeleteID != 12 {
		t.Fatalf("confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "Delete agent? y/n") {
		t.Fatalf("missing agent confirmation: %q", m.View())
	}

	updated, cmd := m.Update(runes("y"))
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("expected delete command and loading state")
	}
	msg := cmd().(deleteMsg)
	if msg.err != nil || api.deletedAgent != 12 || api.deletedSession != 0 || api.deleted != 0 {
		t.Fatalf("delete msg=%+v deletedAgent=%d deletedSession=%d deletedProject=%d", msg, api.deletedAgent, api.deletedSession, api.deleted)
	}
}

func TestModelDeleteAgentFromAgentsTab(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, Title: "API", MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 12, ProjectID: 7, SessionID: 1, Kind: "claude", DisplayName: "reviewer", Status: "running"}},
	})
	m = updated.(Model)
	m = press(m, runes("3"))
	m = press(m, runes("X"))
	if !m.confirm || m.confirmDelete != deleteAgent || m.confirmDeleteID != 12 {
		t.Fatalf("confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
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
	updated, cmd := m.Update(runes("X"))
	m = updated.(Model)

	if m.confirm || cmd != nil || api.deleted != 0 || api.deletedSession != 0 || m.status != "only worktree sessions can be destroyed" {
		t.Fatalf("confirm=%v cmd=%v deleted=%d deletedSession=%d status=%q", m.confirm, cmd, api.deleted, api.deletedSession, m.status)
	}
}

func TestModelDeleteConfirmationDeletesSelectedSecondarySession(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ParentSessionID: 1, ProjectID: 7, SessionName: "pkg", Type: "secondary", OnDelete: "cascade"},
		},
	})
	m = updated.(Model)
	m = press(m, runes("j"))
	m = press(m, runes("X"))
	if !m.confirm || m.confirmDelete != deleteSecondarySession || m.confirmDeleteID != 2 || !strings.Contains(m.View(), "Delete secondary session using cascade policy? y/n") {
		t.Fatalf("confirm=%v target=%d id=%d view=%q", m.confirm, m.confirmDelete, m.confirmDeleteID, m.View())
	}
	updated, cmd := m.Update(runes("y"))
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("expected delete command and loading state")
	}
	msg := cmd().(deleteMsg)
	if msg.err != nil || api.deletedSession != 2 || api.deleteForce {
		t.Fatalf("delete msg=%+v deletedSession=%d force=%v", msg, api.deletedSession, api.deleteForce)
	}
}

// --- misc ----------------------------------------------------------------

func TestModelListCommandFetchesProjectsAndSessions(t *testing.T) {
	api := &fakeAPI{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"}},
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

func TestModelKeepsErrorStatusOnListFailure(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{err: errors.New("daemon unavailable")})
	m = updated.(Model)
	if m.status != "daemon unavailable" || m.loading {
		t.Fatalf("status=%q loading=%v", m.status, m.loading)
	}
}
