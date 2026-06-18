# 12. Daemon-side Desktop Notifications

## Status

Accepted

## Context

ADR 0008 made the **Daemon** the single owner of each **TC Agent**'s `AgentStatus`,
resolving the activity events (`busy` / `idle` / `waiting`) every **Agent Kind**'s
integration reports. The TUI already renders that status — `waiting` bold-red,
`idle` green — but a user is only alerted when they are looking at the Agents view.
The recurring need is the opposite: an agent that has been working unattended
finishes or stops to ask a question, and the user is in another window entirely.

We want an OS-level **Desktop Notification** for exactly those moments. The design
question was *where* the notification decision lives, *which* transitions fire one,
and how to keep a flaky desktop channel from ever harming agent-event processing.

## Decision

**Detect the transition daemon-side, in the usecase that already applies it.**
`usecase.AgentEvent.handleActivity` is the one place where both the agent's old
status and the incoming status are known. It computes the trigger after the status
write commits and raises the notification there. Consequences are intended:
notifications fire **even when no TUI is open**, and never double-fire when several
TUIs are attached. Sorting/notifying in a client would do neither.

**Transitions into `waiting` and `idle` notify.** A notification marks that the
agent has either parked on the user (`waiting`) or finished (`idle`). Integrations
do not all emit a perfectly paired `busy` before every terminal activity event, so
requiring `busy → ...` makes real notifications dependent on hook ordering rather
than the state the user sees. Repeated same-status events do not notify, and
lifecycle `started` / `exited` events still never notify. The predicate is a pure
helper (`notificationFor`) so it is unit-tested independently of delivery.

| transition       | title                | body                    | urgency  |
| ---------------- | -------------------- | ----------------------- | -------- |
| `* → waiting`    | `{name} needs input` | `{project} · {session}` | critical |
| `* → idle`       | `{name} is idle`     | `{project} · {session}` | normal   |

Title and urgency mirror the TUI's visual semantics (`waiting` demands attention,
`idle` is informational). `{name}` is the agent's display name, falling back to
`agent-{id}` exactly as the TUI's row label does.

**Delivery is a dumb, injected mechanism behind a port.** A `Notifier` port lives in
`usecase` (generic — it carries a composed title/body/urgency, not status
knowledge); `internal/infra/desktopnotify` implements it by shelling out to
`notify-send -u <normal|critical> -a tmux-coder <title> <body>` with a 2-second
timeout. `NewNotifier()` selects the real implementation at **runtime** — only when
`GOOS == linux` and `notify-send` resolves on `PATH` — and returns a no-op
otherwise. Runtime selection (not build tags) is deliberate: shelling out needs no
Linux-only imports, and it also covers a Linux host without libnotify installed.

**Best-effort, never disruptive.** The notify call runs outside the state lock,
after the status update has committed, and its error is swallowed (debug log at
most). A wedged or absent notification daemon can delay nothing and fail nothing in
agent-event handling.

## Consequences

- Notifications are independent of any client: they fire headless and exactly once
  per qualifying transition, regardless of how many TUIs are attached.
- The daemon is launched with no explicit `cmd.Env`
  (`internal/client/daemon/daemon.go`), so it inherits the launching client's
  session environment, including `DBUS_SESSION_BUS_ADDRESS`, which `notify-send`
  needs. If the daemon was first auto-launched from a context with no session bus
  (e.g. a bare SSH login), `notify-send` fails and the error is swallowed. We do not
  engineer around this; it is noted in the Linux implementation.
- Always on where `notify-send` is available — there is no config flag to
  enable/disable notifications, and no persistence.
- No coalescing or debounce of rapid transitions (YAGNI); a chatty agent can
  produce several notifications in quick succession.
- Adding a non-Linux backend later means a new `Notifier` implementation and a
  branch in `NewNotifier()`; the usecase policy is untouched.
