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
	id                       int
	projectID                int
	sessionID                int
	kind                     string
	model                    string
	variant                  string
	displayName              string
	tmuxPaneID               string
	paneOwned                bool
	status                   AgentStatus
	statusChangedAt          time.Time
	childPGID                int
	discordNotificationArmed bool
	openCodeSessionID        *string
	openCodeReporterEpoch    uint64
	openCodeSessionSequence  uint64
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

func (a *Agent) ID() int                        { return a.id }
func (a *Agent) ProjectID() int                 { return a.projectID }
func (a *Agent) SessionID() int                 { return a.sessionID }
func (a *Agent) Kind() string                   { return a.kind }
func (a *Agent) Model() string                  { return a.model }
func (a *Agent) Variant() string                { return a.variant }
func (a *Agent) DisplayName() string            { return a.displayName }
func (a *Agent) TmuxPaneID() string             { return a.tmuxPaneID }
func (a *Agent) PaneOwned() bool                { return a.paneOwned }
func (a *Agent) Status() AgentStatus            { return a.status }
func (a *Agent) StatusChangedAt() time.Time     { return a.statusChangedAt }
func (a *Agent) ChildProcessGroupID() int       { return a.childPGID }
func (a *Agent) DiscordNotificationArmed() bool { return a.discordNotificationArmed }

func (a *Agent) OpenCodeSessionID() *string {
	if a.openCodeSessionID == nil {
		return nil
	}
	sessionID := *a.openCodeSessionID
	return &sessionID
}

func (a *Agent) OpenCodeSessionReporterEpoch() uint64 { return a.openCodeReporterEpoch }
func (a *Agent) OpenCodeSessionSequence() uint64      { return a.openCodeSessionSequence }

func (a *Agent) WithOpenCodeSession(sessionID *string, reporterEpoch, sequence uint64) *Agent {
	copy := *a
	copy.openCodeSessionID = nil
	if sessionID != nil && *sessionID != "" {
		id := *sessionID
		copy.openCodeSessionID = &id
	}
	copy.openCodeReporterEpoch = reporterEpoch
	copy.openCodeSessionSequence = sequence
	return &copy
}

func (a *Agent) WithStatus(status AgentStatus, statusChangedAt ...time.Time) *Agent {
	changedAt := a.statusChangedAt
	if status != a.status {
		changedAt = time.Now()
		if len(statusChangedAt) > 0 {
			changedAt = statusChangedAt[0]
		}
	}
	copy := *a
	copy.status = status
	copy.statusChangedAt = changedAt
	return &copy
}

func (a *Agent) WithTmuxPaneID(paneID string) *Agent {
	copy := *a
	copy.tmuxPaneID = paneID
	return &copy
}

func (a *Agent) WithDisplayName(name string) *Agent {
	copy := *a
	copy.displayName = name
	return &copy
}

func (a *Agent) WithModel(model string) *Agent {
	copy := *a
	copy.model = model
	return &copy
}

func (a *Agent) WithVariant(variant string) *Agent {
	copy := *a
	copy.variant = variant
	return &copy
}

func (a *Agent) WithChildProcessGroupID(pgid int) *Agent {
	copy := *a
	copy.childPGID = pgid
	return &copy
}

func (a *Agent) WithDiscordNotificationArmed(armed bool) *Agent {
	copy := *a
	copy.discordNotificationArmed = armed
	return &copy
}

func DefaultAgentDisplayName(id int, kind string) string {
	return "agent-" + strconv.Itoa(id) + "-" + kind
}
