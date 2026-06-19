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
	byID := sessionsByID(allSessions)
	plan, err := domain.PlanWorktreeRemoval(allSessions, session.ID())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
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
	for _, id := range plan.DeleteSessionIDs {
		uc.releaseAndKill(ctx, byID[id])
	}

	return uc.lock.WithWrite(func() error {
		for _, r := range plan.ReparentWorktrees {
			s := byID[r.SessionID]
			reparented := domain.NewWorktreeSession(s.ID(), r.ParentID, s.ProjectID(), s.Name(), s.Branch(), s.WorktreePath())
			if _, err := uc.sessions.Update(ctx, reparented); err != nil {
				return err
			}
		}
		for _, id := range plan.DeleteSessionIDs {
			if err := uc.agents.DeleteBySessionID(ctx, id); err != nil {
				return err
			}
			if err := uc.leases.ReleaseSessionLeases(ctx, id); err != nil {
				return err
			}
			if err := uc.sessions.Delete(ctx, id); err != nil {
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
	byID := sessionsByID(sessions)
	plan, err := domain.PlanSecondaryRemoval(sessions, session.ID())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}

	for _, id := range plan.DeleteSessionIDs {
		uc.releaseAndKill(ctx, byID[id])
	}

	return uc.lock.WithWrite(func() error {
		for _, r := range plan.ReparentSecondaries {
			s := byID[r.SessionID]
			updated := domain.NewSecondarySessionWithTmuxName(s.ID(), r.ParentID, s.ProjectID(), s.Name(), s.TmuxName(), s.RelativeWorkingDirectory(), s.OnDelete())
			if _, err := uc.sessions.Update(ctx, updated); err != nil {
				return err
			}
		}
		for _, id := range plan.DeleteSessionIDs {
			if err := uc.agents.DeleteBySessionID(ctx, id); err != nil {
				return err
			}
			if err := uc.leases.ReleaseSessionLeases(ctx, id); err != nil {
				return err
			}
			if err := uc.sessions.Delete(ctx, id); err != nil {
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
	if session == nil {
		return
	}
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

func sessionsByID(sessions []*domain.Session) map[int]*domain.Session {
	byID := make(map[int]*domain.Session, len(sessions))
	for _, s := range sessions {
		byID[s.ID()] = s
	}
	return byID
}
