package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type CreateSessionInput struct {
	ProjectID                int
	Type                     domain.SessionType
	Branch                   string
	Create                   bool
	BaseBranch               string
	ParentSessionID          int
	RelativeWorkingDirectory string
	PreferredName            string
	OnDelete                 string
}

type CreateSession struct {
	projects IProjectRepository
	sessions ISessionRepository
	tmux     SessionGateway
	git      GitWorktreeGateway
	lock     StateLock
	hooks    WorktreeHookRunner
	leases   ResourceLeaseRepository
}

func NewCreateSession(p IProjectRepository, s ISessionRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock) *CreateSession {
	return NewCreateSessionWithHooks(p, s, tmux, git, l, nil, nil)
}

func NewCreateSessionWithHooks(p IProjectRepository, s ISessionRepository, tmux SessionGateway, git GitWorktreeGateway, l StateLock, hooks WorktreeHookRunner, leases ResourceLeaseRepository) *CreateSession {
	if hooks == nil {
		hooks = missingWorktreeHookRunner{}
	}
	if leases == nil {
		leases = noopResourceLeaseRepository{}
	}
	return &CreateSession{projects: p, sessions: s, tmux: tmux, git: git, lock: l, hooks: hooks, leases: leases}
}

func (uc *CreateSession) Execute(ctx context.Context, in CreateSessionInput) (*domain.Session, error) {
	if in.Type == domain.SecondarySession {
		return uc.createSecondary(ctx, in)
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
	if err := reconcileWorktreeSessions(ctx, uc.sessions, uc.git, uc.tmux, uc.lock, uc.leases); err != nil {
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
	hookToken, err := uc.runConfiguredWorktreeHook(ctx, project, worktreePath, name, tmuxName, in.Branch)
	if err != nil {
		uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
		return nil, err
	}
	hookPromoted := false
	if hookToken != "" {
		defer func() {
			if !hookPromoted {
				_ = uc.leases.ReleaseHookLeases(ctx, hookToken)
				_ = uc.leases.EndHook(ctx, hookToken)
			}
		}()
	}

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
	if hookToken != "" {
		if err := uc.leases.PromoteHookLeases(ctx, hookToken, session.ID()); err != nil {
			_ = uc.tmux.Kill(ctx, tmuxName)
			uc.rollbackCreatedSessionRecord(ctx, session.ID())
			uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
			return nil, err
		}
		if err := uc.leases.EndHook(ctx, hookToken); err != nil {
			_ = uc.tmux.Kill(ctx, tmuxName)
			uc.rollbackCreatedSessionRecord(ctx, session.ID())
			uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
			return nil, err
		}
		hookPromoted = true
	}

	return session, nil
}

func (uc *CreateSession) createSecondary(ctx context.Context, in CreateSessionInput) (*domain.Session, error) {
	if in.ParentSessionID <= 0 {
		return nil, fmt.Errorf("%w: parentSessionId is required", ErrValidation)
	}
	onDelete := in.OnDelete
	if onDelete == "" {
		onDelete = "cascade"
	}
	if onDelete != "cascade" && onDelete != "inherit" {
		return nil, fmt.Errorf("%w: onDelete must be cascade or inherit", ErrValidation)
	}
	relwd, err := normalizeRelativeWorkingDirectory(in.RelativeWorkingDirectory)
	if err != nil {
		return nil, err
	}
	preferredName := strings.TrimSpace(in.PreferredName)
	if relwd == "" && preferredName == "" {
		return nil, fmt.Errorf("%w: preferredName is required when relativeWorkingDirectory is empty", ErrValidation)
	}

	var project *domain.Project
	var parent *domain.Session
	var name string
	var tmuxName string
	var workingDir string
	if err := uc.lock.WithRead(func() error {
		p, parentSession, root, err := uc.secondaryParentRoot(ctx, in.ParentSessionID)
		if err != nil {
			return err
		}
		if in.ProjectID != 0 && in.ProjectID != parentSession.ProjectID() {
			return fmt.Errorf("%w: projectId must match parent session project", ErrValidation)
		}
		if depth, err := uc.sessionDepth(ctx, parentSession.ID()); err != nil {
			return err
		} else if depth >= 5 {
			return fmt.Errorf("%w: maximum session depth exceeded", ErrValidation)
		}
		project = p
		parent = parentSession
		workingDir = filepath.Join(root, relwd)
		sessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		used := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			used[s.Name()] = true
		}
		base := preferredName
		if base == "" {
			base = filepath.Base(relwd)
		}
		name = domain.DeriveSecondarySessionName(base, func(n string) bool { return used[n] })
		tmuxName = domain.DeriveSecondaryTmuxSessionName(project.FullPath(), name)
		return nil
	}); err != nil {
		return nil, err
	}

	info, err := os.Stat(workingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: relativeWorkingDirectory must exist", ErrValidation)
		}
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: relativeWorkingDirectory must be a directory", ErrValidation)
	}

	if err := uc.tmux.Create(ctx, tmuxName, workingDir); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}

	var session *domain.Session
	if err := uc.lock.WithWrite(func() error {
		s, err := uc.sessions.Create(ctx, domain.NewSecondarySessionWithTmuxName(0, parent.ID(), project.ID(), name, tmuxName, relwd, onDelete))
		session = s
		return err
	}); err != nil {
		_ = uc.tmux.Kill(ctx, tmuxName)
		return nil, err
	}
	return session, nil
}

