package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
)

func reconcileWorktreeSessions(ctx context.Context, sessions ISessionRepository, git GitWorktreeGateway, tmux SessionGateway, lock StateLock, leases ResourceLeaseRepository) error {
	if leases == nil {
		leases = noopResourceLeaseRepository{}
	}
	var allSessions []*domain.Session
	var worktrees []*domain.Session
	if err := lock.WithRead(func() error {
		all, err := sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		allSessions = all
		for _, s := range all {
			if s.Type() == domain.WorktreeSession {
				worktrees = append(worktrees, s)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	var missingWorktrees []int
	for _, s := range worktrees {
		exists, err := git.WorktreePathExists(ctx, s.WorktreePath())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if exists {
			continue
		}
		missingWorktrees = append(missingWorktrees, s.ID())
	}
	if len(missingWorktrees) == 0 {
		return nil
	}

	plan, err := domain.PlanWorktreePrune(allSessions, missingWorktrees)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	byID := sessionsByID(allSessions)
	for _, id := range plan.DeleteSessionIDs {
		s := byID[id]
		tmuxExists, err := tmux.Exists(ctx, s.TmuxName())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if tmuxExists {
			if err := tmux.Kill(ctx, s.TmuxName()); err != nil {
				return fmt.Errorf("%w: %v", ErrGateway, err)
			}
		}
	}

	return lock.WithWrite(func() error {
		// Worktree children of a pruned worktree are independent checkouts that
		// survive; reparent them to the nearest ancestor that is not itself
		// being pruned so they stay attached to the tree (ADR-0010). Secondary
		// children are already in the prune set via cascade above.
		for _, r := range plan.ReparentWorktrees {
			s := byID[r.SessionID]
			reparented := domain.NewWorktreeSession(s.ID(), r.ParentID, s.ProjectID(), s.Name(), s.Branch(), s.WorktreePath())
			if _, err := sessions.Update(ctx, reparented); err != nil {
				return err
			}
		}
		for _, id := range plan.DeleteSessionIDs {
			if err := leases.ReleaseSessionLeases(ctx, id); err != nil {
				return err
			}
			if err := sessions.Delete(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
