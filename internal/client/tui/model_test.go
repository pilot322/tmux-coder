package tui

import (
	"context"
	"errors"
	"reflect"
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
	createdAgents     []httpclient.CreateAgentInput
	createdAgent      httpclient.Agent
	createAgentErr    error
	renamedAgentID    int
	renamedAgentName  string
	renamedAgent      httpclient.Agent
	renameAgentErr    error
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

func (a *fakeAPI) CreateAgent(_ context.Context, in httpclient.CreateAgentInput) (httpclient.Agent, error) {
	a.createdAgents = append(a.createdAgents, in)
	return a.createdAgent, a.createAgentErr
}

func (a *fakeAPI) RenameAgent(_ context.Context, id int, displayName string) (httpclient.Agent, error) {
	a.renamedAgentID = id
	a.renamedAgentName = displayName
	return a.renamedAgent, a.renameAgentErr
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

func TestModelSessionRowsNestWorktreesByProvenance(t *testing.T) {
	// standalone (id 2) is a base-ref 'W' worktree (parentless); feat (id 3) is
	// branched off main and feat-backend (id 4) off feat. Despite its lower id,
	// standalone must render at the Project level after main's whole subtree.
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, Title: "API", FullPath: "/work/api", MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 1, Parent: -1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ParentSessionID: -1, ProjectID: 7, SessionName: "api.standalone", Type: "worktree", Branch: "standalone"},
			{ID: 3, ParentSessionID: 1, ProjectID: 7, SessionName: "api.feat", Type: "worktree", Branch: "feat"},
			{ID: 4, ParentSessionID: 3, ProjectID: 7, SessionName: "api.feat-backend", Type: "worktree", Branch: "feat-backend"},
		},
	})
	m = press(m, runes("2")) // sessions tab
	view := m.View()

	mainAt := strings.Index(view, "- api.main")
	featAt := strings.Index(view, "- api.feat (feat)")
	backendAt := strings.Index(view, "- api.feat-backend (feat-backend)")
	standaloneAt := strings.Index(view, "- api.standalone (standalone)")
	if mainAt < 0 || featAt < 0 || backendAt < 0 || standaloneAt < 0 {
		t.Fatalf("missing session rows:\n%q", view)
	}
	if !(mainAt < featAt && featAt < backendAt && backendAt < standaloneAt) {
		t.Fatalf("tree order = main:%d feat:%d backend:%d standalone:%d, want provenance subtree before base worktree\n%q", mainAt, featAt, backendAt, standaloneAt, view)
	}

	// Nesting is shown by indentation: feat one level under main, feat-backend
	// under feat, while the base-ref worktree sits at the Project level.
	if !strings.Contains(view, "      - api.feat (feat)") {
		t.Fatalf("feat should be indented one level under main:\n%q", view)
	}
	if !strings.Contains(view, "        - api.feat-backend (feat-backend)") {
		t.Fatalf("feat-backend should be indented under feat:\n%q", view)
	}
	if !strings.Contains(view, "    - api.standalone (standalone)") || strings.Contains(view, "      - api.standalone (standalone)") {
		t.Fatalf("base-ref worktree should sit at the project level (same indent as main):\n%q", view)
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
	for _, want := range []string{"● reviewer · api-main"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agents view missing %q: %q", want, view)
		}
	}
	for _, notWant := range []string{"claude ·", "[running]", "API /"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("agents view should not contain %q: %q", notWant, view)
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
		{"0", []string{"w worktree", "W base worktree", "X delete"}, []string{"S secondary"}},
		{"1", []string{"w worktree", "W base worktree", "X delete"}, []string{"S secondary"}},
		{"2", []string{"w worktree", "W base worktree", "S secondary", "X delete"}, nil},
		{"3", []string{"X delete"}, []string{"w worktree", "W base worktree", "S secondary"}},
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
	// From the Projects view, 'w' branches off the project's Main Session.
	if in.ProjectID != 7 || in.Type != "worktree" || in.Branch != "feature/login" || !in.CreateWorktree || !in.CreateBranch || in.BaseBranch != "" || in.ParentSessionID != 1 {
		t.Fatalf("create input = %+v, want worktree off main session 1", in)
	}
}

