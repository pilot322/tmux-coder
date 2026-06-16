package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allHookCommands decodes settings bytes and returns every hook command string.
func allHookCommands(t *testing.T, data []byte) []string {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("settings is not valid JSON: %v\n%s", err, data)
	}
	var cmds []string
	for _, entries := range s.Hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				cmds = append(cmds, h.Command)
			}
		}
	}
	return cmds
}

func hasCommand(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}

func TestInstallClaudeHooksWritesSettings(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")
	var out bytes.Buffer

	err := runInstallClaudeHooks(
		[]string{"--settings", settings, "--binary", "/opt/tmux-coder"},
		func(string) string { return "" },
		&out,
	)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	// Assert on the decoded command so JSON quote-escaping does not matter.
	cmds := allHookCommands(t, data)
	if !hasCommand(cmds, `"/opt/tmux-coder" agent-event idle`) {
		t.Fatalf("missing Stop/idle hook in settings: %v", cmds)
	}
	if !hasCommand(cmds, `"/opt/tmux-coder" agent-event waiting`) {
		t.Fatalf("missing Notification/waiting hook in settings: %v", cmds)
	}
	if !strings.Contains(out.String(), settings) {
		t.Fatalf("expected confirmation naming %s, got %q", settings, out.String())
	}
}

func TestInstallClaudeHooksIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	args := []string{"--settings", settings, "--binary", "/opt/tmux-coder"}
	getenv := func(string) string { return "" }

	if err := runInstallClaudeHooks(args, getenv, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(settings)
	if err := runInstallClaudeHooks(args, getenv, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(settings)

	if !bytes.Equal(first, second) {
		t.Fatalf("re-install changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestInstallClaudeHooksRejectsUnknownFlag(t *testing.T) {
	err := runInstallClaudeHooks([]string{"--nope"}, func(string) string { return "" }, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}
