// Command tmux-coderd is the tmux-coder daemon: an HTTP server exposing
// project CRUD endpoints. It is the composition root — the one place that
// constructs concrete infrastructure and wires it into the usecases.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/pilot322/tmux-coder/internal/adapter/httpapi"
	"github.com/pilot322/tmux-coder/internal/daemonaddr"
	"github.com/pilot322/tmux-coder/internal/infra/desktopnotify"
	gitinfra "github.com/pilot322/tmux-coder/internal/infra/git"
	"github.com/pilot322/tmux-coder/internal/infra/hookexec"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/infra/netport"
	processinfra "github.com/pilot322/tmux-coder/internal/infra/process"
	"github.com/pilot322/tmux-coder/internal/infra/tmux"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func main() {
	// .env loads before the logger is built because the log path is derived from
	// the tmux server label, which .env can set; any failure is surfaced once the
	// logger exists.
	envErr := loadEnvFile(".env")

	logger, err := obs.New(obs.RoleDaemon, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-coderd: failed to initialise logging: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	if envErr != nil && !os.IsNotExist(envErr) {
		logger.Warn(ctx, "failed to load .env", "err", envErr.Error())
	}

	addr := "127.0.0.1:" + daemonaddr.Port(os.Getenv)

	state := memory.NewDaemonState()
	gateway := tmux.NewTmuxGateway(logger)
	git := gitinfra.NewGateway(logger)
	hooks := hookexec.NewRunner(logger)
	ports := netport.NewChecker(logger)
	processGw := processinfra.NewProcessGateway(logger)
	notifier := desktopnotify.NewNotifier(desktopnotify.SoundEnabled(os.Getenv))

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gateway, git, state, state.Config(), logger)
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state, logger)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), state.Agents(), gateway, state, logger)
	createSession := usecase.NewCreateSessionWithHooks(state.Projects(), state.Sessions(), gateway, git, state, hooks, state.Leases(), logger)
	listSessions := usecase.NewGetSessions(state.Projects(), state.Sessions(), git, state, logger)
	deleteSession := usecase.NewDeleteSessionWithLeases(state.Sessions(), state.Agents(), gateway, git, state, state.Leases(), logger)
	createAgent := usecase.NewCreateAgent(state.Agents(), state.Projects(), state.Sessions(), gateway, state, logger)
	listAgents := usecase.NewGetAgents(state.Agents(), state.Projects(), state.Sessions(), gateway, state, logger)
	renameAgent := usecase.NewRenameAgent(state.Agents(), state.Projects(), state.Sessions(), state)
	agentEvent := usecase.NewAgentEvent(state.Agents(), state.Projects(), state.Sessions(), notifier, state, logger)
	deleteAgent := usecase.NewDeleteAgent(state.Agents(), gateway, processGw, state, logger)
	acquirePort := usecase.NewAcquirePort(state.Sessions(), state.Leases(), ports, state, logger)

	controller := httpapi.NewProjectController(create, list, del)
	sessionController := httpapi.NewSessionController(createSession, listSessions, deleteSession)
	agentController := httpapi.NewAgentController(createAgent, listAgents, renameAgent, agentEvent, deleteAgent)
	resourceController := httpapi.NewResourceController(acquirePort)
	router := httpapi.NewRouter(controller, sessionController, agentController, resourceController)

	logger.Info(ctx, "tmux-coderd listening", "addr", addr)
	if err := http.ListenAndServe(addr, obs.AccessLog(logger)(router)); err != nil {
		logger.Error(ctx, "http server stopped", "err", err.Error())
		os.Exit(1)
	}
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	return scanner.Err()
}
