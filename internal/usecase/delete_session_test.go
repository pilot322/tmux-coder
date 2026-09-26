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
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestDeleteWorktreeSwitchesAttachedClientsToMainBeforeKill(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	worktreePath := filepath.Join(t.TempDir(), "api.feature")
	var main, worktree *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		worktree, err = sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.feature", "feature", worktreePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{worktreePath: true}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{worktree.TmuxName(): true, main.TmuxName(): true}}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: worktree.ID(), Force: true}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tmux.switched) != 1 || tmux.switched[0] != (switchCall{from: worktree.TmuxName(), to: main.TmuxName()}) {
		t.Fatalf("switched = %+v, want one switch from %q to %q", tmux.switched, worktree.TmuxName(), main.TmuxName())
	}
	switchIdx, killIdx := -1, -1
	for i, e := range events {
		if e == "tmux:switch:"+worktree.TmuxName()+"->"+main.TmuxName() {
			switchIdx = i
		}
		if e == "tmux:kill:"+worktree.TmuxName() {
			killIdx = i
		}
	}
	if switchIdx == -1 || killIdx == -1 || switchIdx > killIdx {
		t.Fatalf("want client switch before kill; events = %v", events)
	}
}

type fakeDestroyRunner struct {
	events *[]string
	req    usecase.WorktreeHookRequest
	err    error
}

func (r *fakeDestroyRunner) RunDestroy(_ context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookResult, error) {
	*r.events = append(*r.events, "hook:destroy")
	r.req = req
	return usecase.WorktreeHookResult{LogPath: "/tmp/destroy.log"}, r.err
}

func TestDeleteWorktreeDestroyHookOrderingAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		config        string
		force         bool
		preflightErr  error
		removeErr     error
		hookErr       error
		wantErr       error
		wantEvents    []string
		wantRemaining bool
	}{
		{name: "ordinary", config: "[worktree]\non-destroy-script = \"destroy.sh\"\non-destroy-timeout = \"3s\"\n", wantEvents: []string{"git:preflight", "hook:destroy", "git:remove"}},
		{name: "dirty", config: "[worktree]\non-destroy-script = \"destroy.sh\"\n", preflightErr: usecase.ErrConflict, wantErr: usecase.ErrConflict, wantEvents: []string{"git:preflight"}, wantRemaining: true},
		{name: "hook failure", config: "[worktree]\non-destroy-script = \"destroy.sh\"\n", hookErr: errors.New("exit 1"), wantErr: usecase.ErrGateway, wantEvents: []string{"git:preflight", "hook:destroy"}, wantRemaining: true},
		{name: "force waits despite failure", config: "[worktree]\non-destroy-script = \"destroy.sh\"\n", force: true, hookErr: errors.New("timed out"), wantEvents: []string{"hook:destroy", "git:remove"}},
		{name: "git fails after hook", config: "[worktree]\non-destroy-script = \"destroy.sh\"\n", removeErr: usecase.ErrConflict, wantErr: usecase.ErrConflict, wantEvents: []string{"git:preflight", "hook:destroy", "git:remove"}, wantRemaining: true},
		{name: "no hook", config: "", wantEvents: []string{"git:remove"}},
		{name: "invalid config even with force", config: "[worktree]\non-destroy-timeout = \"bad\"\n", force: true, wantErr: usecase.ErrValidation, wantRemaining: true},
		{name: "missing script even with force", config: "[worktree]\non-destroy-script = \"missing.sh\"\n", force: true, wantErr: usecase.ErrValidation, wantRemaining: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			projectRoot := filepath.Join(root, "api")
			if err := os.MkdirAll(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(projectRoot, "destroy.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			projects := memory.NewMemoryProjectRepository()
			sessions := memory.NewMemorySessionRepository()
			lock := &spyLock{}
			project, err := projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "api.feature")
			session, err := sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.feature", "feature", path))
			if err != nil {
				t.Fatal(err)
			}
			var events []string
			git := &fakeWorktreeGit{paths: map[string]bool{path: true}, events: &events, preflightErr: tc.preflightErr, removeErr: tc.removeErr}
			hook := &fakeDestroyRunner{events: &events, err: tc.hookErr}
			tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{session.TmuxName(): true}}
			uc := usecase.NewDeleteSessionWithHooks(projects, sessions, memory.NewMemoryAgentRepository(), tmux, git, lock, nil, hook, obs.Nop())
			err = uc.Execute(ctx, usecase.DeleteSessionInput{ID: session.ID(), Force: tc.force})
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("Execute = %v, want %v", err, tc.wantErr)
			}
			var filtered []string
			for _, event := range events {
				if event == "git:preflight" || event == "hook:destroy" || event == "git:remove" {
					filtered = append(filtered, event)
				}
			}
			if !reflect.DeepEqual(filtered, tc.wantEvents) {
				t.Errorf("events = %v, want %v", filtered, tc.wantEvents)
			}
			if (getSession(t, lock, sessions, session.ID()) != nil) != tc.wantRemaining {
				t.Error("session retention differs from expectation")
			}
			if len(filtered) > 0 && hook.req.ScriptPath != "" {
				if hook.req.WorkingDir != path || hook.req.Env["TMUX_CODER_SESSION_ID"] == "" || hook.req.Env["TMUX_CODER_BRANCH"] != "feature" || hook.req.Env["TMUX_CODER_PROJECT_ROOT"] != projectRoot || hook.req.Env["TMUX_CODER_HOOK_TOKEN"] != "" {
					t.Errorf("hook request = %+v", hook.req)
				}
				if tc.name == "ordinary" && hook.req.Timeout != 3*time.Second {
					t.Errorf("timeout = %v", hook.req.Timeout)
				}
			}
		})
	}
}

