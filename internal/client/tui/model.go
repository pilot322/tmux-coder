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
	ListAgents(context.Context, httpclient.ListAgentsInput) ([]httpclient.Agent, error)
	CreateSession(context.Context, httpclient.CreateSessionInput) (httpclient.Session, error)
	DeleteProject(context.Context, int) error
	DeleteSession(context.Context, int, bool) error
	DeleteAgent(context.Context, int) error
}

type deleteTarget uint8

const (
	deleteNothing deleteTarget = iota
	deleteProject
	deleteWorktreeSession
	deleteAgent
	deleteSecondarySession
)

type secondaryPromptStep uint8

const (
	secondaryPromptNone secondaryPromptStep = iota
	secondaryPromptRelativeWorkingDirectory
	secondaryPromptPreferredName
)

type rowKind uint8

const (
	rowProject rowKind = iota
	rowSession
	rowAgent
)

type viewRow struct {
	kind    rowKind
	project httpclient.Project
	session httpclient.Session
	agent   httpclient.Agent
}

type AttachTarget struct {
	SessionName string
	PaneID      string
}

type Model struct {
	ctx                  context.Context
	api                  API
	projects             []httpclient.Project
	sessions             []httpclient.Session
	agents               []httpclient.Agent
	selected             int
	selectedSession      int
	status               string
	loading              bool
	confirm              bool
	confirmDelete        deleteTarget
	confirmDeleteID      int
	help                 bool
	showSessions         bool
	showAgents           bool
	creatingWorktree     bool
	worktreeBranch       string
	worktreeProjectID    int
	creatingSecondary    bool
	secondaryStep        secondaryPromptStep
	secondaryParentID    int
	secondaryRelwd       string
	secondaryName        string
	pendingSelectSession string
	initialSession       string
	attach               AttachTarget
}

type listMsg struct {
	projects []httpclient.Project
	sessions []httpclient.Session
	agents   []httpclient.Agent
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
	titleStyle     = lipgloss.NewStyle().Bold(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	mainStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	worktreeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	secondaryStyle = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("39")), // depth 1
		lipgloss.NewStyle().Foreground(lipgloss.Color("33")), // depth 2
		lipgloss.NewStyle().Foreground(lipgloss.Color("27")), // depth 3
		lipgloss.NewStyle().Foreground(lipgloss.Color("21")), // depth 4
		lipgloss.NewStyle().Foreground(lipgloss.Color("19")), // depth 5+
	}
)

var keys = struct {
	up, down, top, bottom, enter, del, refresh, sessions, agents, worktree, secondary, help, quit key.Binding
}{
	up:        key.NewBinding(key.WithKeys("up", "k")),
	down:      key.NewBinding(key.WithKeys("down", "j")),
	top:       key.NewBinding(key.WithKeys("g")),
	bottom:    key.NewBinding(key.WithKeys("G")),
	enter:     key.NewBinding(key.WithKeys("enter")),
	del:       key.NewBinding(key.WithKeys("X")),
	refresh:   key.NewBinding(key.WithKeys("r")),
	sessions:  key.NewBinding(key.WithKeys("s")),
	agents:    key.NewBinding(key.WithKeys("a")),
	worktree:  key.NewBinding(key.WithKeys("w")),
	secondary: key.NewBinding(key.WithKeys("S")),
	help:      key.NewBinding(key.WithKeys("?")),
	quit:      key.NewBinding(key.WithKeys("q", "esc", "ctrl+c")),
}

func Run(ctx context.Context, api API, initialSession ...string) (AttachTarget, bool, error) {
	m := NewModel(ctx, api, initialSession...)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return AttachTarget{}, false, err
	}
	m = final.(Model)
	if m.attach.SessionName == "" {
		return AttachTarget{}, false, nil
	}
	return m.attach, true, nil
}

