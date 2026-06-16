package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pilot322/tmux-coder/internal/claudehooks"
)

// runInstallClaudeHooks merges tmux-coder's activity-reporting hooks into the
// user's Claude Code settings.json so a wrapped Claude agent reports busy / idle
// / waiting like the OpenCode plugin does. It is idempotent — re-running
// replaces our prior entries instead of duplicating them — and leaves every
// other setting and foreign hook untouched.
//
// --settings overrides the target file (defaults to ~/.claude/settings.json) and
// --binary overrides the hook command's binary path (defaults to this running
// executable); both exist mainly for the installer script and tests.
func runInstallClaudeHooks(args []string, getenv func(string) string, stdout io.Writer) error {
	var settingsPath, binaryPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--settings":
			i++
			if i >= len(args) {
				return fmt.Errorf("--settings requires a value")
			}
			settingsPath = args[i]
		case "--binary":
			i++
			if i >= len(args) {
				return fmt.Errorf("--binary requires a value")
			}
			binaryPath = args[i]
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	if settingsPath == "" {
		home, err := userHome(getenv)
		if err != nil {
			return err
		}
		settingsPath = filepath.Join(home, ".claude", "settings.json")
	}
	if binaryPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve tmux-coder path: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		binaryPath = exe
	}

	existing, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", settingsPath, err)
	}

	merged, err := claudehooks.Merge(existing, binaryPath)
	if err != nil {
		return fmt.Errorf("merge hooks into %s: %w", settingsPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(settingsPath), err)
	}
	if err := os.WriteFile(settingsPath, merged, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", settingsPath, err)
	}

	fmt.Fprintf(stdout, "installed Claude Code activity hooks → %s\n", settingsPath)
	return nil
}

func userHome(getenv func(string) string) (string, error) {
	if home := getenv("HOME"); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return home, nil
}