func TestModelWorktreePromptIgnoresDuplicateSubmitWhileLoading(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ID: 2, ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("1"))
	m = press(m, runes("w"))
	m = press(m, runes("feature/login"))

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("first submit loading=%v cmd nil=%v", m.loading, cmd == nil)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.loading || cmd != nil {
		t.Fatalf("duplicate submit loading=%v cmd nil=%v, want loading and no command", m.loading, cmd == nil)
	}
	if len(api.created) != 0 {
		t.Fatalf("create command should not have run yet, got %d calls", len(api.created))
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

func TestModelWorktreeCreateIgnoresOlderPollRefresh(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ID: 2, ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	initialProjects := []httpclient.Project{{ID: 7, MainSessionName: "api-main"}}
	initialSessions := []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}}
	updated, _ := m.Update(listMsg{seq: 1, projects: initialProjects, sessions: initialSessions})
	m = updated.(Model)

	updated, cmd := m.Update(createSessionMsg{session: api.createdSession})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("create should schedule a refresh")
	}

	api.projects = initialProjects
	api.sessions = []httpclient.Session{
		{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"},
		{ID: 2, ParentSessionID: 1, ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"},
		{ID: 3, ParentSessionID: 2, ProjectID: 7, SessionName: "backend", Type: "secondary"},
	}
	updated, _ = m.Update(cmd().(listMsg))
	m = updated.(Model)

	updated, _ = m.Update(listMsg{seq: 1, projects: initialProjects, sessions: initialSessions})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "api-feature-login") || !strings.Contains(view, "backend") {
		t.Fatalf("stale poll overwrote created worktree tree: %q", view)
	}
}

