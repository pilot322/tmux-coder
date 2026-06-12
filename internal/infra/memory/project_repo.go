// Package memory holds the in-memory adapters: the project and session
// repositories and DaemonState, which bundles them with the RWMutex behind
// usecase.StateLock. Nothing here persists.
//
// The repositories are not internally synchronized; callers access them inside
// a StateLock closure (ADR-0003), so the maps are plain.
package memory

import (
	"context"
	"sort"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.IProjectRepository = (*MemoryProjectRepository)(nil)

type MemoryProjectRepository struct {
	projects map[int]*domain.Project
	nextID   int
}

func NewMemoryProjectRepository() *MemoryProjectRepository {
	return &MemoryProjectRepository{
		projects: make(map[int]*domain.Project),
		nextID:   1, // 0 means "unassigned"
	}
}

// Create assigns the next id and stores a fresh Project, ignoring the caller's
// id, and returns the stored value.
func (r *MemoryProjectRepository) Create(ctx context.Context, p *domain.Project) (*domain.Project, error) {
	id := r.nextID
	r.nextID++
	stored := domain.NewProject(id, p.FullPath(), p.Title())
	r.projects[id] = stored
	return stored, nil
}

func (r *MemoryProjectRepository) GetByID(ctx context.Context, id int) (*domain.Project, error) {
	p, ok := r.projects[id]
	if !ok {
		return nil, usecase.ErrProjectNotFound
	}
	return p, nil
}

func (r *MemoryProjectRepository) GetByFullPath(ctx context.Context, fullPath string) (*domain.Project, error) {
	for _, p := range r.projects {
		if p.FullPath() == fullPath {
			return p, nil
		}
	}
	return nil, usecase.ErrProjectNotFound
}

func (r *MemoryProjectRepository) GetAll(ctx context.Context) ([]*domain.Project, error) {
	all := make([]*domain.Project, 0, len(r.projects))
	for _, p := range r.projects {
		all = append(all, p)
	}
	// Stable output; map iteration order is randomized.
	sort.Slice(all, func(i, j int) bool { return all[i].ID() < all[j].ID() })
	return all, nil
}

func (r *MemoryProjectRepository) Delete(ctx context.Context, id int) error {
	delete(r.projects, id)
	return nil
}
