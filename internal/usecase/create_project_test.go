package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

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
	if res.MainSessionName != "api-main" {
		t.Errorf("MainSessionName = %q, want %q", res.MainSessionName, "api-main")
	}
	if len(gw.created) != 1 || gw.created[0].name != "api-main" || gw.created[0].dir != "/work/api" {
		t.Errorf("gateway.Create calls = %+v, want one {api-main /work/api}", gw.created)
	}
	if gw.ranUnderLock {
		t.Errorf("ADR-0003 violated: tmux exec ran inside the write lock")
	}
	if all, _ := projects.GetAll(ctx); len(all) != 1 {
		t.Errorf("want 1 project stored, got %d", len(all))
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
	gw.exists["api-main"] = false

	if _, err := uc.Execute(ctx, usecase.CreateProjectInput{FullPath: "/work/api"}); err != nil {
		t.Fatalf("Execute (reconcile): %v", err)
	}
	if len(gw.created) != 2 {
		t.Errorf("want gateway.Create called again to heal the missing session, got %d calls", len(gw.created))
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
	if res.MainSessionName != "api-main-2" {
		t.Errorf("MainSessionName = %q, want %q", res.MainSessionName, "api-main-2")
	}
}
