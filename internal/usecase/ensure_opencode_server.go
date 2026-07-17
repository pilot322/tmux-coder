package usecase

import (
	"context"
	"fmt"
)

type OpenCodeServerGateway interface {
	Ensure(ctx context.Context) (string, error)
}

type EnsureOpenCodeServer struct {
	server OpenCodeServerGateway
}

func NewEnsureOpenCodeServer(server OpenCodeServerGateway) *EnsureOpenCodeServer {
	return &EnsureOpenCodeServer{server: server}
}

func (uc *EnsureOpenCodeServer) Execute(ctx context.Context) (string, error) {
	if uc == nil || uc.server == nil {
		return "", fmt.Errorf("%w: OpenCode server is not configured", ErrGateway)
	}
	url, err := uc.server.Ensure(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGateway, err)
	}
	return url, nil
}
