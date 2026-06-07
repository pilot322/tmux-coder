// Package usecase holds tmux-coder's application logic — create, list and
// delete a Project — and the ports those operations depend on. The interfaces
// are declared here, in the consumer; infra/* provides the implementations.
package usecase

import (
	"context"
	"errors"

	"github.com/pilot322/tmux-coder/internal/domain"
)

// ErrProjectNotFound is returned by IProjectRepository lookups when nothing
// matches. Callers test for it with errors.Is.
var ErrProjectNotFound = errors.New("project not found")

// ErrGateway wraps a failure from the SessionGateway (tmux). The HTTP layer
// maps it to 502 Bad Gateway.
var ErrGateway = errors.New("session gateway failure")

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
	GetAll(ctx context.Context) ([]*domain.Session, error)
	GetByProjectID(ctx context.Context, projectID int) ([]*domain.Session, error)
	Delete(ctx context.Context, id int) error
}

// SessionGateway is the port to the dedicated tmux server. Its failures map to
// 502 Bad Gateway at the HTTP edge.
type SessionGateway interface {
	Create(ctx context.Context, name, workingDir string) error
	Kill(ctx context.Context, name string) error
	Exists(ctx context.Context, name string) (bool, error)
}

// StateLock guards the daemon's in-memory state (ADR-0003). Repository calls
// run inside these closures; the tmux exec runs outside them.
type StateLock interface {
	WithRead(fn func() error) error
	WithWrite(fn func() error) error
}
