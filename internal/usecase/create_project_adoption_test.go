package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

// worktreeSessionsOf returns the project's Worktree Sessions.
func worktreeSessionsOf(t *testing.T, sessions *memory.MemorySessionRepository, projectID int) []*domain.Session {
	t.Helper()
	all, err := sessions.GetByProjectID(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	var out []*domain.Session
	for _, s := range all {
		if s.Type() == domain.WorktreeSession {
			out = append(out, s)
		}
	}
	return out
}

func TestCreateProject_RejectsWithWorktreesDetectedWhenUndecided(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{"/work/api.feature": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"}, // the primary working tree
			{Path: "/work/api.feature", Branch: "feature"},
		},
	}
	uc, projects, sessions, gw, _, _ := createFixtureWithGit(git)
	ctx := context.Background()

	_, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	var pre *usecase.PreconditionRequiredError
	if !errors.As(err, &pre) {
		t.Fatalf("error = %v, want *PreconditionRequiredError", err)
	}
	if pre.Code != usecase.CodeWorktreesDetected {
		t.Fatalf("code = %q, want %q", pre.Code, usecase.CodeWorktreesDetected)
	}
	if len(pre.Worktrees) != 1 || pre.Worktrees[0].Path != "/work/api.feature" || pre.Worktrees[0].Branch != "feature" {
		t.Fatalf("worktrees = %+v, want one {/work/api.feature feature}", pre.Worktrees)
	}
	// The first request has zero side effects: no Project, Main Session or tmux.
	if all, _ := projects.GetAll(ctx); len(all) != 0 {
		t.Errorf("project created on a rejected open: %d", len(all))
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Errorf("session created on a rejected open: %d", len(all))
	}
	if len(gw.created) != 0 {
		t.Errorf("tmux session created on a rejected open: %+v", gw.created)
	}
}

func TestCreateProject_TrueAdoptsDetectedWorktreesAsParentlessSessions(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{"/work/api.feature": true, "/work/api.bugfix": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.feature", Branch: "feature"},
			{Path: "/work/api.bugfix", Branch: "bugfix"},
		},
	}
	uc, _, sessions, gw, _, _ := createFixtureWithGit(git)
	ctx := context.Background()
	yes := true

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", CreateWorktreeSessions: &yes})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Created {
		t.Errorf("Created = false, want true")
	}

	wts := worktreeSessionsOf(t, sessions, res.Project.ID())
	if len(wts) != 2 {
		t.Fatalf("worktree sessions = %d, want 2 (one per detected worktree)", len(wts))
	}
	byBranch := map[string]*domain.Session{}
	for _, s := range wts {
		byBranch[s.Branch()] = s
		if s.Parent() != -1 {
			t.Errorf("worktree session %q parent = %d, want -1 (parentless)", s.Name(), s.Parent())
		}
		if s.ProjectID() != res.Project.ID() {
			t.Errorf("worktree session %q projectID = %d, want %d", s.Name(), s.ProjectID(), res.Project.ID())
		}
	}
	feature := byBranch["feature"]
	if feature == nil || feature.WorktreePath() != "/work/api.feature" {
		t.Fatalf("feature worktree session missing or wrong path: %+v", feature)
	}
	// The adopted session got a tmux session created at the worktree path,
	// outside the write lock (ADR-0003).
	var found bool
	for _, c := range gw.created {
		if c.name == feature.TmuxName() {
			found = true
			if c.dir != "/work/api.feature" {
				t.Errorf("adopted tmux dir = %q, want /work/api.feature", c.dir)
			}
		}
	}
	if !found {
		t.Fatalf("adopted tmux session not created; created=%+v", gw.created)
	}
	if gw.ranUnderLock {
		t.Errorf("ADR-0003 violated: a tmux exec ran inside the write lock")
	}
}

func TestCreateProject_FalseSkipsDetectedWorktreeAdoption(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{"/work/api.feature": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.feature", Branch: "feature"},
		},
	}
	uc, _, sessions, _, _, _ := createFixtureWithGit(git)
	ctx := context.Background()
	no := false

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", CreateWorktreeSessions: &no})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Created {
		t.Errorf("Created = false, want true")
	}
	if wts := worktreeSessionsOf(t, sessions, res.Project.ID()); len(wts) != 0 {
		t.Fatalf("worktree sessions = %d, want 0", len(wts))
	}
}

