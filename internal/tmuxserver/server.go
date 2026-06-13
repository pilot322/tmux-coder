package tmuxserver

const (
	EnvName      = "TMUX_CODER_TMUX_SERVER"
	DefaultLabel = "tmux-coder"
)

func Label(getenv func(string) string) string {
	label := getenv(EnvName)
	if label == "" {
		return DefaultLabel
	}
	return label
}
