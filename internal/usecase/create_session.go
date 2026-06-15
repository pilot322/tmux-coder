package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pilot322/tmux-coder/internal/config"
	"github.com/pilot322/tmux-coder/internal/domain"
)

type CreateSessionInput struct {
	ProjectID int
	Type      domain.SessionType
	Branch    string
	// CreateWorktree materializes the git worktree on disk and runs the
	// Worktree Hook. CreateBranch adds the worktree with `-b` (a new branch)
	// rather than checking out an existing one. Their combinations encode the
	// creation modes (ADR-0009): fresh (t,t), existing-branch (t,f), adopt
	// (f,f); (f,t) is rejected.
	CreateWorktree           bool
	CreateBranch             bool
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
	if in.CreateBranch && !in.CreateWorktree {
		return nil, fmt.Errorf("%w: cannot create a branch without a worktree", ErrValidation)
	}
	if !in.CreateBranch && in.BaseBranch != "" {
		return nil, fmt.Errorf("%w: baseBranch is only valid when createBranch is true", ErrValidation)
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

	// parent records the new Worktree Session's Provenance (ADR-0010). A 'w'
	// creation (ParentSessionID set) parents to and branches off a source
	// Session; a 'W' creation (no parent) branches off in.BaseBranch and stays
	// Project-level. Provenance is recorded for every creation mode, so an
	// existing-branch or adopt re-issue that carries the source keeps it as
	// parent. baseBranch is the ref a new branch is cut from; for a Main source
	// it is the checkout's current branch, resolved under CreateBranch below.
	var project *domain.Project
	var name string
	var worktreePath string
	parent := -1
	baseBranch := in.BaseBranch
	mainSource := false
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
				return &StateConflictError{Code: CodeSessionExists, Msg: "worktree session already exists for branch"}
			}
		}
		if in.ParentSessionID > 0 {
			src, err := uc.sessions.GetByID(ctx, in.ParentSessionID)
			if err != nil {
				return err
			}
			if src.ProjectID() != in.ProjectID {
				return fmt.Errorf("%w: source session belongs to a different project", ErrValidation)
			}
			switch src.Type() {
			case domain.WorktreeSession:
				baseBranch = src.Branch()
			case domain.MainSession:
				mainSource = true
			default:
				return fmt.Errorf("%w: a worktree cannot be branched from a secondary session", ErrValidation)
			}
			parent = in.ParentSessionID
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
	// baseBranch is the ref a new branch is cut from (ADR-0010), so it only
	// matters when CreateBranch is set; existing-branch and adopt modes ignore
	// it. For a Main source it is the checkout's committed current branch; a
	// detached HEAD has no branch to cut from. The branch's own existence is
	// validated per-mode below (ADR-0009).
	if in.CreateBranch {
		if mainSource {
			current, err := uc.git.CurrentBranch(ctx, project.FullPath())
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrGateway, err)
			}
			if strings.TrimSpace(current) == "" {
				return nil, fmt.Errorf("%w: cannot branch a worktree from a detached-HEAD main session", ErrValidation)
			}
			baseBranch = current
		}
		if baseBranch != "" {
			ok, err := uc.git.ResolveCommit(ctx, project.FullPath(), baseBranch)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrGateway, err)
			}
			if !ok {
				return nil, fmt.Errorf("%w: baseBranch does not resolve", ErrValidation)
			}
		}
	}

	// The derived path being a worktree of this repo (and the branch it is on)
	// is the one signal both modes are validated against (ADR-0009).
	worktrees, err := uc.git.ListWorktrees(ctx, project.FullPath())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	worktreeBranch, isWorktree := worktreeAtPath(worktrees, worktreePath)

	if in.CreateWorktree { // fresh (t,t) or existing-branch (t,f)
		switch {
		case isWorktree && worktreeBranch == in.Branch:
			return nil, &StateConflictError{Code: CodeWorktreeExists, Msg: "worktree already exists for branch"}
		case isWorktree:
			return nil, &StateConflictError{Code: CodePathBlocked, Msg: fmt.Sprintf("worktree path is checked out on %s, not %s", worktreeBranch, in.Branch)}
		}
		// The path is free of a worktree; reject if it is otherwise occupied.
		pathOccupied, err := uc.git.WorktreePathExists(ctx, worktreePath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if pathOccupied {
			return nil, &StateConflictError{Code: CodePathBlocked, Msg: "worktree path already exists"}
		}
		branchExists, err := uc.git.LocalBranchExists(ctx, project.FullPath(), in.Branch)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrGateway, err)
		}
		switch {
		case in.CreateBranch && branchExists:
			return nil, &StateConflictError{Code: CodeBranchExists, Msg: "branch already exists"}
		case !in.CreateBranch && !branchExists:
			return nil, fmt.Errorf("%w: branch does not exist", ErrConflict)
		}
	} else { // adopt (f,f) — (f,t) was rejected as a validation error above
		switch {
		case isWorktree && worktreeBranch == in.Branch:
			// Proceed: wrap the worktree already on disk (no add, no hook).
		case isWorktree:
			return nil, &StateConflictError{Code: CodePathBlocked, Msg: fmt.Sprintf("worktree at %s is on %s, not %s", worktreePath, worktreeBranch, in.Branch)}
		default:
			return nil, &StateConflictError{Code: CodePathBlocked, Msg: fmt.Sprintf("no worktree to adopt at %s", worktreePath)}
		}
	}

	branchCreated := in.CreateBranch
	worktreeCreated := false
	if in.CreateWorktree {
		if err := uc.git.AddWorktree(ctx, project.FullPath(), worktreePath, in.Branch, baseBranch, in.CreateBranch); err != nil {
			uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, true, branchCreated)
			return nil, fmt.Errorf("%w: %v", ErrGateway, err)
		}
		worktreeCreated = true
	}

	tmuxName := domain.DeriveTmuxSessionName(name)
	var hookToken string
	if in.CreateWorktree {
		hookToken, err = uc.runConfiguredWorktreeHook(ctx, project, worktreePath, name, tmuxName, in.Branch)
		if err != nil {
			uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
			return nil, err
		}
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
		s, err := uc.sessions.Create(ctx, domain.NewWorktreeSession(0, parent, project.ID(), name, in.Branch, worktreePath))
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

	// Apply the Config File's declared Secondary Sessions under this Worktree
	// Session. This runs after the on-create hook (so hook-scaffolded subdirs
	// exist) and after the Worktree Session record, so it sits inside the
	// create's rollback scope (ADR-0007). subdir resolves against the worktree
	// path, while the Config File is read from the Project path.
	if err := materializeSecondarySessions(ctx, uc.sessions, uc.tmux, uc.lock, project.FullPath(), session, worktreePath); err != nil {
		_ = uc.tmux.Kill(ctx, tmuxName)
		uc.rollbackCreatedSessionRecord(ctx, session.ID())
		uc.rollbackCreatedWorktree(ctx, project.FullPath(), worktreePath, in.Branch, worktreeCreated, branchCreated)
		return nil, err
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
		p, parentSession, root, err := secondaryParentRoot(ctx, uc.sessions, uc.projects, in.ParentSessionID)
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
		used := make(map[string]bool)
		for _, s := range sessions {
			if s.Parent() == parentSession.ID() {
				used[s.Name()] = true
			}
		}
		base := preferredName
		if base == "" {
			base = filepath.Base(relwd)
		}
		name = domain.DeriveSecondarySessionName(base, func(n string) bool { return used[n] })
		tmuxName = domain.DeriveSecondaryTmuxSessionName(parentSession.TmuxName(), name)
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

// secondaryParentRoot resolves the filesystem root a Secondary Session is
// anchored to by walking the parent chain up to the Main or Worktree session
// that ultimately roots it. A Main-rooted secondary roots at the project path; a
// Worktree-rooted secondary roots at the worktree checkout. Its working
// directory is this root joined with the secondary's relative working directory.
// Both creation (create_session.go) and healing (create_project.go reconcile)
// resolve the root through here so they agree on where a secondary lives.
func secondaryParentRoot(ctx context.Context, sessions ISessionRepository, projects IProjectRepository, parentID int) (*domain.Project, *domain.Session, string, error) {
	parent, err := sessions.GetByID(ctx, parentID)
	if err != nil {
		return nil, nil, "", err
	}
	project, err := projects.GetByID(ctx, parent.ProjectID())
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
			s, err = sessions.GetByID(ctx, s.Parent())
			if err != nil {
				return nil, nil, "", err
			}
		default:
			return nil, nil, "", fmt.Errorf("%w: unsupported parent session type", ErrValidation)
		}
	}
	return nil, nil, "", fmt.Errorf("%w: secondary parent chain is invalid", ErrValidation)
}

