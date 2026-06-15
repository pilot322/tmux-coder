package domain

import "strings"

// SessionType enumerates the kinds of Session tmux-coder manages.
type SessionType uint8

const (
	MainSession SessionType = iota
	SecondarySession
	WorktreeSession
)

// Session is a 1:1 wrapper around a tmux session on the dedicated server.
// name is the user-facing name; tmuxName is the actual tmux target name.
type Session struct {
	id        int
	parent    int // -1 means parentless
	projectID int
	name      string
	tmuxName  string
	kind      SessionType
	branch    string
	worktree  string
	relwd     string
	onDelete  string
}

// NewSession builds a Session. Use parent == -1 for a parentless session.
func NewSession(id, parent, projectID int, name string, kind SessionType) *Session {
	return &Session{
		id:        id,
		parent:    parent,
		projectID: projectID,
		name:      name,
		tmuxName:  DeriveTmuxSessionName(name),
		kind:      kind,
	}
}

// NewWorktreeSession builds a Worktree Session with the Git metadata tmux-coder
// needs to enforce duplicate branch creation and remove the owned worktree. The
// parent records its Provenance (ADR-0010): the source Session's id, or -1 when
// it was branched from a bare base ref and sits at the Project level.
func NewWorktreeSession(id, parent, projectID int, name, branch, worktree string) *Session {
	return &Session{
		id:        id,
		parent:    parent,
		projectID: projectID,
		name:      name,
		tmuxName:  DeriveTmuxSessionName(name),
		kind:      WorktreeSession,
		branch:    branch,
		worktree:  worktree,
	}
}

// NewSecondarySession builds a Secondary Session with its parent and lifecycle metadata.
func NewSecondarySession(id, parent, projectID int, name, relativeWorkingDirectory, onDelete string) *Session {
	return NewSecondarySessionWithTmuxName(id, parent, projectID, name, DeriveTmuxSessionName(name), relativeWorkingDirectory, onDelete)
}

// NewSecondarySessionWithTmuxName builds a Secondary Session with an explicit tmux target name.
func NewSecondarySessionWithTmuxName(id, parent, projectID int, name, tmuxName, relativeWorkingDirectory, onDelete string) *Session {
	return &Session{
		id:        id,
		parent:    parent,
		projectID: projectID,
		name:      name,
		tmuxName:  tmuxName,
		kind:      SecondarySession,
		relwd:     relativeWorkingDirectory,
		onDelete:  onDelete,
	}
}

func DeriveTmuxSessionName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

func (s *Session) ID() int           { return s.id }
func (s *Session) Parent() int       { return s.parent }
func (s *Session) ProjectID() int    { return s.projectID }
func (s *Session) Name() string      { return s.name }
func (s *Session) TmuxName() string  { return s.tmuxName }
func (s *Session) Type() SessionType { return s.kind }
func (s *Session) Branch() string    { return s.branch }
func (s *Session) WorktreePath() string {
	return s.worktree
}
func (s *Session) RelativeWorkingDirectory() string { return s.relwd }
func (s *Session) OnDelete() string                 { return s.onDelete }
