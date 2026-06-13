package hookexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var _ usecase.WorktreeHookRunner = (*Runner)(nil)

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) Run(ctx context.Context, req usecase.WorktreeHookRequest) (usecase.WorktreeHookResult, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, req.ScriptPath)
	cmd.Dir = req.WorkingDir
	cmd.Env = append(os.Environ(), envMapToList(req.Env)...)
	output, err := cmd.CombinedOutput()
	result := usecase.WorktreeHookResult{Output: string(output)}
	if runCtx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("hook timed out after %s", timeout)
	}
	return result, err
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