// worktreePromptModel returns a model that has just submitted the branch
// "feature/login" for project 7's worktree create, with api primed to return
// createErr on that attempt.
func worktreePromptModel(t *testing.T, api *fakeAPI) Model {
	t.Helper()
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api-main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("1"))
	m = press(m, runes("w"))
	m = press(m, runes("feature/login"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	msg := cmd().(createSessionMsg)
	updated, _ = m.Update(msg)
	return updated.(Model)
}

func TestModelWorktreeBranchExistsPromptsThenCreatesWorktree(t *testing.T) {
	api := &fakeAPI{createErr: &httpclient.APIError{Status: 409, Code: httpclient.CodeBranchExists, Message: "branch already exists"}}
	m := worktreePromptModel(t, api)

	if m.worktreeConflict != httpclient.CodeBranchExists {
		t.Fatalf("worktreeConflict = %q, want %q", m.worktreeConflict, httpclient.CodeBranchExists)
	}
	if !strings.Contains(m.View(), "branch already exists. Create a worktree for it? y/n") {
		t.Fatalf("missing branch-exists prompt: %q", m.View())
	}

	api.createErr = nil
	updated, cmd := m.Update(runes("y"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("y should re-issue the create")
	}
	_ = cmd().(createSessionMsg)
	last := api.created[len(api.created)-1]
	if !last.CreateWorktree || last.CreateBranch {
		t.Fatalf("re-issue = %+v, want createWorktree=true createBranch=false", last)
	}
}

func TestModelWorktreeExistsPromptsThenAdopts(t *testing.T) {
	api := &fakeAPI{createErr: &httpclient.APIError{Status: 409, Code: httpclient.CodeWorktreeExists, Message: "worktree already exists for branch"}}
	m := worktreePromptModel(t, api)

	if m.worktreeConflict != httpclient.CodeWorktreeExists {
		t.Fatalf("worktreeConflict = %q, want %q", m.worktreeConflict, httpclient.CodeWorktreeExists)
	}
	if !strings.Contains(m.View(), "worktree already exists. Create a session? y/n") {
		t.Fatalf("missing worktree-exists prompt: %q", m.View())
	}

	api.createErr = nil
	updated, cmd := m.Update(runes("y"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("y should re-issue the create")
	}
	_ = cmd().(createSessionMsg)
	last := api.created[len(api.created)-1]
	if last.CreateWorktree || last.CreateBranch {
		t.Fatalf("re-issue = %+v, want createWorktree=false createBranch=false (adopt)", last)
	}
}

func TestModelWorktreeConflictPromptNCancels(t *testing.T) {
	api := &fakeAPI{createErr: &httpclient.APIError{Status: 409, Code: httpclient.CodeBranchExists, Message: "branch already exists"}}
	m := worktreePromptModel(t, api)
	before := len(api.created)

	updated, cmd := m.Update(runes("n"))
	m = updated.(Model)
	if m.worktreeConflict != "" || m.worktreeBranch != "" || m.worktreeProjectID != 0 {
		t.Fatalf("n should clear conflict state: conflict=%q branch=%q project=%d", m.worktreeConflict, m.worktreeBranch, m.worktreeProjectID)
	}
	if cmd != nil {
		t.Fatal("n should not re-issue a create")
	}
	if len(api.created) != before {
		t.Fatalf("n issued an extra create: %d", len(api.created)-before)
	}
}

func TestModelWorktreeNonConflictErrorShowsStatus(t *testing.T) {
	api := &fakeAPI{createErr: &httpclient.APIError{Status: 502, Message: "session gateway failure"}}
	m := worktreePromptModel(t, api)
	if m.worktreeConflict != "" {
		t.Fatalf("non-conflict error should not enter conflict state, got %q", m.worktreeConflict)
	}
	if !strings.Contains(m.status, "session gateway failure") {
		t.Fatalf("status = %q, want the error message", m.status)
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

func TestModelWorktreeFromSessionsParentsToSelectedSession(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ID: 3, ProjectID: 7, SessionName: "api.feature-backend", Type: "worktree"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ProjectID: 7, SessionName: "api.feature", Type: "worktree", Branch: "feature"},
		},
	})
	m = updated.(Model)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("j")) // select the worktree session
	m = press(m, runes("w"))
	if !m.creatingWorktree || m.worktreeProjectID != 7 {
		t.Fatalf("creatingWorktree=%v projectID=%d", m.creatingWorktree, m.worktreeProjectID)
	}
	m = press(m, runes("feature-backend"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected create command")
	}
	_ = cmd().(createSessionMsg)
	if len(api.created) != 1 {
		t.Fatalf("created calls = %d", len(api.created))
	}
	in := api.created[0]
	if in.Type != "worktree" || in.Branch != "feature-backend" || !in.CreateWorktree || !in.CreateBranch || in.ParentSessionID != 2 {
		t.Fatalf("create input = %+v, want worktree off parent session 2", in)
	}
}

func TestModelWorktreeFromSecondaryIsRejected(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ParentSessionID: 1, ProjectID: 7, SessionName: "pkg", Type: "secondary"},
		},
	})
	m = updated.(Model)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("j")) // select the secondary session
	m = press(m, runes("w"))
	if m.creatingWorktree {
		t.Fatalf("w on a secondary should be rejected, creatingWorktree=%v", m.creatingWorktree)
	}
	if m.status == "" {
		t.Fatalf("expected a rejection status message")
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

// --- worktree from a bare base ref (W) ----------------------------------

func TestModelWorktreeFromBasePromptCreatesParentlessWorktree(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ID: 2, ProjectID: 7, SessionName: "api.feature", Type: "worktree"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("1")) // projects tab
	m = press(m, runes("W"))
	if !m.creatingWorktreeFromBase || m.worktreeFromBaseProjectID != 7 {
		t.Fatalf("creatingWorktreeFromBase=%v projectID=%d", m.creatingWorktreeFromBase, m.worktreeFromBaseProjectID)
	}
	// Step 1: new branch name.
	m = press(m, runes("feature"))
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Step 2: base ref.
	m = press(m, runes("origin/main"))
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
	if in.ProjectID != 7 || in.Type != "worktree" || in.Branch != "feature" || !in.CreateWorktree || !in.CreateBranch || in.BaseBranch != "origin/main" || in.ParentSessionID != 0 {
		t.Fatalf("create input = %+v, want parentless worktree off origin/main", in)
	}
}

func TestModelWorktreeFromBasePromptShowsBothSteps(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = press(m, runes("1"))
	m = press(m, runes("W"))
	if !strings.Contains(m.View(), "New worktree branch:") {
		t.Fatalf("step 1 should prompt for the branch name:\n%q", m.View())
	}
	m = press(m, runes("feature"))
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.View(), "Base ref:") {
		t.Fatalf("step 2 should prompt for the base ref:\n%q", m.View())
	}
}

