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
	leases   ResourceLeaseRepository
}

func NewDeleteSession(s ISessionRepository, a IAgentRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock) *DeleteSession {
	return NewDeleteSessionWithLeases(s, a, tmux, git, l, nil)
}

func NewDeleteSessionWithLeases(s ISessionRepository, a IAgentRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock, leases ResourceLeaseRepository) *DeleteSession {
	if leases == nil {
		leases = noopResourceLeaseRepository{}
	}
	return &DeleteSession{sessions: s, agents: a, tmux: tmux, git: git, lock: l, leases: leases}
}

func (uc *DeleteSession) Execute(ctx context.Context, in DeleteSessionInput) error {
	if err := reconcileWorktreeSessions(ctx, uc.sessions, uc.git, uc.tmux, uc.lock, uc.leases); err != nil {
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
		return uc.deleteSecondary(ctx, session)
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
		if err := uc.leases.ReleaseSessionLeases(ctx, session.ID()); err != nil {
			return err
		}
		return uc.sessions.Delete(ctx, session.ID())
	})
}

func (uc *DeleteSession) deleteSecondary(ctx context.Context, session *domain.Session) error {
	var sessions []*domain.Session
	if err := uc.lock.WithRead(func() error {
		s, err := uc.sessions.GetAll(ctx)
		sessions = s
		return err
	}); err != nil {
		return err
	}

	toDelete := []*domain.Session{session}
	if session.OnDelete() != "inherit" {
		toDelete = append(toDelete, secondaryDescendants(sessions, session.ID())...)
	}
	for _, s := range toDelete {
		exists, err := uc.tmux.Exists(ctx, s.TmuxName())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if exists {
			if err := uc.tmux.Kill(ctx, s.TmuxName()); err != nil {
				return fmt.Errorf("%w: %v", ErrGateway, err)
			}
		}
	}

	return uc.lock.WithWrite(func() error {
		if session.OnDelete() == "inherit" {
			for _, s := range sessions {
				if s.Type() != domain.SecondarySession || s.Parent() != session.ID() {
					continue
				}
				updated := domain.NewSecondarySessionWithTmuxName(s.ID(), session.Parent(), s.ProjectID(), s.Name(), s.TmuxName(), s.RelativeWorkingDirectory(), s.OnDelete())
				if _, err := uc.sessions.Update(ctx, updated); err != nil {
					return err
				}
			}
		}
		for _, s := range toDelete {
			if err := uc.agents.DeleteBySessionID(ctx, s.ID()); err != nil {
				return err
			}
			if err := uc.sessions.Delete(ctx, s.ID()); err != nil {
				return err
			}
		}
		return nil
	})
}

func secondaryDescendants(sessions []*domain.Session, parentID int) []*domain.Session {
	var out []*domain.Session
	for _, s := range sessions {
		if s.Type() != domain.SecondarySession || s.Parent() != parentID {
			continue
		}
		out = append(out, s)
		out = append(out, secondaryDescendants(sessions, s.ID())...)
	}
	return out
}
