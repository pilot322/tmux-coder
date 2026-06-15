package usecase_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestCreateProject_MaterializesDeclaredSecondaries(t *testing.T) {
	uc, _, sessions, gw, _ := createFixture()
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectConfig(t, root, "[[secondary-sessions]]\nsubdir = \"backend\"\non-delete = \"inherit\"\n")

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: root})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	secs := secondariesOf(t, sessions)
	if len(secs) != 1 {
		t.Fatalf("secondary sessions = %d, want 1", len(secs))
	}
	sec := secs[0]
	mainTmux := res.MainTmuxSessionName
	if sec.Name() != "backend" {
		t.Errorf("secondary name = %q, want backend", sec.Name())
	}
	if sec.TmuxName() != mainTmux+"_backend" {
		t.Errorf("secondary tmux = %q, want %q", sec.TmuxName(), mainTmux+"_backend")
	}
	if sec.RelativeWorkingDirectory() != "backend" {
		t.Errorf("relwd = %q, want backend", sec.RelativeWorkingDirectory())
	}
	if sec.OnDelete() != "inherit" {
		t.Errorf("onDelete = %q, want inherit", sec.OnDelete())
	}
	if sec.ProjectID() != res.Project.ID() {
		t.Errorf("secondary projectID = %d, want %d", sec.ProjectID(), res.Project.ID())
	}
	// The secondary is parented to the Main Session.
	main := mainSessionOf(t, sessions, res.Project.ID())
	if sec.Parent() != main.ID() {
		t.Errorf("secondary parent = %d, want main session %d", sec.Parent(), main.ID())
	}
	// tmux Create ran for the secondary at root/backend, outside the write lock.
	var found bool
	for _, c := range gw.created {
		if c.name == sec.TmuxName() {
			found = true
			if c.dir != filepath.Join(root, "backend") {
				t.Errorf("secondary tmux dir = %q, want %q", c.dir, filepath.Join(root, "backend"))
			}
		}
	}
	if !found {
		t.Fatalf("secondary tmux was not created; created=%+v", gw.created)
	}
	if gw.ranUnderLock {
		t.Errorf("ADR-0003 violated: a tmux exec ran inside the write lock")
	}
}

func TestCreateProject_NewProject(t *testing.T) {
	uc, projects, _, gw, _ := createFixture()
	ctx := context.Background()

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Created {
		t.Errorf("Created = false, want true")
	}
	if res.Project.ID() == 0 {
		t.Errorf("project id was not assigned")
	}
	if res.Project.Title() != "api" {
		t.Errorf("Title = %q, want api", res.Project.Title())
	}
	if res.MainSessionName != "api.main" {
		t.Errorf("MainSessionName = %q, want %q", res.MainSessionName, "api.main")
	}
	if len(gw.created) != 1 || gw.created[0].name != "api_main" || gw.created[0].dir != "/work/api" {
		t.Errorf("gateway.Create calls = %+v, want one {api_main /work/api}", gw.created)
	}
	if gw.ranUnderLock {
		t.Errorf("ADR-0003 violated: tmux exec ran inside the write lock")
	}
	if all, _ := projects.GetAll(ctx); len(all) != 1 {
		t.Errorf("want 1 project stored, got %d", len(all))
	}
}

func TestCreateProject_MaterializesNestedSecondariesInTopoOrder(t *testing.T) {
	uc, _, sessions, _, _ := createFixture()
	ctx := context.Background()
	root := t.TempDir()
	for _, d := range []string{"backend", filepath.Join("backend", "tools")} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The child "tools" is declared before its parent "backend"; the parent
	// must still be created first and the child nested beneath it.
	writeProjectConfig(t, root,
		"[[secondary-sessions]]\nsubdir = \"backend/tools\"\nid = \"tools\"\nparent = \"backend\"\n"+
			"[[secondary-sessions]]\nsubdir = \"backend\"\nid = \"backend\"\n")

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: root})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	secs := secondariesOf(t, sessions)
	if len(secs) != 2 {
		t.Fatalf("secondary sessions = %d, want 2", len(secs))
	}
	var backend, tools *domain.Session
	for _, s := range secs {
		switch s.Name() {
		case "backend":
			backend = s
		case "tools":
			tools = s
		}
	}
	if backend == nil || tools == nil {
		t.Fatalf("missing expected secondaries: %+v", secs)
	}
	main := mainSessionOf(t, sessions, res.Project.ID())
	if backend.Parent() != main.ID() {
		t.Errorf("backend parent = %d, want main %d", backend.Parent(), main.ID())
	}
	if tools.Parent() != backend.ID() {
		t.Errorf("tools parent = %d, want backend %d", tools.Parent(), backend.ID())
	}
	if tools.TmuxName() != backend.TmuxName()+"_tools" {
		t.Errorf("tools tmux = %q, want %q", tools.TmuxName(), backend.TmuxName()+"_tools")
	}
}

