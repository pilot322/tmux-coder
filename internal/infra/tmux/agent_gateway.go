package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.AgentTmuxGateway = (*TmuxGateway)(nil)
var _ usecase.OpenCodeSetupTmuxGateway = (*TmuxGateway)(nil)

var setupBufferID atomic.Uint64

func (g *TmuxGateway) NewWindow(ctx context.Context, sessionName, windowName, workingDir, command string, env []string) (string, error) {
	args := []string{"new-window", "-P", "-F", "#{pane_id}", "-t", sessionName, "-n", windowName, "-c", workingDir}
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

func (g *TmuxGateway) SetPaneInput(ctx context.Context, paneID string, enabled bool) error {
	flag := "-d"
	if enabled {
		flag = "-e"
	}
	if _, err := g.run(ctx, "select-pane", flag, "-t", paneID); err != nil {
		return fmt.Errorf("select-pane %s: %w", flag, err)
	}
	return nil
}

// PasteLiteral loads text over stdin so prompt contents never enter argv,
// shell syntax, tmux key parsing, or structured command logs.
func (g *TmuxGateway) PasteLiteral(ctx context.Context, paneID, text string) error {
	buffer := "tmux-coder-setup-" + strconv.FormatUint(setupBufferID.Add(1), 10)
	cmd := g.cmd(ctx, "load-buffer", "-b", buffer, "-")
	cmd.Stdin = strings.NewReader(text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("load-buffer: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_, err := g.run(ctx,
		"select-pane", "-e", "-t", paneID, ";",
		"paste-buffer", "-p", "-d", "-b", buffer, "-t", paneID, ";",
		"select-pane", "-d", "-t", paneID,
	)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = g.run(cleanupCtx, "delete-buffer", "-b", buffer)
		cancel()
		return fmt.Errorf("paste-buffer: %w", err)
	}
	return nil
}

func (g *TmuxGateway) SendEnter(ctx context.Context, paneID string) error {
	_, err := g.run(ctx,
		"select-pane", "-e", "-t", paneID, ";",
		"send-keys", "-t", paneID, "Enter", ";",
		"select-pane", "-d", "-t", paneID,
	)
	if err != nil {
		return fmt.Errorf("send Enter: %w", err)
	}
	return nil
}

// OpenCode resets a filtered picker's selection asynchronously. Selecting the
// first result explicitly prevents Enter from racing that deferred reset.
func (g *TmuxGateway) ConfirmPickerSelection(ctx context.Context, paneID string) error {
	_, err := g.run(ctx,
		"select-pane", "-e", "-t", paneID, ";",
		"send-keys", "-t", paneID, "Home", "Enter", ";",
		"select-pane", "-d", "-t", paneID,
	)
	if err != nil {
		return fmt.Errorf("confirm picker selection: %w", err)
	}
	return nil
}

func (g *TmuxGateway) cmd(ctx context.Context, args ...string) *exec.Cmd {
	g.log.Debug(ctx, "tmux exec", "args", args)
	full := append([]string{"-L", g.serverLabel}, args...)
	return exec.CommandContext(ctx, g.binary, full...)
}
