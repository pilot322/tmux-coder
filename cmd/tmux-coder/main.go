package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/pilot322/tmux-coder/internal/client/daemon"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/pilot322/tmux-coder/internal/client/tmuxattach"
	"github.com/pilot322/tmux-coder/internal/client/tui"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, os.Getwd); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, getwd func() (string, error)) error {
	addr := daemon.Address(getenv)
	logPath, err := daemon.Ensure(ctx, addr, daemon.Starter{HTTP: http.DefaultClient})
	if err != nil {
		if logPath != "" {
			return fmt.Errorf("start tmux-coderd: %w (log: %s)", err, logPath)
		}
		return fmt.Errorf("start tmux-coderd: %w", err)
	}

	api := httpclient.New(addr, http.DefaultClient)
	if len(args) == 0 {
		currentSession := tmuxattach.CurrentSession(ctx, getenv)
		sessionName, ok, err := tui.Run(ctx, api, currentSession)
		if err != nil || !ok {
			return err
		}
		return tmuxattach.Run(ctx, sessionName, getenv)
	}
	if len(args) == 1 && (args[0] == "o" || args[0] == "open") {
		cwd, err := getwd()
		if err != nil {
			return err
		}
		project, err := api.CreateProject(ctx, cwd)
		if err != nil {
			return err
		}
		return tmuxattach.Run(ctx, project.MainTmuxSessionName, getenv)
	}
	return fmt.Errorf("usage: tmux-coder [open|o]")
}
