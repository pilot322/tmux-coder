package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.GitWorktreeGateway = (*Gateway)(nil)

type Gateway struct {
	binary string
}

func NewGateway() *Gateway {
	return &Gateway{binary: "git"}
}

func (g *Gateway) ValidateBranchName(ctx context.Context, branch string) error {
	if branch == "" {
		return fmt.Errorf("%w: branch is required", usecase.ErrValidation)
	}
	if err := exec.CommandContext(ctx, g.binary, "check-ref-format", "--branch", branch).Run(); err != nil {
		if !isExit(err) {
			return err
		}
		return fmt.Errorf("%w: invalid branch name", usecase.ErrValidation)
	}
	return nil
}

func (g *Gateway) IsWorktreeRoot(ctx context.Context, path string) (bool, error) {
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

func (g *Gateway) AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, create bool) error {
	args := []string{"-C", repoPath, "worktree", "add"}
	if create {
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
	args := []string{"-C", worktreePath, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, worktreePath)
	out, err := exec.CommandContext(ctx, g.binary, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if isExit(err) {
		return fmt.Errorf("%w: git %v: %v: %s", usecase.ErrConflict, args, err, out)
	}
	return fmt.Errorf("git %v: %w: %s", args, err, out)
}

func (g *Gateway) DeleteBranch(ctx context.Context, repoPath, branch string) error {
	return g.run(ctx, "-C", repoPath, "branch", "-D", branch)
}

func (g *Gateway) existsByExit(ctx context.Context, repoPath string, args ...string) (bool, error) {
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
	out, err := exec.CommandContext(ctx, g.binary, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return nil
}

func isExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
