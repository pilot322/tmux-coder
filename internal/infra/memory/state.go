package memory

import (
	"sync"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.StateLock = (*DaemonState)(nil)

// DaemonState owns the daemon's in-memory state: the two repositories and the
// RWMutex that guards them. It satisfies usecase.StateLock.
type DaemonState struct {
	mu       sync.RWMutex
	projects *MemoryProjectRepository
	sessions *MemorySessionRepository
	config   domain.DaemonConfig
}

func NewDaemonState() *DaemonState {
	return &DaemonState{
		projects: NewMemoryProjectRepository(),
		sessions: NewMemorySessionRepository(),
		config:   domain.DefaultDaemonConfig(),
	}
}

func (d *DaemonState) Projects() usecase.IProjectRepository { return d.projects }
func (d *DaemonState) Sessions() usecase.ISessionRepository { return d.sessions }
func (d *DaemonState) Config() domain.DaemonConfig          { return d.config }

func (d *DaemonState) WithRead(fn func() error) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return fn()
}

// WithWrite runs fn under the exclusive lock; the deferred unlock releases it
// even if fn panics.
func (d *DaemonState) WithWrite(fn func() error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return fn()
}
