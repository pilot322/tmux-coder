package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

type API interface {
	ListProjects(context.Context) ([]httpclient.Project, error)
	DeleteProject(context.Context, int) error
}

type Model struct {
	ctx      context.Context
	api      API
	projects []httpclient.Project
	selected int
	status   string
	loading  bool
	confirm  bool
	help     bool
	attach   *httpclient.Project
}

type listMsg struct {
	projects []httpclient.Project
	err      error
}

type deleteMsg struct {
	id  int
	err error
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
)

var keys = struct {
	up, down, top, bottom, enter, del, refresh, help, quit key.Binding
}{
	up:      key.NewBinding(key.WithKeys("up", "k")),
	down:    key.NewBinding(key.WithKeys("down", "j")),
	top:     key.NewBinding(key.WithKeys("g")),
	bottom:  key.NewBinding(key.WithKeys("G")),
	enter:   key.NewBinding(key.WithKeys("enter")),
	del:     key.NewBinding(key.WithKeys("X")),
	refresh: key.NewBinding(key.WithKeys("r")),
	help:    key.NewBinding(key.WithKeys("?")),
	quit:    key.NewBinding(key.WithKeys("q", "esc", "ctrl+c")),
}

func Run(ctx context.Context, api API) (httpclient.Project, bool, error) {
	m := NewModel(ctx, api)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return httpclient.Project{}, false, err
	}
	m = final.(Model)
	if m.attach == nil {
		return httpclient.Project{}, false, nil
	}
	return *m.attach, true, nil
}

func NewModel(ctx context.Context, api API) Model {
	return Model{ctx: ctx, api: api, loading: true}
}

func (m Model) Init() tea.Cmd {
	return m.listCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case listMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.projects = msg.projects
		m.status = ""
		m.confirm = false
		if len(m.projects) == 0 {
			m.selected = 0
		} else if m.selected >= len(m.projects) {
			m.selected = len(m.projects) - 1
		}
	case deleteMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.confirm = false
			return m, nil
		}
		return m, m.listCmd()
	case tea.KeyMsg:
		if m.confirm {
			switch msg.String() {
			case "y":
				if len(m.projects) == 0 {
					m.confirm = false
					return m, nil
				}
				m.loading = true
				return m, m.deleteCmd(m.projects[m.selected].ID)
			case "n", "esc":
				m.confirm = false
				return m, nil
			}
		}

		switch {
		case key.Matches(msg, keys.quit):
			return m, tea.Quit
		case key.Matches(msg, keys.help):
			m.help = !m.help
		case key.Matches(msg, keys.refresh):
			m.loading = true
			return m, m.listCmd()
		case key.Matches(msg, keys.up):
			if m.selected > 0 {
				m.selected--
			}
		case key.Matches(msg, keys.down):
			if m.selected < len(m.projects)-1 {
				m.selected++
			}
		case key.Matches(msg, keys.top):
			m.selected = 0
		case key.Matches(msg, keys.bottom):
			if len(m.projects) > 0 {
				m.selected = len(m.projects) - 1
			}
		case key.Matches(msg, keys.del):
			if len(m.projects) > 0 {
				m.confirm = true
			}
		case key.Matches(msg, keys.enter):
			if len(m.projects) > 0 {
				project := m.projects[m.selected]
				m.attach = &project
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("tmux-coder projects"))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString("Loading projects...\n")
	} else if len(m.projects) == 0 {
		b.WriteString("No projects yet. Run `tmux-coder open` or `tmux-coder o` in a directory to create and attach it.\n")
	} else {
		for i, p := range m.projects {
			line := fmt.Sprintf("%s  %s  %s", filepath.Base(p.FullPath), mutedStyle.Render(p.FullPath), mutedStyle.Render(p.MainSessionName))
			if i == m.selected {
				line = selectStyle.Render("> " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + "\n")
		}
	}

	if m.confirm && len(m.projects) > 0 {
		b.WriteString("\nDelete project? y/n\n")
	}
	if m.status != "" {
		b.WriteString("\n" + errorStyle.Render(m.status) + "\n")
	}
	if m.help {
		b.WriteString("\nKeys: j/k or arrows move, g/G jump, enter attach, X delete, r refresh, ? help, q quit\n")
	} else {
		b.WriteString("\n" + mutedStyle.Render("j/k move  enter attach  X delete  r refresh  ? help  q quit") + "\n")
	}
	return b.String()
}

func (m Model) listCmd() tea.Cmd {
	return func() tea.Msg {
		projects, err := m.api.ListProjects(m.ctx)
		return listMsg{projects: projects, err: err}
	}
}

func (m Model) deleteCmd(id int) tea.Cmd {
	return func() tea.Msg {
		return deleteMsg{id: id, err: m.api.DeleteProject(m.ctx, id)}
	}
}
