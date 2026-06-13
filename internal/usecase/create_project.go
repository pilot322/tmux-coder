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
	lock     StateLock
	config   domain.DaemonConfig
}

func NewCreateProject(p IProjectRepository, s ISessionRepository, g SessionGateway, l StateLock, c domain.DaemonConfig) *CreateProject {
	return &CreateProject{projects: p, sessions: s, gateway: g, lock: l, config: c}
}

// Execute creates a Project for fullPath, or reconciles an existing one.
//
// New project: under the write lock it dedupes, reserves a unique main-session
// name and inserts the records; it then creates the tmux session OUTSIDE the
// lock (ADR-0003) and rolls the records back if that fails. Existing project:
// it reconciles the project's tmux sessions and returns Created=false.
func (uc *CreateProject) Execute(ctx context.Context, in CreateProjectInput) (CreateProjectResult, error) {
	var existing, project *domain.Project
	var session *domain.Session

	err := uc.lock.WithWrite(func() error {
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
		return CreateProjectResult{Project: existing, MainSessionName: main.Name(), MainTmuxSessionName: main.TmuxName(), Created: false}, nil
	}

	if err := uc.gateway.Create(ctx, session.TmuxName(), project.FullPath()); err != nil {
		uc.rollback(ctx, project.ID(), session.ID())
		return CreateProjectResult{}, fmt.Errorf("%w: %v", ErrGateway, err)
	}

	return CreateProjectResult{Project: project, MainSessionName: session.Name(), MainTmuxSessionName: session.TmuxName(), Created: true}, nil
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

// reconcile recreates any of the project's tmux sessions that have gone
// missing (presence/absence only). Records are read under the lock; the tmux
// execs run outside it.
func (uc *CreateProject) reconcile(ctx context.Context, project *domain.Project) error {
	var sessions []*domain.Session
	if err := uc.lock.WithRead(func() error {
		s, err := uc.sessions.GetByProjectID(ctx, project.ID())
		sessions = s
		return err
	}); err != nil {
		return err
	}

	for _, s := range sessions {
		if s.Type() == domain.WorktreeSession {
			continue
		}
		exists, err := uc.gateway.Exists(ctx, s.TmuxName())
		if err != nil {
			return fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if exists {
			continue
		}
		if err := uc.gateway.Create(ctx, s.TmuxName(), project.FullPath()); err != nil {
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

func (uc *CreateProject) rollback(ctx context.Context, projectID, sessionID int) {
	_ = uc.lock.WithWrite(func() error {
		_ = uc.sessions.Delete(ctx, sessionID)
		_ = uc.projects.Delete(ctx, projectID)
		return nil
	})
}
