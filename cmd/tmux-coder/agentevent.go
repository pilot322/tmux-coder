package main

import (
	"io"
	"net/http"
	"os"

	"github.com/pilot322/tmux-coder/internal/agentevent"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

// runAgentEvent starts the agent-event runtime mode: a short-lived emitter a
// Claude Code hook invokes to report the agent's activity. The daemon address
// and agent id come from the environment the wrapper exported into the agent.
func runAgentEvent(args []string) int {
	return agentevent.Run(agentevent.RunConfig{
		Args:   args,
		Getenv: os.Getenv,
		Stdin:  hookStdin(),
		Stderr: os.Stderr,
		NewClient: func(baseURL string, hc *http.Client) agentevent.EventClient {
			return httpclient.New(baseURL, hc)
		},
	})
}

// hookStdin returns os.Stdin only when it is a pipe — the case when Claude Code
// invokes the hook and feeds it a JSON payload. Run a hook by hand from a
// terminal and stdin is a tty with no payload coming; returning nil then avoids
// a blocking read.
func hookStdin() io.Reader {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return os.Stdin
}
