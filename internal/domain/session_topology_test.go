package domain_test

import (
	"reflect"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
)

func TestPlanWorktreeRemovalDeletesSecondaryDescendantsAndReparentsDirectWorktreeChildren(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, -1),
		wt(2, 1),
		wt(3, 2),
		sec(4, 1, "cascade"),
		sec(5, 4, "cascade"),
		sec(6, 2, "cascade"),
	}

	plan, err := domain.PlanWorktreeRemoval(sessions, 1)
	if err != nil {
		t.Fatalf("PlanWorktreeRemoval: %v", err)
	}

	assertInts(t, plan.DeleteSessionIDs, []int{5, 4, 1})
	assertReparents(t, plan.ReparentWorktrees, []domain.SessionReparent{{SessionID: 2, ParentID: -1}})
}

func TestPlanWorktreeRemovalRejectsMalformedParentChains(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, 99),
	}

	if _, err := domain.PlanWorktreeRemoval(sessions, 1); err == nil {
		t.Fatal("PlanWorktreeRemoval error = nil, want malformed topology error")
	}
}

func TestPlanWorktreePruneHealsSurvivingWorktreeDescendants(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, -1),
		wt(2, 1),
		wt(3, 2),
		wt(4, 3),
		sec(5, 2, "cascade"),
		sec(6, 5, "cascade"),
		sec(7, 4, "cascade"),
	}

	plan, err := domain.PlanWorktreePrune(sessions, []int{2, 3})
	if err != nil {
		t.Fatalf("PlanWorktreePrune: %v", err)
	}

	assertInts(t, plan.DeleteSessionIDs, []int{6, 5, 3, 2})
	assertReparents(t, plan.ReparentWorktrees, []domain.SessionReparent{{SessionID: 4, ParentID: 1}})
}

func TestPlanWorktreePruneReparentsThroughMissingAncestorToParentless(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, 99),
		wt(2, 1),
	}

	plan, err := domain.PlanWorktreePrune(sessions, []int{1})
	if err != nil {
		t.Fatalf("PlanWorktreePrune: %v", err)
	}

	assertInts(t, plan.DeleteSessionIDs, []int{1})
	assertReparents(t, plan.ReparentWorktrees, []domain.SessionReparent{{SessionID: 2, ParentID: -1}})
}

func TestPlanSecondaryRemovalCascadeDeletesSecondaryDescendantsChildBeforeParent(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, -1),
		sec(2, 1, "cascade"),
		sec(3, 2, "cascade"),
		sec(4, 3, "cascade"),
	}

	plan, err := domain.PlanSecondaryRemoval(sessions, 2)
	if err != nil {
		t.Fatalf("PlanSecondaryRemoval: %v", err)
	}

	assertInts(t, plan.DeleteSessionIDs, []int{4, 3, 2})
	assertReparents(t, plan.ReparentSecondaries, nil)
}

func TestPlanSecondaryRemovalInheritReparentsOnlyDirectSecondaryChildren(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, -1),
		sec(2, 1, "inherit"),
		sec(3, 2, "cascade"),
		sec(4, 3, "cascade"),
		sec(5, 2, "cascade"),
	}

	plan, err := domain.PlanSecondaryRemoval(sessions, 2)
	if err != nil {
		t.Fatalf("PlanSecondaryRemoval: %v", err)
	}

	assertInts(t, plan.DeleteSessionIDs, []int{2})
	assertReparents(t, plan.ReparentSecondaries, []domain.SessionReparent{{SessionID: 3, ParentID: 1}, {SessionID: 5, ParentID: 1}})
}

func TestSessionTopologyPlannersRejectWorktreeUnderSecondary(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, -1),
		sec(2, 1, "cascade"),
		wt(3, 2),
	}

	if _, err := domain.PlanWorktreeRemoval(sessions, 1); err == nil {
		t.Fatal("PlanWorktreeRemoval error = nil, want malformed topology error")
	}
	if _, err := domain.PlanWorktreePrune(sessions, []int{1}); err == nil {
		t.Fatal("PlanWorktreePrune error = nil, want malformed topology error")
	}
	if _, err := domain.PlanSecondaryRemoval(sessions, 2); err == nil {
		t.Fatal("PlanSecondaryRemoval error = nil, want malformed topology error")
	}
}

func TestSessionTopologyPlannersRejectInvalidTargetsAndCycles(t *testing.T) {
	sessions := []*domain.Session{
		wt(1, 2),
		wt(2, 1),
		sec(3, 1, "cascade"),
	}

	if _, err := domain.PlanWorktreeRemoval(sessions, 99); err == nil {
		t.Fatal("missing worktree removal error = nil")
	}
	if _, err := domain.PlanWorktreeRemoval(sessions, 3); err == nil {
		t.Fatal("secondary worktree removal error = nil")
	}
	if _, err := domain.PlanWorktreePrune(sessions, []int{99}); err == nil {
		t.Fatal("missing worktree prune error = nil")
	}
	if _, err := domain.PlanSecondaryRemoval(sessions, 1); err == nil {
		t.Fatal("worktree secondary removal error = nil")
	}
	if _, err := domain.PlanWorktreeRemoval(sessions, 1); err == nil {
		t.Fatal("cycle error = nil")
	}
	if _, err := domain.PlanWorktreePrune(sessions, []int{1, 2}); err == nil {
		t.Fatal("prune cycle error = nil")
	}
}

func wt(id, parent int) *domain.Session {
	return domain.NewWorktreeSession(id, parent, 1, "wt", "branch", "/tmp/wt")
}

func sec(id, parent int, onDelete string) *domain.Session {
	return domain.NewSecondarySession(id, parent, 1, "sec", "rel", onDelete)
}

func assertInts(t *testing.T, got, want []int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %#v, want %#v", got, want)
	}
}

func assertReparents(t *testing.T, got, want []domain.SessionReparent) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reparents = %#v, want %#v", got, want)
	}
}
