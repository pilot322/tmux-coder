package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pilot322/tmux-coder/internal/client/daemon"
	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/pilot322/tmux-coder/internal/client/menu"
	"github.com/pilot322/tmux-coder/internal/client/tmuxattach"
	"github.com/pilot322/tmux-coder/internal/client/tui"
	"github.com/pilot322/tmux-coder/internal/config"
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

type openAPI interface {
	CreateProject(context.Context, string, *bool, ...string) (httpclient.Project, error)
}

type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("process exited with code %d", e.code)
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
	if len(args) >= 1 && (args[0] == "o" || args[0] == "open") {
		info, statErr := os.Stdin.Stat()
		interactive := statErr == nil && info.Mode()&os.ModeCharDevice != 0
		project, err := runOpen(ctx, args[1:], getwd, api, os.Stdin, os.Stdout, interactive)
		if err != nil {
			return err
		}
		return tmuxattach.Run(ctx, project.MainTmuxSessionName, getenv)
	}
	if len(args) >= 1 && (args[0] == "n" || args[0] == "new") {
		return runNew(ctx, args[1:], getenv, api, addr)
	}
	if len(args) >= 1 && (args[0] == "m" || args[0] == "menu") {
		return runMenu(ctx, args[1:], getenv, api)
	}
	if len(args) >= 1 && args[0] == "acquire-port" {
		return runAcquirePort(ctx, args[1:], getenv, api, os.Stdout)
	}
	return fmt.Errorf("usage: tmux-coder [open|o|new|n|menu|m|acquire-port|install-claude-hooks]")
}

func runMenu(ctx context.Context, args []string, getenv func(string) string, api interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
}) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: tmux-coder menu")
	}
	current, sessions, err := currentManagedSessionDetails(ctx, getenv, api)
	if err != nil {
		return fmt.Errorf("tmux-coder menu must run inside a tmux-coder session: %w", err)
	}
	session, err := menu.ResolveSessionContext(current, sessions)
	if err != nil {
		return fmt.Errorf("resolve current session: %w", err)
	}
	home, err := userHome(getenv)
	if err != nil {
		return err
	}
	actionPath := filepath.Join(home, ".tmux-coder", "actions.toml")
	projectPath := config.ProjectPath(current.Project.FullPath)
	globalActions, err := config.LoadActionFile(actionPath)
	if err != nil {
		return err
	}
	projectFile, err := config.Load(current.Project.FullPath)
	if err != nil {
		return err
	}
	actions, err := menu.Merge(globalActions, projectFile.MenuActions, actionPath, projectPath)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		fmt.Fprintf(os.Stdout, "No Menu Actions configured. Add them to %s or %s.\n", actionPath, projectPath)
		return nil
	}
	selection, ok, err := menu.Run(ctx, actions)
	if err != nil || !ok {
		return err
	}
	if err := menu.Execute(ctx, selection, session, os.Environ(), os.Stdin, os.Stdout, os.Stderr); err != nil {
		var processExit *exec.ExitError
		if errors.As(err, &processExit) {
			return exitCodeError{code: processExit.ExitCode()}
		}
		return err
	}
	return nil
}

