package menu

import (
	"fmt"
	"path/filepath"

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

type SessionContext struct {
	Project          httpclient.Project
	Session          httpclient.Session
	SessionRoot      string
	WorktreeRoot     string
	WorkingDirectory string
	Branch           string
}

// ResolveSessionContext derives filesystem context from the Session Topology.
// A Secondary's relative directory is rooted at its nearest Main or Worktree
// ancestor rather than accumulated through Secondary parents.
func ResolveSessionContext(current httpclient.Session, sessions []httpclient.Session) (SessionContext, error) {
	byID := make(map[int]httpclient.Session, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = session
	}

	root := current
	seen := map[int]bool{}
	for root.Type == "secondary" {
		if seen[root.ID] {
			return SessionContext{}, fmt.Errorf("session %d has a parent cycle", current.ID)
		}
		seen[root.ID] = true
		parent, ok := byID[root.Parent]
		if !ok {
			return SessionContext{}, fmt.Errorf("session %d parent %d is unavailable", root.ID, root.Parent)
		}
		root = parent
	}

	var sessionRoot, worktreeRoot string
	switch root.Type {
	case "main":
		sessionRoot = current.Project.FullPath
	case "worktree":
		sessionRoot = root.Worktree
		worktreeRoot = root.Worktree
	default:
		return SessionContext{}, fmt.Errorf("session %d has unsupported type %q", root.ID, root.Type)
	}
	if sessionRoot == "" {
		return SessionContext{}, fmt.Errorf("session %d has no checkout root", current.ID)
	}

	workingDirectory := sessionRoot
	if current.Type == "secondary" {
		workingDirectory = filepath.Join(sessionRoot, current.RelativeWorkingDirectory)
	}
	return SessionContext{
		Project:          current.Project,
		Session:          current,
		SessionRoot:      sessionRoot,
		WorktreeRoot:     worktreeRoot,
		WorkingDirectory: workingDirectory,
		Branch:           root.Branch,
	}, nil
}
