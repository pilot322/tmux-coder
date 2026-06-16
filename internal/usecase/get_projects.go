package usecase

import (
	"context"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
)

// ProjectView is a Project paired with its resolved main-session name, since
// the name lives on the Main Session record, not on the Project.
type ProjectView struct {
	Project             *domain.Project
	MainSessionName     string
	MainTmuxSessionName string
}

type GetProjects struct {
	projects IProjectRepository
	sessions ISessionRepository
	lock     StateLock
	log      obs.Logger
}

func NewGetProjects(p IProjectRepository, s ISessionRepository, l StateLock, log obs.Logger) *GetProjects {
	return &GetProjects{projects: p, sessions: s, lock: l, log: log.With("component", "get-projects")}
}

// Execute returns every Project with its main-session name. It reads both
// repositories under a single read lock and joins them in memory.
func (uc *GetProjects) Execute(ctx context.Context) ([]ProjectView, error) {
	var views []ProjectView
	err := uc.lock.WithRead(func() error {
		projects, err := uc.projects.GetAll(ctx)
		if err != nil {
			return err
		}
		sessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}

		mainByProject := make(map[int]*domain.Session)
		for _, s := range sessions {
			if s.Type() == domain.MainSession {
				mainByProject[s.ProjectID()] = s
			}
		}

		views = make([]ProjectView, 0, len(projects))
		for _, p := range projects {
			main := mainByProject[p.ID()]
			view := ProjectView{Project: p}
			if main != nil {
				view.MainSessionName = main.Name()
				view.MainTmuxSessionName = main.TmuxName()
			}
			views = append(views, view)
		}
		return nil
	})
	return views, err
}
