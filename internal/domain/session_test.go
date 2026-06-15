package domain_test

import (
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
)

// A Worktree Session records its Provenance in parent: -1 when branched from a
// bare base ref ('W'), or the source Session's id when branched from a Session
// ('w'). The flat name and git metadata are unaffected by provenance.
func TestNewWorktreeSessionCarriesProvenanceParent(t *testing.T) {
	parentless := domain.NewWorktreeSession(1, -1, 7, "api.feature", "feature", "/work/api.feature")
	if parentless.Parent() != -1 {
		t.Errorf("parentless worktree parent = %d, want -1", parentless.Parent())
	}

	nested := domain.NewWorktreeSession(2, 1, 7, "api.feature-backend", "feature-backend", "/work/api.feature-backend")
	if nested.Parent() != 1 {
		t.Errorf("nested worktree parent = %d, want 1", nested.Parent())
	}
	// Provenance lives only in parent; the name stays the flat derived value and
	// the git metadata is untouched (ADR-0010).
	if nested.Name() != "api.feature-backend" || nested.TmuxName() != "api_feature-backend" {
		t.Errorf("name=%q tmux=%q, want flat api.feature-backend", nested.Name(), nested.TmuxName())
	}
	if nested.Branch() != "feature-backend" || nested.WorktreePath() != "/work/api.feature-backend" {
		t.Errorf("branch=%q worktree=%q", nested.Branch(), nested.WorktreePath())
	}
	if nested.Type() != domain.WorktreeSession {
		t.Errorf("type = %v, want WorktreeSession", nested.Type())
	}
}
