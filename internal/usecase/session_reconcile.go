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

	var prune []int
	for _, s := range worktrees {
		exists, err := git.WorktreePathExists(ctx, s.WorktreePath())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if exists {
			continue
		}
		tmuxExists, err := tmux.Exists(ctx, s.TmuxName())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if tmuxExists {
			if err := tmux.Kill(ctx, s.TmuxName()); err != nil {
				return fmt.Errorf("%w: %v", ErrGateway, err)
			}
		}
		toPrune := append([]*domain.Session{s}, secondaryDescendants(allSessions, s.ID())...)
		for _, descendant := range toPrune[1:] {
			tmuxExists, err := tmux.Exists(ctx, descendant.TmuxName())
			if err != nil {
				return fmt.Errorf("%w: %v", ErrGateway, err)
			}
			if tmuxExists {
				if err := tmux.Kill(ctx, descendant.TmuxName()); err != nil {
					return fmt.Errorf("%w: %v", ErrGateway, err)
				}
			}
		}
		for _, p := range toPrune {
			prune = append(prune, p.ID())
		}
	}

	if len(prune) == 0 {
		return nil
	}
	prunedSet := make(map[int]bool, len(prune))
	for _, id := range prune {
		prunedSet[id] = true
	}
	parentOf := make(map[int]int, len(allSessions))
	for _, s := range allSessions {
		parentOf[s.ID()] = s.Parent()
	}
	return lock.WithWrite(func() error {
		// Worktree children of a pruned worktree are independent checkouts that
		// survive; reparent them to the nearest ancestor that is not itself
		// being pruned so they stay attached to the tree (ADR-0010). Secondary
		// children are already in the prune set via cascade above.
		for _, s := range allSessions {
			if prunedSet[s.ID()] || s.Type() != domain.WorktreeSession || !prunedSet[s.Parent()] {
				continue
			}
			newParent := s.Parent()
			for newParent > 0 && prunedSet[newParent] {
				newParent = parentOf[newParent]
			}
			reparented := domain.NewWorktreeSession(s.ID(), newParent, s.ProjectID(), s.Name(), s.Branch(), s.WorktreePath())
			if _, err := sessions.Update(ctx, reparented); err != nil {
				return err
			}
		}
		for _, id := range prune {
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