func TestDestroyHookDoesNotRunForSecondaryOrMissingWorktree(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectRoot := filepath.Join(root, "api")
	if err := os.MkdirAll(filepath.Join(projectRoot, ".tmux-coder"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Intentionally invalid: neither secondary deletion nor reconciliation
	// should even load this Config File.
	if err := os.WriteFile(filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml"), []byte("[worktree]\non-destroy-timeout = \"invalid\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	project, err := projects.Create(ctx, domain.NewProject(0, projectRoot, "api"))
	if err != nil {
		t.Fatal(err)
	}
	main, err := sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := sessions.Create(ctx, domain.NewSecondarySession(0, main.ID(), project.ID(), "logs", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{secondary.TmuxName(): true}}
	hook := &fakeDestroyRunner{events: &events}
	uc := usecase.NewDeleteSessionWithHooks(projects, sessions, memory.NewMemoryAgentRepository(), tmux, git, &spyLock{}, nil, hook, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: secondary.ID()}); err != nil {
		t.Fatalf("secondary deletion: %v", err)
	}
	missing, err := sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.gone", "gone", filepath.Join(root, "gone")))
	if err != nil {
		t.Fatal(err)
	}
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: missing.ID()}); !errors.Is(err, usecase.ErrSessionNotFound) {
		t.Fatalf("reconciled deletion = %v, want ErrSessionNotFound", err)
	}
	if hook.req.ScriptPath != "" {
		t.Fatalf("unexpected destroy hook: %+v", hook.req)
	}
}

func TestDeleteSecondarySwitchesAttachedClientsToMainBeforeKill(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	var main, secondary *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		secondary, err = sessions.Create(ctx, domain.NewSecondarySession(0, main.ID(), project.ID(), "api.logs", "", ""))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{secondary.TmuxName(): true, main.TmuxName(): true}}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: secondary.ID()}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tmux.switched) != 1 || tmux.switched[0] != (switchCall{from: secondary.TmuxName(), to: main.TmuxName()}) {
		t.Fatalf("switched = %+v, want one switch from %q to %q", tmux.switched, secondary.TmuxName(), main.TmuxName())
	}
	switchIdx, killIdx := -1, -1
	for i, e := range events {
		if e == "tmux:switch:"+secondary.TmuxName()+"->"+main.TmuxName() {
			switchIdx = i
		}
		if e == "tmux:kill:"+secondary.TmuxName() {
			killIdx = i
		}
	}
	if switchIdx == -1 || killIdx == -1 || switchIdx > killIdx {
		t.Fatalf("want client switch before kill; events = %v", events)
	}
}

