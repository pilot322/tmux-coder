package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

func TestModelFilterActivatesAndNarrows(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{
		{ID: 1, Title: "api-gateway", MainTmuxSessionName: "api_t"},
		{ID: 2, Title: "web-frontend", MainTmuxSessionName: "web_t"},
		{ID: 3, Title: "database", MainTmuxSessionName: "db_t"},
	}})
	m = press(m, runes("1")) // Projects tab

	m = press(m, runes("f"))
	if !m.filtering {
		t.Fatal("pressing f should open the fuzzy finder")
	}
	m = press(m, runes("web"))
	if m.filterQuery != "web" {
		t.Fatalf("query should accumulate typed runes, got %q", m.filterQuery)
	}

	view := m.View()
	if !strings.Contains(view, "web-frontend") {
		t.Fatalf("matching project should be shown: %q", view)
	}
	if strings.Contains(view, "api-gateway") || strings.Contains(view, "database") {
		t.Fatalf("non-matching projects should be hidden: %q", view)
	}
}

func TestModelFilterCursorRestsOnBestMatch(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "s", TmuxName: "s_t", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, DisplayName: "builder", Status: "idle", TmuxPaneID: "%1"},
			{ID: 2, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", Status: "idle", TmuxPaneID: "%2"},
			{ID: 3, ProjectID: 1, SessionID: 10, DisplayName: "tester", Status: "idle", TmuxPaneID: "%3"},
		},
	})
	m = press(m, runes("3")) // Agents tab
	m = press(m, runes("f"))
	m = press(m, runes("rev"))

	cur, ok := m.cursor()
	if !ok || cur.agent.DisplayName != "reviewer" {
		t.Fatalf("cursor should rest on the only match, got %+v", cur)
	}
	view := m.View()
	if strings.Contains(view, "builder") || strings.Contains(view, "tester") {
		t.Fatalf("non-matching agents should be hidden: %q", view)
	}
}

func TestModelFilterCtrlNMovesWithinMatches(t *testing.T) {
	m := loaded(t, listMsg{
		projects: []httpclient.Project{{ID: 1, MainSessionName: "main"}},
		sessions: []httpclient.Session{{ID: 10, ProjectID: 1, SessionName: "s", TmuxName: "s_t", Type: "main"}},
		agents: []httpclient.Agent{
			{ID: 1, ProjectID: 1, SessionID: 10, DisplayName: "builder", Status: "idle", TmuxPaneID: "%1"},
			{ID: 2, ProjectID: 1, SessionID: 10, DisplayName: "reviewer", Status: "idle", TmuxPaneID: "%2"},
			{ID: 3, ProjectID: 1, SessionID: 10, DisplayName: "tester", Status: "idle", TmuxPaneID: "%3"},
		},
	})
	m = press(m, runes("3"))
	m = press(m, runes("f"))
	m = press(m, runes("e")) // every agent contains 'e'

	first, _ := m.cursor()
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlN})
	second, _ := m.cursor()
	if first.agent.ID == second.agent.ID {
		t.Fatalf("ctrl+n should advance to a different match, stayed on %d", first.agent.ID)
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	back, _ := m.cursor()
	if back.agent.ID != first.agent.ID {
		t.Fatalf("ctrl+p should return to the first match %d, got %d", first.agent.ID, back.agent.ID)
	}
}

func TestModelFilterEnterAttachesHighlightedRow(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{
		{ID: 1, Title: "alpha", MainTmuxSessionName: "alpha_t"},
		{ID: 2, Title: "bravo", MainTmuxSessionName: "bravo_t"},
	}})
	m = press(m, runes("1"))
	m = press(m, runes("f"))
	m = press(m, runes("brav"))

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.attach.SessionName != "bravo_t" {
		t.Fatalf("enter should attach to the highlighted match, got %q", m.attach.SessionName)
	}
	if cmd == nil {
		t.Fatal("enter on a match should return a quit command")
	}
}

func TestModelFilterEscRestoresUnfilteredView(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{
		{ID: 1, Title: "alpha", MainTmuxSessionName: "alpha_t"},
		{ID: 2, Title: "bravo", MainTmuxSessionName: "bravo_t"},
	}})
	m = press(m, runes("1"))
	m = press(m, runes("f"))
	m = press(m, runes("alpha"))

	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filtering {
		t.Fatal("esc should close the fuzzy finder")
	}
	if m.filterQuery != "" {
		t.Fatalf("esc should clear the query, got %q", m.filterQuery)
	}
	view := m.View()
	if !strings.Contains(view, "alpha") || !strings.Contains(view, "bravo") {
		t.Fatalf("both projects should be visible again after esc: %q", view)
	}
}

// While filtering, keys that are bindings in normal mode (w, q, s, digits) must
// be typed into the query instead of triggering their actions.
func TestModelFilterCapturesActionKeysAsText(t *testing.T) {
	m := loaded(t, listMsg{projects: []httpclient.Project{
		{ID: 1, Title: "workspace", MainTmuxSessionName: "ws_t"},
	}})
	m = press(m, runes("1"))
	m = press(m, runes("f"))
	for _, k := range []string{"w", "s"} {
		m = press(m, runes(k))
	}
	if m.creatingWorktree {
		t.Fatal("w should be captured as query text, not start a worktree")
	}
	if m.filterQuery != "ws" {
		t.Fatalf("query should be %q, got %q", "ws", m.filterQuery)
	}
	if m.tab != tabProjects {
		t.Fatalf("digits/letters must not switch tabs while filtering, tab=%d", m.tab)
	}
}
