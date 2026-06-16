package domain

import (
	"strconv"
	"time"
)

type AgentStatus string

const (
	AgentStarting AgentStatus = "starting"
	AgentRunning  AgentStatus = "running"
	AgentBusy     AgentStatus = "busy"
	AgentIdle     AgentStatus = "idle"
	AgentWaiting  AgentStatus = "waiting"
)

type Agent struct {
	id              int
	projectID       int
	sessionID       int
	kind            string
	displayName     string
	tmuxPaneID      string
	paneOwned       bool
	status          AgentStatus
	statusChangedAt time.Time
	childPGID       int
}

func NewAgent(id, projectID, sessionID int, kind, displayName, tmuxPaneID string, paneOwned bool, status AgentStatus, statusChangedAt ...time.Time) *Agent {
	changedAt := time.Now()
	if len(statusChangedAt) > 0 {
		changedAt = statusChangedAt[0]
	}
	return &Agent{
		id:              id,
		projectID:       projectID,
		sessionID:       sessionID,
		kind:            kind,
		displayName:     displayName,
		tmuxPaneID:      tmuxPaneID,
		paneOwned:       paneOwned,
		status:          status,
		statusChangedAt: changedAt,
	}
}

func (a *Agent) ID() int                    { return a.id }
func (a *Agent) ProjectID() int             { return a.projectID }
func (a *Agent) SessionID() int             { return a.sessionID }
func (a *Agent) Kind() string               { return a.kind }
func (a *Agent) DisplayName() string        { return a.displayName }
func (a *Agent) TmuxPaneID() string         { return a.tmuxPaneID }
func (a *Agent) PaneOwned() bool            { return a.paneOwned }
func (a *Agent) Status() AgentStatus        { return a.status }
func (a *Agent) StatusChangedAt() time.Time { return a.statusChangedAt }
func (a *Agent) ChildProcessGroupID() int   { return a.childPGID }

func (a *Agent) WithStatus(status AgentStatus, statusChangedAt ...time.Time) *Agent {
	changedAt := a.statusChangedAt
	if status != a.status {
		changedAt = time.Now()
		if len(statusChangedAt) > 0 {
			changedAt = statusChangedAt[0]
		}
	}
	return &Agent{
		id:              a.id,
		projectID:       a.projectID,
		sessionID:       a.sessionID,
		kind:            a.kind,
		displayName:     a.displayName,
		tmuxPaneID:      a.tmuxPaneID,
		paneOwned:       a.paneOwned,
		status:          status,
		statusChangedAt: changedAt,
		childPGID:       a.childPGID,
	}
}

func (a *Agent) WithTmuxPaneID(paneID string) *Agent {
	return &Agent{
		id:              a.id,
		projectID:       a.projectID,
		sessionID:       a.sessionID,
		kind:            a.kind,
		displayName:     a.displayName,
		tmuxPaneID:      paneID,
		paneOwned:       a.paneOwned,
		status:          a.status,
		statusChangedAt: a.statusChangedAt,
		childPGID:       a.childPGID,
	}
}

func (a *Agent) WithDisplayName(name string) *Agent {
	return &Agent{
		id:              a.id,
		projectID:       a.projectID,
		sessionID:       a.sessionID,
		kind:            a.kind,
		displayName:     name,
		tmuxPaneID:      a.tmuxPaneID,
		paneOwned:       a.paneOwned,
		status:          a.status,
		statusChangedAt: a.statusChangedAt,
		childPGID:       a.childPGID,
	}
}

func (a *Agent) WithChildProcessGroupID(pgid int) *Agent {
	return &Agent{
		id:              a.id,
		projectID:       a.projectID,
		sessionID:       a.sessionID,
		kind:            a.kind,
		displayName:     a.displayName,
		tmuxPaneID:      a.tmuxPaneID,
		paneOwned:       a.paneOwned,
		status:          a.status,
		statusChangedAt: a.statusChangedAt,
		childPGID:       pgid,
	}
}

func DefaultAgentDisplayName(id int, kind string) string {
	return "agent-" + strconv.Itoa(id) + "-" + kind
}
