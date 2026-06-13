package tmuxattach

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/pilot322/tmux-coder/internal/tmuxserver"
)

type Command struct {
	Args      []string
	UnsetTMUX bool
}

func Args(sessionName string, insideTmux bool) []string {
	return ArgsWithServer(tmuxserver.DefaultLabel, sessionName, insideTmux)
}

func ArgsWithServer(serverLabel, sessionName string, insideTmux bool) []string {
	if insideTmux {
		return []string{"-L", serverLabel, "switch-client", "-t", sessionName}
	}
	return []string{"-L", serverLabel, "attach-session", "-t", sessionName}
}

func SelectPaneArgs(paneID string) []string {
	return SelectPaneArgsWithServer(tmuxserver.DefaultLabel, paneID)
}

func SelectPaneArgsWithServer(serverLabel, paneID string) []string {
	return []string{"-L", serverLabel, "select-pane", "-t", paneID}
}

func SelectWindowArgs(paneID string) []string {
	return SelectWindowArgsWithServer(tmuxserver.DefaultLabel, paneID)
}

func SelectWindowArgsWithServer(serverLabel, paneID string) []string {
	return []string{"-L", serverLabel, "select-window", "-t", paneID}
}

func Commands(sessionName string, tmuxEnv string) []Command {
	return CommandsWithServer(tmuxserver.DefaultLabel, sessionName, tmuxEnv)
}

func CommandsWithServer(serverLabel, sessionName string, tmuxEnv string) []Command {
	attach := Command{Args: []string{"-L", serverLabel, "attach-session", "-t", sessionName}}
	if tmuxEnv == "" {
		return []Command{attach}
	}
	return []Command{
		{Args: []string{"-L", serverLabel, "switch-client", "-t", sessionName}},
		{Args: attach.Args, UnsetTMUX: true},
	}
}

func CurrentSession(ctx context.Context, getenv func(string) string) string {
	if getenv("TMUX") == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#S")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func CurrentPaneID(ctx context.Context, getenv func(string) string) string {
	if getenv("TMUX") == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func Run(ctx context.Context, sessionName string, getenv func(string) string) error {
	return runAttach(ctx, sessionName, "", getenv)
}

func RunPane(ctx context.Context, sessionName, paneID string, getenv func(string) string) error {
	return runAttach(ctx, sessionName, paneID, getenv)
}

func runAttach(ctx context.Context, sessionName, paneID string, getenv func(string) string) error {
	if paneID != "" {
		if err := runQuiet(ctx, Command{Args: SelectWindowArgsWithServer(tmuxserver.Label(getenv), paneID)}); err != nil {
			return err
		}
		if err := runQuiet(ctx, Command{Args: SelectPaneArgsWithServer(tmuxserver.Label(getenv), paneID)}); err != nil {
			return err
		}
	}
	commands := CommandsWithServer(tmuxserver.Label(getenv), sessionName, getenv("TMUX"))
	for i, c := range commands {
		if i < len(commands)-1 {
			if err := runQuiet(ctx, c); err == nil {
				return nil
			}
			continue
		}
		err := run(ctx, c)
		if err == nil || i == len(commands)-1 {
			return err
		}
	}
	return nil
}

func run(ctx context.Context, c Command) error {
	cmd := exec.CommandContext(ctx, "tmux", c.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if c.UnsetTMUX {
		cmd.Env = withoutTMUX(os.Environ())
	}
	return cmd.Run()
}

func runQuiet(ctx context.Context, c Command) error {
	cmd := exec.CommandContext(ctx, "tmux", c.Args...)
	if c.UnsetTMUX {
		cmd.Env = withoutTMUX(os.Environ())
	}
	return cmd.Run()
}

func withoutTMUX(env []string) []string {
	out := env[:0]
	for _, v := range env {
		if len(v) >= 5 && v[:5] == "TMUX=" {
			continue
		}
		out = append(out, v)
	}
	return out
}
