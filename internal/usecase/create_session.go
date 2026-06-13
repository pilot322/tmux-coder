package usecase

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type CreateSessionInput struct {
	ProjectID  int
	Type       domain.SessionType
	Branch     string
	Create     bool
	BaseBranch string
}

type CreateSession struct {
	projects IProjectRepository
	sessions ISessionRepository
	tmux     SessionGateway
	git      GitWorktreeGateway
	lock     StateLock
}

func NewCreateSession(p IProjectRepository, s ISessionRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock) *CreateSession {
	return &CreateSession{projects: p, sessions: s, tmux: tmux, git: git, lock: l}
}

func (uc *CreateSession) Execute(ctx context.Context, in CreateSessionInput) (*domain.Session, error) {
	if in.Type == domain.SecondarySession {
		return nil, ErrNotImplemented
	}
	if in.Type == domain.MainSession {
		return nil, fmt.Errorf("%w: main sessions cannot be created through /sessions", ErrValidation)
	}
	if in.Type != domain.WorktreeSession {
		return nil, fmt.Errorf("%w: unsupported session type", ErrValidation)
	}
	if in.ProjectID == 0 {
		return nil, fmt.Errorf("%w: projectId is required", ErrValidation)
	}
	if in.Branch == "" {
		return nil, fmt.Errorf("%w: branch is required", ErrValidation)
	}
	if !in.Create && in.BaseBranch != "" {
		return nil, fmt.Errorf("%w: baseBranch is only valid when create is true", ErrValidation)
	}
	if err := uc.git.ValidateBranchName(ctx, in.Branch); err != nil {
		if errors.Is(err, ErrValidation) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	if err := reconcileWorktreeSessions(ctx, uc.sessions, uc.git, uc.tmux, uc.lock); err != nil {
		return nil, err
	}

	var project *domain.Project
	var name string
	var worktreePath string
	if err := uc.lock.WithWrite(func() error {
		p, err := uc.projects.GetByID(ctx, in.ProjectID)
		if err != nil {
			return err
		}
		project = p
		sessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		used := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			used[s.Name()] = true
			if s.Type() == domain.WorktreeSession && s.ProjectID() == in.ProjectID && s.Branch() == in.Branch {
				return fmt.Errorf("%w: worktree session already exists for branch", ErrConflict)
			}
		}
		name = domain.DeriveWorktreeSessionName(project.FullPath(), in.Branch, func(n string) bool { return used[n] })
		worktreePath = filepath.Join(filepath.Dir(project.FullPath()), name)
		return nil
	}); err != nil {
		return nil, err
	}

	root, err := uc.git.IsWorktreeRoot(ctx, project.FullPath())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	if !root {
		return nil, fmt.Errorf("%w: project path must be a Git worktree root", ErrValidation)
	}
	branchExists, err := uc.git.LocalBranchExists(ctx, project.FullPath(), in.Branch)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	if in.Create && branchExists {
		return nil, fmt.Errorf("%w: branch already exists", ErrConflict)
	}
	if !in.Create && !branchExists {
		return nil, fmt.Errorf("%w: branch does not exist", ErrConflict)
	}
	if in.BaseBranch != "" {
		ok, err := uc.git.ResolveCommit(ctx, project.FullPath(), in.BaseBranch)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if !ok {
			return nil, fmt.Errorf("%w: baseBranch does not resolve", ErrValidation)
		}
	}
	pathExists, err := uc.git.WorktreePathExists(ctx, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	if pathExists {
		return nil, fmt.Errorf("%w: worktree path already exists", ErrConflict)
	}

	branchCreated := in.Create
	if err := uc.git.AddWorktree(ctx, project.FullPath(), worktreePath, in.Branch, in.BaseBranch, in.Create); err != nil {
		uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, true, branchCreated)
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	worktreeCreated := true

	tmuxName := domain.DeriveTmuxSessionName(name)
	if err := uc.tmux.Create(ctx, tmuxName, worktreePath); err != nil {
		uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}

	var session *domain.Session
	if err := uc.lock.WithWrite(func() error {
		s, err := uc.sessions.Create(ctx, domain.NewWorktreeSession(0, project.ID(), name, in.Branch, worktreePath))
		session = s
		return err
	}); err != nil {
		_ = uc.tmux.Kill(ctx, tmuxName)
		uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
		return nil, err
	}

	return session, nil
}

func (uc *CreateSession) rollbackCreatedWorktree(ctx context.Context, repoPath, worktreePath, branch string, worktreeCreated, branchCreated bool) {
	if worktreeCreated {
		_ = uc.git.RemoveWorktree(ctx, worktreePath, true)
	}
	if branchCreated {
		_ = uc.git.DeleteBranch(ctx, repoPath, branch)
	}
}
