// Package tmux implements usecase.SessionGateway by shelling out to a
// dedicated tmux server via os/exec, so tmux-coder's sessions never mix with
// the user's default tmux server.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/tmuxserver"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.SessionGateway = (*TmuxGateway)(nil)

type TmuxGateway struct {
	binary      string
	serverLabel string
	log         obs.Logger
}

func NewTmuxGateway(log obs.Logger) *TmuxGateway {
	return &TmuxGateway{binary: "tmux", serverLabel: tmuxserver.Label(os.Getenv), log: log.With("component", "tmux")}
}

// Create starts a new detached session named name, rooted at workingDir.
func (g *TmuxGateway) Create(ctx context.Context, name, workingDir string) error {
	_, err := g.run(ctx, "new-session", "-d", "-s", name, "-c", workingDir)
	return err
}

// Kill removes the session. A session that is already gone is not an error:
// kill-session exits non-zero when the target is missing (which also happens
// when killing the last attached session tears the server down), and treating
// that as success keeps deletion idempotent so a half-finished delete can be
// retried without stranding the session record.
func (g *TmuxGateway) Kill(ctx context.Context, name string) error {
	exists, err := g.Exists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = g.run(ctx, "kill-session", "-t", name)
	return err
}

// SwitchClients moves every client attached to from over to to, so the from
// session can be killed without detaching the user's terminal. list-clients
// exits non-zero when from no longer exists, which we treat as "no clients".
func (g *TmuxGateway) SwitchClients(ctx context.Context, from, to string) error {
	g.log.Debug(ctx, "tmux exec", "args", []string{"list-clients", "-t", from})
	cmd := exec.CommandContext(ctx, g.binary, "-L", g.serverLabel, "list-clients", "-t", from, "-F", "#{client_name}")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return err
	}
	for _, client := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if client == "" {
			continue
		}
		if _, err := g.run(ctx, "switch-client", "-c", client, "-t", to); err != nil {
			return err
		}
	}
	return nil
}

// Exists reports whether the session is present. has-session exits non-zero
// when the session is absent, which is a normal answer (false, nil) rather
// than an error; only a missing tmux or a cancelled context is a real error.
func (g *TmuxGateway) Exists(ctx context.Context, name string) (bool, error) {
	g.log.Debug(ctx, "tmux exec", "args", []string{"has-session", "-t", name})
	cmd := exec.CommandContext(ctx, g.binary, "-L", g.serverLabel, "has-session", "-t", name)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func (g *TmuxGateway) run(ctx context.Context, args ...string) ([]byte, error) {
	g.log.Debug(ctx, "tmux exec", "args", args)
	full := append([]string{"-L", g.serverLabel}, args...)
	out, err := exec.CommandContext(ctx, g.binary, full...).CombinedOutput()
	if err != nil {
		g.log.Error(ctx, "tmux exec failed", "args", args, "err", err.Error(), "output", strings.TrimSpace(string(out)))
		return out, fmt.Errorf("tmux %v: %w: %s", args, err, out)
	}
	return out, nil
}
