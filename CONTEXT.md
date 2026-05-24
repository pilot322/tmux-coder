# tmux-coder

A CLI tool that wraps a dedicated tmux server to manage projects with multiple worktrees, providing lifecycle management, session orchestration, and agent coordination.

## Language

**Project**:
A base directory containing a `.tmux-coder.toml` config and all **Sessions** attributed to it.
_Avoid_: Workspace, repository

**Session**:
A 1:1 wrapper around a tmux session on the dedicated tmux server. tmux-coder adds metadata (type, parent, project association) and lifecycle management. Comes in three types: **Main Session**, **Worktree Session**, and **Secondary Session**.
_Avoid_: Window, terminal, shell

**Main Session**:
The single **Session** per **Project** that always exists — the home base for interacting with the project as a whole.
_Avoid_: Root session, default session

**Worktree Session**:
A **Session** tied 1:1 to a git worktree. Created and destroyed with the worktree.
_Avoid_: Branch session

**Secondary Session**:
A child **Session** that stems from a **Main Session** or **Worktree Session**. Represents a sub-context within the same worktree (e.g. `packages/frontend`). Can be defined declaratively in config so they are auto-created when a worktree spins up.
_Avoid_: Sub-session, nested session

**Daemon**:
The long-running server process (`tmux-coder-server`) that owns the dedicated tmux server instance, holds runtime state, and maintains the **Agent Registry** in memory.
_Avoid_: Server (when referring to the daemon specifically, to avoid confusion with the tmux server)

**Client**:
The `tmux-coder` CLI invocation that connects to the **Daemon** to issue commands and render the TUI.
_Avoid_: CLI (as a noun for a running instance)

**TC Agent**:
A coding agent process (Claude Code, Codex, etc.) launched and managed by tmux-coder via `tmux-coder -a <agent>`. Registered with the **Daemon** on launch, deregistered on exit. Each Agent has an ID and is linked to a specific **Session** and **Project**.
_Avoid_: Tool, assistant, process

**Agent Registry**:
The in-memory data structure in the **Daemon** that tracks all active **TC Agents** — their IDs, associated **Sessions**, and **Projects**.
_Avoid_: Agent store, agent list

**Event**:
A notification fired by a **TC Agent**'s hook system to the **Daemon** via `tmux-coder event`. Carries a type, agent ID, and optional payload. Used to signal state changes (completion, error, waiting for input).
_Avoid_: Message, signal, notification

**Config File** (`.tmux-coder.toml`):
A TOML file at a **Project**'s root that declares **Secondary Sessions**, environment variables, and hooks. Checked into version control. Runtime state lives elsewhere (SQLite).
_Avoid_: Settings, project file, manifest

## Example dialogue

> **Dev**: I opened the monorepo project and I see three sessions — what are those?
>
> **Domain expert**: The **Main Session** is your project root. The two **Secondary Sessions** are `frontend` and `backend` — they were auto-created from the project config because the Main Session is their parent.
>
> **Dev**: I just created a new worktree for the auth feature. Will it also get frontend and backend sessions?
>
> **Domain expert**: Yes. The config declares those **Secondary Sessions** on every worktree, so the new **Worktree Session** spawned them automatically when it was created.
>
> **Dev**: How is the Daemon involved?
>
> **Domain expert**: The **Client** sent a "create worktree" command to the **Daemon**. The Daemon created the git worktree, spun up the **Worktree Session** and its **Secondary Sessions** on the tmux server, ran the configured hooks, and stored the runtime state in SQLite.
