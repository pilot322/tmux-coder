package tmuxserver

const EnvName = "TMUX_CODER_TMUX_SERVER"

// DefaultLabel is the tmux server label used when TMUX_CODER_TMUX_SERVER is
// unset. It is a var, not a const, so a dev build can override it via
// -ldflags -X to isolate the build's tmux server from the installed one; the
// shipped binary keeps "tmux-coder".
var DefaultLabel = "tmux-coder"

func Label(getenv func(string) string) string {
	label := getenv(EnvName)
	if label == "" {
		return DefaultLabel
	}
	return label
}
