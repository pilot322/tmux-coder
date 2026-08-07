package menu

import (
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/config"
)

func TestMergeOverridesCompleteActionInOriginalSlot(t *testing.T) {
	global := []config.MenuAction{
		{Name: "commit", Description: "global", Key: "c", Script: "global-commit", Argument: config.ArgumentOptional},
		{Name: "review", Script: "global-review", Argument: config.ArgumentNone},
	}
	project := []config.MenuAction{
		{Name: "commit", Script: "project-commit", Argument: config.ArgumentNone},
		{Name: "fix", Script: "project-fix", Argument: config.ArgumentRequired},
	}
	actions, err := Merge(global, project, "/home/me/.tmux-coder/actions.toml", "/repo/.tmux-coder/.tmux-coder.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{actions[0].Name, actions[1].Name, actions[2].Name}; strings.Join(got, ",") != "commit,review,fix" {
		t.Fatalf("order = %v", got)
	}
	if got := actions[0]; got.Script != "project-commit" || got.Description != "" || got.Key != "" || got.Scope != ProjectScope {
		t.Fatalf("override = %+v", got)
	}
}

func TestMergeRejectsDuplicateFinalKeys(t *testing.T) {
	_, err := Merge(
		[]config.MenuAction{{Name: "commit", Key: "c", Script: "one"}},
		[]config.MenuAction{{Name: "checkout", Key: "c", Script: "two"}},
		"global.toml", "project.toml",
	)
	if err == nil || !strings.Contains(err.Error(), `duplicate menu-action key "c"`) || !strings.Contains(err.Error(), "global.toml") || !strings.Contains(err.Error(), "project.toml") {
		t.Fatalf("error = %v", err)
	}
}

func TestMergeValidatesKeysAfterOverrides(t *testing.T) {
	actions, err := Merge(
		[]config.MenuAction{{Name: "one", Key: "x", Script: "one"}, {Name: "two", Key: "y", Script: "two"}},
		[]config.MenuAction{{Name: "one", Script: "override"}, {Name: "three", Key: "x", Script: "three"}},
		"global.toml", "project.toml",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 || actions[2].Name != "three" {
		t.Fatalf("actions = %+v", actions)
	}
}
