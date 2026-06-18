# 13. Optional Notification Sound

## Status

Accepted

## Context

ADR 0012 added daemon-side **Desktop Notifications** that fire when an agent
enters `waiting` or `idle`, delivered visually via `notify-send`. The motivating
case is a user working in another window while an agent runs unattended — but a
purely visual alert is easy to miss when the user is not looking at the screen at
all. An audible cue closes that gap.

Two facts shaped the design. First, `notify-send`'s own sound hints
(`-h string:sound-name:…`) are unreliable: several notification daemons (notably
GNOME's) ignore them, so the hint would often produce no sound. Second, ADR 0012
deliberately shipped notifications *always on, with no config flag* — but a sound
is more intrusive than a silent banner, so it earns a toggle the visual layer does
not need.

## Decision

**Play the cue with a dedicated player, not a notify-send hint.** The mechanism
shells out to `paplay` (PulseAudio/PipeWire), which reliably produces a sound on
modern Linux desktops where notify-send hints are ignored. `paplay` is resolved
on `PATH` at runtime, exactly like `notify-send`: present → the cue is available;
absent → the cue is silently muted but the visual notification is unaffected.
Linux-only, consistent with ADR 0012.

**Sound files are user-configurable under `~/.tmux-coder/sounds`.** The usecase
requests named cues (`agent-idle` for an agent finishing, `agent-waiting` for an
agent needing input). The mechanism resolves those names as files under
`~/.tmux-coder/sounds`, trying common `paplay`/libsndfile extensions such as
`.oga`, `.ogg`, `.wav`, `.flac`, `.aiff`, `.aif`, `.au`, and `.mp3`. If no
specific cue exists, it tries `notification.*`; if no user sound exists, it falls
back to the freedesktop sound-theme file
`/usr/share/sounds/freedesktop/stereo/message.oga`. MP3 support is distro
dependent because it depends on the host's libsndfile build, so Ogg/Vorbis, WAV,
or FLAC are safer choices.

**The request is a port hint; delivery is the mechanism's call.**
`usecase.Notification` has optional `Sound` and `SoundName` fields. The usecase
sets them on both qualifying notifications (the policy *requests* a named sound);
whether one is actually audible is decided entirely by the mechanism — player
present, sound enabled, and a resolvable sound file. The port stays generic and
carries no audio-format knowledge.

**Sound is enabled by config, default on.** The daemon reads
`TMUX_CODER_NOTIFY_SOUND` at startup (composition root, mirroring how
`TMUX_CODERD_PORT` is read) and passes the result to `NewNotifier`. Sound is on
unless the value is an explicit off-value (`0`/`false`/`off`/`no`,
case-insensitive); an unset or unrecognised value keeps it on. The flag governs
sound only — it never affects the visual notification, so ADR 0012's "notifications
are always on" still holds.

**The cue has its own bounded budget.** It plays concurrently with `notify-send`,
but uses a longer sound-specific timeout so ordinary custom sounds are not cut
off by the visual notification's short timeout. Its error is swallowed like any
other delivery failure (ADR 0012's "best-effort, never disruptive").

## Consequences

- An audible cue accompanies notifications on Linux hosts with `paplay`, on by
  default, mutable with `TMUX_CODER_NOTIFY_SOUND=0`.
- Users can customise the completion cue by placing a supported file named
  `agent-idle.*` under `~/.tmux-coder/sounds`; `agent-waiting.*` customises the
  input-needed cue, and `notification.*` is a shared fallback.
- A new external dependency (`paplay`) for the cue only; its absence degrades to
  the previous silent behaviour with no error.
- `paplay`, like `notify-send`, needs the daemon's inherited session environment
  (audio server address). A daemon first auto-launched from a context without one
  produces no sound; we do not engineer around this (same note as ADR 0012).
- The cue blocks the triggering event's HTTP response for the sound's duration
  (bounded by the sound timeout), the same way the visual notification already
  does.