func TestDeleteWorktreeRemovesSessionEvenWhenKillFails(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	worktreePath := filepath.Join(t.TempDir(), "api.feature")
	var main, worktree *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		worktree, err = sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.feature", "feature", worktreePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{worktreePath: true}, events: &events}
	// The worktree session is the one the user is attached to, so killing it
	// tears the connection down and the kill shells out non-zero.
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{worktree.TmuxName(): true, main.TmuxName(): true}, killErr: errors.New("server gone")}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: worktree.ID(), Force: true}); err != nil {
		t.Fatalf("Execute should not surface a best-effort kill failure: %v", err)
	}

	if len(git.removed) != 1 || git.removed[0] != worktreePath {
		t.Fatalf("worktree removed = %v, want [%s]", git.removed, worktreePath)
	}
	if err := lock.WithRead(func() error {
		_, err := sessions.GetByID(ctx, worktree.ID())
		return err
	}); !errors.Is(err, usecase.ErrSessionNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrSessionNotFound (no orphan row)", err)
	}
}

func TestDeleteSecondaryRemovesSessionEvenWhenKillFails(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	var main, secondary *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api", domain.MainSession))
		if err != nil {
			return err
		}
		secondary, err = sessions.Create(ctx, domain.NewSecondarySession(0, main.ID(), project.ID(), "api.logs", "", ""))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{secondary.TmuxName(): true, main.TmuxName(): true}, killErr: errors.New("server gone")}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: secondary.ID()}); err != nil {
		t.Fatalf("Execute should not surface a best-effort kill failure: %v", err)
	}

	if err := lock.WithRead(func() error {
		_, err := sessions.GetByID(ctx, secondary.ID())
		return err
	}); !errors.Is(err, usecase.ErrSessionNotFound) {
		t.Fatalf("GetByID after delete = %v, want ErrSessionNotFound (no orphan row)", err)
	}
}

// getSession returns the stored session for id, or nil if it has been deleted.
func getSession(t *testing.T, lock *spyLock, sessions *memory.MemorySessionRepository, id int) *domain.Session {
	t.Helper()
	var got *domain.Session
	if err := lock.WithRead(func() error {
		s, err := sessions.GetByID(context.Background(), id)
		if errors.Is(err, usecase.ErrSessionNotFound) {
			return nil
		}
		got = s
		return err
	}); err != nil {
		t.Fatalf("GetByID(%d): %v", id, err)
	}
	return got
}