func runOpen(ctx context.Context, args []string, getwd func() (string, error), api openAPI, in io.Reader, out io.Writer, interactive bool) (httpclient.Project, error) {
	var decision *bool
	for _, arg := range args {
		switch arg {
		case "--create-worktree-sessions":
			v := true
			decision = &v
		case "--no-create-worktree-sessions":
			v := false
			decision = &v
		default:
			return httpclient.Project{}, fmt.Errorf("unexpected argument: %s", arg)
		}
	}
	cwd, err := getwd()
	if err != nil {
		return httpclient.Project{}, err
	}
	project, err := api.CreateProject(ctx, cwd, decision)
	if err == nil || decision != nil {
		return project, err
	}
	var apiErr *httpclient.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != httpclient.CodeWorktreesDetected {
		return httpclient.Project{}, err
	}

	adopt := false
	if interactive {
		_, _ = fmt.Fprintln(out, "Git worktrees were detected for this project:")
		for _, wt := range apiErr.Worktrees {
			_, _ = fmt.Fprintf(out, "  %s (%s)\n", wt.Path, wt.Branch)
		}
		_, _ = fmt.Fprint(out, "Create Worktree Sessions for them? [Y/n] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		adopt = answer != "n" && answer != "no"
	}
	return api.CreateProject(ctx, cwd, &adopt)
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
	var model *string
	var variant *string
	var prompt *string
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
		case "--model":
			i++
			if i >= len(args) {
				return fmt.Errorf("--model requires a value")
			}
			v := args[i]
			model = &v
		case "--prompt":
			i++
			if i >= len(args) {
				return fmt.Errorf("--prompt requires a value")
			}
			v := args[i]
			prompt = &v
		case "--variant":
			i++
			if i >= len(args) {
				return fmt.Errorf("--variant requires a value")
			}
			v := args[i]
			variant = &v
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

	if (model != nil || variant != nil || prompt != nil) && kind != "opencode" {
		return fmt.Errorf("--model, --variant, and --prompt are only supported for opencode")
	}
	if variant != nil && model == nil {
		return fmt.Errorf("--variant requires --model")
	}
	if variant != nil && (*variant == "" || strings.IndexFunc(*variant, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0) {
		return fmt.Errorf("--variant must be a non-empty OpenCode model variant")
	}
	if model != nil {
		provider, modelID, ok := strings.Cut(*model, "/")
		if !ok || provider == "" || modelID == "" || strings.IndexFunc(*model, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0 {
			return fmt.Errorf("--model must use canonical provider/model format")
		}
	}
	if prompt != nil && *prompt == "" {
		return fmt.Errorf("--prompt must not be empty")
	}

	explicitSession := sessionID != nil
	if explicitSession {
		sessions, err := api.ListSessions(ctx, httpclient.ListSessionsInput{})
		if err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}
		var target *httpclient.Session
		for i := range sessions {
			if sessions[i].ID == *sessionID {
				target = &sessions[i]
				break
			}
		}
		if target == nil {
			return fmt.Errorf("session %d not found", *sessionID)
		}
		if projectID != nil && *projectID != target.ProjectID {
			return fmt.Errorf("--project-id %d does not match session %d project %d", *projectID, *sessionID, target.ProjectID)
		}
		pid := target.ProjectID
		projectID = &pid
	} else {
		sid, pid, err := currentManagedSession(ctx, getenv, api)
		if err != nil {
			return fmt.Errorf("tmux-coder new must run inside a tmux-coder session unless --session-id is provided: %w", err)
		}
		if projectID != nil && *projectID != pid {
			return fmt.Errorf("--project-id %d does not match current session project %d", *projectID, pid)
		}
		sessionID = &sid
		projectID = &pid
	}

	// An explicit Session always gets an owned window unless the caller also
	// explicitly identifies a pane in that Session.
	if !explicitSession && paneID == nil && getenv("TMUX") != "" {
		pid := tmuxattach.CurrentPaneID(ctx, getenv)
		if pid != "" {
			paneID = &pid
		}
	}

	agent, err := api.CreateAgent(ctx, httpclient.CreateAgentInput{
		ProjectID:   *projectID,
		SessionID:   *sessionID,
		Kind:        kind,
		Model:       model,
		Variant:     variant,
		Prompt:      prompt,
		DisplayName: displayName,
		TmuxPaneID:  paneID,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	// When the user runs `tmux-coder new` inside an existing pane, this
	// process becomes the wrapper for that pane's agent.
	if paneID != nil {
		extraEnv := []string{}
		if model != nil || variant != nil || prompt != nil {
			extraEnv = append(extraEnv, "TMUX_CODER_AGENT_SETUP=1")
		}
		if model != nil {
			extraEnv = append(extraEnv, "TMUX_CODER_AGENT_MODEL="+*model)
		}
		if variant != nil {
			extraEnv = append(extraEnv, "TMUX_CODER_AGENT_VARIANT="+*variant)
		}
		code := runAgentWrapper([]string{strconv.Itoa(agent.ID), kind}, daemonAddr, extraEnv...)
		if code != 0 {
			return exitCodeError{code: code}
		}
		return nil
	}

	fmt.Fprintf(os.Stdout, "agent %d (%s) created — status %s\n", agent.ID, agent.DisplayName, agent.Status)
	return nil
}

func currentManagedSession(ctx context.Context, getenv func(string) string, api interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
}) (int, int, error) {
	session, _, err := currentManagedSessionDetails(ctx, getenv, api)
	if err != nil {
		return 0, 0, err
	}
	return session.ID, session.ProjectID, nil
}

func currentManagedSessionDetails(ctx context.Context, getenv func(string) string, api interface {
	ListSessions(context.Context, httpclient.ListSessionsInput) ([]httpclient.Session, error)
}) (httpclient.Session, []httpclient.Session, error) {
	currentSession := tmuxattach.CurrentSession(ctx, getenv)
	if currentSession == "" {
		return httpclient.Session{}, nil, fmt.Errorf("not inside a tmux-coder session")
	}
	sessions, err := api.ListSessions(ctx, httpclient.ListSessionsInput{})
	if err != nil {
		return httpclient.Session{}, nil, fmt.Errorf("list sessions: %w", err)
	}
	for _, session := range sessions {
		if session.TmuxName == currentSession {
			return session, sessions, nil
		}
	}
	return httpclient.Session{}, nil, fmt.Errorf("current tmux session %q is not managed by tmux-coder", currentSession)
}
