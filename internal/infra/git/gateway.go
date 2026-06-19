package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.GitWorktreeGateway = (*Gateway)(nil)

type Gateway struct {
	binary string
	log    obs.Logger
}

func NewGateway(log obs.Logger) *Gateway {
	return &Gateway{binary: "git", log: log.With("component", "git")}
}

func (g *Gateway) ValidateBranchName(ctx context.Context, branch string) error {
	if branch == "" {
		return fmt.Errorf("%w: branch is required", usecase.ErrValidation)
	}
	g.log.Debug(ctx, "git exec", "args", []string{"check-ref-format", "--branch", branch})
	if err := exec.CommandContext(ctx, g.binary, "check-ref-format", "--branch", branch).Run(); err != nil {
		if !isExit(err) {
			return err
		}
		return fmt.Errorf("%w: invalid branch name", usecase.ErrValidation)
	}
	return nil
}

func (g *Gateway) IsWorktreeRoot(ctx context.Context, path string) (bool, error) {
	g.log.Debug(ctx, "git exec", "args", []string{"rev-parse", "--show-toplevel"}, "repo", path)
	out, err := exec.CommandContext(ctx, g.binary, "-C", path, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		if isExit(err) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) == path, nil
}

func (g *Gateway) LocalBranchExists(ctx context.Context, repoPath, branch string) (bool, error) {
	return g.existsByExit(ctx, repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
}

func (g *Gateway) ResolveCommit(ctx context.Context, repoPath, ref string) (bool, error) {
	return g.existsByExit(ctx, repoPath, "rev-parse", "--verify", ref+"^{commit}")
}

func (g *Gateway) WorktreePathExists(ctx context.Context, path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (g *Gateway) ListWorktrees(ctx context.Context, repoPath string) ([]usecase.WorktreeRef, error) {
	g.log.Debug(ctx, "git exec", "args", []string{"worktree", "list", "--porcelain"}, "repo", repoPath)
	out, err := exec.CommandContext(ctx, g.binary, "-C", repoPath, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree list --porcelain: %w: %s", err, out)
	}
	return parseWorktreePorcelain(out), nil
}

// parseWorktreePorcelain reads the records emitted by
// `git worktree list --porcelain`. Records are separated by blank lines and
// begin with a `worktree <path>` line; a record may carry `branch
// refs/heads/<name>`, `detached`, or `bare`. The bare main repository has no
// checkout to wrap, so it is omitted.
func parseWorktreePorcelain(out []byte) []usecase.WorktreeRef {
	var refs []usecase.WorktreeRef
	var cur usecase.WorktreeRef
	var have, bare bool
	flush := func() {
		if have && !bare {
			refs = append(refs, cur)
		}
		cur, have, bare = usecase.WorktreeRef{}, false, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
			have = true
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			cur.Detached = true
		case line == "bare":
			bare = true
		}
	}
	flush()
	return refs
}

func (g *Gateway) AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, createBranch bool) error {
	args := []string{"-C", repoPath, "worktree", "add"}
	if createBranch {
		args = append(args, "-b", branch, worktreePath)
		if baseBranch != "" {
			args = append(args, baseBranch)
		}
	} else {
		args = append(args, worktreePath, branch)
	}
	return g.run(ctx, args...)
}

func (g *Gateway) RemoveWorktree(ctx context.Context, worktreePath string, force bool) error {
	if repoPath, ok := repoPathFromWorktreeGitFile(worktreePath); ok {
		if err := g.removeWorktreeFrom(ctx, repoPath, worktreePath, force); err == nil {
			return nil
		} else if force && isOrphanedWorktreeDir(worktreePath, repoPath) {
			g.log.Warn(ctx, "removing orphaned worktree directory", "worktree", worktreePath, "repo", repoPath, "err", err.Error())
			return os.RemoveAll(worktreePath)
		} else {
			return err
		}
	}
	return g.removeWorktreeFrom(ctx, worktreePath, worktreePath, force)
}

func (g *Gateway) removeWorktreeFrom(ctx context.Context, repoPath, worktreePath string, force bool) error {
	args := []string{"-C", repoPath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)
	g.log.Debug(ctx, "git exec", "args", args)
	out, err := exec.CommandContext(ctx, g.binary, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if isExit(err) {
		g.log.Error(ctx, "git exec failed", "args", args, "err", err.Error(), "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("%w: git %v: %v: %s", usecase.ErrConflict, args, err, out)
	}
	return fmt.Errorf("git %v: %w: %s", args, err, out)
}

func repoPathFromWorktreeGitFile(worktreePath string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return "", false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return "", false
	}
	marker := string(filepath.Separator) + filepath.Join(".git", "worktrees") + string(filepath.Separator)
	gitdir = filepath.Clean(gitdir)
	idx := strings.Index(gitdir, marker)
	if idx <= 0 {
		return "", false
	}
	return gitdir[:idx], true
}

func isOrphanedWorktreeDir(worktreePath, repoPath string) bool {
	info, err := os.Stat(worktreePath)
	if err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return false
	}
	_, err = os.Stat(gitdir)
	return errors.Is(err, os.ErrNotExist)
}

func (g *Gateway) DeleteBranch(ctx context.Context, repoPath, branch string) error {
	return g.run(ctx, "-C", repoPath, "branch", "-D", branch)
}

func (g *Gateway) CurrentBranch(ctx context.Context, repoPath string) (string, error) {
	g.log.Debug(ctx, "git exec", "args", []string{"branch", "--show-current"}, "repo", repoPath)
	out, err := exec.CommandContext(ctx, g.binary, "-C", repoPath, "branch", "--show-current").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git branch --show-current: %w: %s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *Gateway) existsByExit(ctx context.Context, repoPath string, args ...string) (bool, error) {
	g.log.Debug(ctx, "git exec", "args", args, "repo", repoPath)
	full := append([]string{"-C", repoPath}, args...)
	err := exec.CommandContext(ctx, g.binary, full...).Run()
	if err == nil {
		return true, nil
	}
	if isExit(err) {
		return false, nil
	}
	return false, err
}

func (g *Gateway) run(ctx context.Context, args ...string) error {
	g.log.Debug(ctx, "git exec", "args", args)
	out, err := exec.CommandContext(ctx, g.binary, args...).CombinedOutput()
	if err != nil {
		g.log.Error(ctx, "git exec failed", "args", args, "err", err.Error(), "output", strings.TrimSpace(string(out)))
		return fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return nil
}

func isExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
