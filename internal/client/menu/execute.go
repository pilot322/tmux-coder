package menu

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var ownedEnvironment = []string{
	"TMUX_CODER_ACTION_NAME",
	"TMUX_CODER_ACTION_ARGUMENT",
	"TMUX_CODER_PROJECT_ID",
	"TMUX_CODER_PROJECT_ROOT",
	"TMUX_CODER_PROJECT_TITLE",
	"TMUX_CODER_SESSION_ID",
	"TMUX_CODER_SESSION_NAME",
	"TMUX_CODER_SESSION_TYPE",
	"TMUX_CODER_TMUX_SESSION_NAME",
	"TMUX_CODER_SESSION_ROOT",
	"TMUX_CODER_WORKTREE_ROOT",
	"TMUX_CODER_WORKING_DIRECTORY",
	"TMUX_CODER_BRANCH",
}

func ResolveScript(action Action, session SessionContext) (string, error) {
	path := action.Script
	if !filepath.IsAbs(path) {
		base := session.SessionRoot
		if action.Scope == GlobalScope {
			base = filepath.Dir(action.SourcePath)
		}
		path = filepath.Join(base, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("menu-action %q script %q: %w", action.Name, path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("menu-action %q script %s: %w", action.Name, path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("menu-action %q script %s is not a regular file", action.Name, path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("menu-action %q script %s is not executable", action.Name, path)
	}
	return path, nil
}

// Execute runs the selected script directly through its shebang with attached
// terminal streams. The argument is environment data, never shell source.
func Execute(ctx context.Context, selection Selection, session SessionContext, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	path, err := ResolveScript(selection.Action, session)
	if err != nil {
		return err
	}
	values := map[string]string{
		"TMUX_CODER_ACTION_NAME":       selection.Action.Name,
		"TMUX_CODER_ACTION_ARGUMENT":   selection.Argument,
		"TMUX_CODER_PROJECT_ID":        strconv.Itoa(session.Project.ID),
		"TMUX_CODER_PROJECT_ROOT":      session.Project.FullPath,
		"TMUX_CODER_PROJECT_TITLE":     session.Project.Title,
		"TMUX_CODER_SESSION_ID":        strconv.Itoa(session.Session.ID),
		"TMUX_CODER_SESSION_NAME":      session.Session.SessionName,
		"TMUX_CODER_SESSION_TYPE":      session.Session.Type,
		"TMUX_CODER_TMUX_SESSION_NAME": session.Session.TmuxName,
		"TMUX_CODER_SESSION_ROOT":      session.SessionRoot,
		"TMUX_CODER_WORKTREE_ROOT":     session.WorktreeRoot,
		"TMUX_CODER_WORKING_DIRECTORY": session.WorkingDirectory,
		"TMUX_CODER_BRANCH":            session.Branch,
	}

	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = session.WorkingDirectory
	cmd.Env = authoritativeEnvironment(env, values)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func authoritativeEnvironment(env []string, values map[string]string) []string {
	owned := make(map[string]bool, len(ownedEnvironment))
	for _, key := range ownedEnvironment {
		owned[key] = true
	}
	out := make([]string, 0, len(env)+len(values))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && owned[key] {
			continue
		}
		out = append(out, item)
	}
	for _, key := range ownedEnvironment {
		out = append(out, key+"="+values[key])
	}
	return out
}
