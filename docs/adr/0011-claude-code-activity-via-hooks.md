# 11. Claude Code activity reporting via hooks

## Status

Accepted

## Context

ADR 0008 established a single canonical `AgentStatus` written by two sources, with
each **Agent Kind**'s integration translating its native signals into the
agent-agnostic `busy` / `idle` / `waiting` vocabulary. OpenCode was the first such
integration, via an in-process plugin that subscribes to its event bus and POSTs
to `/agents/{id}/event`.

Claude Code is the second. Unlike OpenCode it exposes no in-process plugin API for
activity; its extension point is **hooks** — external commands it runs on
lifecycle events (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`,
`Notification`, `Stop`, …). Two questions followed: how a hook command reaches the
daemon, and how the per-event commands map onto the canonical statuses given that
each hook is a separate, stateless process.

## Decision

Add a kind-agnostic emitter subcommand, `tmux-coder agent-event <busy|idle|waiting>`,
and bind Claude Code's hooks to it. The emitter mirrors the OpenCode plugin's
contract: it identifies the agent solely from `TMUX_CODER_AGENT_ID` (inert when
unset), normalises `TMUX_CODERD_ADDR` the same way the wrapper does, and POSTs the
event best-effort with a one-second timeout, swallowing every error so a missing
daemon never fails a hook. It accepts only the three activity words; lifecycle
events stay the wrapper's to emit.

Hook → status mapping:

| hook               | status    |
| ------------------ | --------- |
| `SessionStart`     | `idle`    |
| `UserPromptSubmit` | `busy`    |
| `PreToolUse`       | `busy`    |
| `PostToolUse`      | `busy`    |
| `Notification`     | `waiting` |
| `Stop`             | `idle`    |

The mapping is **stateless** but **payload-aware**. The OpenCode plugin keeps a
`blocked` flag so a token streaming mid-prompt cannot clobber `waiting`; that is
possible because it is one long-lived process. Claude Code fires an independent
process per hook, so no such flag can exist. Two facts make a per-hook process
sufficient:

- The real firing order for a permission or question prompt is
  `UserPromptSubmit` → `PreToolUse` → `Notification` — the `Notification` =
  `waiting` lands *after* the `PreToolUse` = `busy`, not before it. `waiting` is
  then released by the next status the agent emits: an approved tool fires
  `PreToolUse`/`PostToolUse` = `busy`, a finished turn fires `Stop` = `idle`.
  Because the daemon resolves activity last-write-wins (ADR 0008), this ordering
  alone is sufficient and no shared state is needed.

- Claude's `Notification` hook is **overloaded**. Besides the permission/question
  prompt (`notification_type: permission_prompt`) it also fires an idle nudge
  ~60s after a finished turn (`notification_type: idle_prompt`, "Claude is waiting
  for your input"). A pure word-mapping would flip a genuinely idle agent to
  `waiting`. So the emitter reads the JSON payload Claude passes a hook on stdin
  and maps an `idle_prompt` notification to `idle`; every other notification stays
  `waiting`. This is the one place the otherwise agent-agnostic emitter inspects a
  Claude-specific field, and it keeps the fix a runtime refinement rather than a
  second binding — a `matcher` split cannot express "every notification *except*
  `idle_prompt`" without a racing catch-all, and would silently drop notification
  types added later.

`SubagentStop` is deliberately unbound — a finished sub-agent does not mean the
main agent is idle, so binding it to `idle` would clobber `busy` mid-turn.

**User interrupt is unobservable.** Pressing Esc to interrupt a streaming response
— or to dismiss a question — fires *no* hook (`Stop` fires only on normal
completion). The agent is then parked on the user, but its last reported status
(`busy` mid-stream, `waiting` on a dismissed question) lingers until the next turn
heals it (`UserPromptSubmit` → … → `Stop`). We accept this rather than ship a
timer: the status is briefly stale, never wrong forever, and self-corrects on the
user's next action. Claude Code exposes no interrupt/cancel hook to do better.

Installation merges these bindings into `~/.claude/settings.json` via
`tmux-coder install-claude-hooks`. The merge is a pure function
(`internal/claudehooks`) so the policy is unit-tested independently of file IO; it
preserves foreign settings and hooks, and is idempotent by recognising and
replacing tmux-coder's own entries rather than appending duplicates. A standalone
plugin was rejected because enabling a Claude Code plugin requires the interactive
marketplace flow and cannot be scripted, whereas a settings merge can.

## Consequences

- A new always-exit-0 emitter path exists; its only non-zero exit is a usage error
  unreachable from the generated hooks, so a hook can never block the agent.
- The hook commands embed an absolute binary path resolved at install time, so
  reporting does not depend on the agent's `PATH`.
- Re-running the installer after a binary move heals the hook commands, because
  stale tmux-coder entries are replaced by path-independent marker matching.
- A question or permission prompt dismissed with Esc leaves the agent at
  `waiting` until its next turn, because the dismissal — like any interrupt —
  fires no hook (see "User interrupt is unobservable" above). The next prompt
  heals it.
- `TMUX_CODER_AGENT_EVENT_DEBUG`, when it names a file, makes the emitter append
  one line per invocation — the firing hook, its `notification_type`, the reported
  status, and the agent id. It is the hook-side counterpart to the OpenCode
  plugin's `TMUX_CODER_PLUGIN_DEBUG` and is how the firing order above was
  established. Off by default; diagnostics only.
