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
	listErr           error
	deleted           int
	created           []httpclient.CreateSessionInput
	createdSession    httpclient.Session
	createErr         error
	listProjectsCalls int
	listSessionsCalls int
}

func (a *fakeAPI) ListProjects(context.Context) ([]httpclient.Project, error) {
	a.listProjectsCalls++
	return a.projects, a.listErr
}

func (a *fakeAPI) ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error) {
	a.listSessionsCalls++
	return a.sessions, nil
}

func (a *fakeAPI) CreateSession(_ context.Context, in httpclient.CreateSessionInput) (httpclient.Session, error) {
	a.created = append(a.created, in)
	return a.createdSession, a.createErr
}

func (a *fakeAPI) DeleteProject(_ context.Context, id int) error {
	a.deleted = id
	return nil
}

func TestModelEnterSelectsProjectMainSessionAndQuits(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}}})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.attach != "api-main" {
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)

	if !m.showSessions || m.selectedSession != 2 {
		t.Fatalf("showSessions=%v selectedSession=%d", m.showSessions, m.selectedSession)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.attach != "web-main" || cmd == nil {
		t.Fatalf("attach=%q cmd nil=%v", m.attach, cmd == nil)
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Backend API") || !strings.Contains(view, "- api-main") || !strings.Contains(view, "- api-work (feature/api)") {
		t.Fatalf("view missing expanded rows: %q", view)
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
	if api.listProjectsCalls != 1 || api.listSessionsCalls != 1 {
		t.Fatalf("listProjectsCalls=%d listSessionsCalls=%d", api.listProjectsCalls, api.listSessionsCalls)
	}
}

func TestModelWorktreePromptCreatesSessionForSelectedProject(t *testing.T) {
	api := &fakeAPI{createdSession: httpclient.Session{ProjectID: 7, SessionName: "api-feature-login", Type: "worktree", Branch: "feature/login"}}
	m := NewModel(context.Background(), api)
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}}})
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
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
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
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 7, MainSessionName: "api-main"}}})
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
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 7}}})
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

func TestModelKeepsErrorStatusOnListFailure(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{err: errors.New("daemon unavailable")})
	m = updated.(Model)
	if m.status != "daemon unavailable" || m.loading {
		t.Fatalf("status=%q loading=%v", m.status, m.loading)
	}
}
