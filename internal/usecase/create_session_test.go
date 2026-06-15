package usecase_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestCreateSessionRunsConfiguredHookBeforeTmuxCreate(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(projectRoot, ".tmux-coder-on-create-worktree.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \".tmux-coder-on-create-worktree.sh\"\non-create-timeout = \"30s\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: &events}
	leases := memory.NewMemoryResourceLeaseRepository()
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, leases)
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	session, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	worktreePath := filepath.Join(parent, "api.feature-login")
	if session.Name() != "api.feature-login" || session.TmuxName() != "api_feature-login" || session.WorktreePath() != worktreePath {
		t.Fatalf("unexpected session: name=%q tmux=%q worktree=%q", session.Name(), session.TmuxName(), session.WorktreePath())
	}
	if !reflect.DeepEqual(events, []string{"git:add", "hook:run", "tmux:create"}) {
		t.Fatalf("events = %v, want git add, hook, tmux create", events)
	}
	if len(hooks.calls) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(hooks.calls))
	}
	call := hooks.calls[0]
	if call.ScriptPath != scriptPath {
		t.Errorf("ScriptPath = %q, want %q", call.ScriptPath, scriptPath)
	}
	if call.WorkingDir != worktreePath {
		t.Errorf("WorkingDir = %q, want %q", call.WorkingDir, worktreePath)
	}
	if call.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", call.Timeout)
	}
	if call.Env["TMUX_CODER_PROJECT_ROOT"] != projectRoot {
		t.Errorf("project root env = %q", call.Env["TMUX_CODER_PROJECT_ROOT"])
	}
	if call.Env["TMUX_CODER_WORKTREE_ROOT"] != worktreePath {
		t.Errorf("worktree root env = %q", call.Env["TMUX_CODER_WORKTREE_ROOT"])
	}
	if call.Env["TMUX_CODER_PROJECT_ID"] != "1" {
		t.Errorf("project id env = %q", call.Env["TMUX_CODER_PROJECT_ID"])
	}
	if call.Env["TMUX_CODER_SESSION_NAME"] != "api.feature-login" {
		t.Errorf("session name env = %q", call.Env["TMUX_CODER_SESSION_NAME"])
	}
	if call.Env["TMUX_CODER_TMUX_SESSION_NAME"] != "api_feature-login" {
		t.Errorf("tmux session env = %q", call.Env["TMUX_CODER_TMUX_SESSION_NAME"])
	}
	if call.Env["TMUX_CODER_BRANCH"] != "feature/login" {
		t.Errorf("branch env = %q", call.Env["TMUX_CODER_BRANCH"])
	}
	if call.Env["TMUX_CODER_HOOK_TOKEN"] == "" {
		t.Fatal("hook token env was empty")
	}
}

