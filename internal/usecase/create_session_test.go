package usecase_test

import (
	"context"
	"errors"
	"fmt"
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

	session, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

	wt, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

// TestCreateSessionRollsBackWorktreeWhenRequestCancelledMidHook reproduces the
// existing-branch leak: a client disconnect cancels the request context while
// the (slow) Worktree Hook runs. The hook process dies, the create fails, and
// rollback must still remove the worktree it added. Because git is shelled out
// via exec.CommandContext, a rollback that reuses the cancelled context runs no
// git at all and the worktree leaks on disk with no Session created.
func TestCreateSessionRollsBackWorktreeWhenRequestCancelledMidHook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
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
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: make(map[string]bool)}
	// The client disconnects mid-hook: cancel the request context, then surface
	// the error a hook process gets when its context dies.
	hooks := &fakeWorktreeHookRunner{events: &events, cancel: cancel, err: context.Canceled}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
	if err == nil {
		t.Fatal("Execute succeeded, want failure after the request was cancelled mid-hook")
	}
	worktreePath := filepath.Join(parent, "api.feature-login")
	if git.paths[worktreePath] {
		t.Errorf("worktree leaked: still on disk after a cancelled create")
	}
	if !reflect.DeepEqual(git.removed, []string{worktreePath}) {
		t.Errorf("removed worktrees = %v, want %v (rollback must clean up despite request cancellation)", git.removed, []string{worktreePath})
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Errorf("sessions stored after cancelled create = %d, want 0", len(all))
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

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

			_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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

	session, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"})
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
		pruned, err = sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.old", "old", filepath.Join(parent, "api.old")))
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
	if _, err := uc.Execute(ctx, usecase.CreateSessionInput{ProjectID: project.ID(), Type: domain.WorktreeSession, Branch: "feature/login", CreateWorktree: true, CreateBranch: true, BaseBranch: "main"}); err != nil {
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

// createFixture wires a CreateSession use case over in-memory repositories and
// programmable Git/tmux fakes, with one project ("api") already stored. New
// creation-mode tests program git.worktrees / git.branches to stage the
// pre-existing state a mode is validated against.
type worktreeFixture struct {
	ctx      context.Context
	uc       *usecase.CreateSession
	project  *domain.Project
	sessions *memory.MemorySessionRepository
	lock     *spyLock
	git      *fakeWorktreeGit
	tmux     *eventTmuxGateway
	hooks    *fakeWorktreeHookRunner
	events   *[]string
	parent   string
}

func newWorktreeFixture(t *testing.T) *worktreeFixture {
	t.Helper()
	ctx := context.Background()
	parent := t.TempDir()
	projectRoot := filepath.Join(parent, "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	events := &[]string{}
	git := &fakeWorktreeGit{paths: make(map[string]bool), branches: make(map[string]bool), events: events}
	tmux := &eventTmuxGateway{events: events, exists: make(map[string]bool)}
	hooks := &fakeWorktreeHookRunner{events: events}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, hooks, memory.NewMemoryResourceLeaseRepository())
	var project *domain.Project
	if err := lock.WithWrite(func() error {
		var err error
		project, err = projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return &worktreeFixture{ctx: ctx, uc: uc, project: project, sessions: sessions, lock: lock, git: git, tmux: tmux, hooks: hooks, events: events, parent: parent}
}

// seedWorktreeSession stores a pre-existing Worktree Session bound to branch on
// the fixture's project, as if one had been created earlier.
func (f *worktreeFixture) seedWorktreeSession(t *testing.T, branch string) {
	t.Helper()
	path := filepath.Join(f.parent, "api."+branch)
	// Mark the worktree present on disk so reconciliation does not prune the
	// seeded session before the duplicate-branch check runs.
	f.git.paths[path] = true
	if err := f.lock.WithWrite(func() error {
		_, err := f.sessions.Create(f.ctx, domain.NewWorktreeSession(0, -1, f.project.ID(), "api."+branch, branch, path))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// worktreePath is the path CreateSession derives for the standard test branch
// "feature/login" under project "api".
func (f *worktreeFixture) worktreePath() string {
	return filepath.Join(f.parent, "api.feature-login")
}

func (f *worktreeFixture) execute(in usecase.CreateSessionInput) (*domain.Session, error) {
	in.ProjectID = f.project.ID()
	in.Type = domain.WorktreeSession
	if in.Branch == "" {
		in.Branch = "feature/login"
	}
	return f.uc.Execute(f.ctx, in)
}

// writeHook configures a no-op on-create Worktree Hook for the fixture's
// project, so tests can assert the hook does (or does not) run.
func (f *worktreeFixture) writeHook(t *testing.T) {
	t.Helper()
	projectRoot := f.project.FullPath()
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder-on-create-worktree.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-create-script = \".tmux-coder-on-create-worktree.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func conflictCode(t *testing.T, err error) string {
	t.Helper()
	var sce *usecase.StateConflictError
	if !errors.As(err, &sce) {
		t.Fatalf("error = %v, want *StateConflictError", err)
	}
	return sce.Code
}

func TestCreateSessionRejectsBranchWithoutWorktree(t *testing.T) {
	f := newWorktreeFixture(t)
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: false, CreateBranch: true})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
}

func TestCreateSessionRejectsBaseBranchWhenNotCreatingBranch(t *testing.T) {
	f := newWorktreeFixture(t)
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: false, BaseBranch: "main"})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
}

func TestCreateSessionFreshConflictsWhenSessionExistsForBranch(t *testing.T) {
	f := newWorktreeFixture(t)
	f.seedWorktreeSession(t, "feature/login")
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: true})
	if code := conflictCode(t, err); code != usecase.CodeSessionExists {
		t.Fatalf("code = %q, want %q", code, usecase.CodeSessionExists)
	}
	if len(*f.events) != 0 {
		t.Fatalf("events = %v, want none (conflict before any side effect)", *f.events)
	}
}

func TestCreateSessionFreshConflictsWhenWorktreeAdoptable(t *testing.T) {
	f := newWorktreeFixture(t)
	f.git.worktrees = []usecase.WorktreeRef{{Path: f.worktreePath(), Branch: "feature/login"}}
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: true})
	if code := conflictCode(t, err); code != usecase.CodeWorktreeExists {
		t.Fatalf("code = %q, want %q", code, usecase.CodeWorktreeExists)
	}
}

