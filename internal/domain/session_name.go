package domain

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// DeriveMainSessionName returns the tmux session name for a project's Main
// Session (ADR-0004): the sanitized path basename plus "-main", bumping a
// numeric suffix (-2, -3, ...) until isTaken reports the candidate is free.
func DeriveMainSessionName(fullPath string, isTaken func(name string) bool) string {
	base := sanitize(filepath.Base(fullPath))

	candidate := base + "-main"
	for n := 2; isTaken(candidate); n++ {
		candidate = fmt.Sprintf("%s-main-%d", base, n)
	}
	return candidate
}

// sanitize replaces characters tmux forbids in session names (".", ":") and
// any whitespace with "-".
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == ':' || unicode.IsSpace(r) {
			return '-'
		}
		return r
	}, s)
}
