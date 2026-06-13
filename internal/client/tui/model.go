package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

type API interface {
	ListProjects(context.Context) ([]httpclient.Project, error)
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
	CreateSession(context.Context, httpclient.CreateSessionInput) (httpclient.Session, error)
	DeleteProject(context.Context, int) error
}

type Model struct {
	ctx                  context.Context
	api                  API
	projects             []httpclient.Project
	sessions             []httpclient.Session
	selected             int
	selectedSession      int
	status               string
	loading              bool
	confirm              bool
	help                 bool
	showSessions         bool
	creatingWorktree     bool
	worktreeBranch       string
	worktreeProjectID    int
	pendingSelectSession string
	attach               string
}

type listMsg struct {
	projects []httpclient.Project
	sessions []httpclient.Session
	err      error
}

type deleteMsg struct {
	id  int
	err error
}

type createSessionMsg struct {
	session httpclient.Session
	err     error
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
)

var keys = struct {
	up, down, top, bottom, enter, del, refresh, sessions, worktree, help, quit key.Binding
}{
	up:       key.NewBinding(key.WithKeys("up", "k")),
	down:     key.NewBinding(key.WithKeys("down", "j")),
	top:      key.NewBinding(key.WithKeys("g")),
	bottom:   key.NewBinding(key.WithKeys("G")),
	enter:    key.NewBinding(key.WithKeys("enter")),
	del:      key.NewBinding(key.WithKeys("X")),
	refresh:  key.NewBinding(key.WithKeys("r")),
	sessions: key.NewBinding(key.WithKeys("s")),
	worktree: key.NewBinding(key.WithKeys("w")),
	help:     key.NewBinding(key.WithKeys("?")),
	quit:     key.NewBinding(key.WithKeys("q", "esc", "ctrl+c")),
}

