package domain

import "strconv"

type AgentStatus string

const (
	AgentStarting AgentStatus = "starting"
	AgentRunning  AgentStatus = "running"
	AgentBusy     AgentStatus = "busy"
	AgentIdle     AgentStatus = "idle"
	AgentWaiting  AgentStatus = "waiting"
)

type Agent struct {
	id          int
	projectID   int
	sessionID   int
	kind        string
	displayName string
	tmuxPaneID  string
	paneOwned   bool
	status      AgentStatus
	childPGID   int
}

func NewAgent(id, projectID, sessionID int, kind, displayName, tmuxPaneID string, paneOwned bool, status AgentStatus) *Agent {
	return &Agent{
		id:          id,
		projectID:   projectID,
		sessionID:   sessionID,
		kind:        kind,
		displayName: displayName,
		tmuxPaneID:  tmuxPaneID,
		paneOwned:   paneOwned,
		status:      status,
	}
}

func (a *Agent) ID() int                  { return a.id }
func (a *Agent) ProjectID() int           { return a.projectID }
func (a *Agent) SessionID() int           { return a.sessionID }
func (a *Agent) Kind() string             { return a.kind }
func (a *Agent) DisplayName() string      { return a.displayName }
func (a *Agent) TmuxPaneID() string       { return a.tmuxPaneID }
func (a *Agent) PaneOwned() bool          { return a.paneOwned }
func (a *Agent) Status() AgentStatus      { return a.status }
func (a *Agent) ChildProcessGroupID() int { return a.childPGID }

func (a *Agent) WithStatus(status AgentStatus) *Agent {
	return &Agent{
		id:          a.id,
		projectID:   a.projectID,
		sessionID:   a.sessionID,
		kind:        a.kind,
		displayName: a.displayName,
		tmuxPaneID:  a.tmuxPaneID,
		paneOwned:   a.paneOwned,
		status:      status,
		childPGID:   a.childPGID,
	}
}

func (a *Agent) WithTmuxPaneID(paneID string) *Agent {
	return &Agent{
		id:          a.id,
		projectID:   a.projectID,
		sessionID:   a.sessionID,
		kind:        a.kind,
		displayName: a.displayName,
		tmuxPaneID:  paneID,
		paneOwned:   a.paneOwned,
		status:      a.status,
		childPGID:   a.childPGID,
	}
}

func (a *Agent) WithDisplayName(name string) *Agent {
	return &Agent{
		id:          a.id,
		projectID:   a.projectID,
		sessionID:   a.sessionID,
		kind:        a.kind,
		displayName: name,
		tmuxPaneID:  a.tmuxPaneID,
		paneOwned:   a.paneOwned,
		status:      a.status,
		childPGID:   a.childPGID,
	}
}

func (a *Agent) WithChildProcessGroupID(pgid int) *Agent {
	return &Agent{
		id:          a.id,
		projectID:   a.projectID,
		sessionID:   a.sessionID,
		kind:        a.kind,
		displayName: a.displayName,
		tmuxPaneID:  a.tmuxPaneID,
		paneOwned:   a.paneOwned,
		status:      a.status,
		childPGID:   pgid,
	}
}

func DefaultAgentDisplayName(id int, kind string) string {
	return "agent-" + strconv.Itoa(id) + "-" + kind
}