func TestDeleteWorktreeReparentsWorktreeChildrenAndCascadesSecondaries(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	lock := &spyLock{}
	base := t.TempDir()
	feat1Path := filepath.Join(base, "api.feat1")
	backendPath := filepath.Join(base, "api.feat1-backend")
	frontendPath := filepath.Join(base, "api.feat1-frontend")
	var main, feat1, backend, frontend, secondary *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, filepath.Join(base, "api"), "api"))
		if err != nil {
			return err
		}
		main, err = sessions.Create(ctx, domain.NewSession(0, -1, project.ID(), "api.main", domain.MainSession))
		if err != nil {
			return err
		}
		feat1, err = sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.feat1", "feat1", feat1Path))
		if err != nil {
			return err
		}
		backend, err = sessions.Create(ctx, domain.NewWorktreeSession(0, feat1.ID(), project.ID(), "api.feat1-backend", "feat1-backend", backendPath))
		if err != nil {
			return err
		}
		frontend, err = sessions.Create(ctx, domain.NewWorktreeSession(0, feat1.ID(), project.ID(), "api.feat1-frontend", "feat1-frontend", frontendPath))
		if err != nil {
			return err
		}
		secondary, err = sessions.Create(ctx, domain.NewSecondarySession(0, feat1.ID(), project.ID(), "logs", "logs", "cascade"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{feat1Path: true, backendPath: true, frontendPath: true}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{
		main.TmuxName(): true, feat1.TmuxName(): true, backend.TmuxName(): true,
		frontend.TmuxName(): true, secondary.TmuxName(): true,
	}}
	uc := usecase.NewDeleteSession(sessions, agents, tmux, git, lock, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: feat1.ID(), Force: true}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// The deleted worktree is gone; its worktree children are promoted to its
	// parent (now parentless, since feat1 itself was branched from main); its
	// secondary cascades.
	if got := getSession(t, lock, sessions, feat1.ID()); got != nil {
		t.Errorf("feat1 should be deleted, still present")
	}
	for _, child := range []*domain.Session{backend, frontend} {
		got := getSession(t, lock, sessions, child.ID())
		if got == nil {
			t.Fatalf("worktree child %q should survive", child.Name())
		}
		if got.Parent() != -1 {
			t.Errorf("child %q parent = %d, want -1 (parentless after reparent)", got.Name(), got.Parent())
		}
	}
	if got := getSession(t, lock, sessions, secondary.ID()); got != nil {
		t.Errorf("secondary should cascade with the worktree, still present")
	}
	// A normal delete never removes branches from disk (ADR-0010).
	if len(git.deletedBranches) != 0 {
		t.Errorf("deleted branches = %v, want none", git.deletedBranches)
	}
}

func TestDeleteSessionReleasesOwnedPortLeases(t *testing.T) {
	ctx := context.Background()
	projects := memory.NewMemoryProjectRepository()
	sessions := memory.NewMemorySessionRepository()
	agents := memory.NewMemoryAgentRepository()
	leases := memory.NewMemoryResourceLeaseRepository()
	lock := &spyLock{}
	worktreePath := filepath.Join(t.TempDir(), "api.feature")
	var session *domain.Session
	if err := lock.WithWrite(func() error {
		project, err := projects.Create(ctx, domain.NewProject(0, "/work/api", "api"))
		if err != nil {
			return err
		}
		session, err = sessions.Create(ctx, domain.NewWorktreeSession(0, -1, project.ID(), "api.feature", "feature", worktreePath))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{ProjectID: session.ProjectID(), OwnerKind: usecase.ResourceLeaseOwnerSession, SessionID: session.ID(), Key: "web", Start: 8000, End: 8000}, func(int) bool { return true }); err != nil {
		t.Fatal(err)
	}

	var events []string
	git := &fakeWorktreeGit{paths: map[string]bool{worktreePath: true}, events: &events}
	tmux := &eventTmuxGateway{events: &events, exists: map[string]bool{session.TmuxName(): true}}
	uc := usecase.NewDeleteSessionWithLeases(sessions, agents, tmux, git, lock, leases, obs.Nop())
	if err := uc.Execute(ctx, usecase.DeleteSessionInput{ID: session.ID(), Force: true}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if err := leases.BeginHook(ctx, "hook-token", usecase.HookLeaseOwner{ProjectID: session.ProjectID()}); err != nil {
		t.Fatal(err)
	}
	port, err := leases.AcquirePort(ctx, usecase.PortLeaseRequest{OwnerKind: usecase.ResourceLeaseOwnerHook, HookToken: "hook-token", Key: "web", Start: 8000, End: 8000}, func(int) bool { return true })
	if err != nil {
		t.Fatalf("AcquirePort after delete: %v", err)
	}
	if port != 8000 {
		t.Fatalf("port after delete = %d, want released port 8000", port)
	}
}
