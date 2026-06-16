// Command tmux-coderd is the tmux-coder daemon: an HTTP server exposing
// project CRUD endpoints. It is the composition root — the one place that
// constructs concrete infrastructure and wires it into the usecases.
package main

import (
	"bufio"
	"log"
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
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func main() {
	if err := loadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		log.Printf("failed to load .env: %v", err)
	}

	addr := "127.0.0.1:" + daemonaddr.Port(os.Getenv)

	state := memory.NewDaemonState()
	gateway := tmux.NewTmuxGateway()
	git := gitinfra.NewGateway()
	hooks := hookexec.NewRunner()
	ports := netport.NewChecker()
	processGw := processinfra.NewProcessGateway()
	notifier := desktopnotify.NewNotifier(desktopnotify.SoundEnabled(os.Getenv))

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gateway, state, state.Config())
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), state.Agents(), gateway, state)
	createSession := usecase.NewCreateSessionWithHooks(state.Projects(), state.Sessions(), gateway, git, state, hooks, state.Leases())
	listSessions := usecase.NewGetSessions(state.Projects(), state.Sessions(), git, state)
	deleteSession := usecase.NewDeleteSessionWithLeases(state.Sessions(), state.Agents(), gateway, git, state, state.Leases())
	createAgent := usecase.NewCreateAgent(state.Agents(), state.Projects(), state.Sessions(), gateway, state)
	listAgents := usecase.NewGetAgents(state.Agents(), state.Projects(), state.Sessions(), gateway, state)
	agentEvent := usecase.NewAgentEvent(state.Agents(), state.Projects(), state.Sessions(), notifier, state)
	deleteAgent := usecase.NewDeleteAgent(state.Agents(), gateway, processGw, state)
	acquirePort := usecase.NewAcquirePort(state.Sessions(), state.Leases(), ports, state)

	controller := httpapi.NewProjectController(create, list, del)
	sessionController := httpapi.NewSessionController(createSession, listSessions, deleteSession)
	agentController := httpapi.NewAgentController(createAgent, listAgents, agentEvent, deleteAgent)
	resourceController := httpapi.NewResourceController(acquirePort)
	router := httpapi.NewRouter(controller, sessionController, agentController, resourceController)

	log.Printf("tmux-coderd listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
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