func Run(ctx context.Context, api API) (string, bool, error) {
	m := NewModel(ctx, api)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", false, err
	}
	m = final.(Model)
	if m.attach == "" {
		return "", false, nil
	}
	return m.attach, true, nil
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
		m.sessions = msg.sessions
		m.status = ""
		m.confirm = false
		m.clampSelection()
		m.selectPendingSession()
	case deleteMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			m.confirm = false
			return m, nil
		}
		return m, m.listCmd()
	case createSessionMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.creatingWorktree = false
		m.worktreeBranch = ""
		m.worktreeProjectID = 0
		m.pendingSelectSession = sessionName(msg.session)
		m.showSessions = true
		m.loading = true
		return m, m.listCmd()
	case tea.KeyMsg:
		if m.creatingWorktree {
			return m.updateWorktreePrompt(msg)
		}

		if m.confirm {
			switch msg.String() {
			case "y":
				if len(m.projects) == 0 {
					m.confirm = false
					return m, nil
				}
				project, ok := m.selectedProject()
				if !ok {
					m.confirm = false
					return m, nil
				}
				m.loading = true
				return m, m.deleteCmd(project.ID)
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
		case key.Matches(msg, keys.sessions):
			if m.showSessions {
				if project, ok := m.selectedProject(); ok {
					m.selected = m.projectIndex(project.ID)
				}
				m.showSessions = false
			} else {
				m.showSessions = true
				m.selectedSession = m.mainSessionIndexForSelectedProject()
			}
			m.clampSelection()
		case key.Matches(msg, keys.worktree):
			if project, ok := m.selectedProject(); ok {
				m.creatingWorktree = true
				m.worktreeBranch = ""
				m.worktreeProjectID = project.ID
				m.status = ""
			}
		case key.Matches(msg, keys.up):
			if m.showSessions {
				if m.selectedSession > 0 {
					m.selectedSession--
				}
			} else if m.selected > 0 {
				m.selected--
			}
		case key.Matches(msg, keys.down):
			if m.showSessions {
				if m.selectedSession < len(m.sessionRows())-1 {
					m.selectedSession++
				}
			} else if m.selected < len(m.projects)-1 {
				m.selected++
			}
		case key.Matches(msg, keys.top):
			if m.showSessions {
				m.selectedSession = 0
			} else {
				m.selected = 0
			}
		case key.Matches(msg, keys.bottom):
			if m.showSessions {
				if rows := m.sessionRows(); len(rows) > 0 {
					m.selectedSession = len(rows) - 1
				}
			} else if len(m.projects) > 0 {
				m.selected = len(m.projects) - 1
			}
		case key.Matches(msg, keys.del):
			if _, ok := m.selectedProject(); ok {
				m.confirm = true
			}
		case key.Matches(msg, keys.enter):
			if target, ok := m.attachTarget(); ok {
				m.attach = target
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
	} else if m.showSessions {
		m.writeSessionRows(&b)
	} else {
		for i, p := range m.projects {
			line := fmt.Sprintf("%s  %s  %s", p.Title, mutedStyle.Render(p.FullPath), mutedStyle.Render(p.MainSessionName))
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
	if m.creatingWorktree {
		b.WriteString("\nNew worktree branch: " + m.worktreeBranch + "\n")
	}
	if m.status != "" {
		b.WriteString("\n" + errorStyle.Render(m.status) + "\n")
	}
	if m.creatingWorktree {
		b.WriteString("\n" + mutedStyle.Render("enter create  esc cancel") + "\n")
	} else if m.help {
		b.WriteString("\nKeys: j/k or arrows move, g/G jump, enter attach, s sessions, w worktree, X delete, r refresh, ? help, q quit\n")
	} else {
		b.WriteString("\n" + mutedStyle.Render("j/k move  enter attach  s sessions  w worktree  X delete  r refresh  ? help  q quit") + "\n")
	}
	return b.String()
}

func (m Model) listCmd() tea.Cmd {
	return func() tea.Msg {
		projects, err := m.api.ListProjects(m.ctx)
		if err != nil {
			return listMsg{err: err}
		}
		sessions, err := m.api.ListSessions(m.ctx, httpclient.ListSessionsInput{})
		return listMsg{projects: projects, sessions: sessions, err: err}
	}
}

func (m Model) deleteCmd(id int) tea.Cmd {
	return func() tea.Msg {
		return deleteMsg{id: id, err: m.api.DeleteProject(m.ctx, id)}
	}
}

func (m Model) createWorktreeCmd(projectID int, branch string) tea.Cmd {
	return func() tea.Msg {
		session, err := m.api.CreateSession(m.ctx, httpclient.CreateSessionInput{
			ProjectID: projectID,
			Type:      "worktree",
			Branch:    branch,
			Create:    true,
		})
		return createSessionMsg{session: session, err: err}
	}
}

func (m Model) updateWorktreePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.creatingWorktree = false
		m.worktreeBranch = ""
		m.worktreeProjectID = 0
		m.status = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.worktreeBranch) > 0 {
			m.worktreeBranch = m.worktreeBranch[:len(m.worktreeBranch)-1]
		}
		return m, nil
	case tea.KeyEnter:
		branch := strings.TrimSpace(m.worktreeBranch)
		if branch == "" {
			m.status = "branch is required"
			return m, nil
		}
		m.worktreeBranch = branch
		m.status = ""
		m.loading = true
		return m, m.createWorktreeCmd(m.worktreeProjectID, branch)
	case tea.KeyRunes:
		m.worktreeBranch += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func (m *Model) clampSelection() {
	if len(m.projects) == 0 {
		m.selected = 0
	} else if m.selected < 0 {
		m.selected = 0
	} else if m.selected >= len(m.projects) {
		m.selected = len(m.projects) - 1
	}
	rows := m.sessionRows()
	if len(rows) == 0 {
		m.selectedSession = 0
	} else if m.selectedSession < 0 {
		m.selectedSession = 0
	} else if m.selectedSession >= len(rows) {
		m.selectedSession = len(rows) - 1
	}
}

func (m *Model) selectPendingSession() {
	if m.pendingSelectSession == "" {
		return
	}
	rows := m.sessionRows()
	for i, s := range rows {
		if sessionName(s) == m.pendingSelectSession {
			m.selectedSession = i
			m.pendingSelectSession = ""
			return
		}
	}
	m.pendingSelectSession = ""
}

func (m Model) writeSessionRows(b *strings.Builder) {
	selected := 0
	rows := m.sessionRows()
	for _, p := range m.projects {
		line := fmt.Sprintf("%s  %s  %s", p.Title, mutedStyle.Render(p.FullPath), mutedStyle.Render(p.MainSessionName))
		b.WriteString("  " + line + "\n")
		for _, s := range rows {
			if s.ProjectID != p.ID {
				continue
			}
			line := "- " + sessionName(s)
			if s.Branch != "" {
				line += " (" + s.Branch + ")"
			}
			if selected == m.selectedSession {
				line = selectStyle.Render("> " + line)
			} else {
				line = "  " + line
			}
			b.WriteString("  " + line + "\n")
			selected++
		}
	}
}

func (m Model) sessionRows() []httpclient.Session {
	rows := make([]httpclient.Session, 0, len(m.sessions))
	for _, p := range m.projects {
		for _, s := range m.sessions {
			if s.ProjectID == p.ID && isMainSession(s, p) {
				rows = append(rows, s)
			}
		}
		for _, s := range m.sessions {
			if s.ProjectID == p.ID && !isMainSession(s, p) {
				rows = append(rows, s)
			}
		}
	}
	return rows
}

func (m Model) selectedProject() (httpclient.Project, bool) {
	if m.showSessions {
		rows := m.sessionRows()
		if m.selectedSession < 0 || m.selectedSession >= len(rows) {
			return httpclient.Project{}, false
		}
		idx := m.projectIndex(rows[m.selectedSession].ProjectID)
		if idx < 0 {
			return httpclient.Project{}, false
		}
		return m.projects[idx], true
	}
	if m.selected < 0 || m.selected >= len(m.projects) {
		return httpclient.Project{}, false
	}
	return m.projects[m.selected], true
}

func (m Model) attachTarget() (string, bool) {
	if m.showSessions {
		rows := m.sessionRows()
		if m.selectedSession < 0 || m.selectedSession >= len(rows) {
			return "", false
		}
		return sessionName(rows[m.selectedSession]), true
	}
	project, ok := m.selectedProject()
	if !ok {
		return "", false
	}
	return project.MainSessionName, true
}

func (m Model) mainSessionIndexForSelectedProject() int {
	if m.selected < 0 || m.selected >= len(m.projects) {
		return 0
	}
	project := m.projects[m.selected]
	rows := m.sessionRows()
	for i, s := range rows {
		if s.ProjectID == project.ID && isMainSession(s, project) {
			return i
		}
	}
	for i, s := range rows {
		if s.ProjectID == project.ID {
			return i
		}
	}
	return 0
}

func (m Model) projectIndex(id int) int {
	for i, p := range m.projects {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func isMainSession(s httpclient.Session, p httpclient.Project) bool {
	return s.Type == "main" || sessionName(s) == p.MainSessionName
}

func sessionName(s httpclient.Session) string {
	if s.SessionName != "" {
		return s.SessionName
	}
	return s.Name
}