// sessionDepth counts how deep a session sits below its nearest Worktree or Main
// root, with that root counted as depth 1. The climb stops at the first
// Worktree/Main ancestor so that nesting worktrees (ADR-0010) does not consume
// the Secondary depth budget measured from each root (ADR-0006).
func (uc *CreateSession) sessionDepth(ctx context.Context, sessionID int) (int, error) {
	depth := 0
	for id := sessionID; id > 0; {
		s, err := uc.sessions.GetByID(ctx, id)
		if err != nil {
			return 0, err
		}
		depth++
		if s.Type() == domain.WorktreeSession || s.Type() == domain.MainSession {
			break
		}
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
	cfg, err := config.Load(project.FullPath())
	if err != nil {
		return "", translateConfigErr(err)
	}
	scriptPath, err := resolveWorktreeHookScript(project.FullPath(), cfg.Worktree.OnCreateScript)
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
		Timeout:    cfg.Worktree.OnCreateTimeout,
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

// worktreeAtPath reports whether target is one of repo's worktrees and, if so,
// the branch it is checked out on. Both sides are canonicalized before
// comparison because `git worktree list` emits symlink-resolved absolute paths.
func worktreeAtPath(refs []WorktreeRef, target string) (branch string, ok bool) {
	want := canonicalPath(target)
	for _, r := range refs {
		if canonicalPath(r.Path) == want {
			return r.Branch, true
		}
	}
	return "", false
}

func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

func (uc *CreateSession) rollbackCreatedWorktree(ctx context.Context, repoPath, worktreePath, branch string, worktreeCreated, branchCreated bool) {
	if worktreeCreated {
		_ = uc.git.RemoveWorktree(ctx, worktreePath, true)
	}
	if branchCreated {
		_ = uc.git.DeleteBranch(ctx, repoPath, branch)
	}
}
