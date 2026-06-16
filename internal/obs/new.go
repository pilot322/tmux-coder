package obs

import (
	"os"
	"time"
)

// New builds the base logger for a role: it derives the role directory from the
// instance label, prunes files past the retention age, opens the role's sink,
// and shapes records as JSON Lines. The composition root calls this once and
// tags per-component loggers off it with With. The sink stays open for the
// process lifetime; the OS reclaims it at exit.
func New(role Role, getenv func(string) string) (Logger, error) {
	dir, err := LogDir(role, getenv)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = sweep(dir, retentionAge, time.Now())

	sink, err := openSink(role, dir, time.Now())
	if err != nil {
		return nil, err
	}
	return newWithWriter(sink, role), nil
}
