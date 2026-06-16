package process

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.AgentProcessGateway = (*ProcessGateway)(nil)

type ProcessGateway struct {
	log obs.Logger
}

func NewProcessGateway(log obs.Logger) *ProcessGateway {
	return &ProcessGateway{log: log.With("component", "process")}
}

func (g *ProcessGateway) TerminateProcessGroup(ctx context.Context, pgid int, sigtermTimeout time.Duration) error {
	if pgid <= 0 {
		return nil
	}

	g.log.Debug(ctx, "signalling process group", "pgid", pgid, "signal", "SIGTERM")
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err != nil {
			g.log.Debug(ctx, "process group exited after SIGTERM", "pgid", pgid)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	g.log.Warn(ctx, "process group survived SIGTERM, sending SIGKILL", "pgid", pgid, "timeout", sigtermTimeout.String())
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("send SIGKILL: %w", err)
	}
	return nil
}
