package domain

import (
	"errors"
	"strings"
	"unicode"
)

var ErrInvalidProjectTitle = errors.New("invalid project title")

func CleanProjectTitle(title string, maxLength int) (string, error) {
	title = strings.TrimSpace(title)
	if !isValidProjectTitle(title, maxLength) {
		return "", ErrInvalidProjectTitle
	}
	return title, nil
}

func DefaultProjectTitle(base string, maxLength int) string {
	title := strings.TrimSpace(base)
	if isValidProjectTitle(title, maxLength) {
		return title
	}

	var b strings.Builder
	lastSpace := false
	for _, r := range title {
		if unicode.IsControl(r) {
			continue
		}
		if unicode.IsSpace(r) {
			if b.Len() > 0 && !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}

	title = strings.TrimSpace(b.String())
	if runeCount(title) > maxLength {
		title = string([]rune(title)[:maxLength])
		title = strings.TrimSpace(title)
	}
	if isValidProjectTitle(title, maxLength) {
		return title
	}
	return "Project"
}

func isValidProjectTitle(title string, maxLength int) bool {
	if maxLength <= 0 {
		maxLength = DefaultMaxProjectTitleLength
	}
	if title == "" || strings.TrimSpace(title) != title || strings.Contains(title, "  ") || runeCount(title) > maxLength {
		return false
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func runeCount(s string) int { return len([]rune(s)) }
