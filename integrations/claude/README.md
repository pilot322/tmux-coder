# Claude Code activity reporting

Claude Code reports no activity on its own; it runs external commands on
lifecycle hooks. This integration binds those hooks to
`tmux-coder agent-event <status>`, a short-lived emitter that POSTs the agent's
activity to the daemon — the hook-driven counterpart to the OpenCode plugin.

The emitter is inert unless `TMUX_CODER_AGENT_ID` is set, so the hooks are safe
to install globally: outside a tmux-coder-managed pane they do nothing. The
wrapper exports `TMUX_CODER_AGENT_ID` (and `TMUX_CODERD_ADDR`) into the agent's
environment, and Claude Code's hook commands inherit it.

## Install

`./dev install` runs `tmux-coder install-claude-hooks`, which idempotently merges
the bindings below into `~/.claude/settings.json`. Every other setting and any
foreign hook is preserved; re-running replaces our entries rather than
duplicating them.

To install by hand, or to scope the hooks to a single project, copy the bindings
in [`hooks.json`](./hooks.json) into the `"hooks"` block of any Claude Code
settings file (`~/.claude/settings.json` or a project's `.claude/settings.json`).

## Event → status mapping

| Claude Code hook   | Reported status |
| ------------------ | --------------- |
| `SessionStart`     | `idle`          |
| `UserPromptSubmit` | `busy`          |
| `PreToolUse`       | `busy`          |
| `PostToolUse`      | `busy`          |
| `Notification`     | `waiting`       |
| `Stop`             | `idle`          |

The mapping is stateless: each hook is an independent process, so there is no
shared "blocked" flag as in the OpenCode plugin. `waiting` (raised when Claude
asks for tool permission or a question) is released naturally by the next status
the agent emits — an approved tool fires `PreToolUse`/`PostToolUse` = `busy`, a
finished turn fires `Stop` = `idle`.

`Notification` is the one overloaded hook. It fires both when Claude needs the
user (`notification_type: permission_prompt` → genuinely `waiting`) and as an
idle nudge ~60s after a finished turn (`notification_type: idle_prompt`). The
emitter reads the hook payload on stdin and reports `idle` for the latter, so a
finished agent is not flipped back to `waiting`.

`SubagentStop` is intentionally unbound: a finished sub-agent does not mean the
main agent is idle, so binding it would clobber `busy` mid-turn.

Pressing Esc to interrupt a response, or to dismiss a question, fires no hook —
Claude Code exposes no interrupt/cancel hook — so the last status (`busy`, or
`waiting` on a dismissed question) lingers until the next turn heals it. This is
a Claude Code limitation, not a configuration gap.

## Debugging

Set `TMUX_CODER_AGENT_EVENT_DEBUG` to a file path to trace hook firings: the
emitter appends one line per invocation with the firing hook, its
`notification_type`, the reported status, and the agent id. The variable must be
present in the agent's environment (e.g. exported before the daemon starts, so it
propagates through tmux to the agent and its hooks). Off by default.
