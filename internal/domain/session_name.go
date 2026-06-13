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