func TestCreateSessionMaterializesSecondariesUnderWorktreeAfterHook(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(projectRoot, ".tmux-coder-on-create-worktree.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \".tmux-coder-on-create-worktree.sh\"\n\n[[secondary-sessions]]\nsubdir = \"backend\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The worktree dir and its subdir must exist when materialization stats it;
	// the on-create hook would normally scaffold these, so the secondary is
	// applied after the hook (ADR-0007).
	worktreePath := filepath.Join(parent, "api.feature-login")
	if err := os.MkdirAll(filepath.Join(worktreePath, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: &events}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	wt, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// The hook runs before the secondary's tmux is created.
	if !reflect.DeepEqual(events, []string{"git:add", "hook:run", "tmux:create", "tmux:create"}) {
		t.Fatalf("events = %v, want git add, hook, worktree tmux, secondary tmux", events)
	}

	secs := secondariesOf(t, sessions)
	if len(secs) != 1 {
		t.Fatalf("secondary sessions = %d, want 1", len(secs))
	}
	sec := secs[0]
	if sec.Parent() != wt.ID() {
		t.Errorf("secondary parent = %d, want worktree session %d", sec.Parent(), wt.ID())
	}
	if sec.TmuxName() != wt.TmuxName()+"_backend" {
		t.Errorf("secondary tmux = %q, want %q", sec.TmuxName(), wt.TmuxName()+"_backend")
	}
	if sec.RelativeWorkingDirectory() != "backend" {
		t.Errorf("relwd = %q, want backend", sec.RelativeWorkingDirectory())
	}
}

func TestCreateSessionSecondaryFailureRollsBackWorktreeBranchAndSession(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The declared subdir is never scaffolded, so materialization fails.
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[[secondary-sessions]]\nsubdir = \"backend\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: &events}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
	worktreePath := filepath.Join(parent, "api.feature-login")
	if git.paths[worktreePath] {
		t.Errorf("worktree path still exists after rollback")
	}
	if !reflect.DeepEqual(git.removed, []string{worktreePath}) {
		t.Errorf("removed worktrees = %v, want %v", git.removed, []string{worktreePath})
	}
	if !reflect.DeepEqual(git.deletedBranches, []string{"feature/login"}) {
		t.Errorf("deleted branches = %v, want feature/login", git.deletedBranches)
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Errorf("sessions stored after failed materialize = %d, want 0", len(all))
	}
	// The worktree tmux session was killed during rollback.
	if tmux.exists["api_feature-login"] {
		t.Errorf("worktree tmux survived rollback")
	}
}

func TestCreateSessionHookFailureRollsBackWorktreeAndBranch(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(projectRoot, ".tmux-coder-on-create-worktree.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \".tmux-coder-on-create-worktree.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: &events, err: errors.New("exit status 1")}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if !errors.Is(err, usecase.ErrGateway) {
		t.Fatalf("Execute error = %v, want ErrGateway", err)
	}
	worktreePath := filepath.Join(parent, "api.feature-login")
	if !reflect.DeepEqual(events, []string{"git:add", "hook:run"}) {
		t.Fatalf("events = %v, want git add then hook only", events)
	}
	if git.paths[worktreePath] {
		t.Fatalf("worktree path still exists after rollback")
	}
	if !reflect.DeepEqual(git.removed, []string{worktreePath}) {
		t.Fatalf("removed worktrees = %v, want %v", git.removed, []string{worktreePath})
	}
	if !reflect.DeepEqual(git.deletedBranches, []string{"feature/login"}) {
		t.Fatalf("deleted branches = %v, want feature/login", git.deletedBranches)
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Fatalf("sessions stored after failed hook = %d, want 0", len(all))
	}
}

func TestCreateSessionRejectsHookScriptSymlinkEscapingProjectRoot(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideScript := filepath.Join(parent, "outside-hook.sh")
	if err := os.WriteFile(outsideScript, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideScript, filepath.Join(projectRoot, "hook.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \"hook.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: &events}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
	if len(hooks.calls) != 0 {
		t.Fatalf("hook ran despite escaping symlink")
	}
}

func TestCreateSessionRejectsInvalidConfiguredHookScript(t *testing.T) {
	tests := []struct {
		name       string
		configured func(parent, projectRoot string) string
		setup      func(t *testing.T, parent, projectRoot string)
	}{
		{
			name: "missing script",
			configured: func(parent, projectRoot string) string {
				return "missing.sh"
			},
		},
		{
			name: "non executable script",
			configured: func(parent, projectRoot string) string {
				return "hook.sh"
			},
			setup: func(t *testing.T, parent, projectRoot string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(projectRoot, "hook.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "absolute path",
			configured: func(parent, projectRoot string) string {
				return filepath.Join(projectRoot, "hook.sh")
			},
			setup: func(t *testing.T, parent, projectRoot string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(projectRoot, "hook.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "parent escape",
			configured: func(parent, projectRoot string) string {
				return "../outside-hook.sh"
			},
			setup: func(t *testing.T, parent, projectRoot string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(parent, "outside-hook.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			parent := t.TempDir()
			projectRoot := filepath.Join(parent, "api")
			if err := os.Mkdir(projectRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.setup != nil {
				tt.setup(t, parent, projectRoot)
			}
			if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
				t.Fatal(err)
			}
			config := "[worktree]\non-create-script = \"" + tt.configured(parent, projectRoot) + "\"\n"
			if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}

			projects := memory.NewMemoryProjectRepository()
			sessions := memory.NewMemorySessionRepository()
			lock := &spyLock{}
			var events []string
			git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
			tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
			hooks := &fakeWorktreeHookRunner{events: &events}
			uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
			var project *domain.Project
			if err := lock.WithWrite(func() error {
				var err error
				project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
				return err
			}); err != nil {
				t.Fatal(err)
			}

			_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
			if !errors.Is(err, usecase.ErrValidation) {
				t.Fatalf("Execute error = %v, want ErrValidation", err)
			}
			if len(hooks.calls) != 0 {
				t.Fatalf("hook ran despite invalid script")
			}
		})
	}
}

func TestCreateSessionTmuxFailureAfterHookReleasesProvisionalLeases(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(projectRoot, ".tmux-coder-on-create-worktree.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \".tmux-coder-on-create-worktree.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	leases := memory.NewMemoryResourceLeaseRepository()
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool), createErr: errors.New("tmux failed")}
	hooks := &fakeWorktreeHookRunner{events: &events, leases: leases, acquirePort: true}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, leases)
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if !errors.Is(err, usecase.ErrGateway) {
		t.Fatalf("Execute error = %v, want ErrGateway", err)
	}
	if err := leases.BeginHook(ctx, "next-hook", usecase.HookLeaseOwner{ProjectID: project.ID()}); err != nil {
		t.Fatal(err)
	}
	port, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{OwnerKind: usecase.ResourceLeaseOwnerHook, HookToken: "next-hook", Key: "web", Start: 8000, End: 8000}, func(int) bool { return true })
	if err != nil {
		t.Fatalf("AcquirePort after rollback: %v", err)
	}
	if port != 8000 {
		t.Fatalf("port after rollback = %d, want released port 8000", port)
	}
}

func TestCreateSessionPromotesHookLeasesToCreatedSession(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(projectRoot, ".tmux-coder-on-create-worktree.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \".tmux-coder-on-create-worktree.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	leases := memory.NewMemoryResourceLeaseRepository()
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: &events, leases: leases, acquirePort: true}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, leases)
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	session, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	acquire := usecase.NewAcquirePort(sessions, leases, &fakePortAvailability{occupied: map[int]bool{8000: true}}, lock)
	out, err := acquire.Execute(ctx, usecase.AcquirePortInput{ProjectID: project.ID(), SessionID: session.ID(), Key: "web", Start: 8000, End: 8000})
	if err != nil {
		t.Fatalf("AcquirePort for created session: %v", err)
	}
	if out.Port != 8000 {
		t.Fatalf("port = %d, want promoted lease 8000", out.Port)
	}
}

func TestCreateSessionReconciliationReleasesPrunedSessionLeases(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	leases := memory.NewMemoryResourceLeaseRepository()
	var project *domain.Project
	var pruned *domain.Session
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		if err != nil {
			return err
		}
		pruned, err = sessions.Create(ctx, domain.NewWorktreeSession(0, project.ID(), "api.old", "old", filepath.Join(parent, "api.old")))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{ProjectID: project.ID(), OwnerKind: usecase.ResourceLeaseOwnerSession, SessionID: pruned.ID(), Key: "web", Start: 8000, End: 8000}, func(int) bool { return true }); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, &fakeWorktreeHookRunner{events: &events}, leases)
	if _, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", Create: true, BaseBranch: "main"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if err := leases.BeginHook(ctx, "next-hook", usecase.HookLeaseOwner{ProjectID: project.ID()}); err != nil {
		t.Fatal(err)
	}
	port, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{OwnerKind: usecase.ResourceLeaseOwnerHook, HookToken: "next-hook", Key: "web", Start: 8000, End: 8000}, func(int) bool { return true })
	if err != nil {
		t.Fatalf("AcquirePort after reconcile: %v", err)
	}
	if port != 8000 {
		t.Fatalf("port after reconcile = %d, want released port 8000", port)
	}
}

