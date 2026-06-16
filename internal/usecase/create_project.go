package usecase

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type CreateProjectInput struct {
	FullPath string
	Title    *string
	// CreateWorktreeSessions is the tri-state decision for adopting any
	// un-adopted worktrees the repo has on disk (ADR-0013): nil means "no
	// decision" and yields a PreconditionRequiredError when such worktrees
	// exist; true bulk-adopts them as parentless Worktree Sessions; false opens
	// normally and skips them. It is a no-op when no un-adopted worktrees exist.
	CreateWorktreeSessions *bool
}

type CreateProjectResult struct {
	Project             *domain.Project
	MainSessionName     string
	MainTmuxSessionName string
	Created             bool // true if newly created, false if it already existed
}

type CreateProject struct {
	projects IProjectRepository
	sessions ISessionRepository
	gateway  SessionGateway
	git      GitWorktreeGateway
	lock     StateLock
	config   domain.DaemonConfig
}

func NewCreateProject(p IProjectRepository, s ISessionRepository, g SessionGateway, git GitWorktreeGateway, l StateLock, c domain.DaemonConfig) *CreateProject {
	return &CreateProject{projects: p, sessions: s, gateway: g, git: git, lock: l, config: c}
}

// Execute creates a Project for fullPath, or reconciles an existing one.
//
// New project: under the write lock it dedupes, reserves a unique main-session
// name and inserts the records; it then creates the tmux session OUTSIDE the
// lock (ADR-0003) and rolls the records back if that fails. Existing project:
// it reconciles the project's tmux sessions and returns Created=false.
func (uc *CreateProject) Execute(ctx context.Context, in CreateProjectInput) (CreateProjectResult, error) {
	// Detection is read-only and runs before any record is written, so a
	// rejected open has zero side effects (ADR-0013).
	detected, err := uc.adoptableWorktrees(ctx, in.FullPath)
	if err != nil {
		return CreateProjectResult{}, err
	}
	if len(detected) > 0 && in.CreateWorktreeSessions == nil {
		return CreateProjectResult{}, &PreconditionRequiredError{
			Code:      CodeWorktreesDetected,
			Msg:       "worktrees detected that are not managed as worktree sessions",
			Worktrees: detected,
		}
	}

	var existing, project *domain.Project
	var session *domain.Session

	err = uc.lock.WithWrite(func() error {
		if p, err := uc.projects.GetByFullPath(ctx, in.FullPath); err == nil {
			existing = p
			return nil
		} else if !errors.Is(err, ErrProjectNotFound) {
			return err
		}
		title, err := uc.projectTitle(in)
		if err != nil {
			return err
		}

		created, err := uc.projects.Create(ctx, domain.NewProject(0, in.FullPath, title))
		if err != nil {
			return err
		}

		name, err := uc.reserveMainSessionName(ctx, in.FullPath)
		if err != nil {
			return err
		}

		s, err := uc.sessions.Create(ctx, domain.NewSession(0, -1, created.ID(), name, domain.MainSession))
		if err != nil {
			return err
		}
		project, session = created, s
		return nil
	})
	if err != nil {
		return CreateProjectResult{}, err
	}

	if existing != nil {
		if err := uc.reconcile(ctx, existing); err != nil {
			return CreateProjectResult{}, err
		}
		main, err := uc.mainSession(ctx, existing.ID())
		if err != nil {
			return CreateProjectResult{}, err
		}
		if uc.shouldAdoptWorktrees(in) {
			uc.adoptWorktrees(ctx, existing, detected)
		}
		return CreateProjectResult{Project: existing, MainSessionName: main.Name(), MainTmuxSessionName: main.TmuxName(), Created: false}, nil
	}

	if err := uc.gateway.Create(ctx, session.TmuxName(), project.FullPath()); err != nil {
		uc.rollback(ctx, project.ID(), session.ID(), session.TmuxName())
		return CreateProjectResult{}, fmt.Errorf("%w: %v", ErrGateway, err)
	}

	// Apply the Config File's declared Secondary Sessions under the Main
	// Session. Materialization is the final in-flow step so it sits inside the
	// create's rollback scope (ADR-0007).
	if err := materializeSecondarySessions(ctx, uc.sessions, uc.gateway, uc.lock, project.FullPath(), session, project.FullPath()); err != nil {
		uc.rollback(ctx, project.ID(), session.ID(), session.TmuxName())
		return CreateProjectResult{}, err
	}

	if uc.shouldAdoptWorktrees(in) {
		uc.adoptWorktrees(ctx, project, detected)
	}

	return CreateProjectResult{Project: project, MainSessionName: session.Name(), MainTmuxSessionName: session.TmuxName(), Created: true}, nil
}

func (uc *CreateProject) shouldAdoptWorktrees(in CreateProjectInput) bool {
	return in.CreateWorktreeSessions != nil && *in.CreateWorktreeSessions
}

