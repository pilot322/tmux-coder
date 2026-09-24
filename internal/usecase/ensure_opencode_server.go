package usecase

import (
	"context"
	"fmt"
)

type OpenCodeServerGateway interface {
	Ensure(ctx context.Context) (OpenCodeConnection, error)
}

// OpenCodeConnection is returned only by the internal loopback resource API.
// Password must never be included in dashboard agent DTOs or links.
type OpenCodeConnection struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

type EnsureOpenCodeServer struct {
	server OpenCodeServerGateway
}

func NewEnsureOpenCodeServer(server OpenCodeServerGateway) *EnsureOpenCodeServer {
	return &EnsureOpenCodeServer{server: server}
}

func (uc *EnsureOpenCodeServer) Execute(ctx context.Context) (OpenCodeConnection, error) {
	if uc == nil || uc.server == nil {
		return OpenCodeConnection{}, fmt.Errorf("%w: OpenCode server is not configured", ErrGateway)
	}
	connection, err := uc.server.Ensure(ctx)
	if err != nil {
		return OpenCodeConnection{}, fmt.Errorf("%w: %v", ErrGateway, err)
	}
	return connection, nil
}
