package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.AgentTmuxGateway = (*TmuxGateway)(nil)

func (g *TmuxGateway) NewWindow(ctx context.Context, sessionName, windowName, workingDir, command string, env []string) (string, error) {
	args := []string{"-L", g.serverLabel, "new-window", "-P", "-F", "#{pane_id}", "-t", sessionName, "-n", windowName, "-c", workingDir}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, command)
	out, err := g.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("new-window: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *TmuxGateway) PaneExists(ctx context.Context, paneID string) (bool, error) {
	cmd := g.cmd(ctx, "list-panes", "-t", paneID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			output := strings.TrimSpace(string(out))
			if isTmuxTargetNotFound(output) {
				g.log.Warn(ctx, "tmux pane target not found", "pane_id", paneID, "status", exitErr.ExitCode(), "output", output)
				return false, nil
			}
			g.log.Warn(ctx, "tmux pane existence check failed", "pane_id", paneID, "status", exitErr.ExitCode(), "output", output)
			return false, fmt.Errorf("list-panes -t %s: %w: %s", paneID, err, output)
		}
		return false, err
	}
	return true, nil
}

func isTmuxTargetNotFound(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "can't find pane") ||
		strings.Contains(output, "can't find window") ||
		strings.Contains(output, "can't find session")
}

func (g *TmuxGateway) RenameWindow(ctx context.Context, paneID, name string) error {
	_, err := g.run(ctx, "rename-window", "-t", paneID, name)
	if err != nil {
		return fmt.Errorf("rename-window: %w", err)
	}
	return nil
}

func (g *TmuxGateway) KillPane(ctx context.Context, paneID string) error {
	_, err := g.run(ctx, "kill-pane", "-t", paneID)
	return err
}

func (g *TmuxGateway) ListPanes(ctx context.Context, sessionName string) ([]string, error) {
	out, err := g.run(ctx, "list-panes", "-t", sessionName, "-F", "#{pane_id}")
	if err != nil {
		return nil, fmt.Errorf("list-panes: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result, nil
}

func (g *TmuxGateway) cmd(ctx context.Context, args ...string) *exec.Cmd {
	g.log.Debug(ctx, "tmux exec", "args", args)
	full := append([]string{"-L", g.serverLabel}, args...)
	return exec.CommandContext(ctx, g.binary, full...)
}