func TestModelWorktreeFromBaseEscCancels(t *testing.T) {
	api := &fakeAPI{}
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = press(m, runes("1"))
	m = press(m, runes("W"))
	m = press(m, runes("feature"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.creatingWorktreeFromBase || m.worktreeFromBaseBranch != "" || m.worktreeFromBaseProjectID != 0 || cmd != nil || len(api.created) != 0 {
		t.Fatalf("after esc: creating=%v branch=%q projectID=%d cmd=%v created=%d", m.creatingWorktreeFromBase, m.worktreeFromBaseBranch, m.worktreeFromBaseProjectID, cmd, len(api.created))
	}
}

func TestModelWorktreeFromBaseIgnoredOnAgentsTab(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 20, ProjectID: 7, SessionID: 1, Kind: "claude"}},
	})
	m = press(m, runes("3")) // agents tab
	m = press(m, runes("W"))
	if m.creatingWorktreeFromBase {
		t.Fatalf("W should be ignored on the agents tab")
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

// --- agent creation ('a') ------------------------------------------------

func TestModelAgentPromptCreatesAgentInSelectedSession(t *testing.T) {
	api := &fakeAPI{createdAgent: httpclient.Agent{ID: 9, ProjectID: 7, SessionID: 2, Kind: "claude", DisplayName: "reviewer"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{
			{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"},
			{ID: 2, ProjectID: 7, SessionName: "api.feature", Type: "worktree", Branch: "feature"},
		},
	})
	m = updated.(Model)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("j")) // select the worktree session
	m = press(m, runes("a"))
	if !m.creatingAgent || m.agentSessionID != 2 || m.agentProjectID != 7 {
		t.Fatalf("creatingAgent=%v sessionID=%d projectID=%d", m.creatingAgent, m.agentSessionID, m.agentProjectID)
	}
	m = press(m, runes("claude"))
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = press(m, runes("reviewer"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("loading=%v cmd nil=%v", m.loading, cmd == nil)
	}
	msg := cmd().(createAgentMsg)
	if msg.err != nil {
		t.Fatalf("create err = %v", msg.err)
	}
	if len(api.createdAgents) != 1 {
		t.Fatalf("created agent calls = %d", len(api.createdAgents))
	}
	in := api.createdAgents[0]
	if in.ProjectID != 7 || in.SessionID != 2 || in.Kind != "claude" || in.DisplayName == nil || *in.DisplayName != "reviewer" || in.TmuxPaneID != nil {
		t.Fatalf("create input = %+v, want claude agent in session 2 owned by the daemon", in)
	}
}

func TestModelAgentPromptOmitsEmptyName(t *testing.T) {
	api := &fakeAPI{createdAgent: httpclient.Agent{ID: 9, ProjectID: 7, SessionID: 1, Kind: "opencode"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("a"))
	m = press(m, runes("opencode"))
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // advance to the name step
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected create command with an empty name")
	}
	_ = cmd().(createAgentMsg)
	if len(api.createdAgents) != 1 {
		t.Fatalf("created agent calls = %d", len(api.createdAgents))
	}
	if in := api.createdAgents[0]; in.DisplayName != nil {
		t.Fatalf("displayName = %v, want nil for an empty name", *in.DisplayName)
	}
}

func TestModelAgentPromptUsesDefaultExecutable(t *testing.T) {
	api := &fakeAPI{createdAgent: httpclient.Agent{ID: 9, ProjectID: 7, SessionID: 1, Kind: defaultAgentExecutable}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("a"))
	m = press(m, tea.KeyMsg{Type: tea.KeyEnter}) // accept the default executable
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected create command with the default executable")
	}
	_ = cmd().(createAgentMsg)
	if len(api.createdAgents) != 1 {
		t.Fatalf("created agent calls = %d", len(api.createdAgents))
	}
	if in := api.createdAgents[0]; in.Kind != defaultAgentExecutable {
		t.Fatalf("kind = %q, want %q", in.Kind, defaultAgentExecutable)
	}
}

func TestModelAgentPromptEscCancels(t *testing.T) {
	api := &fakeAPI{}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("a"))
	m = press(m, runes("claude"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.creatingAgent || m.agentExecutable != "" || m.agentSessionID != 0 || cmd != nil || len(api.createdAgents) != 0 {
		t.Fatalf("creatingAgent=%v executable=%q sessionID=%d cmd=%v created=%d", m.creatingAgent, m.agentExecutable, m.agentSessionID, cmd, len(api.createdAgents))
	}
}

func TestModelAgentFromAgentsTabUsesOwningSession(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, Title: "API", MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 5, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 3, ProjectID: 7, SessionID: 5, Kind: "claude", Status: "running"}},
	})
	m = press(m, runes("3")) // agents tab
	m = press(m, runes("a"))
	if !m.creatingAgent || m.agentSessionID != 5 || m.agentProjectID != 7 {
		t.Fatalf("creatingAgent=%v sessionID=%d projectID=%d, want the selected agent's owning session", m.creatingAgent, m.agentSessionID, m.agentProjectID)
	}
}

