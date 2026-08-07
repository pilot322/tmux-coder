package menu

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
	"github.com/pilot322/tmux-coder/internal/config"
)

func TestExecuteUsesSessionDirectoryAndAuthoritativeEnvironment(t *testing.T) {
	root := t.TempDir()
	working := filepath.Join(root, "packages", "web")
	if err := os.MkdirAll(working, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "actions", "review")
	writeExecutable(t, script, `#!/bin/sh
printf '%s\n' "$PWD" "$INHERITED" "$TMUX_CODER_ACTION_NAME" "$TMUX_CODER_ACTION_ARGUMENT" "$TMUX_CODER_PROJECT_ID" "$TMUX_CODER_PROJECT_ROOT" "$TMUX_CODER_PROJECT_TITLE" "$TMUX_CODER_SESSION_ID" "$TMUX_CODER_SESSION_NAME" "$TMUX_CODER_SESSION_TYPE" "$TMUX_CODER_TMUX_SESSION_NAME" "$TMUX_CODER_SESSION_ROOT" "$TMUX_CODER_WORKTREE_ROOT" "$TMUX_CODER_WORKING_DIRECTORY" "$TMUX_CODER_BRANCH"
`)
	session := SessionContext{
		Project:          httpclient.Project{ID: 7, FullPath: "/main/project", Title: "Project"},
		Session:          httpclient.Session{ID: 9, SessionName: "project.web", TmuxName: "project_web", Type: "secondary"},
		SessionRoot:      root,
		WorktreeRoot:     root,
		WorkingDirectory: working,
		Branch:           "feature/review",
	}
	argument := ` repair  $(touch /tmp/nope); "quoted" `
	selection := Selection{Action: Action{MenuAction: config.MenuAction{Name: "review", Script: "actions/review"}, Scope: ProjectScope}, Argument: argument}
	var stdout bytes.Buffer
	err := Execute(context.Background(), selection, session, []string{"INHERITED=yes", "TMUX_CODER_ACTION_NAME=forged", "TMUX_CODER_BRANCH=wrong"}, nil, &stdout, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{working, "yes", "review", argument, "7", "/main/project", "Project", "9", "project.web", "secondary", "project_web", root, root, working, "feature/review"}
	got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("output lines = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveScriptUsesGlobalActionDirectory(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "actions", "commit")
	writeExecutable(t, script, "#!/bin/sh\n")
	action := Action{MenuAction: config.MenuAction{Name: "commit", Script: "actions/commit"}, Scope: GlobalScope, SourcePath: filepath.Join(dir, "actions.toml")}
	got, err := ResolveScript(action, SessionContext{SessionRoot: "/unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	if got != script {
		t.Fatalf("path = %q, want %q", got, script)
	}
}

func TestResolveScriptRejectsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	nonExecutable := filepath.Join(dir, "plain")
	if err := os.WriteFile(nonExecutable, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"missing", "plain", "."} {
		action := Action{MenuAction: config.MenuAction{Name: "broken", Script: script}, Scope: ProjectScope}
		_, err := ResolveScript(action, SessionContext{SessionRoot: dir})
		if err == nil || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("ResolveScript(%q) error = %v", script, err)
		}
	}
}

func TestExecuteReturnsProcessExitError(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail")
	writeExecutable(t, script, "#!/bin/sh\nexit 23\n")
	err := Execute(context.Background(), Selection{Action: Action{MenuAction: config.MenuAction{Name: "fail", Script: script}}}, SessionContext{SessionRoot: dir, WorkingDirectory: dir}, nil, nil, nil, nil)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 23 {
		t.Fatalf("error = %v", err)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