func TestCreateSessionFreshConflictsWhenWorktreeOnDifferentBranch(t *testing.T) {
	f := newWorktreeFixture(t)
	f.git.worktrees = []usecase.WorktreeRef{{Path: f.worktreePath(), Branch: "other"}}
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: true})
	if code := conflictCode(t, err); code != usecase.CodePathBlocked {
		t.Fatalf("code = %q, want %q", code, usecase.CodePathBlocked)
	}
}

func TestCreateSessionFreshConflictsWhenStrayDirAtPath(t *testing.T) {
	f := newWorktreeFixture(t)
	f.git.paths[f.worktreePath()] = true // occupied, but not a worktree of this repo
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: true})
	if code := conflictCode(t, err); code != usecase.CodePathBlocked {
		t.Fatalf("code = %q, want %q", code, usecase.CodePathBlocked)
	}
}

func TestCreateSessionFreshConflictsWhenBranchOnlyExists(t *testing.T) {
	f := newWorktreeFixture(t)
	f.git.branches["feature/login"] = true // branch exists, no worktree at the derived path
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: true})
	if code := conflictCode(t, err); code != usecase.CodeBranchExists {
		t.Fatalf("code = %q, want %q", code, usecase.CodeBranchExists)
	}
}

func TestCreateSessionExistingBranchAddsWorktreeWithoutCreatingBranchAndRunsHook(t *testing.T) {
	f := newWorktreeFixture(t)
	f.writeHook(t)
	f.git.branches["feature/login"] = true // branch exists, no worktree yet
	session, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: false})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if session.Branch() != "feature/login" {
		t.Fatalf("session branch = %q, want feature/login", session.Branch())
	}
	if !reflect.DeepEqual(*f.events, []string{"git:add", "hook:run", "tmux:create"}) {
		t.Fatalf("events = %v, want git add, hook, tmux create", *f.events)
	}
	if len(f.git.addCalls) != 1 || f.git.addCalls[0].createBranch {
		t.Fatalf("AddWorktree calls = %+v, want one with createBranch=false", f.git.addCalls)
	}
}

func TestCreateSessionExistingBranchConflictsWhenBranchMissing(t *testing.T) {
	f := newWorktreeFixture(t)
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: true, CreateBranch: false})
	if !errors.Is(err, usecase.ErrConflict) {
		t.Fatalf("Execute error = %v, want ErrConflict", err)
	}
	if len(*f.events) != 0 {
		t.Fatalf("events = %v, want none", *f.events)
	}
}

