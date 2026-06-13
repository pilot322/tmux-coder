package memory

import (
	"context"
	"sync"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.ResourceLeaseRepository = (*MemoryResourceLeaseRepository)(nil)

type MemoryResourceLeaseRepository struct {
	mu     sync.Mutex
	hooks  map[string]usecase.HookLeaseOwner
	leases []memoryPortLease
}

type memoryPortLease struct {
	projectID int
	ownerKind usecase.ResourceLeaseOwnerKind
	hookToken string
	sessionID int
	key       string
	port      int
}

func NewMemoryResourceLeaseRepository() *MemoryResourceLeaseRepository {
	return &MemoryResourceLeaseRepository{hooks: make(map[string]usecase.HookLeaseOwner)}
}

func (r *MemoryResourceLeaseRepository) BeginHook(ctx context.Context, token string, owner usecase.HookLeaseOwner) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.hooks[token]; exists {
		return usecase.ErrConflict
	}
	r.hooks[token] = owner
	return nil
}

func (r *MemoryResourceLeaseRepository) EndHook(ctx context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hooks, token)
	return nil
}

func (r *MemoryResourceLeaseRepository) AcquirePort(ctx context.Context, req usecase.PortLeaseRequest, portAvailable func(int) bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	projectID := req.ProjectID
	if req.OwnerKind == usecase.ResourceLeaseOwnerHook {
		hook, ok := r.hooks[req.HookToken]
		if !ok {
			return 0, usecase.ErrValidation
		}
		projectID = hook.ProjectID
	}

	for _, lease := range r.leases {
		if lease.projectID == projectID && lease.ownerKind == req.OwnerKind && lease.key == req.Key && leaseOwnerMatches(lease, req) {
			return lease.port, nil
		}
	}

	used := make(map[int]bool, len(r.leases))
	for _, lease := range r.leases {
		if lease.projectID == projectID {
			used[lease.port] = true
		}
	}
	for port := req.Start; port <= req.End; port++ {
		if used[port] || !portAvailable(port) {
			continue
		}
		r.leases = append(r.leases, memoryPortLease{
			projectID: projectID,
			ownerKind: req.OwnerKind,
			hookToken: req.HookToken,
			sessionID: req.SessionID,
			key:       req.Key,
			port:      port,
		})
		return port, nil
	}
	return 0, usecase.ErrConflict
}

func (r *MemoryResourceLeaseRepository) PromoteHookLeases(ctx context.Context, token string, sessionID int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	hook, ok := r.hooks[token]
	if !ok {
		return usecase.ErrValidation
	}
	for i := range r.leases {
		if r.leases[i].ownerKind == usecase.ResourceLeaseOwnerHook && r.leases[i].hookToken == token {
			r.leases[i].ownerKind = usecase.ResourceLeaseOwnerSession
			r.leases[i].hookToken = ""
			r.leases[i].sessionID = sessionID
			r.leases[i].projectID = hook.ProjectID
		}
	}
	return nil
}

func (r *MemoryResourceLeaseRepository) ReleaseHookLeases(ctx context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.leases[:0]
	for _, lease := range r.leases {
		if lease.ownerKind == usecase.ResourceLeaseOwnerHook && lease.hookToken == token {
			continue
		}
		kept = append(kept, lease)
	}
	r.leases = kept
	return nil
}

func (r *MemoryResourceLeaseRepository) ReleaseSessionLeases(ctx context.Context, sessionID int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.leases[:0]
	for _, lease := range r.leases {
		if lease.ownerKind == usecase.ResourceLeaseOwnerSession && lease.sessionID == sessionID {
			continue
		}
		kept = append(kept, lease)
	}
	r.leases = kept
	return nil
}

func leaseOwnerMatches(lease memoryPortLease, req usecase.PortLeaseRequest) bool {
	switch req.OwnerKind {
	case usecase.ResourceLeaseOwnerHook:
		return lease.hookToken == req.HookToken
	case usecase.ResourceLeaseOwnerSession:
		return lease.sessionID == req.SessionID
	default:
		return false
	}
}
