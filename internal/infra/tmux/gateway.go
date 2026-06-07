// Package tmux implements usecase.SessionGateway by shelling out to a
// dedicated tmux server (-L tmux-coder) via os/exec, so tmux-coder's sessions
// never mix with the user's default tmux server.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.SessionGateway = (*TmuxGateway)(nil)

const serverLabel = "tmux-coder"

type TmuxGateway struct {
	binary string
}

func NewTmuxGateway() *TmuxGateway {
	return &TmuxGateway{binary: "tmux"}
}

// Create starts a new detached session named name, rooted at workingDir.
func (g *TmuxGateway) Create(ctx context.Context, name, workingDir string) error {
	_, err := g.run(ctx, "new-session", "-d", "-s", name, "-c", workingDir)
	return err
}

func (g *TmuxGateway) Kill(ctx context.Context, name string) error {
	_, err := g.run(ctx, "kill-session", "-t", name)
	return err
}

// Exists reports whether the session is present. has-session exits non-zero
// when the session is absent, which is a normal answer (false, nil) rather
// than an error; only a missing tmux or a cancelled context is a real error.
func (g *TmuxGateway) Exists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, g.binary, "-L", serverLabel, "has-session", "-t", name)
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
	full := append([]string{"-L", serverLabel}, args...)
	out, err := exec.CommandContext(ctx, g.binary, full...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("tmux %v: %w: %s", args, err, out)
	}
	return out, nil
}