func (uc *CreateProject) adoptWorktrees(ctx context.Context, project *domain.Project, refs []WorktreeRef) {
	used := make(map[string]bool)
	if err := uc.lock.WithRead(func() error {
		sessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			used[s.Name()] = true
		}
		return nil
	}); err != nil {
		return
	}

	for _, r := range refs {
		name := domain.DeriveWorktreeSessionName(project.FullPath(), r.Branch, func(n string) bool { return used[n] })
		used[name] = true
		tmuxName := domain.DeriveTmuxSessionName(name)
		if err := uc.gateway.Create(ctx, tmuxName, r.Path); err != nil {
			continue
		}
		_ = uc.lock.WithWrite(func() error {
			_, err := uc.sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), name, r.Branch, r.Path))
			return err
		})
	}
}

// adoptableWorktrees returns the repo's worktrees that an open could adopt as
// Worktree Sessions (ADR-0013): `git worktree list` minus the primary working
// tree (the project root). A repo-less path (or a transient git failure) has no
// detectable worktrees, so the open proceeds without offering adoption rather
// than failing.
func (uc *CreateProject) adoptableWorktrees(ctx context.Context, fullPath string) ([]WorktreeRef, error) {
	refs, err := uc.git.ListWorktrees(ctx, fullPath)
	if err != nil {
		return nil, nil
	}
	root := canonicalPath(fullPath)
	adopted := make(map[string]bool)
	if err := uc.lock.WithRead(func() error {
		project, err := uc.projects.GetByFullPath(ctx, fullPath)
		if errors.Is(err, ErrProjectNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		sessions, err := uc.sessions.GetByProjectID(ctx, project.ID())
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if s.Type() == domain.WorktreeSession {
				adopted[canonicalPath(s.WorktreePath())] = true
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var out []WorktreeRef
	for _, r := range refs {
		path := canonicalPath(r.Path)
		if path == root || adopted[path] || r.Detached {
			continue
		}
		exists, err := uc.git.WorktreePathExists(ctx, r.Path)
		if err != nil || !exists {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (uc *CreateProject) projectTitle(in CreateProjectInput) (string, error) {
	limit := uc.config.ProjectTitleLimit()
	if in.Title != nil {
		return domain.CleanProjectTitle(*in.Title, limit)
	}
	return domain.DefaultProjectTitle(filepath.Base(in.FullPath), limit), nil
}

// reserveMainSessionName derives a name unique among existing session names.
// It must run inside the write lock so two concurrent creates can't pick the
// same name (ADR-0004).
func (uc *CreateProject) reserveMainSessionName(ctx context.Context, fullPath string) (string, error) {
	sessions, err := uc.sessions.GetAll(ctx)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		used[s.Name()] = true
	}
	return domain.DeriveMainSessionName(fullPath, func(n string) bool { return used[n] }), nil
}

// healTarget is a tmux session reconcile may need to recreate, paired with the
// working directory it must be recreated in.
type healTarget struct {
	tmuxName   string
	workingDir string
}

// reconcile recreates any of the project's tmux sessions that have gone
// missing (presence/absence only). Records are read and each session's working
// directory resolved under the lock; the tmux execs run outside it (ADR-0003).
//
// A Main Session heals at the project root. A Secondary Session heals at its
// stored relative working directory joined to the root it is anchored to (the
// project for a Main-rooted secondary, the worktree checkout for a
// Worktree-rooted one) — resolving the root needs id lookups along the parent
// chain, which is why the working dirs are computed while the lock is held.
// Worktree Sessions are reconciled separately and are skipped here.
func (uc *CreateProject) reconcile(ctx context.Context, project *domain.Project) error {
	var targets []healTarget
	if err := uc.lock.WithRead(func() error {
		sessions, err := uc.sessions.GetByProjectID(ctx, project.ID())
		if err != nil {
			return err
		}
		for _, s := range sessions {
			switch s.Type() {
			case domain.MainSession:
				targets = append(targets, healTarget{s.TmuxName(), project.FullPath()})
			case domain.SecondarySession:
				_, _, root, err := secondaryParentRoot(ctx, uc.sessions, uc.projects, s.Parent())
				if err != nil {
					return err
				}
				targets = append(targets, healTarget{s.TmuxName(), filepath.Join(root, s.RelativeWorkingDirectory())})
			}
		}
		return nil
	}); err != nil {
		return err
	}

	for _, t := range targets {
		exists, err := uc.gateway.Exists(ctx, t.tmuxName)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if exists {
			continue
		}
		if err := uc.gateway.Create(ctx, t.tmuxName, t.workingDir); err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
	}
	return nil
}

func (uc *CreateProject) mainSession(ctx context.Context, projectID int) (*domain.Session, error) {
	var main *domain.Session
	err := uc.lock.WithRead(func() error {
		sessions, err := uc.sessions.GetByProjectID(ctx, projectID)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if s.Type() == domain.MainSession {
				main = s
				return nil
			}
		}
		return nil
	})
	return main, err
}

// rollback undoes a failed create. The Main Session's tmux target is killed
// outside the lock (ADR-0003); a kill of a never-created session is a harmless
// no-op. materializeSecondarySessions has already unwound any Secondary
// Sessions, so only the Main Session and Project records remain to delete.
func (uc *CreateProject) rollback(ctx context.Context, projectID, sessionID int, tmuxName string) {
	_ = uc.gateway.Kill(ctx, tmuxName)
	_ = uc.lock.WithWrite(func() error {
		_ = uc.sessions.Delete(ctx, sessionID)
		_ = uc.projects.Delete(ctx, projectID)
		return nil
	})
}