func TestModelAgentCreateSelectsNewAgentOnAgentsTab(t *testing.T) {
	api := &fakeAPI{createdAgent: httpclient.Agent{ID: 9, ProjectID: 7, SessionID: 1, Kind: "claude"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)

	updated, cmd := m.Update(createAgentMsg{agent: api.createdAgent})
	m = updated.(Model)
	if !m.loading || m.tab != tabAgents || m.creatingAgent || cmd == nil {
		t.Fatalf("loading=%v tab=%d creatingAgent=%v cmd nil=%v", m.loading, m.tab, m.creatingAgent, cmd == nil)
	}
	if m.agentSel.id != 9 {
		t.Fatalf("agentSel.id = %d, want the newly created agent 9", m.agentSel.id)
	}
}

func TestModelRenameAgentPromptRenamesSelectedAgent(t *testing.T) {
	api := &fakeAPI{renamedAgent: httpclient.Agent{ID: 3, ProjectID: 7, SessionID: 5, Kind: "claude", DisplayName: "new-name"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, Title: "API", MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 5, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 3, ProjectID: 7, SessionID: 5, Kind: "claude", DisplayName: "old", Status: "running"}},
	})
	m = updated.(Model)
	m = press(m, runes("3")) // agents tab
	m = press(m, runes("u"))
	if !m.renamingAgent || m.renameAgentID != 3 || m.renameValue != "old" {
		t.Fatalf("renamingAgent=%v id=%d value=%q", m.renamingAgent, m.renameAgentID, m.renameValue)
	}
	for range "old" {
		m = press(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = press(m, runes("new-name"))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.loading || cmd == nil {
		t.Fatalf("loading=%v cmd nil=%v", m.loading, cmd == nil)
	}
	msg := cmd().(renameAgentMsg)
	if msg.err != nil {
		t.Fatalf("rename err = %v", msg.err)
	}
	if api.renamedAgentID != 3 || api.renamedAgentName != "new-name" {
		t.Fatalf("rename call id=%d name=%q", api.renamedAgentID, api.renamedAgentName)
	}

	updated, cmd = m.Update(msg)
	m = updated.(Model)
	if m.renamingAgent || m.agentSel.id != 3 || cmd == nil {
		t.Fatalf("renamingAgent=%v agentSel=%d cmd nil=%v", m.renamingAgent, m.agentSel.id, cmd == nil)
	}
}

func TestModelRenameAgentRequiresName(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, Title: "API", MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 5, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 3, ProjectID: 7, SessionID: 5, Kind: "claude", DisplayName: "old", Status: "running"}},
	})
	m = press(m, runes("3"))
	m = press(m, runes("u"))
	for range "old" {
		m = press(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || !m.renamingAgent || m.status == "" {
		t.Fatalf("cmd=%v renamingAgent=%v status=%q", cmd, m.renamingAgent, m.status)
	}
}

func TestModelRenameIgnoredOutsideAgentsTab(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 5, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = press(m, runes("2"))
	m = press(m, runes("u"))
	if m.renamingAgent {
		t.Fatal("u should be ignored outside the agents tab")
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
	msg := m.listCmd(1)().(listMsg)

	if msg.err != nil || len(msg.projects) != 1 || len(msg.sessions) != 1 {
		t.Fatalf("msg = %+v", msg)
	}
	if api.listProjectsCalls != 1 || api.listSessionsCalls != 1 || api.listAgentsCalls != 1 {
		t.Fatalf("listProjectsCalls=%d listSessionsCalls=%d listAgentsCalls=%d", api.listProjectsCalls, api.listSessionsCalls, api.listAgentsCalls)
	}
}

func TestModelRefreshDuringConfirmKeepsPrompt(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 12, ProjectID: 7, SessionID: 1, DisplayName: "reviewer", TmuxPaneID: "%12", Status: "running"}},
	})
	m = press(m, runes("j"))
	m = press(m, runes("X"))
	if !m.confirm || m.confirmDelete != deleteAgent || m.confirmDeleteID != 12 {
		t.Fatalf("setup confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}

	// A poll-driven refresh arrives while the confirm prompt is open; it must
	// not stomp the prompt.
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 12, ProjectID: 7, SessionID: 1, DisplayName: "reviewer", TmuxPaneID: "%12", Status: "busy"}},
	})
	m = updated.(Model)

	if !m.confirm || m.confirmDelete != deleteAgent || m.confirmDeleteID != 12 {
		t.Fatalf("refresh stomped confirm: confirm=%v target=%d id=%d", m.confirm, m.confirmDelete, m.confirmDeleteID)
	}
	if !strings.Contains(m.View(), "Delete agent? y/n") {
		t.Fatalf("confirm prompt missing after refresh: %q", m.View())
	}
}

