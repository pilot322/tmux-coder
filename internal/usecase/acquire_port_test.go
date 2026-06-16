package usecase_test

import (
	"context"
	"sync"
	"testing"

	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestAcquirePortDuringHookSkipsOccupiedAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	leases := memory.NewMemoryResourceLeaseRepository()
	if err := leases.BeginHook(ctx, "hook-token", usecase.HookLeaseOwner{ProjectID: 7}); err != nil {
		t.Fatal(err)
	}
	ports := &fakePortAvailability{occupied: map[int]bool{8000: true}}
	uc := usecase.NewAcquirePort(nil, leases, ports, &spyLock{}, obs.Nop())

	first, err := uc.Execute(ctx, usecase.AcquirePortInput{HookToken: "hook-token", Key: "web", Start: 8000, End: 8002})
	if err != nil {
		t.Fatalf("Execute first: %v", err)
	}
	if first.Port != 8001 {
		t.Fatalf("first port = %d, want 8001", first.Port)
	}

	ports.occupied[8001] = true
	second, err := uc.Execute(ctx, usecase.AcquirePortInput{HookToken: "hook-token", Key: "web", Start: 8000, End: 8002})
	if err != nil {
		t.Fatalf("Execute second: %v", err)
	}
	if second.Port != 8001 {
		t.Fatalf("second port = %d, want existing lease 8001", second.Port)
	}
}

func TestAcquirePortUniquenessIsPerProject(t *testing.T) {
	ctx := context.Background()
	leases := memory.NewMemoryResourceLeaseRepository()
	if err := leases.BeginHook(ctx, "project-one", usecase.HookLeaseOwner{ProjectID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := leases.BeginHook(ctx, "project-two", usecase.HookLeaseOwner{ProjectID: 2}); err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewAcquirePort(nil, leases, &fakePortAvailability{occupied: map[int]bool{}}, &spyLock{}, obs.Nop())

	first, err := uc.Execute(ctx, usecase.AcquirePortInput{HookToken: "project-one", Key: "web", Start: 9000, End: 9000})
	if err != nil {
		t.Fatalf("Execute first: %v", err)
	}
	second, err := uc.Execute(ctx, usecase.AcquirePortInput{HookToken: "project-two", Key: "web", Start: 9000, End: 9000})
	if err != nil {
		t.Fatalf("Execute second: %v", err)
	}
	if first.Port != 9000 || second.Port != 9000 {
		t.Fatalf("ports = %d and %d, want both projects to lease 9000", first.Port, second.Port)
	}
}

func TestAcquirePortConcurrentHooksInSameProjectDoNotSharePort(t *testing.T) {
	ctx := context.Background()
	leases := memory.NewMemoryResourceLeaseRepository()
	if err := leases.BeginHook(ctx, "hook-one", usecase.HookLeaseOwner{ProjectID: 7}); err != nil {
		t.Fatal(err)
	}
	if err := leases.BeginHook(ctx, "hook-two", usecase.HookLeaseOwner{ProjectID: 7}); err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewAcquirePort(nil, leases, &fakePortAvailability{occupied: map[int]bool{}}, &spyLock{}, obs.Nop())

	var wg sync.WaitGroup
	ports := make(chan int, 2)
	errs := make(chan error, 2)
	for _, token := range []string{"hook-one", "hook-two"} {
		token := token
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := uc.Execute(ctx, usecase.AcquirePortInput{HookToken: token, Key: "web", Start: 9100, End: 9101})
			if err != nil {
				errs <- err
				return
			}
			ports <- out.Port
		}()
	}
	wg.Wait()
	close(ports)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	seen := make(map[int]bool)
	for port := range ports {
		seen[port] = true
	}
	if len(seen) != 2 || !seen[9100] || !seen[9101] {
		t.Fatalf("ports = %v, want distinct 9100 and 9101", seen)
	}
}

type fakePortAvailability struct {
	occupied map[int]bool
}

func (p *fakePortAvailability) Available(ctx context.Context, port int) bool {
	return !p.occupied[port]
}
