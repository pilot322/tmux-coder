// Package desktopnotify delivers Desktop Notifications to the host by shelling
// out to notify-send, optionally accompanied by an audible cue played through
// paplay. It is the dumb mechanism behind usecase.Notifier; the policy (which
// Agent Status transitions notify, the message content, and whether a sound is
// requested) lives in the usecase.
package desktopnotify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

var (
	_ usecase.Notifier = (*Notifier)(nil)
	_ usecase.Notifier = NoopNotifier{}
)

// notifyTimeout bounds a single notify-send invocation so a wedged notification
// daemon can never stall agent-event processing.
const notifyTimeout = 2 * time.Second

// soundTimeout bounds paplay independently from notify-send. Custom cues are
// often longer than notification daemons need, so sharing notifyTimeout would
// cut otherwise valid sounds short.
const soundTimeout = 10 * time.Second

// soundPlayer is the audio player the sound cue shells out to. paplay ships with
// PulseAudio/PipeWire and reliably produces a sound on modern Linux desktops,
// where notify-send's own sound hints are commonly ignored.
const soundPlayer = "paplay"

// defaultSoundFile is the fallback audible cue. It is part of the freedesktop sound theme
// (sound-theme-freedesktop), present on most desktops; if absent, paplay fails
// and the error is swallowed like any other delivery failure.
const defaultSoundFile = "/usr/share/sounds/freedesktop/stereo/message.oga"

const defaultSoundName = "notification"

var soundExtensions = []string{".oga", ".ogg", ".wav", ".flac", ".aiff", ".aif", ".au", ".mp3"}

// EnvSound toggles the audible cue that accompanies a Desktop Notification.
// Sound is on by default; set TMUX_CODER_NOTIFY_SOUND to 0/false/off/no to mute
// it. It does not affect the visual notification.
const EnvSound = "TMUX_CODER_NOTIFY_SOUND"

// SoundEnabled reports whether the audible cue is enabled, reading EnvSound via
// the supplied getenv. The default is on: only an explicit off-value disables
// it, so an unset or unrecognised value keeps sound on.
func SoundEnabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvSound))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// Notifier delivers Desktop Notifications via notify-send and, when a sound is
// requested and one is available, plays an audible cue via paplay. CommandContext
// is injected so tests can assert the argv without spawning a process.
type Notifier struct {
	CommandContext func(ctx context.Context, name string, args ...string) *exec.Cmd
	// soundEnabled gates the audible cue. It is false when sound is disabled by
	// config or no player resolved on PATH, in which case Notify only shows the
	// visual notification.
	soundEnabled bool
	soundFiles   func() map[string]string
}

// NewNotifier returns a notify-send-backed Notifier when running on Linux with
// notify-send on PATH, otherwise a NoopNotifier. Selection is at runtime rather
// than via build tags: shelling out needs no Linux-only imports, and this also
// covers a Linux host without libnotify installed. soundEnabled requests the
// audible cue; it is honoured only when paplay also resolves on PATH.
func NewNotifier(soundEnabled bool) usecase.Notifier {
	return newNotifier(runtime.GOOS, exec.LookPath, exec.CommandContext, soundEnabled, func() map[string]string {
		return SoundFiles(os.Getenv, fileExists)
	})
}

func newNotifier(goos string, lookPath func(string) (string, error), commandContext func(ctx context.Context, name string, args ...string) *exec.Cmd, soundEnabled bool, soundFiles func() map[string]string) usecase.Notifier {
	if goos != "linux" {
		return NoopNotifier{}
	}
	if _, err := lookPath("notify-send"); err != nil {
		return NoopNotifier{}
	}
	// Sound is an add-on to the visual notification: a missing player mutes the
	// cue but never suppresses the notification itself.
	sound := soundEnabled
	if sound {
		if _, err := lookPath(soundPlayer); err != nil {
			sound = false
		}
	}
	return &Notifier{CommandContext: commandContext, soundEnabled: sound, soundFiles: soundFiles}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// SoundFiles discovers user-configured sound cues in ~/.tmux-coder/sounds. A
// cue named "agent-idle" may be supplied as agent-idle.oga, agent-idle.wav, etc.
// "notification" is the generic fallback for any named cue without its own file.
func SoundFiles(getenv func(string) string, exists func(string) bool) map[string]string {
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		return nil
	}
	dir := filepath.Join(home, ".tmux-coder", "sounds")
	files := make(map[string]string)
	for _, name := range []string{"agent-idle", "agent-waiting", defaultSoundName} {
		if path := firstExistingSound(dir, name, exists); path != "" {
			files[name] = path
		}
	}
	return files
}

func firstExistingSound(dir, name string, exists func(string) bool) string {
	for _, ext := range soundExtensions {
		path := filepath.Join(dir, name+ext)
		if exists(path) {
			return path
		}
	}
	return ""
}

// Notify shells out to notify-send and, when the message requests it and sound
// is enabled, plays the audible cue concurrently. notify-send and paplay each
// run under bounded contexts, so neither can stall event processing indefinitely.
// The cue is best-effort: its error is ignored.
//
// The daemon is launched with no explicit cmd.Env (see
// internal/client/daemon/daemon.go), so it inherits the launching client's
// session env, including DBUS_SESSION_BUS_ADDRESS, which notify-send needs (and
// the audio server address paplay needs). If the daemon was first auto-launched
// from a context with no session bus (e.g. a bare SSH login), both fail; callers
// swallow the error.
func (n *Notifier) Notify(ctx context.Context, msg usecase.Notification) error {
	notifyCtx, notifyCancel := context.WithTimeout(ctx, notifyTimeout)
	defer notifyCancel()

	if msg.Sound && n.soundEnabled {
		soundFile := n.soundFile(msg.SoundName)
		soundCtx, soundCancel := context.WithTimeout(ctx, soundTimeout)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = n.CommandContext(soundCtx, soundPlayer, soundFile).Run()
		}()
		// Wait runs before soundCancel (LIFO), so the cue completes — or hits its
		// own timeout — before the context is torn down.
		defer soundCancel()
		defer wg.Wait()
	}

	cmd := n.CommandContext(notifyCtx, "notify-send", "-u", urgencyFlag(msg.Urgency), "-a", "tmux-coder", msg.Title, msg.Body)
	return cmd.Run()
}

func (n *Notifier) soundFile(name string) string {
	if name == "" {
		name = defaultSoundName
	}
	var files map[string]string
	if n.soundFiles != nil {
		files = n.soundFiles()
	}
	if path := files[name]; path != "" {
		return path
	}
	if path := files[defaultSoundName]; path != "" {
		return path
	}
	return defaultSoundFile
}

func urgencyFlag(u usecase.NotificationUrgency) string {
	if u == usecase.UrgencyCritical {
		return "critical"
	}
	return "normal"
}

// NoopNotifier discards Desktop Notifications. It is selected when the host has
// no usable notify-send.
type NoopNotifier struct{}

func (NoopNotifier) Notify(context.Context, usecase.Notification) error { return nil }
