package domain

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// DeriveMainSessionName returns the tmux session name for a project's Main
// Session (ADR-0004): the sanitized path basename plus ".main", bumping a
// numeric suffix (-2, -3, ...) until isTaken reports the candidate is free.
func DeriveMainSessionName(fullPath string, isTaken func(name string) bool) string {
	base := sanitize(filepath.Base(fullPath))

	candidate := base + ".main"
	for n := 2; isTaken(candidate); n++ {
		candidate = fmt.Sprintf("%s.main-%d", base, n)
	}
	return candidate
}

// DeriveWorktreeSessionName returns the tmux session name for a Worktree
// Session. The same value is also used as the sibling worktree directory name.
func DeriveWorktreeSessionName(projectPath, branch string, isTaken func(name string) bool) string {
	base := sanitize(filepath.Base(projectPath))
	slug := sanitize(branch)
	candidate := base + "." + slug
	for n := 2; isTaken(candidate); n++ {
		candidate = fmt.Sprintf("%s.%s-%d", base, slug, n)
	}
	return candidate
}

// DeriveSecondarySessionName returns a Secondary Session name unique among its
// siblings (ADR-0006 as amended by ADR-0007). The caller scopes isTaken to the
// sibling set, so the same display name may repeat across subtrees.
func DeriveSecondarySessionName(preferredName string, isTaken func(name string) bool) string {
	base := sanitize(preferredName)
	candidate := base
	for n := 2; isTaken(candidate); n++ {
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return candidate
}

// DeriveSecondaryTmuxSessionName returns the globally-unique tmux target for a
// Secondary Session: its parent Session's tmux name plus the derived Secondary
// Session name (ADR-0007). Because the parent tmux name already encodes the
// chain up to the globally-unique root, sibling-unique CLI names suffice — e.g.
// `api_main_backend`, `api_auth_backend`, `api_auth_backend_tools`. The
// CLI-facing Secondary Session name remains unprefixed.
func DeriveSecondaryTmuxSessionName(parentTmuxName, sessionName string) string {
	return parentTmuxName + "_" + DeriveTmuxSessionName(sessionName)
}

// sanitize replaces reserved separator characters (".", ":") and any
// whitespace with "-". Path separators are also replaced so branch names can
// safely become sibling directory basenames.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == ':' || r == '/' || r == '\\' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, s)
}