type fakeWorktreeHookRunner struct {
	events      *[]string
	calls       []usecase.WorktreeHookRequest
	err         error
	leases      usecase.ResourceLeaseRepository
	acquirePort bool
}

func (r *fakeWorktreeHookRunner) Run(ctx context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookResult, error) {
	*r.events = append(*r.events, "hook:run")
	r.calls = append(r.calls, req)
	if r.acquirePort {
		if _, err := r.leases.AcquirePort(ctx, usecase.PortLeaseRequest{OwnerKind: usecase.ResourceLeaseOwnerHook, HookToken: req.Env["TMUX_CODER_HOOK_TOKEN"], Key: "web", Start: 8000, End: 8000}, func(int) bool { return true }); err != nil {
			return usecase.WorktreeHookResult{Output: "acquire port failed"}, err
		}
	}
	if r.err != nil {
		return usecase.WorktreeHookResult{Output: "hook failed"}, r.err
	}
	return usecase.WorktreeHookResult{Output: "hook ok"}, nil
}

type eventTmuxGateway struct {
	events    *[]string
	exists    map[string]bool
	createErr error
}

func (g *eventTmuxGateway) Create(ctx context.Context, name, workingDir string) error {
	*g.events = append(*g.events, "tmux:create")
	if g.createErr != nil {
		return g.createErr
	}
	g.exists[name] = true
	return nil
}

func (g *eventTmuxGateway) Kill(ctx context.Context, name string) error {
	g.exists[name] = false
	return nil
}

func (g *eventTmuxGateway) Exists(ctx context.Context, name string) (bool, error) {
	return g.exists[name], nil
}

type fakeWorktreeGit struct {
	paths           map[string]bool
	events          *[]string
	removed         []string
	deletedBranches []string
}

func (g *fakeWorktreeGit) ValidateBranchName(ctx context.Context, branch string) error { return nil }

func (g *fakeWorktreeGit) IsWorktreeRoot(ctx context.Context, path string) (bool, error) {
	return true, nil
}

func (g *fakeWorktreeGit) LocalBranchExists(ctx context.Context, repoPath, branch string) (bool, error) {
	return false, nil
}

func (g *fakeWorktreeGit) ResolveCommit(ctx context.Context, repoPath, ref string) (bool, error) {
	return true, nil
}

func (g *fakeWorktreeGit) WorktreePathExists(ctx context.Context, path string) (bool, error) {
	return g.paths[path], nil
}

func (g *fakeWorktreeGit) AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, create bool) error {
	*g.events = append(*g.events, "git:add")
	g.paths[worktreePath] = true
	return nil
}

func (g *fakeWorktreeGit) RemoveWorktree(ctx context.Context, worktreePath string, force bool) error {
	g.removed = append(g.removed, worktreePath)
	delete(g.paths, worktreePath)
	return nil
}

func (g *fakeWorktreeGit) DeleteBranch(ctx context.Context, repoPath, branch string) error {
	g.deletedBranches = append(g.deletedBranches, branch)
	return nil
}

func (g *fakeWorktreeGit) CurrentBranch(ctx context.Context, repoPath string) (string, error) {
	return "main", nil
}
