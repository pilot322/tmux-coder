package domain

const DefaultMaxProjectTitleLength = 40

// DaemonConfig holds daemon-wide settings that govern behavior across all
// Projects. It is in memory for now, but is shaped so a file loader can supply
// it later.
type DaemonConfig struct {
	MaxProjectTitleLength int
}

func DefaultDaemonConfig() DaemonConfig {
	return DaemonConfig{MaxProjectTitleLength: DefaultMaxProjectTitleLength}
}

func (c DaemonConfig) ProjectTitleLimit() int {
	if c.MaxProjectTitleLength <= 0 {
		return DefaultMaxProjectTitleLength
	}
	return c.MaxProjectTitleLength
}
