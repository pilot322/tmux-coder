package process

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.AgentProcessGateway = (*ProcessGateway)(nil)

type ProcessGateway struct{}

func NewProcessGateway() *ProcessGateway {
	return &ProcessGateway{}
}

func (g *ProcessGateway) TerminateProcessGroup(ctx context.Context, pgid int, sigtermTimeout time.Duration) error {
	if pgid <= 0 {
		return nil
	}

	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("send SIGKILL: %w", err)
	}
	return nil
}
