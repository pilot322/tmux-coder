package netport

import (
	"context"
	"fmt"
	"net"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.PortAvailabilityGateway = (*Checker)(nil)

type Checker struct{}

func NewChecker() *Checker {
	return &Checker{}
}

func (c *Checker) Available(ctx context.Context, port int) bool {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}
