// Command tmux-coderd is the tmux-coder daemon: an HTTP server exposing
// project CRUD endpoints. It is the composition root — the one place that
// constructs concrete infrastructure and wires it into the usecases.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/pilot322/tmux-coder/internal/adapter/httpapi"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/infra/tmux"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func main() {
	port := os.Getenv("TMUX_CODERD_PORT")
	if port == "" {
		port = "64357"
	}
	addr := ":" + port

	state := memory.NewDaemonState()
	gateway := tmux.NewTmuxGateway()

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gateway, state)
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), gateway, state)

	controller := httpapi.NewProjectController(create, list, del)
	router := httpapi.NewRouter(controller)

	log.Printf("tmux-coderd listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
