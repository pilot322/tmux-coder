package netport

import (
	"context"
	"fmt"
	"net"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.PortAvailabilityGateway = (*Checker)(nil)

type Checker struct {
	log obs.Logger
}

func NewChecker(log obs.Logger) *Checker {
	return &Checker{log: log.With("component", "netport")}
}

func (c *Checker) Available(ctx context.Context, port int) bool {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		c.log.Debug(ctx, "port unavailable", "port", port, "err", err.Error())
		return false
	}
	_ = listener.Close()
	c.log.Debug(ctx, "port available", "port", port)
	return true
}
