package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

// pollInterval is how often the TUI refreshes its data from the daemon so that
// agent status changes appear without a manual refresh.
const pollInterval = time.Second

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

// Tabs are full-screen views switched by the number keys 0-3.
const (
	tabOverview = iota
	tabProjects
	tabSessions
	tabAgents
	tabCount
)

type viewRow struct {
	kind    rowKind
	project httpclient.Project
	session httpclient.Session
	agent   httpclient.Agent
}

// selection tracks the chosen entity by ID within a tab's ordered rows. idx is
// the last resolved position, kept only to clamp to a neighbour when the entity
// disappears (delete/refresh).
type selection struct {
	id  int
	idx int
}

type AttachTarget struct {
	SessionName string
	PaneID      string
}

type Model struct {
	ctx      context.Context
	api      API
	projects []httpclient.Project
	sessions []httpclient.Session
	agents   []httpclient.Agent

	tab          int
	projectSel   selection
	sessionSel   selection
	agentSel     selection
	overviewKind rowKind

	status          string
	loading         bool
	confirm         bool
	confirmDelete   deleteTarget
	confirmDeleteID int
	help            bool

	creatingWorktree  bool
	worktreeBranch    string
	worktreeProjectID int

	creatingSecondary bool
	secondaryStep     secondaryPromptStep
	secondaryParentID int
	secondaryRelwd    string
	secondaryName     string

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

type tickMsg struct{}

type createSessionMsg struct {
	session httpclient.Session
	err     error
}

var (
	errorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	mainStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	worktreeStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
	tabActiveStyle   = lipgloss.NewStyle().Reverse(true).Bold(true)
	tabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	secondaryStyle   = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("39")), // depth 1
		lipgloss.NewStyle().Foreground(lipgloss.Color("33")), // depth 2
		lipgloss.NewStyle().Foreground(lipgloss.Color("27")), // depth 3
		lipgloss.NewStyle().Foreground(lipgloss.Color("21")), // depth 4
		lipgloss.NewStyle().Foreground(lipgloss.Color("19")), // depth 5+
	}
)

const noProjectsMsg = "No projects yet. Run `tmux-coder open` or `tmux-coder o` in a directory to create and attach it."

const helpText = "Keys: j/k or ctrl+n/ctrl+p or arrows move, g/G jump, 0-3 switch tab, enter attach, X delete, w worktree (Projects), S secondary (Sessions), r refresh, ? help, q quit"

var keys = struct {
	up, down, top, bottom, enter, del, refresh, worktree, secondary, help, quit, tab key.Binding
}{
	up:        key.NewBinding(key.WithKeys("up", "k", "ctrl+p")),
	down:      key.NewBinding(key.WithKeys("down", "j", "ctrl+n")),
	top:       key.NewBinding(key.WithKeys("g")),
	bottom:    key.NewBinding(key.WithKeys("G")),
	enter:     key.NewBinding(key.WithKeys("enter")),
	del:       key.NewBinding(key.WithKeys("X")),
	refresh:   key.NewBinding(key.WithKeys("r")),
	worktree:  key.NewBinding(key.WithKeys("w")),
	secondary: key.NewBinding(key.WithKeys("S")),
	help:      key.NewBinding(key.WithKeys("?")),
	quit:      key.NewBinding(key.WithKeys("q", "esc", "ctrl+c")),
	tab:       key.NewBinding(key.WithKeys("0", "1", "2", "3")),
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
	m := Model{ctx: ctx, api: api, loading: true}
	if len(initialSession) > 0 {
		m.initialSession = initialSession[0]
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.listCmd(), m.tickCmd())
}

