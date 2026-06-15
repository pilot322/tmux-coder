package usecase

// Conflict codes carried on a StateConflictError. They are a wire contract:
// the HTTP adapter copies them onto the 409 body and the Client matches on them
// to offer the right follow-up prompt, so these strings must not drift.
const (
	CodeSessionExists  = "session_exists"
	CodeWorktreeExists = "worktree_exists"
	CodePathBlocked    = "path_blocked"
	CodeBranchExists   = "branch_exists"
)

// StateConflictError is an ErrConflict (it maps to 409) that additionally
// carries a machine-readable Code describing which pre-existing Git/session
// state blocked the create, so the Client can react without parsing prose.
type StateConflictError struct {
	Code string
	Msg  string
}

func (e *StateConflictError) Error() string { return e.Msg }

// Is reports StateConflictError as an ErrConflict so existing
// errors.Is(err, ErrConflict) checks (and the 409 mapping) keep working.
func (e *StateConflictError) Is(target error) bool { return target == ErrConflict }
