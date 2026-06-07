package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type DeleteProject struct {
	projects IProjectRepository
	sessions ISessionRepository
	gateway  SessionGateway
	lock     StateLock
}

func NewDeleteProject(p IProjectRepository, s ISessionRepository, g SessionGateway, l StateLock) *DeleteProject {
	return &DeleteProject{projects: p, sessions: s, gateway: g, lock: l}
}

// Execute removes a Project and its Sessions. It returns ErrProjectNotFound
// for an unknown id. It kills the tmux sessions BEFORE removing records,
// tolerating sessions that are already gone; a real kill failure returns
// ErrGateway and leaves the records in place.
func (uc *DeleteProject) Execute(ctx context.Context, id int) error {
	var sessions []*domain.Session
	if err := uc.lock.WithRead(func() error {
		if _, err := uc.projects.GetByID(ctx, id); err != nil {
			return err
		}
		s, err := uc.sessions.GetByProjectID(ctx, id)
		sessions = s
		return err
	}); err != nil {
		return err
	}

	for _, s := range sessions {
		exists, err := uc.gateway.Exists(ctx, s.Name())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if !exists {
			continue
		}
		if err := uc.gateway.Kill(ctx, s.Name()); err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
	}

	return uc.lock.WithWrite(func() error {
		for _, s := range sessions {
			if err := uc.sessions.Delete(ctx, s.ID()); err != nil {
				return err
			}
		}
		return uc.projects.Delete(ctx, id)
	})
}