func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// modalActive reports whether a confirm or text-entry prompt is open. Background
// refreshes must leave that interaction state untouched.
func (m Model) modalActive() bool {
	return m.confirm || m.creatingWorktree || m.creatingSecondary
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case listMsg:
		m.loading = false
		if msg.err != nil {
			if !m.modalActive() {
				m.status = msg.err.Error()
			}
			return m, nil
		}
		m.projects = msg.projects
		m.sessions = msg.sessions
		m.agents = msg.agents
		// A refresh may arrive from background polling while a confirm or
		// text-entry prompt is open; it must not clear that interaction state.
		if !m.modalActive() {
			m.status = ""
			m.confirm = false
			m.confirmDelete = deleteNothing
			m.confirmDeleteID = 0
		}
		m.selectInitialSession()
		m.selectPendingSession()
		m.normalizeSelection()
	case tickMsg:
		return m, tea.Batch(m.listCmd(), m.tickCmd())
	case deleteMsg:
		m.loading = false
		m.confirm = false
		m.confirmDelete = deleteNothing
		m.confirmDeleteID = 0
		if msg.err != nil {
			m.status = msg.err.Error()
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
		m.tab = tabSessions
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
		case key.Matches(msg, keys.tab):
			s := msg.String()
			if len(s) == 1 && s[0] >= '0' && s[0] <= '3' {
				m.switchTab(int(s[0] - '0'))
			}
		case key.Matches(msg, keys.help):
			m.help = !m.help
		case key.Matches(msg, keys.refresh):
			m.loading = true
			return m, m.listCmd()
		case key.Matches(msg, keys.worktree):
			m.startWorktree()
		case key.Matches(msg, keys.secondary):
			m.startSecondary()
		case key.Matches(msg, keys.up):
			m.move(-1)
		case key.Matches(msg, keys.down):
			m.move(1)
		case key.Matches(msg, keys.top):
			m.moveTo(0)
		case key.Matches(msg, keys.bottom):
			m.moveToEnd()
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
	b.WriteString(m.tabStrip())
	b.WriteString("\n")

	if m.loading {
		b.WriteString("Loading...\n")
	} else {
		switch m.tab {
		case tabProjects:
			m.writeProjectsView(&b)
		case tabSessions:
			m.writeSessionsView(&b, false)
		case tabAgents:
			m.writeAgentsView(&b)
		default:
			m.writeSessionsView(&b, true)
		}
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
		b.WriteString("\n" + helpText + "\n")
	} else {
		b.WriteString("\n" + mutedStyle.Render(m.footer()) + "\n")
	}
	return b.String()
}

func (m Model) tabStrip() string {
	labels := [tabCount]string{"0 Overview", "1 Projects", "2 Sessions", "3 Agents"}
	parts := make([]string, tabCount)
	for i, l := range labels {
		if i == m.tab {
			parts[i] = tabActiveStyle.Render(l)
		} else {
			parts[i] = tabInactiveStyle.Render(l)
		}
	}
	strip := strings.Join(parts, " · ")
	rule := mutedStyle.Render(strings.Repeat("─", lipgloss.Width(strip)))
	return strip + "\n" + rule
}

func (m Model) footer() string {
	parts := []string{"j/k move", "enter attach"}
	switch m.tab {
	case tabProjects:
		parts = append(parts, "w worktree", "X delete")
	case tabSessions:
		parts = append(parts, "S secondary", "X delete")
	default:
		parts = append(parts, "X delete")
	}
	parts = append(parts, "0-3 tabs", "r refresh", "? help", "q quit")
	return strings.Join(parts, "  ")
}

func (m Model) writeProjectsView(b *strings.Builder) {
	if len(m.projects) == 0 {
		b.WriteString(noProjectsMsg + "\n")
		return
	}
	cur, has := m.cursor()
	for _, p := range m.projects {
		header := fmt.Sprintf("%s (%s · %s)", p.Title, pluralize(m.sessionCount(p.ID), "session"), pluralize(m.agentCount(p.ID), "agent"))
		path := "- path: " + p.FullPath
		if has && cur.kind == rowProject && cur.project.ID == p.ID {
			b.WriteString(selectStyle.Render("> "+header) + "\n")
		} else {
			b.WriteString("  " + header + "\n")
		}
		b.WriteString("  " + mutedStyle.Render(path) + "\n")
	}
}

func (m Model) writeSessionsView(b *strings.Builder, withAgents bool) {
	if len(m.projects) == 0 {
		b.WriteString(noProjectsMsg + "\n")
		return
	}
	cur, has := m.cursor()
	for _, p := range m.projects {
		b.WriteString("  " + projectHeaderLine(p) + "\n")
		for _, s := range m.sessionRows() {
			if s.ProjectID != p.ID {
				continue
			}
			selected := has && cur.kind == rowSession && cur.session.ID == s.ID
			m.writeSessionLine(b, s, selected)
			if !withAgents {
				continue
			}
			for _, a := range m.agentsForSession(s.ID) {
				selAgent := has && cur.kind == rowAgent && cur.agent.ID == a.ID
				m.writeAgentUnderSession(b, s, a, selAgent)
			}
		}
	}
}

func (m Model) writeAgentsView(b *strings.Builder) {
	if len(m.agents) == 0 {
		b.WriteString("No active agents.\n")
		return
	}
	cur, has := m.cursor()
	for _, a := range m.agents {
		line := m.agentRowLabel(a)
		if has && cur.kind == rowAgent && cur.agent.ID == a.ID {
			b.WriteString(selectStyle.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
}

func (m Model) writeSessionLine(b *strings.Builder, s httpclient.Session, selected bool) {
	prefix := strings.Repeat("  ", m.sessionDepth(s))
	content := "- " + sessionName(s)
	if s.Branch != "" {
		content += " (" + s.Branch + ")"
	}
	content = styleSession(s, m.sessionDepth(s), content)
	var line string
	if selected {
		line = selectStyle.Render("> " + prefix + content)
	} else {
		line = "  " + prefix + content
	}
	b.WriteString("  " + line + "\n")
}

func (m Model) writeAgentUnderSession(b *strings.Builder, s httpclient.Session, a httpclient.Agent, selected bool) {
	line := strings.Repeat("  ", m.sessionDepth(s)+1) + "- " + agentLabel(a)
	if selected {
		line = selectStyle.Render("> " + line)
	} else {
		line = "  " + line
	}
	b.WriteString("  " + line + "\n")
}

func projectHeaderLine(p httpclient.Project) string {
	return fmt.Sprintf("%s  %s  %s", p.Title, mutedStyle.Render(p.FullPath), mutedStyle.Render(p.MainSessionName))
}

func styleSession(s httpclient.Session, depth int, content string) string {
	switch s.Type {
	case "main":
		return mainStyle.Render(content)
	case "worktree":
		return worktreeStyle.Render(content)
	case "secondary":
		d := depth
		if d >= len(secondaryStyle) {
			d = len(secondaryStyle) - 1
		}
		return secondaryStyle[d].Render(content)
	}
	return content
}

func (m Model) agentRowLabel(a httpclient.Agent) string {
	name := a.DisplayName
	if name == "" {
		name = fmt.Sprintf("agent-%d", a.ID)
	}
	label := a.Kind + " · " + name
	if a.Status != "" {
		label += " " + agentStatusStyle(a.Status).Render("["+a.Status+"]")
	}
	return label + "  " + mutedStyle.Render(m.agentContext(a))
}

func (m Model) agentContext(a httpclient.Agent) string {
	project := m.projectByID(a.ProjectID).Title
	if project == "" {
		project = a.Project.Title
	}
	session := ""
	if s, ok := m.sessionByID(a.SessionID); ok {
		session = sessionName(s)
	} else {
		session = sessionName(a.Session)
	}
	return project + " / " + session
}

func pluralize(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
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

// rows returns the selectable rows for a tab in display order. Project header
// lines in the Overview and Sessions views are not selectable, so they are not
// included here.
func (m Model) rows(tab int) []viewRow {
	switch tab {
	case tabProjects:
		rows := make([]viewRow, 0, len(m.projects))
		for _, p := range m.projects {
			rows = append(rows, viewRow{kind: rowProject, project: p})
		}
		return rows
	case tabAgents:
		rows := make([]viewRow, 0, len(m.agents))
		for _, a := range m.agents {
			session, _ := m.sessionByID(a.SessionID)
			rows = append(rows, viewRow{kind: rowAgent, project: m.projectByID(a.ProjectID), session: session, agent: a})
		}
		return rows
	case tabSessions:
		rows := make([]viewRow, 0, len(m.sessions))
		for _, p := range m.projects {
			for _, s := range m.sessionRows() {
				if s.ProjectID != p.ID {
					continue
				}
				rows = append(rows, viewRow{kind: rowSession, project: p, session: s})
			}
		}
		return rows
	default: // tabOverview
		rows := make([]viewRow, 0, len(m.sessions)+len(m.agents))
		for _, p := range m.projects {
			for _, s := range m.sessionRows() {
				if s.ProjectID != p.ID {
					continue
				}
				rows = append(rows, viewRow{kind: rowSession, project: p, session: s})
				for _, a := range m.agentsForSession(s.ID) {
					rows = append(rows, viewRow{kind: rowAgent, project: p, session: s, agent: a})
				}
			}
		}
		return rows
	}
}

// activeSelection reports which entity kind the active tab's cursor tracks and
// the stored selection for it. Overview shares the session selection with the
// Sessions view, and the agent selection with the Agents view.
func (m Model) activeSelection() (rowKind, selection) {
	switch m.tab {
	case tabProjects:
		return rowProject, m.projectSel
	case tabAgents:
		return rowAgent, m.agentSel
	case tabSessions:
		return rowSession, m.sessionSel
	default:
		if m.overviewKind == rowAgent {
			return rowAgent, m.agentSel
		}
		return rowSession, m.sessionSel
	}
}

func (m Model) currentIndex(rows []viewRow) int {
	if len(rows) == 0 {
		return -1
	}
	kind, sel := m.activeSelection()
	if sel.id != 0 {
		for i, r := range rows {
			if r.kind == kind && rowID(r) == sel.id {
				return i
			}
		}
	}
	idx := sel.idx
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	return idx
}

func (m *Model) selectIndex(rows []viewRow, i int) {
	if len(rows) == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(rows) {
		i = len(rows) - 1
	}
	r := rows[i]
	switch m.tab {
	case tabProjects:
		m.projectSel = selection{id: r.project.ID, idx: i}
	case tabAgents:
		m.agentSel = selection{id: r.agent.ID, idx: i}
	case tabSessions:
		m.sessionSel = selection{id: r.session.ID, idx: i}
	default:
		m.overviewKind = r.kind
		if r.kind == rowAgent {
			m.agentSel = selection{id: r.agent.ID, idx: i}
		} else {
			m.sessionSel = selection{id: r.session.ID, idx: i}
		}
	}
}

func (m *Model) move(delta int) {
	rows := m.rows(m.tab)
	if len(rows) == 0 {
		return
	}
	m.selectIndex(rows, m.currentIndex(rows)+delta)
}

func (m *Model) moveTo(i int) {
	rows := m.rows(m.tab)
	if len(rows) == 0 {
		return
	}
	m.selectIndex(rows, i)
}

func (m *Model) moveToEnd() {
	rows := m.rows(m.tab)
	if len(rows) == 0 {
		return
	}
	m.selectIndex(rows, len(rows)-1)
}

// normalizeSelection re-resolves the active tab's selection against current
// data, clamping to the nearest neighbour when the chosen entity is gone.
func (m *Model) normalizeSelection() {
	rows := m.rows(m.tab)
	if len(rows) == 0 {
		return
	}
	m.selectIndex(rows, m.currentIndex(rows))
}

func (m *Model) switchTab(tab int) {
	if tab == m.tab || tab < 0 || tab >= tabCount {
		return
	}
	m.tab = tab
	m.status = ""
	m.normalizeSelection()
}

func (m Model) cursor() (viewRow, bool) {
	rows := m.rows(m.tab)
	i := m.currentIndex(rows)
	if i < 0 || i >= len(rows) {
		return viewRow{}, false
	}
	return rows[i], true
}

func (m *Model) selectPendingSession() {
	if m.pendingSelectSession == "" {
		return
	}
	defer func() { m.pendingSelectSession = "" }()
	for _, s := range m.sessions {
		if sessionName(s) == m.pendingSelectSession {
			m.sessionSel.id = s.ID
			m.overviewKind = rowSession
			return
		}
	}
}

func (m *Model) selectInitialSession() {
	if m.initialSession == "" {
		return
	}
	defer func() { m.initialSession = "" }()
	for _, s := range m.sessions {
		if sessionName(s) == m.initialSession || tmuxSessionName(s) == m.initialSession {
			m.sessionSel.id = s.ID
			m.overviewKind = rowSession
			return
		}
	}
}

func (m *Model) startWorktree() {
	if m.tab != tabProjects {
		return
	}
	row, ok := m.cursor()
	if !ok || row.kind != rowProject {
		m.status = "no project selected"
		return
	}
	m.creatingWorktree = true
	m.worktreeBranch = ""
	m.worktreeProjectID = row.project.ID
	m.status = ""
}

func (m *Model) startSecondary() {
	if m.tab != tabSessions {
		return
	}
	row, ok := m.cursor()
	if !ok || row.kind != rowSession {
		m.status = "no session selected"
		return
	}
	m.creatingSecondary = true
	m.secondaryStep = secondaryPromptRelativeWorkingDirectory
	m.secondaryParentID = row.session.ID
	m.secondaryRelwd = row.session.RelativeWorkingDirectory
	m.secondaryName = ""
	m.status = ""
}

func (m *Model) requestDeleteConfirmation() {
	m.confirm = false
	m.confirmDelete = deleteNothing
	m.confirmDeleteID = 0
	m.status = ""

	row, ok := m.cursor()
	if !ok {
		return
	}
	switch row.kind {
	case rowAgent:
		m.confirm = true
		m.confirmDelete = deleteAgent
		m.confirmDeleteID = row.agent.ID
	case rowSession:
		m.requestSessionDelete(row.session)
	case rowProject:
		m.confirm = true
		m.confirmDelete = deleteProject
		m.confirmDeleteID = row.project.ID
	}
}

func (m *Model) requestSessionDelete(s httpclient.Session) {
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
}

func (m Model) attachTarget() (AttachTarget, bool) {
	row, ok := m.cursor()
	if !ok {
		return AttachTarget{}, false
	}
	switch row.kind {
	case rowAgent:
		name := tmuxSessionName(row.session)
		if name == "" {
			if s, ok := m.sessionByID(row.agent.SessionID); ok {
				name = tmuxSessionName(s)
			}
		}
		if name == "" {
			return AttachTarget{}, false
		}
		return AttachTarget{SessionName: name, PaneID: row.agent.TmuxPaneID}, true
	case rowSession:
		return AttachTarget{SessionName: tmuxSessionName(row.session)}, true
	case rowProject:
		return AttachTarget{SessionName: row.project.MainTmuxSessionName}, true
	}
	return AttachTarget{}, false
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

func (m Model) sessionCount(projectID int) int {
	n := 0
	for _, s := range m.sessions {
		if s.ProjectID == projectID {
			n++
		}
	}
	return n
}

func (m Model) agentCount(projectID int) int {
	n := 0
	for _, a := range m.agents {
		if a.ProjectID == projectID {
			n++
		}
	}
	return n
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

func (m Model) projectByID(id int) httpclient.Project {
	for _, p := range m.projects {
		if p.ID == id {
			return p
		}
	}
	return httpclient.Project{}
}

func (m Model) sessionByID(id int) (httpclient.Session, bool) {
	for _, s := range m.sessions {
		if s.ID == id {
			return s, true
		}
	}
	return httpclient.Session{}, false
}

func rowID(r viewRow) int {
	switch r.kind {
	case rowProject:
		return r.project.ID
	case rowSession:
		return r.session.ID
	case rowAgent:
		return r.agent.ID
	}
	return 0
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
	if a.Status == "" {
		return name
	}
	return agentStatusStyle(a.Status).Render(fmt.Sprintf("%s [%s]", name, a.Status))
}

// agentStatusStyle colors an agent label by status. Activity that wants the
// user's attention stands out; background activity is dimmed; lifecycle states
// stay neutral. Colors are intentionally provisional.
func agentStatusStyle(status string) lipgloss.Style {
	switch status {
	case "waiting":
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	case "idle":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	case "busy":
		return lipgloss.NewStyle().Faint(true)
	default:
		return lipgloss.NewStyle()
	}
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