func TestCreateProject_AdoptionDecisionNoOpsWhenNoWorktreesDetected(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
		},
	}
	uc, _, sessions, _, _, _ := createFixtureWithGit(git)
	ctx := context.Background()
	yes := true

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", CreateWorktreeSessions: &yes})
	if err != nil {
		t.Fatalf("Execute with true: %v", err)
	}
	if wts := worktreeSessionsOf(t, sessions, res.Project.ID()); len(wts) != 0 {
		t.Fatalf("worktree sessions after true = %d, want 0", len(wts))
	}

	no := false
	res, err = uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", CreateWorktreeSessions: &no})
	if err != nil {
		t.Fatalf("Execute with false: %v", err)
	}
	if wts := worktreeSessionsOf(t, sessions, res.Project.ID()); len(wts) != 0 {
		t.Fatalf("worktree sessions after false = %d, want 0", len(wts))
	}
}

func TestCreateProject_DoesNotOfferAlreadyAdoptedWorktrees(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{"/work/api.feature": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.feature", Branch: "feature"},
		},
	}
	uc, _, sessions, _, _, _ := createFixtureWithGit(git)
	ctx := context.Background()
	yes := true

	first, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", CreateWorktreeSessions: &yes})
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if wts := worktreeSessionsOf(t, sessions, first.Project.ID()); len(wts) != 1 {
		t.Fatalf("worktree sessions after adoption = %d, want 1", len(wts))
	}

	second, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	if err != nil {
		t.Fatalf("second Execute without decision: %v", err)
	}
	if second.Created {
		t.Errorf("Created = true, want false for existing project")
	}
	if wts := worktreeSessionsOf(t, sessions, first.Project.ID()); len(wts) != 1 {
		t.Fatalf("worktree sessions after reopen = %d, want still 1", len(wts))
	}
}

func TestCreateProject_DoesNotOfferDetachedOrPrunableWorktrees(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{"/work/api.detached": true},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.detached", Detached: true},
			{Path: "/work/api.prunable", Branch: "old"},
		},
	}
	uc, _, sessions, _, _, _ := createFixtureWithGit(git)
	ctx := context.Background()

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	if err != nil {
		t.Fatalf("Execute without decision: %v", err)
	}
	if wts := worktreeSessionsOf(t, sessions, res.Project.ID()); len(wts) != 0 {
		t.Fatalf("worktree sessions = %d, want 0", len(wts))
	}
}

func TestCreateProject_WorktreeAdoptionIsBestEffortAndDedupesNames(t *testing.T) {
	git := &fakeWorktreeGit{
		paths: map[string]bool{
			"/work/api.main-a": true,
			"/work/api.main-b": true,
			"/work/api.fail":   true,
		},
		worktrees: []usecase.WorktreeRef{
			{Path: "/work/api", Branch: "main"},
			{Path: "/work/api.main-a", Branch: "main"},
			{Path: "/work/api.main-b", Branch: "main"},
			{Path: "/work/api.fail", Branch: "fail"},
		},
	}
	uc, _, sessions, gw, _, _ := createFixtureWithGit(git)
	gw.createErrDir = map[string]error{"/work/api.fail": errors.New("tmux create failed")}
	ctx := context.Background()
	yes := true

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", CreateWorktreeSessions: &yes})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wts := worktreeSessionsOf(t, sessions, res.Project.ID())
	if len(wts) != 2 {
		t.Fatalf("worktree sessions = %d, want 2 successful adoptions", len(wts))
	}
	main := mainSessionOf(t, sessions, res.Project.ID())
	names := map[string]bool{main.Name(): true}
	for _, s := range wts {
		if s.WorktreePath() == "/work/api.fail" {
			t.Fatalf("failed worktree was adopted: %+v", s)
		}
		if names[s.Name()] {
			t.Fatalf("duplicate session name %q", s.Name())
		}
		names[s.Name()] = true
	}
}
