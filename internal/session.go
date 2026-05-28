// Package sessions - all things session
package main

import "fmt"

type SessionType uint8

const (
	MAIN_SESSION SessionType = iota
	SECONDARY_SESSION
	WORKTREE_SESSION
)

type Session struct {
	id          int
	parent      int // -1 is parentless
	projectID   int
	sessionName string
	sessionType SessionType
}
