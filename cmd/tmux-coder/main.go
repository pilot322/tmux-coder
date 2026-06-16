// Command tmux-coder is the interactive tmux-coder client. It is a dual-mode
// binary: the default mode is a short-lived CLI/TUI, and the hidden
// "agent-wrapper" subcommand is the long-running pane babysitter started by
// tmux or by the client itself when it takes over the current pane.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const (
	agentWrapperCommand       = "agent-wrapper"
	agentEventCommand         = "agent-event"
	installClaudeHooksCommand = "install-claude-hooks"
)

func main() {
	// Composition root: dispatch the short-lived plumbing subcommands before any
	// client-only setup or daemon launch runs. agent-wrapper and agent-event are
	// invoked by tmux and by agent hooks; install-claude-hooks is a one-shot
	// config writer that must not start the daemon.
	if isAgentWrapperMode(os.Args) {
		os.Exit(runAgentWrapper(os.Args[2:], ""))
	}
	if isSubcommand(os.Args, agentEventCommand) {
		os.Exit(runAgentEvent(os.Args[2:]))
	}
	if isSubcommand(os.Args, installClaudeHooksCommand) {
		if err := runInstallClaudeHooks(os.Args[2:], os.Getenv, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := runClient(context.Background(), os.Args[1:], os.Getenv, os.Getwd); err != nil {
		var wrapperExit agentWrapperExitError
		if errors.As(err, &wrapperExit) {
			os.Exit(wrapperExit.code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func isAgentWrapperMode(args []string) bool {
	return isSubcommand(args, agentWrapperCommand)
}

func isSubcommand(args []string, name string) bool {
	return len(args) >= 2 && args[1] == name
}
