package hookexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.WorktreeHookRunner = (*Runner)(nil)

const hookLogRetentionAge = 14 * 24 * time.Hour

type Runner struct {
	log obs.Logger
}

func NewRunner(log obs.Logger) *Runner {
	return &Runner{log: log.With("component", "hookexec")}
}

func (r *Runner) Run(ctx context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookResult, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	logPath, logErr := newHookLogPath()
	if logErr != nil {
		r.log.Warn(ctx, "worktree hook log unavailable", "err", logErr.Error())
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	r.log.Debug(ctx, "running worktree hook", "script", req.ScriptPath, "dir", req.WorkingDir, "timeout", timeout.String(), "hook_log", logPath)
	cmd := exec.CommandContext(runCtx, req.ScriptPath)
	cmd.Dir = req.WorkingDir
	cmd.Env = append(os.Environ(), envMapToList(req.Env)...)
	output, err := cmd.CombinedOutput()
	result := usecase.WorktreeHookResult{Output: string(output), LogPath: logPath}
	if logPath != "" {
		requestID, _ := obs.RequestIDFrom(ctx)
		if err := writeHookLog(logPath, req, timeout, requestID, output, time.Now()); err != nil {
			r.log.Warn(ctx, "write worktree hook log failed", "hook_log", logPath, "err", err.Error())
			result.LogPath = ""
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		r.log.Error(ctx, "worktree hook timed out", "script", req.ScriptPath, "timeout", timeout.String(), "hook_log", result.LogPath)
		return result, fmt.Errorf("hook timed out after %s", timeout)
	}
	if err != nil {
		r.log.Error(ctx, "worktree hook failed", "script", req.ScriptPath, "err", err.Error(), "hook_log", result.LogPath)
	}
	return result, err
}

func newHookLogPath() (string, error) {
	dir, err := obs.LogDir(obs.RoleDaemon, os.Getenv)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "hooks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = sweepHookLogs(dir, hookLogRetentionAge, time.Now())
	name := fmt.Sprintf("%s-%s-worktree-on-create.log", time.Now().UTC().Format("20060102T150405.000000000Z"), obs.NewRequestID())
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

func writeHookLog(path string, req usecase.WorktreeHookRequest, timeout time.Duration, requestID string, output []byte, now time.Time) error {
	var b strings.Builder
	fprintf := func(format string, args ...any) { _, _ = fmt.Fprintf(&b, format, args...) }
	fprintf("timestamp: %s\n", now.UTC().Format(time.RFC3339Nano))
	if requestID != "" {
		fprintf("request_id: %s\n", requestID)
	}
	fprintf("hook_kind: worktree-on-create\n")
	fprintf("script_path: %s\n", req.ScriptPath)
	fprintf("working_dir: %s\n", req.WorkingDir)
	fprintf("timeout: %s\n", timeout)
	writeEnvSummary(&b, req.Env)
	fprintf("--- output ---\n")
	b.Write(output)
	if len(output) == 0 || output[len(output)-1] != '\n' {
		b.WriteByte('\n')
	}
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
