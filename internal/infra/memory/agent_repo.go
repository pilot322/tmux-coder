package memory

import (
	"context"
	"sort"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.IAgentRepository = (*MemoryAgentRepository)(nil)

type MemoryAgentRepository struct {
	agents map[int]*domain.Agent
	nextID int
}

func NewMemoryAgentRepository() *MemoryAgentRepository {
	return &MemoryAgentRepository{
		agents: make(map[int]*domain.Agent),
		nextID: 1,
	}
}

func (r *MemoryAgentRepository) Create(ctx context.Context, a *domain.Agent) (*domain.Agent, error) {
	id := r.nextID
	r.nextID++
	stored := domain.NewAgent(id, a.ProjectID(), a.SessionID(), a.Kind(), a.DisplayName(), a.TmuxPaneID(), a.PaneOwned(), a.Status(), a.StatusChangedAt())
	if stored.DisplayName() == "" {
		stored = stored.WithDisplayName(domain.DefaultAgentDisplayName(id, a.Kind()))
	}
	r.agents[id] = stored
	return stored, nil
}

func (r *MemoryAgentRepository) GetByID(ctx context.Context, id int) (*domain.Agent, error) {
	a, ok := r.agents[id]
	if !ok {
		return nil, usecase.ErrAgentNotFound
	}
	return a, nil
}

func (r *MemoryAgentRepository) GetAll(ctx context.Context) ([]*domain.Agent, error) {
	all := make([]*domain.Agent, 0, len(r.agents))
	for _, a := range r.agents {
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID() < all[j].ID() })
	return all, nil
}

func (r *MemoryAgentRepository) GetBySessionID(ctx context.Context, sessionID int) ([]*domain.Agent, error) {
	out := make([]*domain.Agent, 0)
	for _, a := range r.agents {
		if a.SessionID() == sessionID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (r *MemoryAgentRepository) Update(ctx context.Context, a *domain.Agent) (*domain.Agent, error) {
	if _, ok := r.agents[a.ID()]; !ok {
		return nil, usecase.ErrAgentNotFound
	}
	r.agents[a.ID()] = a
	return a, nil
}

func (r *MemoryAgentRepository) Delete(ctx context.Context, id int) error {
	delete(r.agents, id)
	return nil
}

func (r *MemoryAgentRepository) DeleteByProjectID(ctx context.Context, projectID int) error {
	for id, a := range r.agents {
		if a.ProjectID() == projectID {
			delete(r.agents, id)
		}
	}
	return nil
}

func (r *MemoryAgentRepository) DeleteBySessionID(ctx context.Context, sessionID int) error {
	for id, a := range r.agents {
		if a.SessionID() == sessionID {
			delete(r.agents, id)
		}
	}
	return nil
}
