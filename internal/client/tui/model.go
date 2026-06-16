package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/sahilm/fuzzy"
)

// pollInterval is how often the TUI refreshes its data from the daemon so that
// agent status changes appear without a manual refresh.
const pollInterval = time.Second

type API interface {
	ListProjects(context.Context) ([]httpclient.Project, error)
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
	ListAgents(context.Context, httpclient.ListAgentsInput) ([]httpclient.Agent, error)
	CreateSession(context.Context, httpclient.CreateSessionInput) (httpclient.Session, error)
	CreateAgent(context.Context, httpclient.CreateAgentInput) (httpclient.Agent, error)
	RenameAgent(context.Context, int, string) (httpclient.Agent, error)
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

type worktreeBasePromptStep uint8

const (
	worktreeBaseStepNone worktreeBasePromptStep = iota
	worktreeBaseStepBranch
	worktreeBaseStepBaseRef
)

type agentPromptStep uint8

const (
	agentPromptNone agentPromptStep = iota
	agentPromptExecutable
	agentPromptName
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

	// groupAgents buckets the Agents view by project when on. Ephemeral view
	// state — not persisted and never sent to the daemon.
	groupAgents bool

	// filtering drives the fuzzy finder ('f'). While on, the active view
	// collapses to a flat, score-ranked list of its rows matched against
	// filterQuery, and key input is routed to updateFilter instead of the
	// normal bindings. Cleared on esc.
	filtering   bool
	filterQuery string

	status          string
	loading         bool
	confirm         bool
	confirmDelete   deleteTarget
	confirmDeleteID int
	help            bool

	creatingWorktree  bool
	worktreeBranch    string
	worktreeProjectID int
	worktreeParentID  int
	// worktreeConflict holds the Daemon's conflict code (branch_exists or
	// worktree_exists) while the user is being asked y/n whether to re-issue the
	// create in the resolving mode. Empty when no such prompt is pending. The
	// re-issue reuses worktreeProjectID/worktreeParentID/worktreeBranch, which
	// both the 'w' and 'W' flows populate before firing their create.
	worktreeConflict string

	creatingWorktreeFromBase  bool
	worktreeFromBaseStep      worktreeBasePromptStep
	worktreeFromBaseProjectID int
	worktreeFromBaseBranch    string
	worktreeFromBaseRef       string

	creatingSecondary bool
	secondaryStep     secondaryPromptStep
	secondaryParentID int
	secondaryRelwd    string
	secondaryName     string

	// creatingAgent drives the 'a' flow: a two-step prompt (executable, then an
	// optional name) that spawns an agent in the resolved target session. The
	// daemon owns the new pane, so this is wholly client-side.
	creatingAgent   bool
	agentStep       agentPromptStep
	agentProjectID  int
	agentSessionID  int
	agentExecutable string
	agentName       string

	renamingAgent bool
	renameAgentID int
	renameValue   string

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

type createAgentMsg struct {
	agent httpclient.Agent
	err   error
}

type renameAgentMsg struct {
	agent httpclient.Agent
	err   error
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
	defaultStyle      = lipgloss.NewStyle()
	filterPromptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
)

const noProjectsMsg = "No projects yet. Run `tmux-coder open` or `tmux-coder o` in a directory to create and attach it."

const defaultAgentExecutable = "opencode"

const helpText = "Keys: j/k or ctrl+n/ctrl+p or arrows move, g/G jump, 0-3 switch tab, enter attach, a agent, u rename (Agents), X delete, w worktree (off session), W base worktree (off ref), S secondary (Sessions), s group (Agents), f filter, r refresh, ? help, q quit"

var keys = struct {
	up, down, top, bottom, enter, del, refresh, worktree, worktreeBase, secondary, agent, rename, group, filter, help, quit, tab key.Binding
}{
	up:           key.NewBinding(key.WithKeys("up", "k", "ctrl+p")),
	down:         key.NewBinding(key.WithKeys("down", "j", "ctrl+n")),
	top:          key.NewBinding(key.WithKeys("g")),
	bottom:       key.NewBinding(key.WithKeys("G")),
	enter:        key.NewBinding(key.WithKeys("enter")),
	del:          key.NewBinding(key.WithKeys("X")),
	refresh:      key.NewBinding(key.WithKeys("r")),
	worktree:     key.NewBinding(key.WithKeys("w")),
	worktreeBase: key.NewBinding(key.WithKeys("W")),
	secondary:    key.NewBinding(key.WithKeys("S")),
	agent:        key.NewBinding(key.WithKeys("a")),
	rename:       key.NewBinding(key.WithKeys("u")),
	group:        key.NewBinding(key.WithKeys("s")),
	filter:       key.NewBinding(key.WithKeys("f")),
	help:         key.NewBinding(key.WithKeys("?")),
	quit:         key.NewBinding(key.WithKeys("q", "esc", "ctrl+c")),
	tab:          key.NewBinding(key.WithKeys("0", "1", "2", "3")),
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
	return m.confirm || m.creatingWorktree || m.creatingWorktreeFromBase || m.creatingSecondary || m.creatingAgent || m.renamingAgent || m.worktreeConflict != ""
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
			// A worktree/branch-exists conflict offers a follow-up create; keep
			// the pending branch + project (not yet cleared) and prompt y/n.
			var apiErr *httpclient.APIError
			if errors.As(msg.err, &apiErr) && (apiErr.Code == httpclient.CodeBranchExists || apiErr.Code == httpclient.CodeWorktreeExists) {
				m.creatingWorktree = false
				m.creatingWorktreeFromBase = false
				m.worktreeConflict = apiErr.Code
				m.status = ""
				return m, nil
			}
			m.status = msg.err.Error()
			return m, nil
		}
		m.creatingWorktree = false
		m.worktreeConflict = ""
		m.worktreeBranch = ""
		m.worktreeProjectID = 0
		m.worktreeParentID = 0
		m.creatingWorktreeFromBase = false
		m.worktreeFromBaseStep = worktreeBaseStepNone
		m.worktreeFromBaseProjectID = 0
		m.worktreeFromBaseBranch = ""
		m.worktreeFromBaseRef = ""
		m.creatingSecondary = false
		m.secondaryStep = secondaryPromptNone
		m.secondaryParentID = 0
		m.secondaryRelwd = ""
		m.secondaryName = ""
		m.pendingSelectSession = sessionName(msg.session)
		m.tab = tabSessions
		m.loading = true
		return m, m.listCmd()
	case createAgentMsg:
		m.loading = false
		if msg.err != nil {
			// Keep the prompt open so the user can correct and resubmit.
			m.status = msg.err.Error()
			return m, nil
		}
		m.resetAgentPrompt()
		m.agentSel = selection{id: msg.agent.ID}
		m.tab = tabAgents
		m.loading = true
		return m, m.listCmd()
	case renameAgentMsg:
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.resetRenamePrompt()
		m.agentSel = selection{id: msg.agent.ID}
		m.loading = true
		return m, m.listCmd()
	case tea.KeyMsg:
		if m.worktreeConflict != "" {
			return m.updateWorktreeConflict(msg)
		}
		if m.creatingSecondary {
			return m.updateSecondaryPrompt(msg)
		}
		if m.creatingWorktree {
			return m.updateWorktreePrompt(msg)
		}
		if m.creatingWorktreeFromBase {
			return m.updateWorktreeFromBasePrompt(msg)
		}
		if m.creatingAgent {
			return m.updateAgentPrompt(msg)
		}
		if m.renamingAgent {
			return m.updateRenamePrompt(msg)
		}
		if m.filtering {
			return m.updateFilter(msg)
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
		case key.Matches(msg, keys.worktreeBase):
			m.startWorktreeFromBase()
		case key.Matches(msg, keys.secondary):
			m.startSecondary()
		case key.Matches(msg, keys.agent):
			m.startAgent()
		case key.Matches(msg, keys.rename):
			m.startRename()
		case key.Matches(msg, keys.group):
			// Grouping is a presentation toggle scoped to the Agents view.
			if m.tab == tabAgents {
				m.groupAgents = !m.groupAgents
				m.normalizeSelection()
			}
		case key.Matches(msg, keys.filter):
			m.startFilter()
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
	} else if m.filtering {
		m.writeFilterView(&b)
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
	if m.creatingWorktreeFromBase {
		if m.worktreeFromBaseStep == worktreeBaseStepBaseRef {
			b.WriteString("\nBase ref: " + m.worktreeFromBaseRef + "\n")
		} else {
			b.WriteString("\nNew worktree branch: " + m.worktreeFromBaseBranch + "\n")
		}
	}
	if m.creatingAgent {
		if m.agentStep == agentPromptName {
			b.WriteString("\nAgent name (optional): " + m.agentName + "\n")
		} else {
			b.WriteString("\nAgent executable (default " + defaultAgentExecutable + "): " + m.agentExecutable + "\n")
		}
	}
	if m.renamingAgent {
		b.WriteString("\nAgent name: " + m.renameValue + "\n")
	}
	switch m.worktreeConflict {
	case httpclient.CodeBranchExists:
		b.WriteString("\nbranch already exists. Create a worktree for it? y/n\n")
	case httpclient.CodeWorktreeExists:
		b.WriteString("\nworktree already exists. Create a session? y/n\n")
	}
	if m.status != "" {
		b.WriteString("\n" + errorStyle.Render(m.status) + "\n")
	}
	if m.creatingSecondary {
		b.WriteString("\n" + mutedStyle.Render("enter next/create  esc cancel") + "\n")
	} else if m.creatingWorktree {
		b.WriteString("\n" + mutedStyle.Render("enter create  esc cancel") + "\n")
	} else if m.creatingWorktreeFromBase {
		b.WriteString("\n" + mutedStyle.Render("enter next/create  esc cancel") + "\n")
	} else if m.creatingAgent {
		b.WriteString("\n" + mutedStyle.Render("enter next/create  esc cancel") + "\n")
	} else if m.renamingAgent {
		b.WriteString("\n" + mutedStyle.Render("enter rename  esc cancel") + "\n")
	} else if m.worktreeConflict != "" {
		b.WriteString("\n" + mutedStyle.Render("y confirm  n cancel") + "\n")
	} else if m.filtering {
		b.WriteString("\n" + mutedStyle.Render("ctrl+n/ctrl+p move  enter open  esc cancel") + "\n")
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
	parts := []string{"j/k move", "enter attach", "a agent"}
	switch m.tab {
	case tabOverview, tabProjects:
		parts = append(parts, "w worktree", "W base worktree", "X delete")
	case tabSessions:
		parts = append(parts, "w worktree", "W base worktree", "S secondary", "X delete")
	default:
		parts = append(parts, "u rename", "X delete")
	}
	parts = append(parts, "f filter", "0-3 tabs", "r refresh", "? help", "q quit")
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
	lastProject := -1
	for _, row := range m.agentRows() {
		// In grouped mode each project gets a non-selectable header, emitted
		// when the project changes — exactly like the Sessions tab.
		if m.groupAgents && row.project.ID != lastProject {
			b.WriteString("  " + projectHeaderLine(row.project) + "\n")
			lastProject = row.project.ID
		}
		a := row.agent
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
	return sessionStyle(s, depth).Render(content)
}

// sessionStyle is the color a session row is drawn in: Main red, worktree
// orange, secondaries shaded by depth. Shared by the tree views and the fuzzy
// finder so a session keeps its color when filtered.
func sessionStyle(s httpclient.Session, depth int) lipgloss.Style {
	switch s.Type {
	case "main":
		return mainStyle
	case "worktree":
		return worktreeStyle
	case "secondary":
		d := depth
		if d >= len(secondaryStyle) {
			d = len(secondaryStyle) - 1
		}
		return secondaryStyle[d]
	}
	return defaultStyle
}

// agentRows is the single source of order for the Agents view: agents are
// always status-sorted (waiting first), so the rows feeding navigation and the
// rows feeding rendering can never disagree. Both rows(tabAgents) and
// writeAgentsView consume it.
func (m Model) agentRows() []viewRow {
	// appendSorted status-sorts agents in place and maps them to rows.
	appendSorted := func(rows []viewRow, agents []httpclient.Agent) []viewRow {
		sortAgentsByStatus(agents)
		for _, a := range agents {
			session, _ := m.sessionByID(a.SessionID)
			rows = append(rows, viewRow{kind: rowAgent, project: m.projectByID(a.ProjectID), session: session, agent: a})
		}
		return rows
	}
	if m.groupAgents {
		rows := make([]viewRow, 0, len(m.agents))
		for _, p := range m.projects {
			bucket := make([]httpclient.Agent, 0)
			for _, a := range m.agents {
				if a.ProjectID == p.ID {
					bucket = append(bucket, a)
				}
			}
			rows = appendSorted(rows, bucket)
		}
		return rows
	}
	all := make([]httpclient.Agent, len(m.agents))
	copy(all, m.agents)
	return appendSorted(make([]viewRow, 0, len(all)), all)
}

// sortAgentsByStatus orders agents by Agent Status rank (waiting first), then by
// ascending id as a stable tiebreaker so the order is deterministic across the
// 1 s poll.
func sortAgentsByStatus(agents []httpclient.Agent) {
	sort.SliceStable(agents, func(i, j int) bool {
		ri, rj := agentStatusRank(agents[i].Status), agentStatusRank(agents[j].Status)
		if ri != rj {
			return ri < rj
		}
		return agents[i].ID < agents[j].ID
	})
}

// agentStatusRank ranks an Agent Status for the Agents view sort: statuses that
// want the user's attention sort to the top, lifecycle states to the bottom,
// anything unknown last.
func agentStatusRank(status string) int {
	switch status {
	case "waiting":
		return 0
	case "idle":
		return 1
	case "busy":
		return 2
	case "running":
		return 3
	case "starting":
		return 4
	default:
		return 5
	}
}

func (m Model) agentRowLabel(a httpclient.Agent) string {
	name := a.DisplayName
	if name == "" {
		name = fmt.Sprintf("agent-%d", a.ID)
	}
	icon := agentStatusStyle(a.Status).Render(agentStatusIcon(a.Status))
	return icon + " " + name + mutedStyle.Render(" · "+m.agentSession(a))
}

func (m Model) agentSession(a httpclient.Agent) string {
	if s, ok := m.sessionByID(a.SessionID); ok {
		return sessionName(s)
	}
	return sessionName(a.Session)
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

// createAgentCmd asks the daemon to spawn an agent in the target session. The
// daemon owns the new pane (TmuxPaneID left nil), running the executable via the
// agent-wrapper. An empty name is sent as no display name.
func (m Model) createAgentCmd(projectID, sessionID int, executable, name string) tea.Cmd {
	return func() tea.Msg {
		var displayName *string
		if name != "" {
			displayName = &name
		}
		agent, err := m.api.CreateAgent(m.ctx, httpclient.CreateAgentInput{
			ProjectID:   projectID,
			SessionID:   sessionID,
			Kind:        executable,
			DisplayName: displayName,
		})
		return createAgentMsg{agent: agent, err: err}
	}
}

func (m Model) renameAgentCmd(id int, name string) tea.Cmd {
	return func() tea.Msg {
		agent, err := m.api.RenameAgent(m.ctx, id, strings.TrimSpace(name))
		return renameAgentMsg{agent: agent, err: err}
	}
}

func (m Model) createWorktreeCmd(projectID, parentID int, branch string, createWorktree, createBranch bool) tea.Cmd {
	return func() tea.Msg {
		session, err := m.api.CreateSession(m.ctx, httpclient.CreateSessionInput{
			ProjectID:       projectID,
			Type:            "worktree",
			Branch:          branch,
			CreateWorktree:  createWorktree,
			CreateBranch:    createBranch,
			ParentSessionID: parentID,
		})
		return createSessionMsg{session: session, err: err}
	}
}

func (m Model) createWorktreeFromBaseCmd(projectID int, branch, baseRef string) tea.Cmd {
	return func() tea.Msg {
		session, err := m.api.CreateSession(m.ctx, httpclient.CreateSessionInput{
			ProjectID:      projectID,
			Type:           "worktree",
			Branch:         branch,
			CreateWorktree: true,
			CreateBranch:   true,
			BaseBranch:     baseRef,
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
		m.worktreeParentID = 0
		m.status = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.worktreeBranch) > 0 {
			m.worktreeBranch = m.worktreeBranch[:len(m.worktreeBranch)-1]
		}
		return m, nil
	case tea.KeyEnter:
		if m.loading {
			return m, nil
		}
		branch := strings.TrimSpace(m.worktreeBranch)
		if branch == "" {
			m.status = "branch is required"
			return m, nil
		}
		m.worktreeBranch = branch
		m.status = ""
		m.loading = true
		return m, m.createWorktreeCmd(m.worktreeProjectID, m.worktreeParentID, branch, true, true)
	case tea.KeyRunes:
		m.worktreeBranch += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// updateWorktreeConflict handles the y/n prompt raised after a create returned
// a worktree_exists or branch_exists conflict. On y it re-issues the create in
// the mode that resolves the conflict (ADR-0009): branch_exists adds a worktree
// for the existing branch (t,f); worktree_exists adopts the worktree (f,f). The
// re-issue keeps the source's Provenance parent (ADR-0010) via worktreeParentID,
// so an existing-branch or adopted worktree nests under the gesture's source.
func (m Model) updateWorktreeConflict(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch msg.String() {
	case "y":
		if m.loading {
			return m, nil
		}
		code := m.worktreeConflict
		m.worktreeConflict = ""
		m.status = ""
		m.loading = true
		// branch_exists → create a worktree for the existing branch (t,f);
		// worktree_exists → adopt the existing worktree (f,f).
		createWorktree := code == httpclient.CodeBranchExists
		return m, m.createWorktreeCmd(m.worktreeProjectID, m.worktreeParentID, m.worktreeBranch, createWorktree, false)
	case "n", "esc":
		m.worktreeConflict = ""
		m.worktreeBranch = ""
		m.worktreeProjectID = 0
		m.worktreeParentID = 0
		m.status = ""
		return m, nil
	}
	return m, nil
}

func (m Model) updateWorktreeFromBasePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.creatingWorktreeFromBase = false
		m.worktreeFromBaseStep = worktreeBaseStepNone
		m.worktreeFromBaseProjectID = 0
		m.worktreeFromBaseBranch = ""
		m.worktreeFromBaseRef = ""
		m.status = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if m.worktreeFromBaseStep == worktreeBaseStepBaseRef {
			if len(m.worktreeFromBaseRef) > 0 {
				m.worktreeFromBaseRef = m.worktreeFromBaseRef[:len(m.worktreeFromBaseRef)-1]
			}
		} else if len(m.worktreeFromBaseBranch) > 0 {
			m.worktreeFromBaseBranch = m.worktreeFromBaseBranch[:len(m.worktreeFromBaseBranch)-1]
		}
		return m, nil
	case tea.KeyEnter:
		if m.worktreeFromBaseStep == worktreeBaseStepBranch {
			branch := strings.TrimSpace(m.worktreeFromBaseBranch)
			if branch == "" {
				m.status = "branch is required"
				return m, nil
			}
			m.worktreeFromBaseBranch = branch
			m.worktreeFromBaseStep = worktreeBaseStepBaseRef
			m.status = ""
			return m, nil
		}
		if m.loading {
			return m, nil
		}
		baseRef := strings.TrimSpace(m.worktreeFromBaseRef)
		if baseRef == "" {
			m.status = "base ref is required"
			return m, nil
		}
		m.worktreeFromBaseRef = baseRef
		m.status = ""
		m.loading = true
		// Stash the create's identity in the shared worktree fields so a
		// branch_exists/worktree_exists conflict re-issues for the same
		// project/branch; a 'W' create is Project-level, so it has no parent.
		m.worktreeProjectID = m.worktreeFromBaseProjectID
		m.worktreeParentID = 0
		m.worktreeBranch = m.worktreeFromBaseBranch
		return m, m.createWorktreeFromBaseCmd(m.worktreeFromBaseProjectID, m.worktreeFromBaseBranch, baseRef)
	case tea.KeyRunes:
		if m.worktreeFromBaseStep == worktreeBaseStepBaseRef {
			m.worktreeFromBaseRef += string(msg.Runes)
		} else {
			m.worktreeFromBaseBranch += string(msg.Runes)
		}
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

// updateAgentPrompt handles key input for the 'a' flow: the first step captures
// the agent executable (required), the second an optional name. Enter advances
// from the executable to the name, then fires the create; an empty name means no
// name is sent.
func (m Model) updateAgentPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.resetAgentPrompt()
		m.status = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if m.agentStep == agentPromptName {
			if len(m.agentName) > 0 {
				m.agentName = m.agentName[:len(m.agentName)-1]
			}
		} else if len(m.agentExecutable) > 0 {
			m.agentExecutable = m.agentExecutable[:len(m.agentExecutable)-1]
		}
		return m, nil
	case tea.KeyEnter:
		if m.agentStep == agentPromptExecutable {
			executable := strings.TrimSpace(m.agentExecutable)
			if executable == "" {
				executable = defaultAgentExecutable
			}
			m.agentExecutable = executable
			m.agentStep = agentPromptName
			m.status = ""
			return m, nil
		}
		m.agentName = strings.TrimSpace(m.agentName)
		m.status = ""
		m.loading = true
		return m, m.createAgentCmd(m.agentProjectID, m.agentSessionID, m.agentExecutable, m.agentName)
	case tea.KeyRunes:
		if m.agentStep == agentPromptName {
			m.agentName += string(msg.Runes)
		} else {
			m.agentExecutable += string(msg.Runes)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateRenamePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.resetRenamePrompt()
		m.status = ""
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.renameValue) > 0 {
			m.renameValue = m.renameValue[:len(m.renameValue)-1]
		}
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.renameValue)
		if name == "" {
			m.status = "name is required"
			return m, nil
		}
		m.renameValue = name
		m.status = ""
		m.loading = true
		return m, m.renameAgentCmd(m.renameAgentID, name)
	case tea.KeyRunes:
		m.renameValue += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// rows returns the selectable rows that drive navigation and rendering for a
// tab. While the fuzzy finder is open on the active tab it returns the matched
// rows, score-ranked; otherwise it returns the full set in display order. Both
// the cursor (currentIndex/selectIndex) and the view consume this, so a filtered
// list can never disagree with what the user sees.
func (m Model) rows(tab int) []viewRow {
	base := m.baseRows(tab)
	if m.filtering && tab == m.tab {
		filtered, _ := m.fuzzyFilter(base, m.filterQuery)
		return filtered
	}
	return base
}

// baseRows returns the selectable rows for a tab in display order. Project
// header lines in the Overview and Sessions views are not selectable, so they
// are not included here.
func (m Model) baseRows(tab int) []viewRow {
	switch tab {
	case tabProjects:
		rows := make([]viewRow, 0, len(m.projects))
		for _, p := range m.projects {
			rows = append(rows, viewRow{kind: rowProject, project: p})
		}
		return rows
	case tabAgents:
		return m.agentRows()
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

// startFilter opens the fuzzy finder on the active tab. The view collapses to a
// flat ranked list (writeFilterView) and updateFilter takes over key input.
func (m *Model) startFilter() {
	m.filtering = true
	m.filterQuery = ""
	m.status = ""
}

// updateFilter handles key input while the fuzzy finder is open: typing edits
// the query (and re-resolves the cursor to the best match), ctrl+n/ctrl+p and
// the arrows move within the matches, enter attaches to the highlighted row,
// and esc closes the finder and restores the unfiltered view.
func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		m.filtering = false
		m.filterQuery = ""
		m.status = ""
		m.normalizeSelection()
		return m, nil
	case tea.KeyEnter:
		if target, ok := m.attachTarget(); ok {
			m.attach = target
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyUp, tea.KeyCtrlP:
		m.move(-1)
		return m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		m.move(1)
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if r := []rune(m.filterQuery); len(r) > 0 {
			m.filterQuery = string(r[:len(r)-1])
		}
		m.filterCursorToTop()
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.filterQuery += string(msg.Runes)
		m.filterCursorToTop()
		return m, nil
	}
	return m, nil
}

// filterCursorToTop rests the cursor on the top-ranked match after the query
// changes, so the highlighted row tracks the best match as the user types.
func (m *Model) filterCursorToTop() {
	rows := m.rows(m.tab)
	if len(rows) > 0 {
		m.selectIndex(rows, 0)
	}
}

// fuzzyFilter ranks base rows against query (best match first) and returns the
// surviving rows alongside the byte offsets of their matched characters, used by
// writeFilterView to highlight. An empty query passes the rows through unranked
// with no highlights.
func (m Model) fuzzyFilter(base []viewRow, query string) ([]viewRow, [][]int) {
	if strings.TrimSpace(query) == "" {
		return base, make([][]int, len(base))
	}
	texts := make([]string, len(base))
	for i, r := range base {
		texts[i] = m.rowFilterText(r)
	}
	matches := fuzzy.Find(query, texts)
	rows := make([]viewRow, 0, len(matches))
	idxs := make([][]int, 0, len(matches))
	for _, match := range matches {
		rows = append(rows, base[match.Index])
		idxs = append(idxs, match.MatchedIndexes)
	}
	return rows, idxs
}

// filterSeg is one styled run of a row's fuzzy-finder label. The concatenated
// segment texts are what the row is matched against, so a match's byte offsets
// index straight into the rendered runs.
type filterSeg struct {
	text  string
	style lipgloss.Style
}

// rowFilterSegments builds a row's fuzzy-finder label as styled runs that keep
// the colors of the normal views: a session in its type color, an agent behind
// its status icon, with the disambiguating context (project, owning session)
// muted. It carries enough context to disambiguate a flat list.
func (m Model) rowFilterSegments(r viewRow) []filterSeg {
	switch r.kind {
	case rowProject:
		return []filterSeg{
			{r.project.Title, defaultStyle},
			{"  " + r.project.FullPath, mutedStyle},
		}
	case rowSession:
		name := sessionName(r.session)
		if r.session.Branch != "" {
			name += " (" + r.session.Branch + ")"
		}
		segs := []filterSeg{{name, sessionStyle(r.session, m.sessionDepth(r.session))}}
		if r.project.Title != "" {
			segs = append(segs, filterSeg{"  " + r.project.Title, mutedStyle})
		}
		return segs
	case rowAgent:
		name := r.agent.DisplayName
		if name == "" {
			name = fmt.Sprintf("agent-%d", r.agent.ID)
		}
		ctx := ""
		if r.agent.Status != "" {
			ctx += "  " + r.agent.Status
		}
		ctx += "  " + m.agentSession(r.agent)
		if r.project.Title != "" {
			ctx += "  " + r.project.Title
		}
		return []filterSeg{
			{agentStatusIcon(r.agent.Status) + " ", agentStatusStyle(r.agent.Status)},
			{name, defaultStyle},
			{ctx, mutedStyle},
		}
	}
	return nil
}

// rowFilterText is the plain text a row is matched against — the concatenation
// of its styled segments, so match offsets and rendering stay in lockstep.
func (m Model) rowFilterText(r viewRow) string {
	var b strings.Builder
	for _, seg := range m.rowFilterSegments(r) {
		b.WriteString(seg.text)
	}
	return b.String()
}

func (m Model) writeFilterView(b *strings.Builder) {
	b.WriteString(filterPromptStyle.Render("/") + " " + m.filterQuery + "▏\n")
	rows, matched := m.fuzzyFilter(m.baseRows(m.tab), m.filterQuery)
	if len(rows) == 0 {
		b.WriteString("  " + mutedStyle.Render("no matches") + "\n")
		return
	}
	cur, has := m.cursor()
	for i, r := range rows {
		text := m.renderFilterLabel(r, matched[i])
		if has && cur.kind == r.kind && rowID(cur) == rowID(r) {
			b.WriteString(selectStyle.Render("> ") + text + "\n")
		} else {
			b.WriteString("  " + text + "\n")
		}
	}
}

// renderFilterLabel draws a row's styled segments, bolding and underlining the
// fuzzy-matched characters in place so the match stands out without losing the
// segment's color. matched holds byte offsets into rowFilterText (as
// sahilm/fuzzy reports them); the running offset maps each segment's local byte
// position onto that same space, and ranging a segment yields whole runes.
func (m Model) renderFilterLabel(r viewRow, matched []int) string {
	set := make(map[int]bool, len(matched))
	for _, i := range matched {
		set[i] = true
	}
	var b strings.Builder
	offset := 0
	for _, seg := range m.rowFilterSegments(r) {
		for pos, ch := range seg.text {
			style := seg.style
			if set[offset+pos] {
				style = style.Bold(true).Underline(true)
			}
			b.WriteString(style.Render(string(ch)))
		}
		offset += len(seg.text)
	}
	return b.String()
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

// startWorktree begins a 'w' creation: the new worktree branches off a source
// Session and is parented to it (ADR-0010). The source is resolved from the
// active view's selection — the Project's Main Session (Projects), the selected
// session (Sessions), or the selected session / the selected agent's owning
// session (Overview). A Secondary source is rejected since it shares its root's
// worktree.
func (m *Model) startWorktree() {
	switch m.tab {
	case tabOverview, tabProjects, tabSessions:
	default:
		return
	}
	row, ok := m.cursor()
	if !ok || row.project.ID == 0 {
		m.status = "no project selected"
		return
	}
	src, ok := m.worktreeSource(row)
	if !ok {
		m.status = "no source session selected"
		return
	}
	if src.Type == "secondary" {
		m.status = "cannot create a worktree from a secondary session"
		return
	}
	m.creatingWorktree = true
	m.worktreeBranch = ""
	m.worktreeProjectID = src.ProjectID
	m.worktreeParentID = src.ID
	m.status = ""
}

// worktreeSource maps the active view's selected row to the source Session a 'w'
// creation branches from.
func (m Model) worktreeSource(row viewRow) (httpclient.Session, bool) {
	switch m.tab {
	case tabProjects:
		return m.mainSessionOf(row.project.ID)
	case tabSessions:
		if row.kind == rowSession {
			return row.session, true
		}
	case tabOverview:
		switch row.kind {
		case rowSession:
			return row.session, true
		case rowAgent:
			return m.sessionByID(row.agent.SessionID)
		}
	}
	return httpclient.Session{}, false
}

func (m Model) mainSessionOf(projectID int) (httpclient.Session, bool) {
	p := m.projectByID(projectID)
	for _, s := range m.sessions {
		if s.ProjectID == projectID && isMainSession(s, p) {
			return s, true
		}
	}
	return httpclient.Session{}, false
}

// startWorktreeFromBase begins a 'W' creation: the new worktree branches off a
// bare base ref that no Session represents, so it is parentless and renders at
// the Project level (ADR-0010). It targets the selected row's Project.
func (m *Model) startWorktreeFromBase() {
	switch m.tab {
	case tabOverview, tabProjects, tabSessions:
	default:
		return
	}
	row, ok := m.cursor()
	if !ok || row.project.ID == 0 {
		m.status = "no project selected"
		return
	}
	m.creatingWorktreeFromBase = true
	m.worktreeFromBaseStep = worktreeBaseStepBranch
	m.worktreeFromBaseProjectID = row.project.ID
	m.worktreeFromBaseBranch = ""
	m.worktreeFromBaseRef = ""
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

// startAgent begins an 'a' creation: spawn an agent in the session the cursor
// resolves to. It is available wherever a row maps to a session — a selected
// session, an agent's owning session, or a project's Main Session.
func (m *Model) startAgent() {
	row, ok := m.cursor()
	if !ok {
		m.status = "no session selected"
		return
	}
	target, ok := m.agentTargetSession(row)
	if !ok {
		m.status = "no session selected"
		return
	}
	m.creatingAgent = true
	m.agentStep = agentPromptExecutable
	m.agentProjectID = target.ProjectID
	m.agentSessionID = target.ID
	m.agentExecutable = ""
	m.agentName = ""
	m.status = ""
}

func (m *Model) startRename() {
	if m.tab != tabAgents {
		return
	}
	row, ok := m.cursor()
	if !ok || row.kind != rowAgent {
		m.status = "no agent selected"
		return
	}
	m.renamingAgent = true
	m.renameAgentID = row.agent.ID
	m.renameValue = row.agent.DisplayName
	if m.renameValue == "" {
		m.renameValue = fmt.Sprintf("agent-%d", row.agent.ID)
	}
	m.status = ""
}

func (m *Model) resetRenamePrompt() {
	m.renamingAgent = false
	m.renameAgentID = 0
	m.renameValue = ""
}

// agentTargetSession maps the cursor's row to the session a new agent runs in.
func (m Model) agentTargetSession(row viewRow) (httpclient.Session, bool) {
	switch row.kind {
	case rowSession:
		return row.session, true
	case rowAgent:
		return m.sessionByID(row.agent.SessionID)
	case rowProject:
		return m.mainSessionOf(row.project.ID)
	}
	return httpclient.Session{}, false
}

func (m *Model) resetAgentPrompt() {
	m.creatingAgent = false
	m.agentStep = agentPromptNone
	m.agentProjectID = 0
	m.agentSessionID = 0
	m.agentExecutable = ""
	m.agentName = ""
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

// sessionRows lays each Project out as a tree walked by Provenance (ADR-0010):
// the Main Session followed by its full subtree (nested worktrees and their
// secondaries), then parentless 'W' worktrees at the Project level. A worktree
// whose provenance parent is no longer present also surfaces at the Project
// level so it never falls out of the tree.
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
			rows = m.appendSessionChildren(rows, p.ID, s.ID)
			break
		}
		for _, s := range m.sessions {
			if s.ProjectID != p.ID || s.Type != "worktree" {
				continue
			}
			if pid := parentID(s); pid > 0 {
				if _, ok := m.sessionByID(pid); ok {
					continue // nested under an existing parent, rendered there
				}
			}
			rows = append(rows, s)
			rows = m.appendSessionChildren(rows, p.ID, s.ID)
		}
		if mainID == 0 {
			rows = m.appendSecondaryChildren(rows, p.ID, 0)
		}
	}
	return rows
}

// appendSessionChildren appends the worktree and secondary children of a parent
// session, depth-first, so nested worktrees and secondaries follow the session
// they descend from.
func (m Model) appendSessionChildren(rows []httpclient.Session, projectID, parent int) []httpclient.Session {
	for _, s := range m.sessions {
		if s.ProjectID != projectID || parentID(s) != parent {
			continue
		}
		switch s.Type {
		case "worktree", "secondary":
			rows = append(rows, s)
			rows = m.appendSessionChildren(rows, projectID, s.ID)
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

// sessionDepth is how far a session sits below the Project level, counting each
// ancestor session in its Provenance/parent chain. Main and parentless 'W'
// worktrees are at depth 0; nested worktrees and secondaries indent by their
// depth.
func (m Model) sessionDepth(s httpclient.Session) int {
	depth := 0
	for id := parentID(s); id > 0; {
		parent, ok := m.sessionByID(id)
		if !ok {
			break
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

// agentStatusIcon maps an agent status to a compact glyph shown at the start of
// its row. The glyphs share one geometric family so they read as a set; color
// (via agentStatusStyle) carries the urgency.
func agentStatusIcon(status string) string {
	switch status {
	case "waiting":
		return "◆"
	case "busy":
		return "◐"
	case "idle":
		return "○"
	case "running":
		return "●"
	case "starting":
		return "◌"
	default:
		return "·"
	}
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
