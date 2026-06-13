package usecase

import (
	"context"

	"github.com/pilot322/tmux-coder/internal/domain"
)

type SessionTypeFilter int

const (
	AnySessionType SessionTypeFilter = iota
	MainSessionType
	SecondarySessionType
	WorktreeSessionType
)

type GetSessionsInput struct {
	Type      SessionTypeFilter
	ProjectID *int
}

type SessionView struct {
	Session         *domain.Session
	Project         *domain.Project
	MainSessionName string
}

type GetSessions struct {
	projects IProjectRepository
	sessions ISessionRepository
	lock     StateLock
}

func NewGetSessions(p IProjectRepository, s ISessionRepository, l StateLock) *GetSessions {
	return &GetSessions{projects: p, sessions: s, lock: l}
}

func (uc *GetSessions) Execute(ctx context.Context, in GetSessionsInput) ([]SessionView, error) {
	var views []SessionView
	err := uc.lock.WithRead(func() error {
		sessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		projects, err := uc.projects.GetAll(ctx)
		if err != nil {
			return err
		}
		projectsByID := make(map[int]*domain.Project, len(projects))
		for _, p := range projects {
			projectsByID[p.ID()] = p
		}
		mainByProject := make(map[int]string)
		for _, s := range sessions {
			if s.Type() == domain.MainSession {
				mainByProject[s.ProjectID()] = s.Name()
			}
		}

		views = make([]SessionView, 0, len(sessions))
		for _, s := range sessions {
			if in.ProjectID != nil && s.ProjectID() != *in.ProjectID {
				continue
			}
			if in.Type != AnySessionType && !matchesSessionType(s.Type(), in.Type) {
				continue
			}
			p := projectsByID[s.ProjectID()]
			if p == nil {
				continue
			}
			views = append(views, SessionView{Session: s, Project: p, MainSessionName: mainByProject[p.ID()]})
		}
		return nil
	})
	return views, err
}

func matchesSessionType(kind domain.SessionType, filter SessionTypeFilter) bool {
	switch filter {
	case MainSessionType:
		return kind == domain.MainSession
	case SecondarySessionType:
		return kind == domain.SecondarySession
	case WorktreeSessionType:
		return kind == domain.WorktreeSession
	default:
		return true
	}
}