func TestCreateSessionAdoptWrapsExistingWorktreeWithoutAddOrHook(t *testing.T) {
	f := newWorktreeFixture(t)
	f.writeHook(t) // a hook is configured but adoption must not run it
	f.git.worktrees = []usecase.WorktreeRef{{Path: f.worktreePath(), Branch: "feature/login"}}
	session, err := f.execute(usecase.CreateSessionInput{CreateWorktree: false, CreateBranch: false})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if session.Branch() != "feature/login" || session.WorktreePath() != f.worktreePath() {
		t.Fatalf("session = branch %q path %q, want feature/login at %q", session.Branch(), session.WorktreePath(), f.worktreePath())
	}
	// No worktree materialization and no hook — only the tmux session is created.
	if !reflect.DeepEqual(*f.events, []string{"tmux:create"}) {
		t.Fatalf("events = %v, want only tmux:create", *f.events)
	}
	if len(f.git.addCalls) != 0 {
		t.Fatalf("AddWorktree calls = %+v, want none for adoption", f.git.addCalls)
	}
	if len(f.hooks.calls) != 0 {
		t.Fatalf("hook ran during adoption: %+v", f.hooks.calls)
	}
}

func TestCreateSessionAdoptKeepsProvenanceParent(t *testing.T) {
	f := newWorktreeFixture(t)
	// A 'w' gesture that hits a worktree_exists conflict re-issues as adopt while
	// carrying its source; the adopted worktree must still nest under that source
	// (ADR-0010 provenance is recorded for every creation mode).
	sourcePath := filepath.Join(f.parent, "api.feature")
	f.git.paths[sourcePath] = true // present on disk so reconcile does not prune it
	var source *domain.Session
	if err := f.lock.WithWrite(func() error {
		var err error
		source, err = f.sessions.Create(f.ctx, domain.NewWorktreeSession(0, -1, f.project.ID(), "api.feature", "feature", sourcePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.git.worktrees = []usecase.WorktreeRef{{Path: f.worktreePath(), Branch: "feature/login"}}

	session, err := f.execute(usecase.CreateSessionInput{CreateWorktree: false, CreateBranch: false, ParentSessionID: source.ID()})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if session.Parent() != source.ID() {
		t.Fatalf("adopted worktree parent = %d, want source %d (provenance carried through adopt)", session.Parent(), source.ID())
	}
	if len(f.git.addCalls) != 0 {
		t.Fatalf("AddWorktree calls = %+v, want none for adoption", f.git.addCalls)
	}
}

func TestCreateSessionAdoptConflictsWhenWorktreeOnDifferentBranch(t *testing.T) {
	f := newWorktreeFixture(t)
	f.git.worktrees = []usecase.WorktreeRef{{Path: f.worktreePath(), Branch: "other"}}
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: false, CreateBranch: false})
	if code := conflictCode(t, err); code != usecase.CodePathBlocked {
		t.Fatalf("code = %q, want %q", code, usecase.CodePathBlocked)
	}
}

func TestCreateSessionAdoptConflictsWhenNothingToAdopt(t *testing.T) {
	f := newWorktreeFixture(t)
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: false, CreateBranch: false})
	if code := conflictCode(t, err); code != usecase.CodePathBlocked {
		t.Fatalf("code = %q, want %q", code, usecase.CodePathBlocked)
	}
}

func TestCreateSessionAdoptRollbackLeavesWorktreeAndBranchIntact(t *testing.T) {
	f := newWorktreeFixture(t)
	f.git.worktrees = []usecase.WorktreeRef{{Path: f.worktreePath(), Branch: "feature/login"}}
	f.tmux.createErr = errors.New("tmux failed")
	_, err := f.execute(usecase.CreateSessionInput{CreateWorktree: false, CreateBranch: false})
	if !errors.Is(err, usecase.ErrGateway) {
		t.Fatalf("Execute error = %v, want ErrGateway", err)
	}
	if len(f.git.removed) != 0 {
		t.Fatalf("removed worktrees = %v, want none (adoption must not remove the worktree)", f.git.removed)
	}
	if len(f.git.deletedBranches) != 0 {
		t.Fatalf("deleted branches = %v, want none (adoption must not delete the branch)", f.git.deletedBranches)
	}
}

