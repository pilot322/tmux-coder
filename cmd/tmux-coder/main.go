package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/pilot322/tmux-coder/internal/binresolve"
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
	if len(args) >= 1 && (args[0] == "n" || args[0] == "new") {
		return runNew(ctx, args[1:], getenv, api, addr)
	}
	return fmt.Errorf("usage: tmux-coder [open|o|new|n]")
}

type agentAPI interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
	CreateAgent(context.Context, httpclient.CreateAgentInput) (httpclient.Agent, error)
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

	if paneID != nil {
		return runWrapper(ctx, agent, kind, daemonAddr)
	}

	fmt.Fprintf(os.Stdout, "agent %d (%s) created — status %s\n", agent.ID, agent.DisplayName, agent.Status)
	return nil
}

func runWrapper(ctx context.Context, agent httpclient.Agent, kind, daemonAddr string) error {
	executable, _ := os.Executable()
	wrapper, err := binresolve.ResolveSiblingThenPath(executable, "tmux-coderd-wrapper", exec.LookPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, wrapper, strconv.Itoa(agent.ID), kind)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(os.Environ(), []string{
		fmt.Sprintf("TMUX_CODER_AGENT_ID=%d", agent.ID),
		fmt.Sprintf("TMUX_CODER_AGENT_KIND=%s", kind),
		fmt.Sprintf("TMUX_CODER_PROJECT_ID=%d", agent.ProjectID),
		fmt.Sprintf("TMUX_CODER_SESSION_ID=%d", agent.SessionID),
		fmt.Sprintf("TMUX_CODER_PANE_ID=%s", agent.TmuxPaneID),
		fmt.Sprintf("TMUX_CODERD_ADDR=%s", daemonAddr),
	})
	return cmd.Run()
}

func mergeEnv(env []string, values []string) []string {
	out := append([]string{}, env...)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		replaced := false
		for i, existing := range out {
			if strings.HasPrefix(existing, key+"=") {
				out[i] = value
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, value)
		}
	}
	return out
}
