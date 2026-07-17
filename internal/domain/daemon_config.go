package domain

import "strconv"

const DefaultMaxProjectTitleLength = 40

// DefaultOpenCodeServerPort is a string so development builds can replace it
// with a per-worktree value using -ldflags -X.
var DefaultOpenCodeServerPort = "39155"

// DaemonConfig holds daemon-wide settings that govern behavior across all
// Projects. It is in memory for now, but is shaped so a file loader can supply
// it later.
type DaemonConfig struct {
	MaxProjectTitleLength int
	DiscordWebhookNotify  string
	OpenCodeServerPort    int
}

func (c DaemonConfig) DiscordWebhookConfigured() bool {
	return c.DiscordWebhookNotify != ""
}

func DefaultDaemonConfig() DaemonConfig {
	port, err := strconv.Atoi(DefaultOpenCodeServerPort)
	if err != nil || port < 1 || port > 65535 {
		port = 39155
	}
	return DaemonConfig{
		MaxProjectTitleLength: DefaultMaxProjectTitleLength,
		OpenCodeServerPort:    port,
	}
}

func (c DaemonConfig) ProjectTitleLimit() int {
	if c.MaxProjectTitleLength <= 0 {
		return DefaultMaxProjectTitleLength
	}
	return c.MaxProjectTitleLength
}
