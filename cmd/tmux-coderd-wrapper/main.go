package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, exec.CommandContext, func(baseURL string, hc *http.Client) agentEventClient {
		return httpclient.New(baseURL, hc)
	}))
}

type commandContextFunc func(context.Context, string, ...string) *exec.Cmd

type agentEventClient interface {
	SendAgentStarted(context.Context, int, int) error
	SendAgentEvent(context.Context, int, string) error
}

func run(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer, commandContext commandContextFunc, newClient func(string, *http.Client) agentEventClient) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: tmux-coderd-wrapper <agentID> <kind>")
		return 1
	}

	agentID, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "invalid agent ID: %v\n", err)
		return 1
	}
	kind := args[1]

	daemonAddr := daemonBaseURL(getenv("TMUX_CODERD_ADDR"))
	paneID := getenv("TMUX_CODER_PANE_ID")
	if paneID == "" {
		paneID = currentPaneID(context.Background())
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	api := newClient(daemonAddr, nil)

	cmd := commandContext(context.Background(), kind)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = withEnv(os.Environ(), "TMUX_CODER_PANE_ID="+paneID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "failed to start %s: %v\n", kind, err)
		return 1
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer notifyCancel()
	_ = api.SendAgentStarted(notifyCtx, agentID, pgid)

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case sig := <-sigCh:
		_ = syscall.Kill(-pgid, sig.(syscall.Signal))
		waitErr = <-waitCh
	}

	eventCtx, eventCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer eventCancel()
	_ = api.SendAgentEvent(eventCtx, agentID, "exited")

	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "agent %s exited with error: %v\n", kind, waitErr)
		return 1
	}
	return 0
}

func daemonBaseURL(raw string) string {
	if raw == "" {
		return "http://127.0.0.1:64357"
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "http://" + raw
}

func currentPaneID(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pane_id}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func withEnv(env []string, values ...string) []string {
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
