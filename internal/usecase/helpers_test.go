package usecase_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// writeProjectConfig writes a Config File body under projectRoot/.tmux-coder.
func writeProjectConfig(t *testing.T, projectRoot, body string) {
	t.Helper()
	dir := filepath.Join(projectRoot, ".tmux-coder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tmux-coder.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mainSessionOf returns the project's Main Session.
func mainSessionOf(t *testing.T, sessions *memory.MemorySessionRepository, projectID int) *domain.Session {
	t.Helper()
	all, err := sessions.GetByProjectID(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.Type() == domain.MainSession {
			return s
		}
	}
	t.Fatalf("no main session for project %d", projectID)
	return nil
}

// secondariesOf returns the project's Secondary Sessions, ordered by id.
func secondariesOf(t *testing.T, sessions *memory.MemorySessionRepository) []*domain.Session {
	t.Helper()
	all, err := sessions.GetAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []*domain.Session
	for _, s := range all {
		if s.Type() == domain.SecondarySession {
			out = append(out, s)
		}
	}
	return out
}

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
	createErrDir map[string]error
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
	if err := g.createErrDir[dir]; err != nil {
		return err
	}
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

func (g *fakeGateway) SwitchClients(ctx context.Context, from, to string) error {
	return nil
}

// createFixture wires a CreateProject against real in-memory repos, the spy
// lock and the fake gateway, returning all of them for assertions. Its Git
// gateway reports no worktrees, so worktree detection (ADR-0013) is inert;
// detection tests use createFixtureWithGit to program the repo's worktrees.
func createFixture() (*usecase.CreateProject, *memory.MemoryProjectRepository, *memory.MemorySessionRepository, *fakeGateway, *spyLock) {
	uc, projects, sessions, gw, lock, _ := createFixtureWithGit(&fakeWorktreeGit{paths: make(map[string]bool)})
	return uc, projects, sessions, gw, lock
}

// createFixtureWithGit is createFixture with a caller-supplied Git gateway, so
// worktree-detection and bulk-adoption tests can stage the on-disk worktrees an
// open is validated against. It also returns the Git fake for assertions.
func createFixtureWithGit(git *fakeWorktreeGit) (*usecase.CreateProject, *memory.MemoryProjectRepository, *memory.MemorySessionRepository, *fakeGateway, *spyLock, *fakeWorktreeGit) {
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	gw := newFakeGateway(lock)
	uc := usecase.NewCreateProject(projects, sessions, gw, git, lock, domain.DefaultDaemonConfig())
	return uc, projects, sessions, gw, lock, git
}