func TestCreateProject_MaterializeFailureRollsBackEverything(t *testing.T) {
	uc, projects, sessions, gw, _ := createFixture()
	ctx := context.Background()
	root := t.TempDir()
	// "backend" exists but "missing" does not — the second declaration fails.
	if err := os.Mkdir(filepath.Join(root, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProjectConfig(t, root, "[[secondary-sessions]]\nsubdir = \"backend\"\n[[secondary-sessions]]\nsubdir = \"missing\"\n")

	_, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: root})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
	if all, _ := projects.GetAll(ctx); len(all) != 0 {
		t.Errorf("project records not rolled back: %d remain", len(all))
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Errorf("session records not rolled back: %d remain (want main + secondaries gone)", len(all))
	}
	// Every tmux session created during the attempt was killed: main and the
	// one successfully-created secondary.
	for _, c := range gw.created {
		if gw.exists[c.name] {
			t.Errorf("tmux session %q survived rollback", c.name)
		}
	}
}

func TestCreateProject_RejectsMalformedConfig(t *testing.T) {
	uc, projects, sessions, _, _ := createFixture()
	ctx := context.Background()
	root := t.TempDir()
	writeProjectConfig(t, root, "[[secondary-sessions]]\nsubdir = \"a\"\nparent = \"ghost\"\n")

	_, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: root})
	if !errors.Is(err, usecase.ErrValidation) {
		t.Fatalf("Execute error = %v, want ErrValidation", err)
	}
	if all, _ := projects.GetAll(ctx); len(all) != 0 {
		t.Errorf("project records not rolled back: %d remain", len(all))
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Errorf("session records not rolled back: %d remain", len(all))
	}
}

func TestCreateProject_CustomTitle(t *testing.T) {
	uc, _, _, _, _ := createFixture()
	ctx := context.Background()
	title := "  Backend API  "

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", Title: &title})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Project.Title() != "Backend API" {
		t.Errorf("Title = %q, want Backend API", res.Project.Title())
	}
}

func TestCreateProject_DuplicateIgnoresTitle(t *testing.T) {
	uc, _, _, _, _ := createFixture()
	ctx := context.Background()
	firstTitle := "Backend API"
	secondTitle := "Different API"

	first, _ := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", Title: &firstTitle})
	second, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", Title: &secondTitle})
	if err != nil {
		t.Fatalf("Execute (duplicate): %v", err)
	}
	if second.Project.Title() != first.Project.Title() {
		t.Errorf("duplicate Title = %q, want %q", second.Project.Title(), first.Project.Title())
	}

	invalidTitle := "Backend  API"
	third, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", Title: &invalidTitle})
	if err != nil {
		t.Fatalf("Execute (duplicate invalid title): %v", err)
	}
	if third.Project.Title() != first.Project.Title() {
		t.Errorf("duplicate invalid Title = %q, want %q", third.Project.Title(), first.Project.Title())
	}
}

func TestCreateProject_RejectsInvalidTitle(t *testing.T) {
	uc, _, _, _, _ := createFixture()
	ctx := context.Background()

	for _, title := range []string{"   ", "Backend  API", "abcdefghijklmnopqrstuvwxyzabcdefghijklmno"} {
		_, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api", Title: &title})
		if !errors.Is(err, domain.ErrInvalidProjectTitle) {
			t.Fatalf("title %q: want ErrInvalidProjectTitle, got %v", title, err)
		}
	}
}

