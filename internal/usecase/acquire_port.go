package usecase

import (
	"context"
	"fmt"

	"github.com/pilot322/tmux-coder/internal/obs"
)

type PortAvailabilityGateway interface {
	Available(ctx context.Context, port int) bool
}

type AcquirePortInput struct {
	Key       string
	Start     int
	End       int
	HookToken string
	ProjectID int
	SessionID int
}

type AcquirePortOutput struct {
	Port int
}

type AcquirePort struct {
	sessions ISessionRepository
	leases   ResourceLeaseRepository
	ports    PortAvailabilityGateway
	lock     StateLock
	log      obs.Logger
}

func NewAcquirePort(s ISessionRepository, leases ResourceLeaseRepository, ports PortAvailabilityGateway, l StateLock, log obs.Logger) *AcquirePort {
	if leases == nil {
		leases = noopResourceLeaseRepository{}
	}
	if ports == nil {
		ports = alwaysAvailablePorts{}
	}
	return &AcquirePort{sessions: s, leases: leases, ports: ports, lock: l, log: log.With("component", "acquire-port")}
}

func (uc *AcquirePort) Execute(ctx context.Context, in AcquirePortInput) (AcquirePortOutput, error) {
	if in.Key == "" {
		return AcquirePortOutput{}, fmt.Errorf("%w: key is required", ErrValidation)
	}
	if in.Start < 1 || in.Start > 65535 || in.End < 1 || in.End > 65535 || in.Start > in.End {
		return AcquirePortOutput{}, fmt.Errorf("%w: port range is invalid", ErrValidation)
	}

	req := PortLeaseRequest{Key: in.Key, Start: in.Start, End: in.End}
	if in.HookToken != "" {
		req.OwnerKind = ResourceLeaseOwnerHook
		req.HookToken = in.HookToken
	} else {
		if in.ProjectID == 0 || in.SessionID == 0 {
			return AcquirePortOutput{}, fmt.Errorf("%w: projectId and sessionId are required", ErrValidation)
		}
		if uc.sessions == nil || uc.lock == nil {
			return AcquirePortOutput{}, fmt.Errorf("%w: session lookup is not configured", ErrGateway)
		}
		if err := uc.lock.WithRead(func() error {
			session, err := uc.sessions.GetByID(ctx, in.SessionID)
			if err != nil {
				return err
			}
			if session.ProjectID() != in.ProjectID {
				return fmt.Errorf("%w: session does not belong to project", ErrValidation)
			}
			return nil
		}); err != nil {
			return AcquirePortOutput{}, err
		}
		req.OwnerKind = ResourceLeaseOwnerSession
		req.ProjectID = in.ProjectID
		req.SessionID = in.SessionID
	}

	port, err := uc.leases.AcquirePort(ctx, req, func(port int) bool {
		return uc.ports.Available(ctx, port)
	})
	if err != nil {
		return AcquirePortOutput{}, err
	}
	uc.log.Info(ctx, "port acquired", "key", in.Key, "port", port, "owner", string(req.OwnerKind))
	return AcquirePortOutput{Port: port}, nil
}

type alwaysAvailablePorts struct{}

func (alwaysAvailablePorts) Available(ctx context.Context, port int) bool { return true }
