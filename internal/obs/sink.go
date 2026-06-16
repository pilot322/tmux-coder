package obs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// Rotation bounds for the long-lived per-pid files. DEBUG is voluminous, so the
// size cap and backup count matter — many instances log in parallel.
const (
	maxSizeMB  = 50
	maxBackups = 5
)

// openSink opens the file a role writes to, creating the role directory first.
// The granularity is hybrid by design (ADR-0012): the long-lived roles each own
// a <pid>.log rotated in place by lumberjack; agent-event, spawned fresh on
// every hook fire, appends to a shared per-day file so thousands of sub-second
// invocations don't carpet the directory with one-line files.
func openSink(role Role, dir string, now time.Time) (io.WriteCloser, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if role == RoleAgentEvent {
		name := now.Format("2006-01-02") + ".log"
		return os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	}
	return &lumberjack.Logger{
		Filename:   filepath.Join(dir, fmt.Sprintf("%d.log", os.Getpid())),
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		Compress:   true,
	}, nil
}
