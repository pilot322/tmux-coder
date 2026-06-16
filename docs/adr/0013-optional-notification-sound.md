# 13. Optional Notification Sound

## Status

Accepted

## Context

ADR 0012 added daemon-side **Desktop Notifications** that fire on `busy → waiting`
and `busy → idle`, delivered visually via `notify-send`. The motivating case is a
user working in another window while an agent runs unattended — but a purely
visual alert is easy to miss when the user is not looking at the screen at all. An
audible cue closes that gap.

Two facts shaped the design. First, `notify-send`'s own sound hints
(`-h string:sound-name:…`) are unreliable: several notification daemons (notably
GNOME's) ignore them, so the hint would often produce no sound. Second, ADR 0012
deliberately shipped notifications *always on, with no config flag* — but a sound
is more intrusive than a silent banner, so it earns a toggle the visual layer does
not need.

## Decision

**Play the cue with a dedicated player, not a notify-send hint.** The mechanism
shells out to `paplay` (PulseAudio/PipeWire), which reliably produces a sound on
modern Linux desktops where notify-send hints are ignored. It plays the
freedesktop sound-theme file `…/freedesktop/stereo/message.oga`. `paplay` is
resolved on `PATH` at runtime, exactly like `notify-send`: present → the cue is
available; absent → the cue is silently muted but the visual notification is
unaffected. Linux-only, consistent with ADR 0012.

**The request is a port hint; delivery is the mechanism's call.** `usecase.Notification`
gains an optional `Sound bool`. The usecase sets it on both qualifying
notifications (the policy *requests* a sound); whether one is actually audible is
decided entirely by the mechanism — player present *and* sound enabled. The port
stays generic and carries no audio knowledge.

**Sound is enabled by config, default on.** The daemon reads
`TMUX_CODER_NOTIFY_SOUND` at startup (composition root, mirroring how
`TMUX_CODERD_PORT` is read) and passes the result to `NewNotifier`. Sound is on
unless the value is an explicit off-value (`0`/`false`/`off`/`no`,
case-insensitive); an unset or unrecognised value keeps it on. The flag governs
sound only — it never affects the visual notification, so ADR 0012's "notifications
are always on" still holds.

**The cue shares the notify budget.** It plays concurrently with `notify-send`
under the same 2-second timeout, so requesting a sound can add at most the notify
budget and never an unbounded stall. Its error is swallowed like any other
delivery failure (ADR 0012's "best-effort, never disruptive").

## Consequences

- An audible cue accompanies notifications on Linux hosts with `paplay`, on by
  default, mutable with `TMUX_CODER_NOTIFY_SOUND=0`.
- A new external dependency (`paplay`) for the cue only; its absence degrades to
  the previous silent behaviour with no error.
- `paplay`, like `notify-send`, needs the daemon's inherited session environment
  (audio server address). A daemon first auto-launched from a context without one
  produces no sound; we do not engineer around this (same note as ADR 0012).
- One fixed sound for both transitions — no per-urgency cue and no configurable
  sound file yet (YAGNI). A future change would add a hint to `Notification` or a
  second env var; the port and config seam already accommodate it.
- The cue blocks the triggering event's HTTP response for the sound's duration
  (bounded by the notify timeout), the same way the visual notification already
  does.
