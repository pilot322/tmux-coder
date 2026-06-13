package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/pilot322/tmux-coder/internal/client/daemon"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/pilot322/tmux-coder/internal/client/tmuxattach"
	"github.com/pilot322/tmux-coder/internal/client/tui"
)

type agentAPI interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
	CreateAgent(context.Context, httpclient.CreateAgentInput) (httpclient.Agent, error)
}

type agentWrapperExitError struct {
	code int
}

func (e agentWrapperExitError) Error() string {
	return fmt.Sprintf("agent wrapper exited with code %d", e.code)
}

func runClient(ctx context.Context, args []string, getenv func(string) string, getwd func() (string, error)) error {
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
	return fmt.Errorf("usage: tmux-coder [open|o|new|n]")
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
		currentSession := tmuxattach.CurrentSession(ctx, getenv)
		if currentSession == "" {
			return fmt.Errorf("tmux-coder new must run inside a tmux-coder session unless --session-id and --project-id are provided")
		}
		sessions, err := api.ListSessions(ctx, httpclient.ListSessionsInput{})
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		for _, session := range sessions {
			if session.TmuxName == currentSession || session.SessionName == currentSession {
				sid := session.ID
				pid := session.ProjectID
				sessionID = &sid
				projectID = &pid
				break
			}
		}
		if sessionID == nil || projectID == nil {
			return fmt.Errorf("current tmux session %q is not managed by tmux-coder", currentSession)
		}
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