func normalizeRelativeWorkingDirectory(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "." {
		return "", nil
	}
	if filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%w: relativeWorkingDirectory must be relative", ErrValidation)
	}
	clean := filepath.Clean(trimmed)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: relativeWorkingDirectory must not escape the session root", ErrValidation)
	}
	return clean, nil
}

func (uc *CreateSession) secondaryParentRoot(ctx context.Context, parentID int) (*domain.Project, *domain.Session, string, error) {
	parent, err := uc.sessions.GetByID(ctx, parentID)
	if err != nil {
		return nil, nil, "", err
	}
	project, err := uc.projects.GetByID(ctx, parent.ProjectID())
	if err != nil {
		return nil, nil, "", err
	}
	root := project.FullPath()
	for s := parent; s != nil; {
		switch s.Type() {
		case domain.MainSession:
			return project, parent, root, nil
		case domain.WorktreeSession:
			return project, parent, s.WorktreePath(), nil
		case domain.SecondarySession:
			if s.Parent() <= 0 {
				return nil, nil, "", fmt.Errorf("%w: secondary parent chain is invalid", ErrValidation)
			}
			s, err = uc.sessions.GetByID(ctx, s.Parent())
			if err != nil {
				return nil, nil, "", err
			}
		default:
			return nil, nil, "", fmt.Errorf("%w: unsupported parent session type", ErrValidation)
		}
	}
	return nil, nil, "", fmt.Errorf("%w: secondary parent chain is invalid", ErrValidation)
}

func (uc *CreateSession) sessionDepth(ctx context.Context, sessionID int) (int, error) {
	depth := 0
	for id := sessionID; id > 0; {
		s, err := uc.sessions.GetByID(ctx, id)
		if err != nil {
			return 0, err
		}
		depth++
		id = s.Parent()
	}
	return depth, nil
}

func (uc *CreateSession) rollbackCreatedSessionRecord(ctx context.Context, sessionID int) {
	_ = uc.lock.WithWrite(func() error {
		_ = uc.leases.ReleaseSessionLeases(ctx, sessionID)
		return uc.sessions.Delete(ctx, sessionID)
	})
}

func (uc *CreateSession) runConfiguredWorktreeHook(ctx context.Context, project *domain.Project, worktreePath, sessionName, tmuxName, branch string) (string, error) {
	cfg, err := loadWorktreeHookConfig(project.FullPath())
	if err != nil {
		return "", err
	}
	scriptPath, err := resolveWorktreeHookScript(project.FullPath(), cfg.Script)
	if err != nil {
		return "", err
	}
	if scriptPath == "" {
		return "", nil
	}
	token, err := newWorktreeHookToken()
	if err != nil {
		return "", err
	}
	if err := uc.leases.BeginHook(ctx, token, HookLeaseOwner{
		ProjectID:       project.ID(),
		SessionName:     sessionName,
		TmuxSessionName: tmuxName,
		Branch:          branch,
		WorktreePath:    worktreePath,
	}); err != nil {
		return "", err
	}
	result, err := uc.hooks.Run(ctx, WorktreeHookRequest{
		ScriptPath: scriptPath,
		WorkingDir: worktreePath,
		Timeout:    cfg.Timeout,
		Env:        worktreeHookEnv(project.FullPath(), worktreePath, project.ID(), sessionName, tmuxName, branch, token),
	})
	if err != nil {
		_ = uc.leases.ReleaseHookLeases(ctx, token)
		_ = uc.leases.EndHook(ctx, token)
		if result.Output != "" {
			return "", fmt.Errorf("%w: worktree hook failed: %v: %s", ErrGateway, err, result.Output)
		}
		return "", fmt.Errorf("%w: worktree hook failed: %v", ErrGateway, err)
	}
	return token, nil
}

func (uc *CreateSession) rollbackCreatedWorktree(ctx context.Context, repoPath, worktreePath, branch string, worktreeCreated, branchCreated bool) {
	if worktreeCreated {
		_ = uc.git.RemoveWorktree(ctx, worktreePath, true)
	}
	if branchCreated {
		_ = uc.git.DeleteBranch(ctx, repoPath, branch)
	}
}