func TestModelRefreshDuringWorktreePromptKeepsInput(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = press(m, runes("1"))
	m = press(m, runes("w"))
	m = press(m, runes("feature/login"))
	if !m.creatingWorktree || m.worktreeBranch != "feature/login" {
		t.Fatalf("setup creating=%v branch=%q", m.creatingWorktree, m.worktreeBranch)
	}
	m.status = "branch is required"

	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 7, MainSessionName: "api.main"}},
		sessions: []httpclient.Session{{ID: 1, ProjectID: 7, SessionName: "api.main", Type: "main"}},
	})
	m = updated.(Model)

	if !m.creatingWorktree || m.worktreeBranch != "feature/login" || m.status != "branch is required" {
		t.Fatalf("refresh stomped worktree prompt: creating=%v branch=%q status=%q", m.creatingWorktree, m.worktreeBranch, m.status)
	}
}

func TestModelTickSchedulesSilentRefresh(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}}})
	if m.loading {
		t.Fatal("precondition: loading should be false after initial load")
	}

	updated, cmd := m.Update(tickMsg{})
	m = updated.(Model)
	if m.loading {
		t.Fatal("tick refresh must not flip into the loading state")
	}
	if cmd == nil {
		t.Fatal("tick should schedule work")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("tick should batch a refresh and a re-arm, got %T len=%d", cmd(), len(batch))
	}
}

func TestAgentStatusStyleHighlightsAttention(t *testing.T) {
	if !agentStatusStyle("waiting").GetBold() {
		t.Error("waiting should be bold to signal it needs attention")
	}
	if !agentStatusStyle("busy").GetFaint() {
		t.Error("busy should be dimmed")
	}
	running := agentStatusStyle("running")
	if running.GetBold() || running.GetFaint() {
		t.Error("running should be neutral (not bold or faint)")
	}
	if agentStatusStyle("idle").GetForeground() == running.GetForeground() {
		t.Error("idle should be visually distinct from neutral running")
	}
}

// --- agents view: status sort & grouping --------------------------------

func TestAgentRowsFlatStatusSorted(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, Status: "starting"},
			{ID: 2, ProjectID: 1, SessionID: 10, Status: "running"},
			{ID: 3, ProjectID: 1, SessionID: 10, Status: "busy"},
			{ID: 4, ProjectID: 1, SessionID: 10, Status: "idle"},
			{ID: 5, ProjectID: 1, SessionID: 10, Status: "waiting"},
			{ID: 6, ProjectID: 1, SessionID: 10, Status: ""},
		},
	})

	var got []string
	for _, r := range m.agentRows() {
		got = append(got, r.agent.Status)
	}
	want := []string{"waiting", "idle", "busy", "running", "starting", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agentRows status order = %v, want %v", got, want)
	}
}

func TestAgentRowsTiebreaksByAscendingID(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 30, ProjectID: 1, SessionID: 10, Status: "waiting"},
			{ID: 12, ProjectID: 1, SessionID: 10, Status: "waiting"},
		},
	})

	rows := m.agentRows()
	if len(rows) != 2 || rows[0].agent.ID != 12 || rows[1].agent.ID != 30 {
		t.Fatalf("within a status, agents should sort by ascending id; got %d then %d", rows[0].agent.ID, rows[1].agent.ID)
	}
}

func TestModelGroupToggleOnlyAffectsAgentsTab(t *testing.T) {
	base := listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents:   []httpclient.Agent{{ID: 1, ProjectID: 1, SessionID: 10, Status: "running"}},
	}

	// On the Agents tab, 's' toggles grouping on and off.
	m := loaded(t, base)
	m = press(m, runes("3"))
	m = press(m, runes("s"))
	if !m.groupAgents {
		t.Fatalf("'s' on agents tab should turn grouping on")
	}
	m = press(m, runes("s"))
	if m.groupAgents {
		t.Fatalf("'s' on agents tab should toggle grouping back off")
	}

	// On any other tab, 's' is a no-op.
	m = loaded(t, base)
	m = press(m, runes("2")) // sessions tab
	m = press(m, runes("s"))
	if m.groupAgents {
		t.Fatalf("'s' outside the agents tab should not toggle grouping")
	}
}

