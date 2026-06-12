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
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/infra/tmux"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func main() {
	if err := loadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		log.Printf("failed to load .env: %v", err)
	}

	addr := ":" + daemonPort()

	state := memory.NewDaemonState()
	gateway := tmux.NewTmuxGateway()

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gateway, state, state.Config())
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), gateway, state)

	controller := httpapi.NewProjectController(create, list, del)
	router := httpapi.NewRouter(controller)

	log.Printf("tmux-coderd listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}

func daemonPort() string {
	port := os.Getenv("TMUX_CODERD_PORT")
	if port == "" {
		return "64357"
	}
	return port
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
