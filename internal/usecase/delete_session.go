package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
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
	log      obs.Logger
}

func NewDeleteSession(s ISessionRepository, a IAgentRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock, log obs.Logger) *DeleteSession {
	return NewDeleteSessionWithLeases(s, a, tmux, git, l, nil, log)
}

func NewDeleteSessionWithLeases(s ISessionRepository, a IAgentRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock, leases ResourceLeaseRepository, log obs.Logger) *DeleteSession {
	if leases == nil {
		leases = noopResourceLeaseRepository{}
	}
	return &DeleteSession{sessions: s, agents: a, tmux: tmux, git: git, lock: l, leases: leases, log: log.With("component", "delete-session")}
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

	uc.log.Info(ctx, "deleting session", "session_id", session.ID(), "name", session.Name(), "force", in.Force)
	switch session.Type() {
	case domain.MainSession:
		return fmt.Errorf("%w: main sessions cannot be deleted through /sessions", ErrValidation)
	case domain.SecondarySession:
		return uc.deleteSecondary(ctx, session)
	case domain.WorktreeSession:
		return uc.deleteWorktree(ctx, session, in.Force)
	default:
		return fmt.Errorf("%w: unsupported session type", ErrValidation)
	}
}

// deleteWorktree removes a Worktree Session and its owned worktree. Its Secondary
// children cascade — their subdirectories vanished with the worktree — while its
// Worktree children are independent checkouts that survive and are reparented to
// this session's parent (ADR-0010).
func (uc *DeleteSession) deleteWorktree(ctx context.Context, session *domain.Session, force bool) error {
	var allSessions []*domain.Session
	if err := uc.lock.WithRead(func() error {
		s, err := uc.sessions.GetAll(ctx)
		allSessions = s
		return err
	}); err != nil {
		return err
	}

	if err := uc.git.RemoveWorktree(ctx, session.WorktreePath(), force); err != nil {
		if errors.Is(err, ErrConflict) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrGateway, err)
	}

	// The worktree is already gone, so the records must be removed to stay
	// consistent — listed-but-unattachable sessions are worse than stray tmux
	// sessions, which the kills below clear best-effort.
	cascade := secondaryDescendants(allSessions, session.ID())
	uc.releaseAndKill(ctx, session)
	for _, s := range cascade {
		uc.releaseAndKill(ctx, s)
	}

	return uc.lock.WithWrite(func() error {
		for _, s := range allSessions {
			if s.Type() != domain.WorktreeSession || s.Parent() != session.ID() {
				continue
			}
			reparented := domain.NewWorktreeSession(s.ID(), session.Parent(), s.ProjectID(), s.Name(), s.Branch(), s.WorktreePath())
			if _, err := uc.sessions.Update(ctx, reparented); err != nil {
				return err
			}
		}
		for _, s := range append(cascade, session) {
			if err := uc.agents.DeleteBySessionID(ctx, s.ID()); err != nil {
				return err
			}
			if err := uc.leases.ReleaseSessionLeases(ctx, s.ID()); err != nil {
				return err
			}
			if err := uc.sessions.Delete(ctx, s.ID()); err != nil {
				return err
			}
		}
		return nil
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
		uc.releaseAndKill(ctx, s)
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

// releaseAndKill detaches the doomed session from any attached clients and then
// kills it. Both steps are best-effort: the caller has already committed to
// deletion (the worktree is gone or the session is a Secondary being pruned),
// so a tmux failure must never abort the record removal that follows. Killing
// the session the user is attached to can itself tear the server down and make
// kill-session exit non-zero, which is exactly why the kill is not surfaced.
func (uc *DeleteSession) releaseAndKill(ctx context.Context, session *domain.Session) {
	uc.switchClientsToMain(ctx, session)
	_ = uc.tmux.Kill(ctx, session.TmuxName())
}

// switchClientsToMain moves any tmux clients attached to the doomed session
// over to its project's Main Session before it is killed, so a user sitting
// inside the session they delete is reattached rather than detached. It is
// best-effort: a missing Main Session or a tmux error must not abort deletion.
func (uc *DeleteSession) switchClientsToMain(ctx context.Context, session *domain.Session) {
	var mainTmuxName string
	_ = uc.lock.WithRead(func() error {
		all, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		for _, s := range all {
			if s.Type() == domain.MainSession && s.ProjectID() == session.ProjectID() {
				mainTmuxName = s.TmuxName()
				break
			}
		}
		return nil
	})
	if mainTmuxName == "" || mainTmuxName == session.TmuxName() {
		return
	}
	_ = uc.tmux.SwitchClients(ctx, session.TmuxName(), mainTmuxName)
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
