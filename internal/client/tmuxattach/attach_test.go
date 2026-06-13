package tmuxattach_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/pilot322/tmux-coder/internal/client/tmuxattach"
)

func TestArgsSwitchClientInsideTmux(t *testing.T) {
	got := tmuxattach.Args("api-main", true)
	want := []string{"-L", "tmux-coder", "switch-client", "-t", "api-main"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v", got)
	}
}

func TestArgsAttachOutsideTmux(t *testing.T) {
	got := tmuxattach.Args("api-main", false)
	want := []string{"-L", "tmux-coder", "attach-session", "-t", "api-main"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v", got)
	}
}

func TestArgsWithServerUsesCustomLabel(t *testing.T) {
	got := tmuxattach.ArgsWithServer("tmux-coder-test-1", "api-main", true)
	want := []string{"-L", "tmux-coder-test-1", "switch-client", "-t", "api-main"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v", got)
	}
}

func TestSelectPaneArgs(t *testing.T) {
	got := tmuxattach.SelectPaneArgs("%7")
	want := []string{"-L", "tmux-coder", "select-pane", "-t", "%7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v", got)
	}
}

func TestCommandsOutsideTmuxAttachesDirectly(t *testing.T) {
	got := tmuxattach.Commands("api-main", "")
	want := []tmuxattach.Command{{Args: []string{"-L", "tmux-coder", "attach-session", "-t", "api-main"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v", got)
	}
}

func TestCommandsInsideTmuxFallbackAttachesWithTMUXUnset(t *testing.T) {
	got := tmuxattach.Commands("api-main", "/tmp/tmux/default,123,0")
	want := []tmuxattach.Command{
		{Args: []string{"-L", "tmux-coder", "switch-client", "-t", "api-main"}},
		{Args: []string{"-L", "tmux-coder", "attach-session", "-t", "api-main"}, UnsetTMUX: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v", got)
	}
}

func TestCommandsWithServerUsesCustomLabel(t *testing.T) {
	got := tmuxattach.CommandsWithServer("tmux-coder-test-1", "api-main", "")
	want := []tmuxattach.Command{{Args: []string{"-L", "tmux-coder-test-1", "attach-session", "-t", "api-main"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v", got)
	}
}

func TestCurrentSessionOutsideTmuxReturnsEmpty(t *testing.T) {
	got := tmuxattach.CurrentSession(context.Background(), func(string) string { return "" })
	if got != "" {
		t.Fatalf("current session = %q, want empty", got)
	}
}
