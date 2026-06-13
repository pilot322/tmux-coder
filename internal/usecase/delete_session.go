package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type DeleteSessionInput struct {
	ID    int
	Force bool
}

type DeleteSession struct {
	sessions ISessionRepository
	agents   IAgentRepository
	tmux     SessionGateway
	git      GitWorktreeGateway
	lock     StateLock
}

func NewDeleteSession(s ISessionRepository, a IAgentRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock) *DeleteSession {
	return &DeleteSession{sessions: s, agents: a, tmux: tmux, git: git, lock: l}
}

func (uc *DeleteSession) Execute(ctx context.Context, in DeleteSessionInput) error {
	if err := reconcileWorktreeSessions(ctx, uc.sessions, uc.git, uc.tmux, uc.lock); err != nil {
		return err
	}

	var session *domain.Session
	if err := uc.lock.WithRead(func() error {
		s, err := uc.sessions.GetByID(ctx, in.ID)
		session = s
		return err
	}); err != nil {
		return err
	}

	switch session.Type() {
	case domain.MainSession:
		return fmt.Errorf("%w: main sessions cannot be deleted through /sessions", ErrValidation)
	case domain.SecondarySession:
		return ErrNotImplemented
	case domain.WorktreeSession:
		// continue
	default:
		return fmt.Errorf("%w: unsupported session type", ErrValidation)
	}

	if err := uc.git.RemoveWorktree(ctx, session.WorktreePath(), in.Force); err != nil {
		if errors.Is(err, ErrConflict) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrGateway, err)
	}
	exists, err := uc.tmux.Exists(ctx, session.TmuxName())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGateway, err)
	}
	if exists {
		if err := uc.tmux.Kill(ctx, session.TmuxName()); err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
	}

	return uc.lock.WithWrite(func() error {
		if err := uc.agents.DeleteBySessionID(ctx, session.ID()); err != nil {
			return err
		}
		return uc.sessions.Delete(ctx, session.ID())
	})
}
