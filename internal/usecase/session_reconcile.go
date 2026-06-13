package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/domain"
)

func reconcileWorktreeSessions(ctx context.Context, sessions ISessionRepository, git GitWorktreeGateway, tmux SessionGateway, lock StateLock) error {
	var worktrees []*domain.Session
	if err := lock.WithRead(func() error {
		all, err := sessions.GetAll(ctx)
		if err != nil {
			return err
		}
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
		tmuxExists, err := tmux.Exists(ctx, s.Name())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if tmuxExists {
			if err := tmux.Kill(ctx, s.Name()); err != nil {
				return fmt.Errorf("%w: %v", ErrGateway, err)
			}
		}
		prune = append(prune, s.ID())
	}

	if len(prune) == 0 {
		return nil
	}
	return lock.WithWrite(func() error {
		for _, id := range prune {
			if err := sessions.Delete(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
