package domain

// SessionType enumerates the kinds of Session tmux-coder manages.
type SessionType uint8

const (
	MainSession SessionType = iota
	SecondarySession
	WorktreeSession
)

// Session is a 1:1 wrapper around a tmux session on the dedicated server.
// name is also the tmux target name and must be unique on that server.
type Session struct {
	id        int
	parent    int // -1 means parentless
	projectID int
	name      string
	kind      SessionType
}

// NewSession builds a Session. Use parent == -1 for a parentless session.
func NewSession(id, parent, projectID int, name string, kind SessionType) *Session {
	return &Session{
		id:        id,
		parent:    parent,
		projectID: projectID,
		name:      name,
		kind:      kind,
	}
}

func (s *Session) ID() int           { return s.id }
func (s *Session) Parent() int       { return s.parent }
func (s *Session) ProjectID() int    { return s.projectID }
func (s *Session) Name() string      { return s.name }
func (s *Session) Type() SessionType { return s.kind }
