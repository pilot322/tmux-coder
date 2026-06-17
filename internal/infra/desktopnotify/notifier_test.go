package desktopnotify

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sync"
	"testing"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

// recorder captures every argv Notify shells out to, guarded against the
// concurrent sound + notify-send invocations.
type recorder struct {
	mu    sync.Mutex
	argvs [][]string
}

func (r *recorder) commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	r.mu.Lock()
	r.argvs = append(r.argvs, append([]string{name}, args...))
	r.mu.Unlock()
	return exec.CommandContext(ctx, "true") // harmless; we only assert the argv
}

func (r *recorder) find(name string) ([]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.argvs {
		if a[0] == name {
			return a, true
		}
	}
	return nil, false
}

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
			rec := &recorder{}
			n := &Notifier{CommandContext: rec.commandContext}
			n.Notify(context.Background(), usecase.Notification{
				Title:   tc.want[5],
				Body:    "api · main",
				Urgency: tc.urgency,
			})
			got, _ := rec.find("notify-send")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNotifierPlaysSoundWhenRequestedAndEnabled(t *testing.T) {
	rec := &recorder{}
	n := &Notifier{CommandContext: rec.commandContext, soundEnabled: true}
	n.Notify(context.Background(), usecase.Notification{Title: "t", Body: "b", Sound: true})

	got, ok := rec.find(soundPlayer)
	if !ok {
		t.Fatalf("expected the sound player to be invoked, argvs = %v", rec.argvs)
	}
	if want := []string{soundPlayer, defaultSoundFile}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sound argv = %v, want %v", got, want)
	}
	if _, ok := rec.find("notify-send"); !ok {
		t.Fatalf("expected notify-send to still be invoked alongside the sound")
	}
}

func TestNotifierUsesConfiguredNamedSound(t *testing.T) {
	rec := &recorder{}
	n := &Notifier{CommandContext: rec.commandContext, soundEnabled: true, soundFiles: map[string]string{"agent-idle": "/home/me/.tmux-coder/sounds/agent-idle.oga"}}
	n.Notify(context.Background(), usecase.Notification{Title: "t", Body: "b", Sound: true, SoundName: "agent-idle"})

	got, ok := rec.find(soundPlayer)
	if !ok {
		t.Fatalf("expected the sound player to be invoked, argvs = %v", rec.argvs)
	}
	if want := []string{soundPlayer, "/home/me/.tmux-coder/sounds/agent-idle.oga"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sound argv = %v, want %v", got, want)
	}
}

func TestNotifierFallsBackToConfiguredNotificationSound(t *testing.T) {
	rec := &recorder{}
	n := &Notifier{CommandContext: rec.commandContext, soundEnabled: true, soundFiles: map[string]string{defaultSoundName: "/home/me/.tmux-coder/sounds/notification.wav"}}
	n.Notify(context.Background(), usecase.Notification{Title: "t", Body: "b", Sound: true, SoundName: "missing"})

	got, ok := rec.find(soundPlayer)
	if !ok {
		t.Fatalf("expected the sound player to be invoked, argvs = %v", rec.argvs)
	}
	if want := []string{soundPlayer, "/home/me/.tmux-coder/sounds/notification.wav"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sound argv = %v, want %v", got, want)
	}
}

func TestNotifierSkipsSound(t *testing.T) {
	cases := []struct {
		name         string
		soundEnabled bool
		msgSound     bool
	}{
		{"disabled even when requested", false, true},
		{"enabled but not requested", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			n := &Notifier{CommandContext: rec.commandContext, soundEnabled: tc.soundEnabled}
			n.Notify(context.Background(), usecase.Notification{Title: "t", Body: "b", Sound: tc.msgSound})
			if _, ok := rec.find(soundPlayer); ok {
				t.Fatalf("did not expect the sound player to be invoked")
			}
			if _, ok := rec.find("notify-send"); !ok {
				t.Fatalf("notify-send should still fire")
			}
		})
	}
}

func TestNewNotifierSelection(t *testing.T) {
	ok := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	// onlyNotifySend resolves notify-send but not the sound player, so sound is
	// requested but unavailable.
	onlyNotifySend := func(name string) (string, error) {
		if name == soundPlayer {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}

	if _, isNoop := newNotifier("linux", missing, exec.CommandContext, true, nil).(NoopNotifier); !isNoop {
		t.Fatalf("missing notify-send should select the noop notifier")
	}
	if _, isNoop := newNotifier("darwin", ok, exec.CommandContext, true, nil).(NoopNotifier); !isNoop {
		t.Fatalf("non-linux host should select the noop notifier")
	}

	n, isReal := newNotifier("linux", ok, exec.CommandContext, true, nil).(*Notifier)
	if !isReal {
		t.Fatalf("linux with notify-send present should select the real notifier")
	}
	if !n.soundEnabled {
		t.Fatalf("sound requested with a player present should be enabled")
	}

	muted, _ := newNotifier("linux", onlyNotifySend, exec.CommandContext, true, nil).(*Notifier)
	if muted == nil || muted.soundEnabled {
		t.Fatalf("a missing sound player should mute sound but keep the real notifier")
	}

	off, _ := newNotifier("linux", ok, exec.CommandContext, false, nil).(*Notifier)
	if off == nil || off.soundEnabled {
		t.Fatalf("sound disabled by config should not be enabled even with a player present")
	}
}

func TestSoundFilesDiscoversConfiguredSounds(t *testing.T) {
	home := "/home/me"
	existing := map[string]bool{
		"/home/me/.tmux-coder/sounds/agent-idle.oga":   true,
		"/home/me/.tmux-coder/sounds/notification.wav": true,
	}
	got := SoundFiles(func(key string) string {
		if key == "HOME" {
			return home
		}
		return ""
	}, func(path string) bool { return existing[path] })

	want := map[string]string{
		"agent-idle":     "/home/me/.tmux-coder/sounds/agent-idle.oga",
		defaultSoundName: "/home/me/.tmux-coder/sounds/notification.wav",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SoundFiles() = %v, want %v", got, want)
	}
}

func TestSoundEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", true},   // unset → default on
		{"1", true},  // unrecognised true-ish → on
		{"on", true}, // unrecognised → on (only off-values mute)
		{"0", false},
		{"false", false},
		{"OFF", false}, // case-insensitive
		{"no", false},
	}
	for _, tc := range cases {
		if got := SoundEnabled(func(string) string { return tc.val }); got != tc.want {
			t.Fatalf("SoundEnabled(%q) = %v, want %v", tc.val, got, tc.want)
		}
	}
}

func TestNoopNotifierDoesNothing(t *testing.T) {
	if err := (NoopNotifier{}).Notify(context.Background(), usecase.Notification{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("NoopNotifier.Notify should return nil, got %v", err)
	}
}
