# tmux-coder

A CLI tool that wraps a dedicated tmux server to manage projects with multiple worktrees, providing lifecycle management, session orchestration, and agent coordination.

## Language

**Project**:
A base directory managed by tmux-coder, with all **Sessions** attributed to it. A **Project** has a mutable title used only as its display label; it may contain a `.tmux-coder/.tmux-coder.toml` **Config File** for declarative behavior.
_Avoid_: Workspace, repository

**Session**:
A 1:1 wrapper around a tmux session on the dedicated tmux server. tmux-coder stores both a user-facing `sessionName` and a `tmuxSessionName` target name, adds metadata (type, parent, project association), and manages lifecycle. Comes in three types: **Main Session**, **Worktree Session**, and **Secondary Session**.
_Avoid_: Window, terminal, shell

**Main Session**:
The single **Session** per **Project** that always exists — the home base for interacting with the project as a whole.
_Avoid_: Root session, default session

**Worktree Session**:
A **Session** tied 1:1 to a git worktree for the same **Project**. Created with its git worktree; deleting the Worktree Session removes that worktree, while deleting the Project only removes tmux-coder's ownership of the Session. A Worktree Session records its **Provenance**: the **Session** it was created from becomes its `parent` (a **Main Session** or another **Worktree Session**), or it is parentless when created directly from a bare base branch. Provenance is fixed at creation and is independent of where Git later moves branches.
_Avoid_: Branch session

**Provenance**:
The creation-time origin of a **Worktree Session**, recorded as its `parent`. When a Worktree Session is created *from* another **Session** (its source), that source becomes the parent and the new Session renders nested beneath it. When created from a bare base branch that no Session represents, the Worktree Session is parentless and renders at the **Project** level. Provenance is a frozen structural fact, not a live Git merge-base.
_Avoid_: Lineage, ancestry, base

**Secondary Session**:
A child **Session** that stems from a **Main Session**, **Worktree Session**, or another **Secondary Session**. Represents a sub-context within the same worktree (e.g. `packages/frontend`) and may be declared in a **Config File**.
_Avoid_: Sub-session, nested session

**Daemon**:
The long-running server process (`tmux-coderd`) that owns the dedicated tmux server instance, holds runtime state, and maintains the **Agent Registry** in memory.
_Avoid_: Server (when referring to the daemon specifically, to avoid confusion with the tmux server)

**Daemon Config**:
Daemon-wide settings that govern tmux-coder behavior across all **Projects**. Distinct from a per-Project **Config File**.
_Avoid_: Settings, global config

**Client**:
The `tmux-coder` CLI invocation that connects to the **Daemon** to issue commands and render the TUI.
_Avoid_: CLI (as a noun for a running instance)

**TC Agent**:
A pane-backed coding agent process (Claude Code, Codex, OpenCode, etc.) launched and managed by tmux-coder. Each **TC Agent** has an ID, belongs to exactly one **Session** and **Project**, and may have a non-unique display name used as a human label.
_Avoid_: Tool, assistant, process

**Agent Kind**:
The executable family for a **TC Agent** (for example `opencode`, `claude`, or `codex`). The kind identifies what agent program tmux-coder launches; it is distinct from the agent's display name.
_Avoid_: Agent type, command, display name

**Agent Display Name**:
A human-facing label for a **TC Agent**, used for presentation and tmux window labels. It is not the agent's identity; the **TC Agent** ID remains authoritative.
_Avoid_: Agent ID, Agent Kind

**Agent Registry**:
The in-memory data structure in the **Daemon** that tracks active **TC Agents** — their IDs, associated **Sessions**, **Projects**, pane identity, and current **Agent Status**. It is an active set, not a durable history.
_Avoid_: Agent store, agent list

**Agent Status**:
The single canonical, agent-agnostic state of a **TC Agent** in the **Agent Registry**: `starting`, `running`, `busy`, `idle`, `waiting`, or `exited` (terminal — removes the agent). `starting`/`exited` are the only values implying the process is not confirmed alive; every other value implies a live process. The lifecycle values (`starting`, `running`, `exited`) are owned by the wrapper; the activity values (`busy`, `idle`, `waiting`) are reported by the agent itself. An **Agent Kind** that does not report activity rests at `running`. Each kind's integration translates its native signals into this shared vocabulary; the Daemon never learns kind-specific terms.
_Avoid_: Agent state, activity, mode

**Event**:
A notification sent to the **Daemon** about a **TC Agent**, carrying an event type, agent ID, and optional payload. Two sources emit them: the `tmux-coder agent-wrapper` subcommand reports lifecycle events (`started`, `exited`) derived from the OS process, and an **Agent Kind**'s own integration (e.g. the OpenCode plugin) reports activity events (`busy`, `idle`, `waiting`) translated from that kind's native signals. Every event type names a target **Agent Status**; the Daemon applies it under a fixed conflict policy (terminal `exited` wins; `started` records process identity but never downgrades a richer status).
_Avoid_: Message, signal, notification

**Reconciliation**:
The process by which the **Daemon** heals drift between its in-memory record of a **Session** and the runtime resources it owns: the tmux session for every **Session**, and the git worktree for a **Worktree Session**. Triggered on write operations, never on plain reads.
_Avoid_: Sync, refresh, resync

**Worktree Hook**:
A **Project**-declared lifecycle script that customizes setup around a **Worktree Session**. It belongs to tmux-coder's lifecycle, not Git's hook system.
_Avoid_: Git hook, shell command

**Worktree Adoption**:
Taking a git worktree that already exists on disk under management as a **Worktree Session**, without re-materializing it — tmux-coder neither creates the worktree nor runs its **Worktree Hooks**, only wrapping the existing checkout in a Session. Contrast with creating a Worktree Session, which materializes a new worktree and runs its hooks.
_Avoid_: Import, attach, link, reuse

**Resource Lease**:
A **Daemon**-owned reservation of a local resource value for a **Project** and a **Session** or in-progress **Worktree Session** creation.
_Avoid_: Lock, allocation

**Port Lease**:
A **Resource Lease** for a TCP port value used by a **Project**'s runnable local environment.
_Avoid_: Port setting, env var

**Config File** (`.tmux-coder/.tmux-coder.toml`):
A TOML file inside a **Project** at `.tmux-coder/.tmux-coder.toml` that declares **Secondary Sessions**, environment variables, and hooks. Checked into version control. Most runtime state (**Sessions**, **Agent Registry**) lives only in the **Daemon**'s memory and is rebuilt on start; durable persistence (eventually SQLite) is limited to **Projects**.
_Avoid_: Settings, project file, manifest

## Example dialogue

> **Dev**: I opened the monorepo project and I see three sessions — what are those?
>
> **Domain expert**: The **Main Session** is your project root. The two **Secondary Sessions** are `frontend` and `backend` — they were auto-created from the project config because the Main Session is their parent.
>
> **Dev**: I just created a new worktree for the auth feature. Will it also get frontend and backend sessions?
>
> **Domain expert**: Those are **Secondary Sessions** declared by the project config. The new **Worktree Session** is the parent context they belong under when that config is applied to a worktree.
>
> **Dev**: How is the Daemon involved?
>
> **Domain expert**: The **Client** sent a "create worktree" command to the **Daemon**. The Daemon created the git worktree, spun up the **Worktree Session** on the tmux server, and recorded the Session as runtime state.
