// Package daemonaddr resolves the HTTP address of the tmux-coder daemon. It is
// the neutral home for the daemon port default, imported by the client launcher
// (internal/client/daemon), the daemon's composition root (cmd/tmux-coderd), and
// the agent wrapper — so none of them has to duplicate the literal or import one
// another just for it. It mirrors tmuxserver, which plays the same role for the
// tmux server label.
package daemonaddr

// EnvName is the environment variable that overrides the daemon port at runtime.
const EnvName = "TMUX_CODERD_PORT"

// DefaultPort is the daemon HTTP port when EnvName is unset. It is a var, not a
// const, so a dev build can override it via -ldflags -X to isolate the build's
// daemon from the installed one; the shipped binary keeps "64357".
var DefaultPort = "64357"

// Port returns the daemon port: TMUX_CODERD_PORT if set, else DefaultPort.
func Port(getenv func(string) string) string {
	if p := getenv(EnvName); p != "" {
		return p
	}
	return DefaultPort
}

// Address returns the daemon base URL, honouring TMUX_CODERD_PORT.
func Address(getenv func(string) string) string {
	return "http://127.0.0.1:" + Port(getenv)
}

// DefaultAddress returns the daemon base URL for the baked-in default port,
// ignoring the environment. It is the fallback when no address is supplied.
func DefaultAddress() string {
	return "http://127.0.0.1:" + DefaultPort
}
