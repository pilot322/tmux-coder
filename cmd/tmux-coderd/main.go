// Command tmux-coderd is the tmux-coder daemon: an HTTP server exposing
// project CRUD endpoints. It is the composition root — the one place that
// constructs concrete infrastructure and wires it into the usecases.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pilot322/tmux-coder/internal/adapter/httpapi"
	"github.com/pilot322/tmux-coder/internal/adapter/webdashboard"
	"github.com/pilot322/tmux-coder/internal/daemonaddr"
	"github.com/pilot322/tmux-coder/internal/daemonconfig"
	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/infra/desktopnotify"
	"github.com/pilot322/tmux-coder/internal/infra/discordnotify"
	gitinfra "github.com/pilot322/tmux-coder/internal/infra/git"
	"github.com/pilot322/tmux-coder/internal/infra/hookexec"
	"github.com/pilot322/tmux-coder/internal/infra/memory"
	"github.com/pilot322/tmux-coder/internal/infra/netport"
	"github.com/pilot322/tmux-coder/internal/infra/opencodeserver"
	processinfra "github.com/pilot322/tmux-coder/internal/infra/process"
	"github.com/pilot322/tmux-coder/internal/infra/tmux"
	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/tmuxserver"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func main() {
	// Daemon env loads before the logger is built because the log path is derived
	// from the tmux server label, which the file can set; any failure is surfaced
	// once the logger exists.
	envErr := loadDaemonEnv()

	logger, err := obs.New(obs.RoleDaemon, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-coderd: failed to initialise logging: %v\n", err)
		os.Exit(1)
	}
	ctx := context.Background()
	if envErr != nil && !os.IsNotExist(envErr) {
		logger.Warn(ctx, "failed to load daemon env", "err", envErr.Error())
	}

	addr := "127.0.0.1:" + daemonaddr.Port(os.Getenv)

	config, err := daemonconfig.Load()
	if err != nil {
		logger.Error(ctx, "failed to load daemon config", "err", err.Error())
		os.Exit(1)
	}
	state := memory.NewDaemonStateWithConfig(config)
	gateway := tmux.NewTmuxGateway(logger)
	git := gitinfra.NewGateway(logger)
	hooks := hookexec.NewRunner(logger)
	ports := netport.NewChecker(logger)
	processGw := processinfra.NewProcessGateway(logger)
	notifier := desktopnotify.NewNotifier(desktopnotify.SoundEnabled(os.Getenv))
	openCodeServer := opencodeserver.NewManager(logger, config.OpenCodeServerPort)
	openCodeSetup := usecase.NewOpenCodeSetupCoordinator(gateway, logger)
	reportOpenCodeSession := usecase.NewReportOpenCodeSession(state.Agents(), state)
	discordNotifier := discordnotify.NewNotifier(config.DiscordWebhookNotify)

	create := usecase.NewCreateProject(state.Projects(), state.Sessions(), gateway, git, state, state.Config(), logger)
	list := usecase.NewGetProjects(state.Projects(), state.Sessions(), state, logger)
	del := usecase.NewDeleteProject(state.Projects(), state.Sessions(), state.Agents(), gateway, state, logger)
	listSessions := usecase.NewGetSessions(state.Projects(), state.Sessions(), git, state, logger)
	deleteSession := usecase.NewDeleteSessionWithLeases(state.Sessions(), state.Agents(), gateway, git, state, state.Leases(), logger)
	createSession := usecase.NewCreateSessionWithSetupLifecycle(state.Projects(), state.Sessions(), gateway, git, state, hooks, state.Leases(), deleteSession, notifier, logger)
	createAgent := usecase.NewCreateAgentWithOpenCodeSetup(state.Agents(), state.Projects(), state.Sessions(), gateway, processGw, state, logger, openCodeSetup)
	listAgents := usecase.NewGetAgents(state.Agents(), state.Projects(), state.Sessions(), gateway, state, logger)
	renameAgent := usecase.NewRenameAgent(state.Agents(), state.Projects(), state.Sessions(), gateway, state, logger)
	setAgentDiscordNotification := usecase.NewSetAgentDiscordNotification(state.Agents(), state.Projects(), state.Sessions(), config, state)
	agentEvent := usecase.NewAgentEventWithDiscord(state.Agents(), state.Projects(), state.Sessions(), notifier, discordNotifier, state, logger)
	deleteAgent := usecase.NewDeleteAgent(state.Agents(), gateway, processGw, state, logger)
	acquirePort := usecase.NewAcquirePort(state.Sessions(), state.Leases(), ports, state, logger)
	ensureOpenCodeServer := usecase.NewEnsureOpenCodeServer(openCodeServer)

	controller := httpapi.NewProjectController(create, list, del)
	sessionController := httpapi.NewSessionController(createSession, listSessions, deleteSession)
	agentController := httpapi.NewAgentController(
		createAgent, listAgents, renameAgent, setAgentDiscordNotification, agentEvent, deleteAgent,
		httpapi.WithOpenCodeSetupCoordinator(openCodeSetup),
		httpapi.WithReportOpenCodeSession(reportOpenCodeSession),
		httpapi.WithOpenCodePublicURL(config.OpenCodePublicURL),
		httpapi.WithInternalDaemonAddress(addr),
	)
	resourceController := httpapi.NewResourceController(acquirePort, ensureOpenCodeServer)
	internalRouter := httpapi.NewRouter(controller, sessionController, agentController, resourceController)
	dashboardRouter := httpapi.NewDashboardRouter(controller, sessionController, agentController, webdashboard.Handler())
	internalServer := &http.Server{
		Addr:    addr,
		Handler: obs.AccessLog(logger)(internalRouter),
	}
	dashboardServer := newDashboardServer(config.DashboardListenAddress, obs.AccessLog(logger)(dashboardRouter))

	internalListener, err := net.Listen("tcp", internalServer.Addr)
	if err != nil {
		openCodeServer.Close()
		logger.Error(ctx, "failed to listen", "server", "internal", "addr", internalServer.Addr, "err", err.Error())
		os.Exit(1)
	}
	dashboardListener, err := net.Listen("tcp", dashboardServer.Addr)
	if err != nil {
		_ = internalListener.Close()
		openCodeServer.Close()
		logger.Error(ctx, "failed to listen", "server", "dashboard", "addr", dashboardServer.Addr, "err", err.Error())
		os.Exit(1)
	}

	listenLog := logger.With("internal_addr", internalServer.Addr, "dashboard_addr", dashboardServer.Addr)
	if config.DashboardPublicURL != "" {
		listenLog = listenLog.With("dashboard_public_url", config.DashboardPublicURL)
	}
	if config.OpenCodePublicURL != "" {
		listenLog = listenLog.With("opencode_public_url", config.OpenCodePublicURL)
	}
	listenLog.Info(ctx, "tmux-coderd listening")

	type serverExit struct {
		name string
		err  error
	}
	exits := make(chan serverExit, 2)
	go func() {
		exits <- serverExit{name: "internal", err: internalServer.Serve(internalListener)}
	}()
	go func() {
		exits <- serverExit{name: "dashboard", err: dashboardServer.Serve(dashboardListener)}
	}()

	stopped := <-exits
	peerName := "internal"
	peerServer := internalServer
	peerListener := internalListener
	if stopped.name == "internal" {
		peerName = "dashboard"
		peerServer = dashboardServer
		peerListener = dashboardListener
	}
	if err := peerServer.Close(); err != nil {
		logger.Warn(ctx, "failed to close peer http server", "server", peerName, "err", err.Error())
	}
	_ = peerListener.Close()
	openCodeServer.Close()
	logger.Error(ctx, "http server stopped", "server", stopped.name, "err", stopped.err.Error())
	os.Exit(1)
}

func newDashboardServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
}

func loadDaemonEnv() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var ignoredKeys []string
	if domain.IsDevelopmentBuild() {
		ignoredKeys = []string{
			daemonaddr.EnvName,
			daemonconfig.OpenCodeServerPortEnv,
			daemonconfig.DashboardListenAddressEnv,
			daemonconfig.DashboardPublicURLEnv,
			daemonconfig.OpenCodePublicURLEnv,
			tmuxserver.EnvName,
		}
	}
	return loadEnvFile(filepath.Join(home, ".tmux-coder", ".env"), ignoredKeys...)
}

func loadEnvFile(path string, ignoredKeys ...string) error {
	ignored := make(map[string]struct{}, len(ignoredKeys))
	for _, key := range ignoredKeys {
		ignored[key] = struct{}{}
	}
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
		if _, skip := ignored[key]; skip {
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
