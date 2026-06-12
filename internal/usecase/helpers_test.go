package usecase_test

import (
	"context"
	"sync"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// spyLock is a real RWMutex-backed StateLock that also records whether control
// is currently inside a write critical section, so tests can assert that the
// tmux exec runs outside it (ADR-0003).
type spyLock struct {
	mu      sync.RWMutex
	inWrite bool
}

func (l *spyLock) WithRead(fn func() error) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fn()
}

func (l *spyLock) WithWrite(fn func() error) error {
	l.mu.Lock()
	l.inWrite = true
	defer func() {
		l.inWrite = false
		l.mu.Unlock()
	}()
	return fn()
}

// gwCall records the arguments of a SessionGateway.Create call.
type gwCall struct {
	name string
	dir  string
}

// fakeGateway stands in for tmux. It models has-session by flipping a flag on
// Create/Kill, and can be programmed to fail.
type fakeGateway struct {
	lock *spyLock

	created      []gwCall
	killed       []string
	exists       map[string]bool
	createErr    error
	killErr      error
	ranUnderLock bool // set if Create was called inside a write critical section
}

func newFakeGateway(lock *spyLock) *fakeGateway {
	return &fakeGateway{lock: lock, exists: make(map[string]bool)}
}

func (g *fakeGateway) Create(ctx context.Context, name, dir string) error {
	if g.lock != nil && g.lock.inWrite {
		g.ranUnderLock = true
	}
	g.created = append(g.created, gwCall{name, dir})
	if g.createErr != nil {
		return g.createErr
	}
	g.exists[name] = true
	return nil
}

func (g *fakeGateway) Kill(ctx context.Context, name string) error {
	g.killed = append(g.killed, name)
	if g.killErr != nil {
		return g.killErr
	}
	g.exists[name] = false
	return nil
}

func (g *fakeGateway) Exists(ctx context.Context, name string) (bool, error) {
	return g.exists[name], nil
}

// createFixture wires a CreateProject against real in-memory repos, the spy
// lock and the fake gateway, returning all of them for assertions.
func createFixture() (*usecase.CreateProject, *memory.MemoryProjectRepository, *memory.MemorySessionRepository, *fakeGateway, *spyLock) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	gw := newFakeGateway(lock)
	uc := usecase.NewCreateProject(projects, sessions, gw, lock, domain.DefaultDaemonConfig())
	return uc, projects, sessions, gw, lock
}
