package hookexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/tmuxserver"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.WorktreeHookRunner = (*Runner)(nil)
var _ usecase.WorktreeDestroyHookRunner = (*Runner)(nil)

const hookLogRetentionAge = 14 * 24 * time.Hour

type Runner struct {
	binary      string
	serverLabel string
	log         obs.Logger
}

func NewRunner(log obs.Logger) *Runner {
	return &Runner{binary: "tmux", serverLabel: tmuxserver.Label(os.Getenv), log: log.With("component", "hookexec")}
}

type execution struct {
	binary      string
	serverLabel string
	paneID      string
	statusPath  string
	ackPath     string
	logPath     string
}

func (r *Runner) Start(ctx context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookExecution, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	logPath, logErr := newHookLogPath("worktree-on-create")
	if logErr != nil {
		r.log.Warn(ctx, "worktree hook log unavailable", "err", logErr.Error())
	}
	if logPath != "" {
		requestID, _ := obs.RequestIDFrom(ctx)
		if err := writeHookLogHeader(logPath, "worktree-on-create", req, timeout, requestID, time.Now()); err != nil {
			r.log.Warn(ctx, "write worktree hook log failed", "hook_log", logPath, "err", err.Error())
			logPath = ""
		}
	}
	stateBase := logPath
	if stateBase == "" {
		stateBase = filepath.Join(os.TempDir(), "tmux-coder-hook-"+obs.NewRequestID())
	}
	statusPath := stateBase + ".status"
	ackPath := stateBase + ".ack"
	command := hookCommand(req.ScriptPath, logPath, statusPath, ackPath, timeout)
	args := []string{"-L", r.serverLabel, "new-window", "-d", "-t", req.Env["TMUX_CODER_TMUX_SESSION_NAME"], "-n", "worktree-setup", "-c", req.WorkingDir, "-P", "-F", "#{pane_id}"}
	for _, value := range envMapToList(req.Env) {
		args = append(args, "-e", value)
	}
	args = append(args, command)
	r.log.Debug(ctx, "starting worktree hook window", "script", req.ScriptPath, "dir", req.WorkingDir, "timeout", timeout.String(), "hook_log", logPath)
	output, err := exec.CommandContext(ctx, r.binary, args...).CombinedOutput()
	if err != nil {
		_ = os.Remove(statusPath)
		_ = os.Remove(ackPath)
		return nil, fmt.Errorf("start worktree setup window: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return &execution{binary: r.binary, serverLabel: r.serverLabel, paneID: strings.TrimSpace(string(output)), statusPath: statusPath, ackPath: ackPath, logPath: logPath}, nil
}

// RunDestroy waits for the script before returning. GNU timeout forwards TERM
// and bounds a hook that ignores it; cancellation kills its process group too.
func (r *Runner) RunDestroy(ctx context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookResult, error) {
	result := usecase.WorktreeHookResult{}
	logPath, err := newHookLogPath("worktree-on-destroy")
	if err != nil {
		r.log.Warn(ctx, "worktree destroy hook log unavailable", "err", err.Error())
	} else {
		requestID, _ := obs.RequestIDFrom(ctx)
		if err := writeHookLogHeader(logPath, "worktree-on-destroy", req, req.Timeout, requestID, time.Now()); err != nil {
			r.log.Warn(ctx, "write worktree destroy hook log failed", "err", err.Error())
		} else {
			result.LogPath = logPath
		}
	}
	seconds := strconv.FormatFloat(req.Timeout.Seconds(), 'f', 3, 64) + "s"
	cmd := exec.CommandContext(ctx, "timeout", "--signal=TERM", "--kill-after=5s", seconds, req.ScriptPath)
	cmd.Dir = req.WorkingDir
	cmd.Env = make([]string, 0, len(os.Environ())+len(req.Env))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "TMUX_CODER_HOOK_TOKEN" {
			continue
		}
		if _, override := req.Env[key]; !override {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, envMapToList(req.Env)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	output, runErr := cmd.CombinedOutput()
	result.Output = string(output)
	if result.LogPath != "" {
		file, err := os.OpenFile(result.LogPath, os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, err = file.Write(output)
			_ = file.Close()
		}
		if err != nil {
			r.log.Warn(ctx, "append worktree destroy hook log failed", "hook_log", result.LogPath, "err", err.Error())
		}
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if exit, ok := runErr.(*exec.ExitError); ok && exit.ExitCode() == 124 {
			return result, fmt.Errorf("destroy hook timed out after %s", req.Timeout)
		}
		return result, fmt.Errorf("destroy hook: %w", runErr)
	}
	return result, nil
}

func (e *execution) Wait(ctx context.Context) (usecase.WorktreeHookResult, error) {
	contents, err := e.waitForFile(ctx, e.statusPath, false)
	result := usecase.WorktreeHookResult{LogPath: e.logPath}
	if e.logPath != "" {
		if output, readErr := os.ReadFile(e.logPath); readErr == nil {
			result.Output = string(output)
		}
	}
	if err != nil {
		return result, err
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil {
		return result, fmt.Errorf("read hook status: %w", err)
	}
	if status == 0 {
		e.closeWindow(ctx)
		e.cleanup()
		return result, nil
	}
	if status == 124 {
		return result, fmt.Errorf("hook timed out")
	}
	return result, fmt.Errorf("hook exited with status %d", status)
}

func (e *execution) WaitForAcknowledgement(ctx context.Context) error {
	_, err := e.waitForFile(ctx, e.ackPath, true)
	e.cleanup()
	return err
}

func (e *execution) cleanup() {
	_ = os.Remove(e.statusPath)
	_ = os.Remove(e.ackPath)
}

func (e *execution) waitForFile(ctx context.Context, path string, paneGoneIsAcknowledgement bool) ([]byte, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		contents, err := os.ReadFile(path)
		if err == nil {
			return contents, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		if e.paneID != "" && !e.paneExists(ctx) {
			if paneGoneIsAcknowledgement {
				return nil, nil
			}
			return nil, fmt.Errorf("worktree setup window closed before the hook completed")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *execution) paneExists(ctx context.Context) bool {
	return exec.CommandContext(ctx, e.binary, "-L", e.serverLabel, "list-panes", "-t", e.paneID).Run() == nil
}

func (e *execution) closeWindow(ctx context.Context) {
	if e.paneID != "" {
		_ = exec.CommandContext(ctx, e.binary, "-L", e.serverLabel, "kill-window", "-t", e.paneID).Run()
	}
}

func hookCommand(scriptPath, logPath, statusPath, ackPath string, timeout time.Duration) string {
	logTarget := logPath
	if logTarget == "" {
		logTarget = "/dev/null"
	}
	body := `set -o pipefail
timeout --signal=TERM --kill-after=5s "$1" "$2" 2>&1 | tee -a "$3"
status=${PIPESTATUS[0]}
tmp="$4.tmp.$$"
printf '%s\n' "$status" > "$tmp"
mv "$tmp" "$4"
if [ "$status" -eq 0 ]; then
  exit 0
fi
printf '\nWorktree setup failed. Press Enter to remove this Worktree Session.\n'
IFS= read -r _
: > "$5"`
	seconds := strconv.FormatFloat(timeout.Seconds(), 'f', 3, 64) + "s"
	return "bash -c " + shellQuote(body) + " worktree-setup " + shellQuote(seconds) + " " + shellQuote(scriptPath) + " " + shellQuote(logTarget) + " " + shellQuote(statusPath) + " " + shellQuote(ackPath)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func newHookLogPath(kind string) (string, error) {
	dir, err := obs.LogDir(obs.RoleDaemon, os.Getenv)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = sweepHookLogs(dir, hookLogRetentionAge, time.Now())
	name := fmt.Sprintf("%s-%s-%s.log", time.Now().UTC().Format("20060102T150405.000000000Z"), obs.NewRequestID(), kind)
	return filepath.Join(dir, name), nil
}

func sweepHookLogs(dir string, maxAge time.Duration, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

func writeHookLogHeader(path, kind string, req usecase.WorktreeHookRequest, timeout time.Duration, requestID string, now time.Time) error {
	var b strings.Builder
	fprintf := func(format string, args ...any) { _, _ = fmt.Fprintf(&b, format, args...) }
	fprintf("timestamp: %s\n", now.UTC().Format(time.RFC3339Nano))
	if requestID != "" {
		fprintf("request_id: %s\n", requestID)
	}
	fprintf("hook_kind: %s\n", kind)
	fprintf("script_path: %s\n", req.ScriptPath)
	fprintf("working_dir: %s\n", req.WorkingDir)
	fprintf("timeout: %s\n", timeout)
	writeEnvSummary(&b, req.Env)
	fprintf("--- output ---\n")
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func writeEnvSummary(b *strings.Builder, env map[string]string) {
	fprintf := func(format string, args ...any) { _, _ = fmt.Fprintf(b, format, args...) }
	keys := []string{
		"TMUX_CODER_PROJECT_ID",
		"TMUX_CODER_SESSION_ID",
		"TMUX_CODER_WORKTREE_ROOT",
		"TMUX_CODER_PROJECT_ROOT",
		"TMUX_CODER_BRANCH",
		"TMUX_CODER_SESSION_NAME",
		"TMUX_CODER_TMUX_SESSION_NAME",
	}
	for _, key := range keys {
		if val, ok := env[key]; ok {
			fprintf("%s: %s\n", strings.TrimPrefix(strings.ToLower(key), "tmux_coder_"), val)
		}
	}
	_, tokenSet := env["TMUX_CODER_HOOK_TOKEN"]
	fprintf("hook_token_set: %t\n", tokenSet)
}

func envMapToList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
