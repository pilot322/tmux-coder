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

const agentWrapperCommand = "agent-wrapper"

func main() {
	// Composition root: dispatch between the two runtime modes before any
	// client-only or wrapper-only setup runs.
	if isAgentWrapperMode(os.Args) {
		os.Exit(runAgentWrapper(os.Args[2:], ""))
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
	return len(args) >= 2 && args[1] == agentWrapperCommand
}
