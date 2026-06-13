package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/pilot322/tmux-coder/internal/agentwrapper"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

// runAgentWrapper starts the agent-wrapper runtime mode. daemonAddr is only
// supplied when the client itself is taking over the current pane; in the
// hidden-subcommand path the daemon address is already present in the
// environment set by tmux or the caller.
func runAgentWrapper(args []string, daemonAddr string) int {
	env := os.Environ()
	if daemonAddr != "" {
		env = agentwrapper.WithEnv(env, fmt.Sprintf("TMUX_CODERD_ADDR=%s", daemonAddr))
	}

	return agentwrapper.Run(agentwrapper.RunConfig{
		Args:           args,
		Getenv:         os.Getenv,
		Env:            env,
		Stdin:          os.Stdin,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
		CommandContext: exec.CommandContext,
		NewClient: func(baseURL string, hc *http.Client) agentwrapper.AgentEventClient {
			return httpclient.New(baseURL, hc)
		},
	})
}
