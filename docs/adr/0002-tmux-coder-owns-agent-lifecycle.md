# 2. tmux-coder owns agent lifecycle

## Status

Accepted

## Context

We need the daemon to maintain an in-memory registry of active agents so it can correlate events (completion, errors) to specific agents, sessions, and projects. The question was how agents get registered.

Two approaches were considered:

1. **Agent self-registration via startup hooks** — agents use their own hook system to call `tmux-coder agent register` when they start. This is decoupled but depends on each agent having a startup hook, which not all do (e.g. Claude Code has post-tool and stop hooks but no dedicated startup hook).

2. **tmux-coder wraps agent launch** — tmux-coder registers the agent with the daemon, injects environment (including `TMUX_CODER_AGENT_ID`), and reports lifecycle events. The long-running pane babysitter is implemented as a hidden `tmux-coder agent-wrapper <id> <kind>` subcommand rather than a separate binary, so the same installed artefact serves both the interactive client and the wrapper runtime.

## Decision

Option 2: tmux-coder owns the full agent lifecycle. The wrapper logic lives in `internal/agentwrapper` and is exposed through a hidden `agent-wrapper` subcommand of `tmux-coder`. Public commands such as `tmux-coder new`/`tmux-coder n` use that subcommand when taking over the current pane, and the daemon uses it as the command for new tmux windows.

## Consequences

- Registration is guaranteed — no dependency on agent-specific hook availability.
- The agent ID and related context are injected as environment variables, so agent hooks can reference them when firing events back to the daemon.
- Agents launched outside tmux-coder are not tracked. This is acceptable for now — orchestration features assume tmux-coder-managed agents.
- The wrapper remains alive as the parent of the external agent process so it can report `started` and `exited`; it does not `exec`-replace itself with the agent.
- For borrowed panes, tmux-coder does not kill the user-owned tmux pane on delete. The wrapper starts the agent in a child process group, reports that process-group id to the daemon, and deletion terminates that process group instead.
- The wrapper is a hidden `tmux-coder agent-wrapper` subcommand backed by `internal/agentwrapper`, eliminating a separate `tmux-coderd-wrapper` binary while keeping the interactive client and wrapper runtime cleanly separated at the composition root.
- The Agent Registry tracks active agents only. Terminal lifecycle events remove records; no history is kept yet.
- This positions tmux-coder as the single entry point for agent management within a project.