// worktreeProvenanceUC wires a CreateSession against in-memory repos and a fake
// Git gateway for the ADR-0010 provenance tests, returning the use case, the
// session repo (to inspect parents), the git fake (to inspect AddWorktree) and
// the created Project.
func worktreeProvenanceUC(t *testing.T, git *fakeWorktreeGit) (context.Context, *usecase.CreateSession, *memory.MemorySessionRepository, *domain.Project) {
	t.Helper()
	ctx := context.Background()
	projectRoot := filepath.Join(t.TempDir(), "api")
	if err := os.Mkdir(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	lock := &spyLock{}
	tmux := &eventTmuxGateway{events: git.events, exists: make(map[string]bool)}
	uc := usecase.NewCreateSessionWithHooks(projects, sessions, tmux, git, lock, &fakeWorktreeHookRunner{events: git.events}, memory.NewMemoryResourceLeaseRepository())
	project, err := projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, uc, sessions, project
}

func TestCreateSessionWorktreeFromWorktreeParentsToSourceAndBranchesOffItsBranch(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	ctx, uc, sessions, project := worktreeProvenanceUC(t, git)

	sourcePath := filepath.Join(filepath.Dir(project.FullPath()), "api.feature")
	git.paths[sourcePath] = true
	source, err := sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.feature", "feature", sourcePath))
	if err != nil {
		t.Fatal(err)
	}

	child, err := uc.Execute(ctx, usecase.CreateSessionInput{
		ProjectID:       project.ID(),
		Type:            domain.WorktreeSession,
		Branch:          "feature-backend",
		CreateWorktree:  true,
		CreateBranch:    true,
		ParentSessionID: source.ID(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if child.Parent() != source.ID() {
		t.Errorf("child parent = %d, want source %d (provenance)", child.Parent(), source.ID())
	}
	// The new branch is cut from the source worktree's committed branch tip.
	if len(git.addCalls) != 1 || git.addCalls[0].baseBranch != "feature" || !git.addCalls[0].createBranch {
		t.Errorf("AddWorktree = %+v, want one create off baseBranch=feature", git.addCalls)
	}
}

func TestCreateSessionWorktreeFromMainParentsToMainAndBranchesOffCurrentBranch(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events, currentBranch: "develop"}
	ctx, uc, sessions, project := worktreeProvenanceUC(t, git)

	main, err := sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api.main", domain.MainSession))
	if err != nil {
		t.Fatal(err)
	}

	child, err := uc.Execute(ctx, usecase.CreateSessionInput{
		ProjectID:       project.ID(),
		Type:            domain.WorktreeSession,
		Branch:          "feature",
		CreateWorktree:  true,
		CreateBranch:    true,
		ParentSessionID: main.ID(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if child.Parent() != main.ID() {
		t.Errorf("child parent = %d, want main %d", child.Parent(), main.ID())
	}
	if len(git.addCalls) != 1 || git.addCalls[0].baseBranch != "develop" {
		t.Errorf("AddWorktree = %+v, want baseBranch=develop (main's current branch)", git.addCalls)
	}
}

func TestCreateSessionWorktreeFromDetachedMainIsRejected(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events, currentBranch: ""}
	ctx, uc, sessions, project := worktreeProvenanceUC(t, git)

	main, err := sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api.main", domain.MainSession))
	if err != nil {
		t.Fatal(err)
	}

	_, err = uc.Execute(ctx, usecase.CreateSessionInput{
		ProjectID:       project.ID(),
		Type:            domain.WorktreeSession,
		Branch:          "feature",
		CreateWorktree:  true,
		CreateBranch:    true,
		ParentSessionID: main.ID(),
	})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation for detached HEAD", err)
	}
	if len(git.addCalls) != 0 {
		t.Errorf("AddWorktree should not run for a detached-HEAD source, got %+v", git.addCalls)
	}
}

func TestCreateSessionWorktreeFromSecondaryIsRejected(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	ctx, uc, sessions, project := worktreeProvenanceUC(t, git)

	main, err := sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api.main", domain.MainSession))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := sessions.Create(ctx, domain.NewSecondarySession(0, main.ID(), project.ID(), "backend", "backend", "cascade"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = uc.Execute(ctx, usecase.CreateSessionInput{
		ProjectID:       project.ID(),
		Type:            domain.WorktreeSession,
		Branch:          "feature",
		CreateWorktree:  true,
		CreateBranch:    true,
		ParentSessionID: secondary.ID(),
	})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation for a secondary source", err)
	}
	if len(git.addCalls) != 0 {
		t.Errorf("AddWorktree should not run for a secondary source, got %+v", git.addCalls)
	}
}

func TestCreateSessionWorktreeFromBaseRefIsParentlessAndValidatesRef(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	ctx, uc, _, project := worktreeProvenanceUC(t, git)

	child, err := uc.Execute(ctx, usecase.CreateSessionInput{
		ProjectID:      project.ID(),
		Type:           domain.WorktreeSession,
		Branch:         "feature",
		CreateWorktree: true,
		CreateBranch:   true,
		BaseBranch:     "origin/main",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if child.Parent() != -1 {
		t.Errorf("child parent = %d, want -1 (parentless 'W')", child.Parent())
	}
	if len(git.addCalls) != 1 || git.addCalls[0].baseBranch != "origin/main" {
		t.Errorf("AddWorktree = %+v, want baseBranch=origin/main", git.addCalls)
	}
}

func TestCreateSessionWorktreeFromUnresolvableBaseRefIsRejected(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events, unresolvable: map[string]bool{"nope": true}}
	ctx, uc, _, project := worktreeProvenanceUC(t, git)

	_, err := uc.Execute(ctx, usecase.CreateSessionInput{
		ProjectID:      project.ID(),
		Type:           domain.WorktreeSession,
		Branch:         "feature",
		CreateWorktree: true,
		CreateBranch:   true,
		BaseBranch:     "nope",
	})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation for unresolvable base ref", err)
	}
	if len(git.addCalls) != 0 {
		t.Errorf("AddWorktree should not run for an unresolvable base ref, got %+v", git.addCalls)
	}
}

func TestCreateSecondaryDepthBudgetResetsAtWorktreeRoot(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	ctx, uc, sessions, project := worktreeProvenanceUC(t, git)

	main, err := sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api.main", domain.MainSession))
	if err != nil {
		t.Fatal(err)
	}
	// Four nested worktrees: main -> wt1 -> wt2 -> wt3 -> wt4. Measured from the
	// root the chain spans five levels, so without the depth-budget reset it
	// would exhaust the Secondary depth-5 cap and reject any secondary under wt4.
	base := filepath.Dir(project.FullPath())
	parentID := main.ID()
	var deepest *domain.Session
	for i := 1; i <= 4; i++ {
		wtPath := filepath.Join(base, fmt.Sprintf("api.wt%d", i))
		if err := os.Mkdir(wtPath, 0o755); err != nil {
			t.Fatal(err)
		}
		wt, err := sessions.Create(ctx, domain.NewWorktreeSession(0, parentID, project.ID(), fmt.Sprintf("api.wt%d", i), fmt.Sprintf("wt%d", i), wtPath))
		if err != nil {
			t.Fatal(err)
		}
		parentID = wt.ID()
		deepest = wt
	}

	// A secondary directly under the deepest worktree must be allowed: the
	// worktree root resets the budget, so the secondary sits at depth 2.
	sec, err := uc.Execute(ctx, usecase.CreateSessionInput{
		Type:            domain.SecondarySession,
		ParentSessionID: deepest.ID(),
		PreferredName:   "logs",
	})
	if err != nil {
		t.Fatalf("secondary under a deeply-nested worktree should be allowed: %v", err)
	}
	if sec.Parent() != deepest.ID() {
		t.Errorf("secondary parent = %d, want deepest worktree %d", sec.Parent(), deepest.ID())
	}
}

func TestCreateSecondaryDepthCapStillEnforcedFromWorktreeRoot(t *testing.T) {
	var events []string
	git := &fakeWorktreeGit{paths: make(map[string]bool), events: &events}
	ctx, uc, sessions, project := worktreeProvenanceUC(t, git)

	main, err := sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api.main", domain.MainSession))
	if err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(filepath.Dir(project.FullPath()), "api.feature")
	if err := os.Mkdir(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	wt, err := sessions.Create(ctx, domain.NewWorktreeSession(0, main.ID(), project.ID(), "api.feature", "feature", wtPath))
	if err != nil {
		t.Fatal(err)
	}

	// The worktree root is depth 1, so four nested secondaries (depths 2..5) are
	// allowed and a fifth under the deepest must be rejected by the depth-5 cap.
	parentID := wt.ID()
	for i := 1; i <= 4; i++ {
		sec, err := uc.Execute(ctx, usecase.CreateSessionInput{
			Type:            domain.SecondarySession,
			ParentSessionID: parentID,
			PreferredName:   fmt.Sprintf("lvl%d", i),
		})
		if err != nil {
			t.Fatalf("secondary at level %d should be allowed: %v", i, err)
		}
		parentID = sec.ID()
	}
	if _, err := uc.Execute(ctx, usecase.CreateSessionInput{
		Type:            domain.SecondarySession,
		ParentSessionID: parentID,
		PreferredName:   "toodeep",
	}); !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("fifth nested secondary error = %v, want ErrValidation (depth cap)", err)
	}
}

type fakeWorktreeHookRunner struct {
	events      *[]string
	calls       []usecase.WorktreeHookRequest
	err         error
	cancel      context.CancelFunc // models a client disconnecting while the hook runs
	leases      usecase.ResourceLeaseRepository
	acquirePort bool
}

func (r *fakeWorktreeHookRunner) Run(ctx context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookResult, error) {
	*r.events = append(*r.events, "hook:run")
	r.calls = append(r.calls, req)
	if r.cancel != nil {
		r.cancel()
	}
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

type switchCall struct {
	from string
	to   string
}

type eventTmuxGateway struct {
	events    *[]string
	exists    map[string]bool
	createErr error
	killErr   error
	switched  []switchCall
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
	*g.events = append(*g.events, "tmux:kill:"+name)
	if g.killErr != nil {
		return g.killErr
	}
	g.exists[name] = false
	return nil
}

func (g *eventTmuxGateway) Exists(ctx context.Context, name string) (bool, error) {
	return g.exists[name], nil
}

func (g *eventTmuxGateway) SwitchClients(ctx context.Context, from, to string) error {
	*g.events = append(*g.events, "tmux:switch:"+from+"->"+to)
	g.switched = append(g.switched, switchCall{from: from, to: to})
	return nil
}

type addWorktreeCall struct {
	worktreePath string
	branch       string
	baseBranch   string
	createBranch bool
}

type fakeWorktreeGit struct {
	paths           map[string]bool
	worktrees       []usecase.WorktreeRef
	branches        map[string]bool
	events          *[]string
	addCalls        []addWorktreeCall
	removed         []string
	deletedBranches []string
	currentBranch   string          // returned by CurrentBranch ("" models detached HEAD)
	unresolvable    map[string]bool // refs ResolveCommit reports as not resolving
}

func (g *fakeWorktreeGit) ValidateBranchName(ctx context.Context, branch string) error { return nil }

func (g *fakeWorktreeGit) IsWorktreeRoot(ctx context.Context, path string) (bool, error) {
	return true, nil
}

func (g *fakeWorktreeGit) LocalBranchExists(ctx context.Context, repoPath, branch string) (bool, error) {
	return g.branches[branch], nil
}

func (g *fakeWorktreeGit) ResolveCommit(ctx context.Context, repoPath, ref string) (bool, error) {
	return !g.unresolvable[ref], nil
}

func (g *fakeWorktreeGit) WorktreePathExists(ctx context.Context, path string) (bool, error) {
	return g.paths[path], nil
}

func (g *fakeWorktreeGit) ListWorktrees(ctx context.Context, repoPath string) ([]usecase.WorktreeRef, error) {
	return g.worktrees, nil
}

func (g *fakeWorktreeGit) AddWorktree(ctx context.Context, repoPath, worktreePath, branch, baseBranch string, createBranch bool) error {
	*g.events = append(*g.events, "git:add")
	g.addCalls = append(g.addCalls, addWorktreeCall{worktreePath, branch, baseBranch, createBranch})
	g.paths[worktreePath] = true
	return nil
}

func (g *fakeWorktreeGit) RemoveWorktree(ctx context.Context, worktreePath string, force bool) error {
	// Mirror exec.CommandContext: a cancelled context runs no git at all.
	if err := ctx.Err(); err != nil {
		return err
	}
	g.removed = append(g.removed, worktreePath)
	delete(g.paths, worktreePath)
	return nil
}

func (g *fakeWorktreeGit) DeleteBranch(ctx context.Context, repoPath, branch string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.deletedBranches = append(g.deletedBranches, branch)
	return nil
}

func (g *fakeWorktreeGit) CurrentBranch(ctx context.Context, repoPath string) (string, error) {
	return g.currentBranch, nil
}
