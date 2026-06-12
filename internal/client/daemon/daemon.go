package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const DefaultPort = "64357"

type Starter struct {
	Executable func() (string, error)
	LookPath   func(string) (string, error)
	Start      func(binary, logPath string) error
	HTTP       *http.Client
}

func Address(getenv func(string) string) string {
	port := getenv("TMUX_CODERD_PORT")
	if port == "" {
		port = DefaultPort
	}
	return "http://127.0.0.1:" + port
}

func ResolveBinary(executable string, lookPath func(string) (string, error)) (string, error) {
	if executable != "" {
		sibling := filepath.Join(filepath.Dir(executable), "tmux-coderd")
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath("tmux-coderd")
	if err != nil {
		return "", fmt.Errorf("find tmux-coderd: %w", err)
	}
	return path, nil
}

func Ensure(ctx context.Context, addr string, starter Starter) (string, error) {
	if starter.HTTP == nil {
		starter.HTTP = http.DefaultClient
	}
	if probe(ctx, starter.HTTP, addr) == nil {
		return "", nil
	}

	executable := ""
	if starter.Executable != nil {
		path, err := starter.Executable()
		if err != nil {
			return "", err
		}
		executable = path
	} else if path, err := os.Executable(); err == nil {
		executable = path
	}
	binary, err := ResolveBinary(executable, starter.LookPath)
	if err != nil {
		return "", err
	}

	logFile, err := os.CreateTemp("", "tmux-coderd-*.log")
	if err != nil {
		return "", err
	}
	logPath := logFile.Name()
	_ = logFile.Close()

	start := starter.Start
	if start == nil {
		start = startProcess
	}
	if err := start(binary, logPath); err != nil {
		return logPath, err
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := probe(ctx, starter.HTTP, addr); err == nil {
			return logPath, nil
		}
		select {
		case <-ctx.Done():
			return logPath, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return logPath, fmt.Errorf("tmux-coderd did not become ready")
}

func probe(ctx context.Context, hc *http.Client, addr string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/projects", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe returned %s", resp.Status)
	}
	return nil
}

func startProcess(binary, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(binary)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return nil
}
