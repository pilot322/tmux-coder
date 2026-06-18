package obs

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pilot322/tmux-coder/internal/tmuxserver"
)

const installedTmuxServerLabel = "tmux-coder"

// Role names the kind of process a log file belongs to. It is the leaf
// directory under an instance's logs/ tree and is recorded as the role field on
// every line, so a shared file stays sliceable by process kind.
type Role string

const (
	RoleDaemon     Role = "daemon"
	RoleTUI        Role = "tui"
	RoleAgentEvent Role = "agent-event"
	RoleWrapper    Role = "wrapper"
)

// LogDir returns the directory a process of the given role writes its logs to.
// The path is a pure function of the instance identity the binaries already
// carry — the tmux server label — so every process of one instance agrees on it
// for free. The installed build (label "tmux-coder") logs under
// ~/.tmux-coder/logs/<role>; a dev build (label "tmux-coder-<worktree>") nests
// under ~/.tmux-coder/logs/dev-<worktree>/<role>, keeping parallel instances
// from interleaving on disk.
func LogDir(role Role, getenv func(string) string) (string, error) {
	home := getenv("HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}

	base := filepath.Join(home, ".tmux-coder", "logs")
	if label := tmuxserver.Label(getenv); label != installedTmuxServerLabel {
		worktree := strings.TrimPrefix(label, installedTmuxServerLabel+"-")
		base = filepath.Join(base, "dev-"+worktree)
	}
	return filepath.Join(base, string(role)), nil
}
