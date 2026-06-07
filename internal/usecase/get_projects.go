package usecase

import (
	"context"

	"github.com/pilot322/tmux-coder/internal/domain"
)

// ProjectView is a Project paired with its resolved main-session name, since
// the name lives on the Main Session record, not on the Project.
type ProjectView struct {
	Project         *domain.Project
	MainSessionName string
}

type GetProjects struct {
	projects IProjectRepository
	sessions ISessionRepository
	lock     StateLock
}

func NewGetProjects(p IProjectRepository, s ISessionRepository, l StateLock) *GetProjects {
	return &GetProjects{projects: p, sessions: s, lock: l}
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

		mainByProject := make(map[int]string)
		for _, s := range sessions {
			if s.Type() == domain.MainSession {
				mainByProject[s.ProjectID()] = s.Name()
			}
		}

		views = make([]ProjectView, 0, len(projects))
		for _, p := range projects {
			views = append(views, ProjectView{Project: p, MainSessionName: mainByProject[p.ID()]})
		}
		return nil
	})
	return views, err
}
