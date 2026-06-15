package memory

import (
	"context"
	"sort"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.ISessionRepository = (*MemorySessionRepository)(nil)

type MemorySessionRepository struct {
	sessions map[int]*domain.Session
	nextID   int
}

func NewMemorySessionRepository() *MemorySessionRepository {
	return &MemorySessionRepository{
		sessions: make(map[int]*domain.Session),
		nextID:   1,
	}
}

func (r *MemorySessionRepository) Create(ctx context.Context, s *domain.Session) (*domain.Session, error) {
	id := r.nextID
	r.nextID++
	stored := domain.NewSession(id, s.Parent(), s.ProjectID(), s.Name(), s.Type())
	if s.Type() == domain.WorktreeSession {
		stored = domain.NewWorktreeSession(id, s.Parent(), s.ProjectID(), s.Name(), s.Branch(), s.WorktreePath())
	} else if s.Type() == domain.SecondarySession {
		stored = domain.NewSecondarySessionWithTmuxName(id, s.Parent(), s.ProjectID(), s.Name(), s.TmuxName(), s.RelativeWorkingDirectory(), s.OnDelete())
	}
	r.sessions[id] = stored
	return stored, nil
}

func (r *MemorySessionRepository) GetByID(ctx context.Context, id int) (*domain.Session, error) {
	s, ok := r.sessions[id]
	if !ok {
		return nil, usecase.ErrSessionNotFound
	}
	return s, nil
}

func (r *MemorySessionRepository) GetAll(ctx context.Context) ([]*domain.Session, error) {
	all := make([]*domain.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		all = append(all, s)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID() < all[j].ID() })
	return all, nil
}

func (r *MemorySessionRepository) GetByProjectID(ctx context.Context, projectID int) ([]*domain.Session, error) {
	out := make([]*domain.Session, 0)
	for _, s := range r.sessions {
		if s.ProjectID() == projectID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (r *MemorySessionRepository) Update(ctx context.Context, s *domain.Session) (*domain.Session, error) {
	if _, ok := r.sessions[s.ID()]; !ok {
		return nil, usecase.ErrSessionNotFound
	}
	stored := domain.NewSession(s.ID(), s.Parent(), s.ProjectID(), s.Name(), s.Type())
	if s.Type() == domain.WorktreeSession {
		stored = domain.NewWorktreeSession(s.ID(), s.Parent(), s.ProjectID(), s.Name(), s.Branch(), s.WorktreePath())
	} else if s.Type() == domain.SecondarySession {
		stored = domain.NewSecondarySessionWithTmuxName(s.ID(), s.Parent(), s.ProjectID(), s.Name(), s.TmuxName(), s.RelativeWorkingDirectory(), s.OnDelete())
	}
	r.sessions[s.ID()] = stored
	return stored, nil
}

func (r *MemorySessionRepository) Delete(ctx context.Context, id int) error {
	delete(r.sessions, id)
	return nil
}