func TestAgentRowsGroupedByProject(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{
			{ID: 1, Title: "API", MainSessionName: "api-main"},
			{ID: 2, Title: "WEB", MainSessionName: "web-main"},
		},
		sessions: []httpclient.Session{
			{ID: 10, ProjectID: 1, SessionName: "api-main", Type: "main"},
			{ID: 20, ProjectID: 2, SessionName: "web-main", Type: "main"},
		},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, Status: "running"},
			{ID: 2, ProjectID: 1, SessionID: 10, Status: "waiting"},
			{ID: 3, ProjectID: 2, SessionID: 20, Status: "busy"},
			{ID: 4, ProjectID: 2, SessionID: 20, Status: "waiting"},
		},
	})
	m = press(m, runes("3"))
	m = press(m, runes("s")) // group on

	// Agents bucket by project (existing project order), status-sorted within
	// each bucket: project 1 [waiting 2, running 1], then project 2 [waiting 4, busy 3].
	var gotIDs []int
	for _, r := range m.agentRows() {
		gotIDs = append(gotIDs, r.agent.ID)
	}
	wantIDs := []int{2, 1, 4, 3}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("grouped agentRows ids = %v, want %v", gotIDs, wantIDs)
	}

	// Navigation rows stay selectable-only: one per agent, no header rows.
	navRows := m.rows(tabAgents)
	if len(navRows) != 4 {
		t.Fatalf("rows(tabAgents) should hold only the 4 agents, got %d", len(navRows))
	}
	for _, r := range navRows {
		if r.kind != rowAgent {
			t.Fatalf("rows(tabAgents) should contain only agent rows, got kind %d", r.kind)
		}
	}

	// The rendered view shows a non-selectable header per project group.
	view := m.View()
	if !strings.Contains(view, "API") || !strings.Contains(view, "WEB") {
		t.Fatalf("grouped agents view should show a header per project: %q", view)
	}
}

func TestModelAgentCursorFollowsAgentAcrossResort(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, Status: "idle"},
			{ID: 2, ProjectID: 1, SessionID: 10, Status: "waiting"},
		},
	})
	m = press(m, runes("3"))
	m = press(m, runes("j")) // waiting(2) at top, so j selects idle(1)
	if m.agentSel.id != 1 {
		t.Fatalf("setup: expected agent 1 selected, got %d", m.agentSel.id)
	}

	// A poll flips the statuses, reversing the sort order. The cursor must stay
	// glued to agent 1 by identity, not to its old position.
	updated, _ := m.Update(listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, Status: "waiting"},
			{ID: 2, ProjectID: 1, SessionID: 10, Status: "idle"},
		},
	})
	m = updated.(Model)
	if cur, _ := m.cursor(); cur.agent.ID != 1 {
		t.Fatalf("cursor should follow agent 1 across the re-sort, got %d", cur.agent.ID)
	}
}

func TestModelJumpToTopAndBottomWorkInAgentsView(t *testing.T) {
	base := listMsg{
		projects: []httpclient.Project{{ID: 1, Title: "API", MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "main", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, Status: "waiting"},
			{ID: 2, ProjectID: 1, SessionID: 10, Status: "busy"},
			{ID: 3, ProjectID: 1, SessionID: 10, Status: "running"},
		},
	}
	// g/G keep their jump-to-top / jump-to-bottom meaning in the Agents view,
	// both flat and grouped — s (group) does not shadow them.
	for _, grouped := range []bool{false, true} {
		m := loaded(t, base)
		m = press(m, runes("3"))
		if grouped {
			m = press(m, runes("s"))
		}
		rows := m.agentRows()
		last := rows[len(rows)-1].agent.ID
		first := rows[0].agent.ID

		m = press(m, runes("G"))
		if cur, _ := m.cursor(); cur.agent.ID != last {
			t.Fatalf("grouped=%v: G should jump to last agent %d, got %d", grouped, last, cur.agent.ID)
		}
		m = press(m, runes("g"))
		if cur, _ := m.cursor(); cur.agent.ID != first {
			t.Fatalf("grouped=%v: g should jump to first agent %d, got %d", grouped, first, cur.agent.ID)
		}
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
