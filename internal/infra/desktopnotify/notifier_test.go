package desktopnotify

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

func TestNotifierBuildsNotifySendArgv(t *testing.T) {
	cases := []struct {
		name    string
		urgency usecase.NotificationUrgency
		want    []string
	}{
		{"critical", usecase.UrgencyCritical, []string{"notify-send", "-u", "critical", "-a", "tmux-coder", "agent needs input", "api · main"}},
		{"normal", usecase.UrgencyNormal, []string{"notify-send", "-u", "normal", "-a", "tmux-coder", "agent is idle", "api · main"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			n := &Notifier{
				CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
					got = append([]string{name}, args...)
					return exec.CommandContext(ctx, "true") // harmless; we only assert the argv
				},
			}
			n.Notify(context.Background(), usecase.Notification{
				Title:   tc.want[5],
				Body:    "api · main",
				Urgency: tc.urgency,
			})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNoopNotifierDoesNothing(t *testing.T) {
	if err := (NoopNotifier{}).Notify(context.Background(), usecase.Notification{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("NoopNotifier.Notify should return nil, got %v", err)
	}
}

func TestNewNotifierSelection(t *testing.T) {
	ok := func(string) (string, error) { return "/usr/bin/notify-send", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }

	if _, isNoop := newNotifier("linux", missing, exec.CommandContext).(NoopNotifier); !isNoop {
		t.Fatalf("missing notify-send should select the noop notifier")
	}
	if _, isNoop := newNotifier("darwin", ok, exec.CommandContext).(NoopNotifier); !isNoop {
		t.Fatalf("non-linux host should select the noop notifier")
	}
	if _, isReal := newNotifier("linux", ok, exec.CommandContext).(*Notifier); !isReal {
		t.Fatalf("linux with notify-send present should select the real notifier")
	}
}