func NewModel(ctx context.Context, api API, initialSession ...string) Model {
	m := Model{ctx: ctx, api: api, loading: true, showSessions: true, showAgents: true}
	if len(initialSession) > 0 {
		m.initialSession = initialSession[0]
	}
	return m
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
		m.agents = msg.agents
		m.status = ""
		m.confirm = false
		m.confirmDelete = deleteNothing
		m.confirmDeleteID = 0
		m.clampSelection()
		m.selectInitialSession()
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
		m.creatingSecondary = false
		m.secondaryStep = secondaryPromptNone
		m.secondaryParentID = 0
		m.secondaryRelwd = ""
		m.secondaryName = ""
		m.pendingSelectSession = sessionName(msg.session)
		m.showSessions = true
		m.loading = true
		return m, m.listCmd()
	case tea.KeyMsg:
		if m.creatingSecondary {
			return m.updateSecondaryPrompt(msg)
		}
		if m.creatingWorktree {
			return m.updateWorktreePrompt(msg)
		}

		if m.confirm {
			switch msg.String() {
			case "y":
				if m.confirmDelete == deleteNothing {
					m.confirm = false
					return m, nil
				}
				m.loading = true
				return m, m.deleteCmd(m.confirmDelete, m.confirmDeleteID)
			case "n", "esc":
				m.confirm = false
				m.confirmDelete = deleteNothing
				m.confirmDeleteID = 0
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
					m.selected = m.projectRowIndex(project.ID)
				}
				m.showSessions = false
			} else {
				project, ok := m.selectedProject()
				m.showSessions = true
				if ok {
					m.selectedSession = m.mainSessionRowIndexForProject(project.ID)
				} else {
					m.selectedSession = 0
				}
			}
			m.clampSelection()
		case key.Matches(msg, keys.agents):
			row, hasRow := m.selectedRow()
			m.showAgents = !m.showAgents
			if hasRow {
				m.selectEquivalentRow(row)
			}
			m.clampSelection()
		case key.Matches(msg, keys.worktree):
			if project, ok := m.selectedProject(); ok {
				m.creatingWorktree = true
				m.worktreeBranch = ""
				m.worktreeProjectID = project.ID
				m.status = ""
			}
		case key.Matches(msg, keys.secondary):
			if session, ok := m.selectedSessionRow(); ok {
				m.creatingSecondary = true
				m.secondaryStep = secondaryPromptRelativeWorkingDirectory
				m.secondaryParentID = session.ID
				m.secondaryRelwd = session.RelativeWorkingDirectory
				m.secondaryName = ""
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
				if m.selectedSession < len(m.sessionViewRows())-1 {
					m.selectedSession++
				}
			} else if m.selected < len(m.projectViewRows())-1 {
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
				if rows := m.sessionViewRows(); len(rows) > 0 {
					m.selectedSession = len(rows) - 1
				}
			} else if rows := m.projectViewRows(); len(rows) > 0 {
				m.selected = len(rows) - 1
			}
		case key.Matches(msg, keys.del):
			m.requestDeleteConfirmation()
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
		m.writeProjectRows(&b)
	}

	if m.confirm {
		switch m.confirmDelete {
		case deleteProject:
			b.WriteString("\nDelete project? y/n\n")
		case deleteWorktreeSession:
			b.WriteString("\nDestroy worktree session and worktree? y/n\n")
		case deleteAgent:
			b.WriteString("\nDelete agent? y/n\n")
		case deleteSecondarySession:
			policy := "cascade"
			if s, ok := m.sessionByID(m.confirmDeleteID); ok && s.OnDelete != "" {
				policy = s.OnDelete
			}
			b.WriteString("\nDelete secondary session using " + policy + " policy? y/n\n")
		}
	}
	if m.creatingSecondary {
		if m.secondaryStep == secondaryPromptPreferredName {
			b.WriteString("\nPreferred name (optional): " + m.secondaryName + "\n")
		} else {
			b.WriteString("\nRelative working directory: " + m.secondaryRelwd + "\n")
		}
	}
	if m.creatingWorktree {
		b.WriteString("\nNew worktree branch: " + m.worktreeBranch + "\n")
	}
	if m.status != "" {
		b.WriteString("\n" + errorStyle.Render(m.status) + "\n")
	}
	if m.creatingSecondary {
		b.WriteString("\n" + mutedStyle.Render("enter next/create  esc cancel") + "\n")
	} else if m.creatingWorktree {
		b.WriteString("\n" + mutedStyle.Render("enter create  esc cancel") + "\n")
	} else if m.help {
		b.WriteString("\nKeys: j/k or arrows move, g/G jump, enter attach, s sessions, a agents, w worktree, S secondary, X delete, r refresh, ? help, q quit\n")
	} else {
		b.WriteString("\n" + mutedStyle.Render("j/k move  enter attach  s sessions  a agents  w worktree  S secondary  X delete  r refresh  ? help  q quit") + "\n")
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
		if err != nil {
			return listMsg{projects: projects, err: err}
		}
		agents, err := m.api.ListAgents(m.ctx, httpclient.ListAgentsInput{})
		return listMsg{projects: projects, sessions: sessions, agents: agents, err: err}
	}
}

