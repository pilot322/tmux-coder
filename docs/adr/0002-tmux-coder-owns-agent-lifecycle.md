# 2. tmux-coder owns agent lifecycle

## Status

Accepted

## Context

We need the daemon to maintain an in-memory registry of active agents so it can correlate events (completion, errors) to specific agents, sessions, and projects. The question was how agents get registered.

Two approaches were considered:

1. **Agent self-registration via startup hooks** — agents use their own hook system to call `tmux-coder agent register` when they start. This is decoupled but depends on each agent having a startup hook, which not all do (e.g. Claude Code has post-tool and stop hooks but no dedicated startup hook).

2. **tmux-coder wraps agent launch** — the user runs `tmux-coder -a claude`, which registers the agent with the daemon, injects environment (including `TMUX_CODER_AGENT_ID`), then exec's the agent process. On exit, tmux-coder deregisters the agent.

## Decision

Option 2: tmux-coder owns the full agent lifecycle via `tmux-coder -a <agent>`.

## Consequences

- Registration is guaranteed — no dependency on agent-specific hook availability.
- The agent ID is injected as an env var, so agent hooks (like Claude Code's `Stop` hook) can reference it when firing events back to the daemon.
- Agents launched outside of `tmux-coder -a` are not tracked. This is acceptable for now — orchestration features assume tmux-coder-managed agents.
- Deregistration is simple: when the wrapped process exits, tmux-coder deregisters it. No history is kept (can be revisited later).
- This positions tmux-coder as the single entry point for agent management within a project.
