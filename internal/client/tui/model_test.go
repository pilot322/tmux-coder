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
	projects []httpclient.Project
	listErr  error
	deleted  int
}

func (a *fakeAPI) ListProjects(context.Context) ([]httpclient.Project, error) {
	return a.projects, a.listErr
}

func (a *fakeAPI) DeleteProject(_ context.Context, id int) error {
	a.deleted = id
	return nil
}

func TestModelEnterSelectsProjectAndQuits(t *testing.T) {
	m := NewModel(context.Background(), &fakeAPI{})
	updated, _ := m.Update(listMsg{projects: []httpclient.Project{{ID: 1, MainSessionName: "api-main"}}})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.attach == nil || m.attach.MainSessionName != "api-main" {
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