func (m Model) deleteCmd(target deleteTarget, id int) tea.Cmd {
	return func() tea.Msg {
		switch target {
		case deleteProject:
			return deleteMsg{id: id, err: m.api.DeleteProject(m.ctx, id)}
		case deleteWorktreeSession:
			return deleteMsg{id: id, err: m.api.DeleteSession(m.ctx, id, true)}
		case deleteAgent:
			return deleteMsg{id: id, err: m.api.DeleteAgent(m.ctx, id)}
		case deleteSecondarySession:
			return deleteMsg{id: id, err: m.api.DeleteSession(m.ctx, id, false)}
		default:
			return deleteMsg{id: id}
		}
	}
}

func (m Model) createSecondaryCmd(parentID int, relwd, preferredName string) tea.Cmd {
	return func() tea.Msg {
		session, err := m.api.CreateSession(m.ctx, httpclient.CreateSessionInput{
			Type:                     "secondary",
			ParentSessionID:          parentID,
			RelativeWorkingDirectory: strings.TrimSpace(relwd),
			PreferredName:            strings.TrimSpace(preferredName),
			OnDelete:                 "cascade",
		})
		return createSessionMsg{session: session, err: err}
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

func (m Model) updateSecondaryPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.creatingSecondary = false
		m.secondaryStep = secondaryPromptNone
		m.secondaryParentID = 0
		m.secondaryRelwd = ""
		m.secondaryName = ""
		m.status = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if m.secondaryStep == secondaryPromptPreferredName {
			if len(m.secondaryName) > 0 {
				m.secondaryName = m.secondaryName[:len(m.secondaryName)-1]
			}
		} else if len(m.secondaryRelwd) > 0 {
			m.secondaryRelwd = m.secondaryRelwd[:len(m.secondaryRelwd)-1]
		}
		return m, nil
	case tea.KeyEnter:
		if m.secondaryStep == secondaryPromptRelativeWorkingDirectory {
			m.secondaryRelwd = strings.TrimSpace(m.secondaryRelwd)
			m.secondaryStep = secondaryPromptPreferredName
			m.status = ""
			return m, nil
		}
		m.secondaryName = strings.TrimSpace(m.secondaryName)
		m.status = ""
		m.loading = true
		return m, m.createSecondaryCmd(m.secondaryParentID, m.secondaryRelwd, m.secondaryName)
	case tea.KeyRunes:
		if m.secondaryStep == secondaryPromptPreferredName {
			m.secondaryName += string(msg.Runes)
		} else {
			m.secondaryRelwd += string(msg.Runes)
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) clampSelection() {
	projectRows := m.projectViewRows()
	if len(projectRows) == 0 {
		m.selected = 0
	} else if m.selected < 0 {
		m.selected = 0
	} else if m.selected >= len(projectRows) {
		m.selected = len(projectRows) - 1
	}
	rows := m.sessionViewRows()
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
	rows := m.sessionViewRows()
	for i, row := range rows {
		if row.kind == rowSession && sessionName(row.session) == m.pendingSelectSession {
			m.selectedSession = i
			m.pendingSelectSession = ""
			return
		}
	}
	m.pendingSelectSession = ""
}

func (m *Model) selectInitialSession() {
	if m.initialSession == "" {
		return
	}
	defer func() { m.initialSession = "" }()
	rows := m.sessionViewRows()
	for i, row := range rows {
		if row.kind == rowSession && (sessionName(row.session) == m.initialSession || tmuxSessionName(row.session) == m.initialSession) {
			m.selectedSession = i
			return
		}
	}
}

func (m Model) writeProjectRows(b *strings.Builder) {
	selected := 0
	for _, p := range m.projects {
		line := fmt.Sprintf("%s  %s  %s", p.Title, mutedStyle.Render(p.FullPath), mutedStyle.Render(p.MainSessionName))
		if selected == m.selected {
			line = selectStyle.Render("> " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
		selected++

		if !m.showAgents {
			continue
		}
		for _, a := range m.agentsForProject(p.ID) {
			line := "- " + agentLabel(a)
			if selected == m.selected {
				line = selectStyle.Render("> " + line)
			} else {
				line = "  " + line
			}
			b.WriteString("  " + line + "\n")
			selected++
		}
	}
}

func (m Model) writeSessionRows(b *strings.Builder) {
	selected := 0
	for _, p := range m.projects {
		line := fmt.Sprintf("%s  %s  %s", p.Title, mutedStyle.Render(p.FullPath), mutedStyle.Render(p.MainSessionName))
		b.WriteString("  " + line + "\n")
		for _, s := range m.sessionRows() {
			if s.ProjectID != p.ID {
				continue
			}
			prefix := strings.Repeat("  ", m.sessionDepth(s))
			content := "- " + sessionName(s)
			if s.Branch != "" {
				content += " (" + s.Branch + ")"
			}
			switch s.Type {
			case "main":
				content = mainStyle.Render(content)
			case "worktree":
				content = worktreeStyle.Render(content)
			case "secondary":
				d := m.sessionDepth(s)
				if d >= len(secondaryStyle) {
					d = len(secondaryStyle) - 1
				}
				content = secondaryStyle[d].Render(content)
			}
			if selected == m.selectedSession {
				line = selectStyle.Render("> " + prefix + content)
			} else {
				line = "  " + prefix + content
			}
			b.WriteString("  " + line + "\n")
			selected++

			if !m.showAgents {
				continue
			}
			for _, a := range m.agentsForSession(s.ID) {
				line := strings.Repeat("  ", m.sessionDepth(s)+1) + "- " + agentLabel(a)
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
}

func (m Model) projectViewRows() []viewRow {
	rows := make([]viewRow, 0, len(m.projects)+len(m.agents))
	for _, p := range m.projects {
		rows = append(rows, viewRow{kind: rowProject, project: p})
		if !m.showAgents {
			continue
		}
		for _, a := range m.agentsForProject(p.ID) {
			rows = append(rows, viewRow{kind: rowAgent, project: p, agent: a})
		}
	}
	return rows
}

func (m Model) sessionViewRows() []viewRow {
	rows := make([]viewRow, 0, len(m.sessions)+len(m.agents))
	for _, p := range m.projects {
		for _, s := range m.sessionRows() {
			if s.ProjectID != p.ID {
				continue
			}
			rows = append(rows, viewRow{kind: rowSession, project: p, session: s})
			if !m.showAgents {
				continue
			}
			for _, a := range m.agentsForSession(s.ID) {
				rows = append(rows, viewRow{kind: rowAgent, project: p, session: s, agent: a})
			}
		}
	}
	return rows
}

func (m Model) sessionRows() []httpclient.Session {
	rows := make([]httpclient.Session, 0, len(m.sessions))
	for _, p := range m.projects {
		mainID := 0
		for _, s := range m.sessions {
			if s.ProjectID != p.ID || !isMainSession(s, p) {
				continue
			}
			rows = append(rows, s)
			mainID = s.ID
			rows = m.appendSecondaryChildren(rows, p.ID, s.ID)
		}
		for _, s := range m.sessions {
			if s.ProjectID != p.ID || s.Type != "worktree" {
				continue
			}
			rows = append(rows, s)
			rows = m.appendSecondaryChildren(rows, p.ID, s.ID)
		}
		if mainID == 0 {
			rows = m.appendSecondaryChildren(rows, p.ID, 0)
		}
	}
	return rows
}

func (m Model) appendSecondaryChildren(rows []httpclient.Session, projectID, parent int) []httpclient.Session {
	for _, s := range m.sessions {
		if s.ProjectID != projectID || s.Type != "secondary" || parentID(s) != parent {
			continue
		}
		rows = append(rows, s)
		rows = m.appendSecondaryChildren(rows, projectID, s.ID)
	}
	return rows
}

func (m Model) sessionDepth(s httpclient.Session) int {
	if s.Type != "secondary" {
		return 0
	}
	depth := 0
	for id := parentID(s); id > 0; {
		parent, ok := m.sessionByID(id)
		if !ok || parent.Type != "secondary" {
			return depth + 1
		}
		depth++
		id = parentID(parent)
	}
	return depth
}

func (m Model) selectedProject() (httpclient.Project, bool) {
	row, ok := m.selectedRow()
	if !ok {
		return httpclient.Project{}, false
	}
	if row.project.ID != 0 {
		return row.project, true
	}
	idx := m.projectIndex(row.agent.ProjectID)
	if idx < 0 {
		return httpclient.Project{}, false
	}
	return m.projects[idx], true
}

func (m Model) selectedSessionRow() (httpclient.Session, bool) {
	row, ok := m.selectedRow()
	if !ok || row.kind != rowSession {
		return httpclient.Session{}, false
	}
	return row.session, true
}

func (m Model) selectedAgentRow() (httpclient.Agent, bool) {
	row, ok := m.selectedRow()
	if !ok || row.kind != rowAgent {
		return httpclient.Agent{}, false
	}
	return row.agent, true
}

func (m *Model) requestDeleteConfirmation() {
	m.confirm = false
	m.confirmDelete = deleteNothing
	m.confirmDeleteID = 0
	m.status = ""

	if agent, ok := m.selectedAgentRow(); ok {
		m.confirm = true
		m.confirmDelete = deleteAgent
		m.confirmDeleteID = agent.ID
		return
	}

	if m.showSessions {
		s, ok := m.selectedSessionRow()
		if !ok {
			return
		}
		if s.Type == "secondary" {
			m.confirm = true
			m.confirmDelete = deleteSecondarySession
			m.confirmDeleteID = s.ID
			return
		}
		if s.Type != "worktree" {
			m.status = "only worktree sessions can be destroyed"
			return
		}
		m.confirm = true
		m.confirmDelete = deleteWorktreeSession
		m.confirmDeleteID = s.ID
		return
	}

	project, ok := m.selectedProject()
	if !ok {
		return
	}
	m.confirm = true
	m.confirmDelete = deleteProject
	m.confirmDeleteID = project.ID
}

func (m Model) attachTarget() (AttachTarget, bool) {
	row, ok := m.selectedRow()
	if !ok {
		return AttachTarget{}, false
	}
	switch row.kind {
	case rowAgent:
		sessionName := tmuxSessionName(row.session)
		if sessionName == "" {
			if s, ok := m.sessionByID(row.agent.SessionID); ok {
				sessionName = tmuxSessionName(s)
			}
		}
		if sessionName == "" {
			return AttachTarget{}, false
		}
		return AttachTarget{SessionName: sessionName, PaneID: row.agent.TmuxPaneID}, true
	case rowSession:
		return AttachTarget{SessionName: tmuxSessionName(row.session)}, true
	case rowProject:
		return AttachTarget{SessionName: row.project.MainTmuxSessionName}, true
	}
	return AttachTarget{}, false
}

func (m Model) mainSessionRowIndexForProject(projectID int) int {
	rows := m.sessionViewRows()
	for i, row := range rows {
		if row.kind == rowSession && row.project.ID == projectID && isMainSession(row.session, row.project) {
			return i
		}
	}
	for i, row := range rows {
		if row.kind == rowSession && row.project.ID == projectID {
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

func (m Model) projectRowIndex(id int) int {
	for i, row := range m.projectViewRows() {
		if row.kind == rowProject && row.project.ID == id {
			return i
		}
	}
	return 0
}

func (m Model) selectedRow() (viewRow, bool) {
	if m.showSessions {
		rows := m.sessionViewRows()
		if m.selectedSession < 0 || m.selectedSession >= len(rows) {
			return viewRow{}, false
		}
		return rows[m.selectedSession], true
	}
	rows := m.projectViewRows()
	if m.selected < 0 || m.selected >= len(rows) {
		return viewRow{}, false
	}
	return rows[m.selected], true
}

func (m *Model) selectEquivalentRow(row viewRow) {
	if m.showSessions {
		sessionID := row.session.ID
		if row.kind == rowAgent {
			sessionID = row.agent.SessionID
		}
		if sessionID != 0 {
			for i, candidate := range m.sessionViewRows() {
				if candidate.kind == rowSession && candidate.session.ID == sessionID {
					m.selectedSession = i
					return
				}
			}
		}
		if row.project.ID != 0 {
			m.selectedSession = m.mainSessionRowIndexForProject(row.project.ID)
		}
		return
	}

	projectID := row.project.ID
	if row.kind == rowAgent {
		projectID = row.agent.ProjectID
	}
	if projectID != 0 {
		m.selected = m.projectRowIndex(projectID)
	}
}

func (m Model) agentsForProject(projectID int) []httpclient.Agent {
	agents := make([]httpclient.Agent, 0)
	for _, a := range m.agents {
		if a.ProjectID == projectID {
			agents = append(agents, a)
		}
	}
	return agents
}

func (m Model) agentsForSession(sessionID int) []httpclient.Agent {
	agents := make([]httpclient.Agent, 0)
	for _, a := range m.agents {
		if a.SessionID == sessionID {
			agents = append(agents, a)
		}
	}
	return agents
}

func (m Model) sessionByID(id int) (httpclient.Session, bool) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, true
		}
	}
	return httpclient.Session{}, false
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

func tmuxSessionName(s httpclient.Session) string {
	if s.TmuxName != "" {
		return s.TmuxName
	}
	return sessionName(s)
}

func agentLabel(a httpclient.Agent) string {
	name := a.DisplayName
	if name == "" {
		name = fmt.Sprintf("agent-%d-%s", a.ID, a.Kind)
	}
	if a.Status != "" {
		return fmt.Sprintf("%s [%s]", name, a.Status)
	}
	return name
}

func parentID(s httpclient.Session) int {
	if s.ParentSessionID != 0 {
		return s.ParentSessionID
	}
	if s.Parent > 0 {
		return s.Parent
	}
	return 0
}
