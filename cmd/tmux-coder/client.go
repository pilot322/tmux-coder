package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/pilot322/tmux-coder/internal/client/daemon"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/pilot322/tmux-coder/internal/client/tmuxattach"
	"github.com/pilot322/tmux-coder/internal/client/tui"
	"github.com/pilot322/tmux-coder/internal/daemonaddr"
)

type agentAPI interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
	CreateAgent(context.Context, httpclient.CreateAgentInput) (httpclient.Agent, error)
}

type acquirePortAPI interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
	AcquirePort(context.Context, httpclient.AcquirePortInput) (int, error)
}

type agentWrapperExitError struct {
	code int
}

func (e agentWrapperExitError) Error() string {
	return fmt.Sprintf("agent wrapper exited with code %d", e.code)
}

func runClient(ctx context.Context, args []string, getenv func(string) string, getwd func() (string, error)) error {
	addr := daemonaddr.Address(getenv)
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
		target, ok, err := tui.Run(ctx, api, currentSession)
		if err != nil || !ok {
			return err
		}
		if target.PaneID != "" {
			return tmuxattach.RunPane(ctx, target.SessionName, target.PaneID, getenv)
		}
		return tmuxattach.Run(ctx, target.SessionName, getenv)
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
	if len(args) >= 1 && (args[0] == "n" || args[0] == "new") {
		return runNew(ctx, args[1:], getenv, api, addr)
	}
	if len(args) >= 1 && args[0] == "acquire-port" {
		return runAcquirePort(ctx, args[1:], getenv, api, os.Stdout)
	}
	return fmt.Errorf("usage: tmux-coder [open|o|new|n|acquire-port]")
}

func runAcquirePort(ctx context.Context, args []string, getenv func(string) string, api acquirePortAPI, out io.Writer) error {
	var key string
	var start, end int
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--start":
			i++
			if i >= len(args) {
				return fmt.Errorf("--start requires a value")
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--start must be an integer")
			}
			start = v
		case "--end":
			i++
			if i >= len(args) {
				return fmt.Errorf("--end requires a value")
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--end must be an integer")
			}
			end = v
		default:
			if key == "" {
				key = args[i]
			} else {
				return fmt.Errorf("unexpected argument: %s", args[i])
			}
		}
	}
	if key == "" {
		return fmt.Errorf("usage: tmux-coder acquire-port KEY --start N --end M")
	}
	if start == 0 {
		return fmt.Errorf("--start is required")
	}
	if end == 0 {
		return fmt.Errorf("--end is required")
	}

	in := httpclient.AcquirePortInput{Key: key, Start: start, End: end}
	if token := getenv("TMUX_CODER_HOOK_TOKEN"); token != "" {
		in.HookToken = token
	} else {
		sessionID, projectID, err := currentManagedSession(ctx, getenv, api)
		if err != nil {
			return err
		}
		in.SessionID = sessionID
		in.ProjectID = projectID
	}

	port, err := api.AcquirePort(ctx, in)
	if err != nil {
		return fmt.Errorf("acquire port: %w", err)
	}
	_, err = fmt.Fprintf(out, "%d\n", port)
	return err
}

func runNew(ctx context.Context, args []string, getenv func(string) string, api agentAPI, daemonAddr string) error {
	kind := "opencode"
	kindSet := false
	var displayName *string
	var paneID *string
	var sessionID *int
	var projectID *int

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) {
				return fmt.Errorf("--name requires a value")
			}
			v := args[i]
			displayName = &v
		case "--pane":
			i++
			if i >= len(args) {
				return fmt.Errorf("--pane requires a value")
			}
			v := args[i]
			paneID = &v
		case "--session-id":
			i++
			if i >= len(args) {
				return fmt.Errorf("--session-id requires a value")
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--session-id must be an integer")
			}
			sessionID = &v
		case "--project-id":
			i++
			if i >= len(args) {
				return fmt.Errorf("--project-id requires a value")
			}
			v, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("--project-id must be an integer")
			}
			projectID = &v
		default:
			if !kindSet {
				kind = args[i]
				kindSet = true
			} else {
				return fmt.Errorf("unexpected argument: %s", args[i])
			}
		}
		i++
	}

	if paneID == nil && getenv("TMUX") != "" {
		pid := tmuxattach.CurrentPaneID(ctx, getenv)
		if pid != "" {
			paneID = &pid
		}
	}

	if sessionID == nil || projectID == nil {
		sid, pid, err := currentManagedSession(ctx, getenv, api)
		if err != nil {
			return fmt.Errorf("tmux-coder new must run inside a tmux-coder session unless --session-id and --project-id are provided: %w", err)
		}
		sessionID = &sid
		projectID = &pid
	}

	agent, err := api.CreateAgent(ctx, httpclient.CreateAgentInput{
		ProjectID:   *projectID,
		SessionID:   *sessionID,
		Kind:        kind,
		DisplayName: displayName,
		TmuxPaneID:  paneID,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// When the user runs `tmux-coder new` inside an existing pane, this
	// process becomes the wrapper for that pane's agent.
	if paneID != nil {
		code := runAgentWrapper([]string{strconv.Itoa(agent.ID), kind}, daemonAddr)
		if code != 0 {
			return agentWrapperExitError{code: code}
		}
		return nil
	}

	fmt.Fprintf(os.Stdout, "agent %d (%s) created — status %s\n", agent.ID, agent.DisplayName, agent.Status)
	return nil
}

func currentManagedSession(ctx context.Context, getenv func(string) string, api interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
}) (int, int, error) {
	currentSession := tmuxattach.CurrentSession(ctx, getenv)
	if currentSession == "" {
		return 0, 0, fmt.Errorf("not inside a tmux-coder session")
	}
	sessions, err := api.ListSessions(ctx, httpclient.ListSessionsInput{})
	if err != nil {
		return 0, 0, fmt.Errorf("list sessions: %w", err)
	}
	for _, session := range sessions {
		if session.TmuxName == currentSession || session.SessionName == currentSession {
			return session.ID, session.ProjectID, nil
		}
	}
	return 0, 0, fmt.Errorf("current tmux session %q is not managed by tmux-coder", currentSession)
}
