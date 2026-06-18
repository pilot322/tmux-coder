package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pilot322/tmux-coder/internal/config"
	"github.com/pilot322/tmux-coder/internal/domain"
)

// materializeSecondarySessions applies a Project's Config File template of
// declared Secondary Sessions under a freshly-created root Session (ADR-0007).
// projectRoot locates the Config File; rootDir is the directory that `subdir`
// resolves against — the Project path for a Main-rooted tree, the worktree path
// for a Worktree-rooted tree.
//
// It mirrors reconcileWorktreeSessions' free-function shape and honours
// ADR-0003: each record is written inside a short write critical section while
// the tmux Create runs outside the lock. Materialization is all-or-nothing —
// the Config File is validated statically up front, and any later failure
// unwinds every Secondary Session created so far (killing tmux, deleting the
// record) in reverse before returning, leaving the caller's own rollback to
// undo the root Session, worktree and branch.
func materializeSecondarySessions(ctx context.Context, sessions ISessionRepository, tmux SessionGateway, lock StateLock, projectRoot string, root *domain.Session, rootDir string) error {
	cfg, err := config.Load(projectRoot)
	if err != nil {
		return translateConfigErr(err)
	}
	if len(cfg.Secondaries) == 0 {
		return nil
	}

	// runtimeByConfigID maps a declaration's config-local id to the Session it
	// produced, so a child can resolve its `parent` to a real parent id.
	runtimeByConfigID := make(map[string]*domain.Session, len(cfg.Secondaries))
	// usedByParent tracks the sibling names taken under each runtime parent, so
	// display names need only be sibling-unique (ADR-0007).
	usedByParent := make(map[int]map[string]bool)
	var created []*domain.Session

	unwind := func() {
		cleanupCtx, cancel := detachedCleanupContext(ctx)
		defer cancel()
		for i := len(created) - 1; i >= 0; i-- {
			_ = tmux.Kill(cleanupCtx, created[i].TmuxName())
			id := created[i].ID()
			_ = lock.WithWrite(func() error { return sessions.Delete(cleanupCtx, id) })
		}
	}

	for _, decl := range cfg.Secondaries {
		parent := root
		if decl.Parent != "" {
			// Topological order guarantees the parent was created already.
			parent = runtimeByConfigID[decl.Parent]
		}

		relwd, err := normalizeRelativeWorkingDirectory(decl.Subdir)
		if err != nil {
			unwind()
			return err
		}
		base := strings.TrimSpace(decl.Name)
		if base == "" {
			base = filepath.Base(relwd)
		}
		used := usedByParent[parent.ID()]
		if used == nil {
			used = make(map[string]bool)
			usedByParent[parent.ID()] = used
		}
		name := domain.DeriveSecondarySessionName(base, func(n string) bool { return used[n] })
		tmuxName := domain.DeriveSecondaryTmuxSessionName(parent.TmuxName(), name)
		workingDir := filepath.Join(rootDir, relwd)

		info, err := os.Stat(workingDir)
		if err != nil {
			unwind()
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: secondary-session subdir %q does not exist", ErrValidation, decl.Subdir)
			}
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if !info.IsDir() {
			unwind()
			return fmt.Errorf("%w: secondary-session subdir %q is not a directory", ErrValidation, decl.Subdir)
		}

		if err := tmux.Create(ctx, tmuxName, workingDir); err != nil {
			unwind()
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}

		var session *domain.Session
		if err := lock.WithWrite(func() error {
			s, err := sessions.Create(ctx, domain.NewSecondarySessionWithTmuxName(0, parent.ID(), root.ProjectID(), name, tmuxName, relwd, decl.OnDelete))
			session = s
			return err
		}); err != nil {
			cleanupCtx, cancel := detachedCleanupContext(ctx)
			_ = tmux.Kill(cleanupCtx, tmuxName)
			cancel()
			unwind()
			return err
		}

		used[name] = true
		if decl.ID != "" {
			runtimeByConfigID[decl.ID] = session
		}
		created = append(created, session)
	}

	return nil
}
