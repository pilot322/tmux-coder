// Package usecase holds tmux-coder's application logic — create, list and
// delete a Project — and the ports those operations depend on. The interfaces
// are declared here, in the consumer; infra/* provides the implementations.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/pilot322/tmux-coder/internal/domain"
)

// ErrProjectNotFound is returned by IProjectRepository lookups when nothing
// matches. Callers test for it with errors.Is.
var ErrProjectNotFound = errors.New("project not found")

// ErrGateway wraps a failure from the SessionGateway (tmux). The HTTP layer
// maps it to 502 Bad Gateway.
var ErrGateway = errors.New("session gateway failure")

// ErrValidation is returned for invalid API inputs or requests that violate the
// current session creation/deletion rules. The HTTP layer maps it to 400.
var ErrValidation = errors.New("validation error")

// ErrConflict is returned for expected Git/session state conflicts, such as a
// duplicate Worktree Session or dirty worktree removal. The HTTP layer maps it to 409.
var ErrConflict = errors.New("conflict")

// ErrNotImplemented marks Session operations intentionally out of this slice.
var ErrNotImplemented = errors.New("not implemented")

// ErrSessionNotFound is returned when a Session id is unknown.
var ErrSessionNotFound = errors.New("session not found")

var ErrAgentNotFound = errors.New("agent not found")

// IProjectRepository persists Projects. The repository assigns ids, so Create
// returns the stored Project with its id set.
type IProjectRepository interface {
	Create(ctx context.Context, p *domain.Project) (*domain.Project, error)
	GetByID(ctx context.Context, id int) (*domain.Project, error)
	GetByFullPath(ctx context.Context, fullPath string) (*domain.Project, error)
	GetAll(ctx context.Context) ([]*domain.Project, error)
	Delete(ctx context.Context, id int) error
}

// ISessionRepository persists Sessions. The repository assigns ids on Create.
type ISessionRepository interface {
	Create(ctx context.Context, s *domain.Session) (*domain.Session, error)
	GetByID(ctx context.Context, id int) (*domain.Session, error)
	GetAll(ctx context.Context) ([]*domain.Session, error)
	GetByProjectID(ctx context.Context, projectID int) ([]*domain.Session, error)
	Update(ctx context.Context, s *domain.Session) (*domain.Session, error)
	Delete(ctx context.Context, id int) error
}

// WorktreeRef is one entry of `git worktree list` — the checkout path and the
// branch it is on (empty when Detached). Used to decide, in one authoritative
// call, whether a derived worktree path already belongs to this repo.
type WorktreeRef struct {
	Path     string
	Branch   string
	Detached bool
}

// GitWorktreeGateway is the port to Git for Worktree Session lifecycle.
type GitWorktreeGateway interface {
	ValidateBranchName(ctx context.Context, branch string) error
	IsWorktreeRoot(ctx context.Context, path string) (bool, error)
	LocalBranchExists(ctx context.Context, repoPath, branch string) (bool, error)
	ResolveCommit(ctx context.Context, repoPath, ref string) (bool, error)
	WorktreePathExists(ctx context.Context, path string) (bool, error)
	ListWorktrees(ctx context.Context, repoPath string) ([]WorktreeRef, error)
	AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, createBranch bool) error
	RemoveWorktree(ctx context.Context, worktreePath string, force bool) error
	DeleteBranch(ctx context.Context, repoPath, branch string) error
	CurrentBranch(ctx context.Context, repoPath string) (string, error)
}

// SessionGateway is the port to the dedicated tmux server. Its failures map to
// 502 Bad Gateway at the HTTP edge.
type SessionGateway interface {
	Create(ctx context.Context, name, workingDir string) error
	Kill(ctx context.Context, name string) error
	Exists(ctx context.Context, name string) (bool, error)
	// SwitchClients moves every tmux client currently attached to the from
	// session over to the to session. It is a no-op (nil error) when from has
	// no attached clients or no longer exists, so callers can switch a session's
	// clients away unconditionally before killing it.
	SwitchClients(ctx context.Context, from, to string) error
}

// StateLock guards the daemon's in-memory state (ADR-0003). Repository calls
// run inside these closures; the tmux exec runs outside them.
type StateLock interface {
	WithRead(fn func() error) error
	WithWrite(fn func() error) error
}

type IAgentRepository interface {
	Create(ctx context.Context, a *domain.Agent) (*domain.Agent, error)
	GetByID(ctx context.Context, id int) (*domain.Agent, error)
	GetAll(ctx context.Context) ([]*domain.Agent, error)
	GetBySessionID(ctx context.Context, sessionID int) ([]*domain.Agent, error)
	Update(ctx context.Context, a *domain.Agent) (*domain.Agent, error)
	Delete(ctx context.Context, id int) error
	DeleteByProjectID(ctx context.Context, projectID int) error
	DeleteBySessionID(ctx context.Context, sessionID int) error
}

type AgentTmuxGateway interface {
	NewWindow(ctx context.Context, sessionName, workingDir, command string, env []string) (string, error)
	PaneExists(ctx context.Context, paneID string) (bool, error)
	KillPane(ctx context.Context, paneID string) error
	ListPanes(ctx context.Context, sessionName string) ([]string, error)
}

type AgentProcessGateway interface {
	TerminateProcessGroup(ctx context.Context, pgid int, sigtermTimeout time.Duration) error
}

// NotificationUrgency is the host urgency level of a Desktop Notification.
type NotificationUrgency int

const (
	UrgencyNormal NotificationUrgency = iota
	UrgencyCritical
)

// Notification is a single Desktop Notification to deliver to the host.
type Notification struct {
	Title   string
	Body    string
	Urgency NotificationUrgency
}

// Notifier delivers a Desktop Notification to the host. It is generic — the
// usecase composes the content and decides which Agent Status transitions
// notify. Delivery is best-effort: a returned error is logged and otherwise
// ignored by callers.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}
