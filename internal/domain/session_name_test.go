package domain_test

import (
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
)

// taken builds an isTaken predicate from a fixed set of already-used names.
func taken(names ...string) func(string) bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return func(name string) bool { return set[name] }
}

func TestDeriveMainSessionName(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		isTaken func(string) bool
		want    string
	}{
		{"basename plus .main suffix", "/work/api", taken(), "api.main"},
		{"numeric suffix on first collision", "/work/api", taken("api.main"), "api.main-2"},
		{"keeps bumping past multiple collisions", "/work/api", taken("api.main", "api.main-2"), "api.main-3"},
		{"sanitizes dots", "/work/my.api", taken(), "my-api.main"},
		{"sanitizes colons", "/work/web:cache", taken(), "web-cache.main"},
		{"sanitizes whitespace", "/work/my service", taken(), "my-service.main"},
		{"ignores trailing slash", "/work/api/", taken(), "api.main"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.DeriveMainSessionName(tt.path, tt.isTaken)
			if got != tt.want {
				t.Errorf("DeriveMainSessionName(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDeriveWorktreeSessionName(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		branch  string
		isTaken func(string) bool
		want    string
	}{
		{"basename dot branch slug", "/work/api", "feature/login", taken(), "api.feature-login"},
		{"numeric suffix on first collision", "/work/api", "feature/login", taken("api.feature-login"), "api.feature-login-2"},
		{"sanitizes basename before dot separator", "/work/my.api", "feature", taken(), "my-api.feature"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.DeriveWorktreeSessionName(tt.path, tt.branch, tt.isTaken)
			if got != tt.want {
				t.Errorf("DeriveWorktreeSessionName(%q, %q) = %q, want %q", tt.path, tt.branch, got, tt.want)
			}
		})
	}
}

func TestDeriveSecondaryTmuxSessionName(t *testing.T) {
	tests := []struct {
		name           string
		parentTmuxName string
		sessionName    string
		want           string
	}{
		{"secondary under main root", "api_main", "backend", "api_main_backend"},
		{"secondary under worktree root", "api_auth", "backend", "api_auth_backend"},
		{"secondary nested under another secondary", "api_auth_backend", "tools", "api_auth_backend_tools"},
		{"replaces dot separators in the session name", "api_main", "web.app", "api_main_web_app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.DeriveSecondaryTmuxSessionName(tt.parentTmuxName, tt.sessionName)
			if got != tt.want {
				t.Errorf("DeriveSecondaryTmuxSessionName(%q, %q) = %q, want %q", tt.parentTmuxName, tt.sessionName, got, tt.want)
			}
		})
	}
}

func TestDeriveSecondarySessionName(t *testing.T) {
	tests := []struct {
		name          string
		preferredName string
		isTaken       func(string) bool
		want          string
	}{
		{"unprefixed preferred name", "backend", taken(), "backend"},
		{"sibling collision bumps suffix", "backend", taken("backend"), "backend-2"},
		{"sanitizes separators", "web.app", taken(), "web-app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.DeriveSecondarySessionName(tt.preferredName, tt.isTaken)
			if got != tt.want {
				t.Errorf("DeriveSecondarySessionName(%q) = %q, want %q", tt.preferredName, got, tt.want)
			}
		})
	}
}

func TestDeriveTmuxSessionNameReplacesDotSeparators(t *testing.T) {
	got := domain.DeriveTmuxSessionName("api.feature-login")
	if got != "api_feature-login" {
		t.Fatalf("DeriveTmuxSessionName() = %q, want %q", got, "api_feature-login")
	}
}
