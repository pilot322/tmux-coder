# 8. Single agent status field with two writers

## Status

Accepted

## Context

We are adding richer agent state (busy / idle / waiting) on top of the existing
lifecycle status (`starting`, `running`, `exited`). The first reporting agent is
OpenCode, via a plugin that emits activity events to the daemon.

There are two natural sources of truth for an agent's state, and they know
different things:

- The **wrapper** knows OS-process facts — the agent is alive, here is its
  process-group id, it has exited. It cannot know whether the agent is blocked on
  the model or on the user.
- The **agent's own integration** (the OpenCode plugin) knows activity — it
  finished a turn, it is asking for a permission, it is generating. It cannot
  reliably report `exited`, because a `SIGKILL` leaves no chance to emit.

Two shapes were considered:

1. **Two orthogonal fields** — a lifecycle `status` owned by the wrapper and a
   separate `activity` axis owned by the plugin. No writer ever contends for the
   same field, at the cost of a second field everything (domain, DTO, TUI) must
   carry and reason about.

2. **One merged `status` enum** with both writers — `starting`, `running`,
   `busy`, `idle`, `waiting`, `exited` in a single field.

## Decision

Option 2: a single `AgentStatus` enum, written by both the wrapper and the
agent integration. The vocabulary is canonical and agent-agnostic; each agent
kind's integration translates its native signals into these words, so the daemon
never learns kind-specific terms. A kind that reports no activity rests at
`running`.

Because one field now has two writers, a fixed conflict policy resolves
contention:

- **`exited` is terminal and always wins** — it removes the agent regardless of
  current status.
- **`started` records the process-group id unconditionally, but promotes status
  to `running` only from `starting`.** It must never downgrade a richer status
  (e.g. a `busy`/`idle` the plugin already reported) back to `running`.
- **Activity events (`busy`/`idle`/`waiting`) are last-write-wins**, with
  ordering guaranteed by the plugin sending serially rather than the daemon
  tracking sequence numbers.

## Consequences

- The invariant "any status other than `starting`/`exited` implies the process
  is alive" holds, which is why a merged field is workable at all.
- `AgentEvent.handleStarted` must split into "always record pgid" and "promote
  to running only from starting" — without that guard a late-arriving `started`
  can clobber a status the plugin already set. The race is unlikely (the wrapper
  POSTs `started` before OpenCode finishes booting its plugin runtime), but the
  guard removes it entirely.
- Reversing to two orthogonal axes later is a meaningful change — it touches the
  domain model, the event API vocabulary, the plugin contract, and the TUI. We
  accept that cost for the simplicity of a single field today.
- The event API stays closed: `AgentEvent.Execute` rejects any event string
  outside the known set, so an unrecognised word is a validation error, not a
  silently stored status.