func TestCreateProject_RollsBackOnGatewayFailure(t *testing.T) {
	uc, projects, sessions, gw, _ := createFixture()
	gw.createErr = errors.New("tmux exploded")
	ctx := context.Background()

	_, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	if !errors.Is(err, usecase.ErrGateway) {
		t.Fatalf("want ErrGateway, got %v", err)
	}
	if all, _ := projects.GetAll(ctx); len(all) != 0 {
		t.Errorf("project records not rolled back: %d remain", len(all))
	}
	if all, _ := sessions.GetAll(ctx); len(all) != 0 {
		t.Errorf("session records not rolled back: %d remain", len(all))
	}
}

func TestCreateProject_DeduplicatesByFullPath(t *testing.T) {
	uc, projects, _, gw, _ := createFixture()
	ctx := context.Background()

	first, _ := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	second, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	if err != nil {
		t.Fatalf("Execute (duplicate): %v", err)
	}
	if second.Created {
		t.Errorf("Created = true on duplicate, want false")
	}
	if second.Project.ID() != first.Project.ID() {
		t.Errorf("duplicate returned id %d, want %d", second.Project.ID(), first.Project.ID())
	}
	if all, _ := projects.GetAll(ctx); len(all) != 1 {
		t.Errorf("want 1 project after duplicate create, got %d", len(all))
	}
	// tmux session was already present, so no recreate.
	if len(gw.created) != 1 {
		t.Errorf("gateway.Create called %d times, want 1", len(gw.created))
	}
}

func TestCreateProject_ReconcilesMissingSessionOnDuplicate(t *testing.T) {
	uc, _, _, gw, _ := createFixture()
	ctx := context.Background()

	_, _ = uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	// Simulate the tmux session having died between requests.
	gw.exists["api_main"] = false

	if _, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"}); err != nil {
		t.Fatalf("Execute (reconcile): %v", err)
	}
	if len(gw.created) != 2 {
		t.Errorf("want gateway.Create called again to heal the missing session, got %d calls", len(gw.created))
	}
}

func TestCreateProject_ReconcileSecondaryHealsAtWorktreeRoot(t *testing.T) {
	uc, _, sessions, gw, _ := createFixture()
	ctx := context.Background()

	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	projectID := res.Project.ID()

	// A worktree session rooted at its own checkout, with a secondary session
	// nested under it pointing at a relative subdirectory.
	wt, err := sessions.Create(ctx, domain.NewWorktreeSession(0, projectID, "api.feat", "feat", "/work/api-feat"))
	if err != nil {
		t.Fatalf("seed worktree session: %v", err)
	}
	if _, err := sessions.Create(ctx, domain.NewSecondarySessionWithTmuxName(0, wt.ID(), projectID, "api.sub", "sec_tmux", "sub", "cascade")); err != nil {
		t.Fatalf("seed secondary session: %v", err)
	}

	// The main tmux is healthy; only the secondary's tmux has died.
	gw.exists["api_main"] = true
	gw.exists["sec_tmux"] = false

	if _, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"}); err != nil {
		t.Fatalf("Execute (reconcile): %v", err)
	}

	var healed *gwCall
	for i := range gw.created {
		if gw.created[i].name == "sec_tmux" {
			healed = &gw.created[i]
		}
	}
	if healed == nil {
		t.Fatalf("secondary tmux was not healed; created=%+v", gw.created)
	}
	if healed.dir != "/work/api-feat/sub" {
		t.Errorf("healed secondary dir = %q, want %q (root joined with relwd, not the project root)", healed.dir, "/work/api-feat/sub")
	}
}

func TestCreateProject_BumpsNameOnCrossProjectCollision(t *testing.T) {
	uc, _, _, _, _ := createFixture()
	ctx := context.Background()

	_, _ = uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"})
	res, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/personal/api"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.MainSessionName != "api.main-2" {
		t.Errorf("MainSessionName = %q, want %q", res.MainSessionName, "api.main-2")
	}
}
