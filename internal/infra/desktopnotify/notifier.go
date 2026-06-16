// Package desktopnotify delivers Desktop Notifications to the host by shelling
// out to notify-send. It is the dumb mechanism behind usecase.Notifier; the
// policy (which Agent Status transitions notify, and the message content) lives
// in the usecase.
package desktopnotify

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var (
	_ usecase.Notifier = (*Notifier)(nil)
	_ usecase.Notifier = NoopNotifier{}
)

// notifyTimeout bounds a single notify-send invocation so a wedged notification
// daemon can never stall agent-event processing.
const notifyTimeout = 2 * time.Second

// Notifier delivers Desktop Notifications via notify-send. CommandContext is
// injected so tests can assert the argv without spawning a process.
type Notifier struct {
	CommandContext func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewNotifier returns a notify-send-backed Notifier when running on Linux with
// notify-send on PATH, otherwise a NoopNotifier. Selection is at runtime rather
// than via build tags: shelling out needs no Linux-only imports, and this also
// covers a Linux host without libnotify installed.
func NewNotifier() usecase.Notifier {
	return newNotifier(runtime.GOOS, exec.LookPath, exec.CommandContext)
}

func newNotifier(goos string, lookPath func(string) (string, error), commandContext func(ctx context.Context, name string, args ...string) *exec.Cmd) usecase.Notifier {
	if goos != "linux" {
		return NoopNotifier{}
	}
	if _, err := lookPath("notify-send"); err != nil {
		return NoopNotifier{}
	}
	return &Notifier{CommandContext: commandContext}
}

// Notify shells out to notify-send. The daemon is launched with no explicit
// cmd.Env (see internal/client/daemon/daemon.go), so it inherits the launching
// client's session env, including DBUS_SESSION_BUS_ADDRESS, which notify-send
// needs. If the daemon was first auto-launched from a context with no session
// bus (e.g. a bare SSH login), notify-send fails; callers swallow the error.
func (n *Notifier) Notify(ctx context.Context, msg usecase.Notification) error {
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	cmd := n.CommandContext(ctx, "notify-send", "-u", urgencyFlag(msg.Urgency), "-a", "tmux-coder", msg.Title, msg.Body)
	return cmd.Run()
}

func urgencyFlag(u usecase.NotificationUrgency) string {
	if u == usecase.UrgencyCritical {
		return "critical"
	}
	return "normal"
}

// NoopNotifier discards Desktop Notifications. It is selected when the host has
// no usable notify-send.
type NoopNotifier struct{}

func (NoopNotifier) Notify(context.Context, usecase.Notification) error { return nil }
