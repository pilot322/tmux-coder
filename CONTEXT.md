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

**Session Topology**:
The parent-child structure among **Sessions** within a **Project**, including **Provenance** for **Worktree Sessions** and **Secondary Session** nesting under a **Main Session** or **Worktree Session** root. It is structural metadata, not a representation of Git branch ancestry or filesystem layout.
_Avoid_: Session tree, hierarchy, lineage

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
The in-memory data structure in the **Daemon** that tracks active **TC Agents** — their IDs, associated **Sessions**, **Projects**, pane identity, requested canonical OpenCode model and variant (when present), and current **Agent Status**. It is an active set, not a durable history. Initial prompts are never retained in it.
_Avoid_: Agent store, agent list

**Agent Status**:
The single canonical, agent-agnostic state of a **TC Agent** in the **Agent Registry**: `starting`, `running`, `busy`, `idle`, `waiting`, or `exited` (terminal — removes the agent). `starting`/`exited` are the only values implying the process is not confirmed alive; every other value implies a live process. The lifecycle values (`starting`, `running`, `exited`) are owned by the wrapper; the activity values (`busy`, `idle`, `waiting`) are reported by the agent itself. An **Agent Kind** that does not report activity rests at `running`. Each kind's integration translates its native signals into this shared vocabulary; the Daemon never learns kind-specific terms.
_Avoid_: Agent state, activity, mode

**Agent Startup Setup**:
A transient, one-shot handshake used while creating an OpenCode **TC Agent** with a requested model, variant, and/or initial prompt. The OpenCode TUI plugin validates catalog readiness and opens the model and variant pickers; the **Daemon** temporarily disables pane input, drives literal picker/prompt input, and verifies model and variant selection against isolated OpenCode state. Agent Startup Setup has a fixed deadline and is separate from **Agent Status**. Prompt contents are discarded on every completion path.
_Avoid_: Initial status, launch status, Agent configuration

**Agent Status Changed At**:
The point in time when a **TC Agent**'s canonical **Agent Status** last changed from one value to another. Repeated **Events** that keep the same Agent Status do not move it.
_Avoid_: Last updated, last event time

**Event**:
A notification sent to the **Daemon** about a **TC Agent**, carrying an event type, agent ID, and optional payload. Two sources emit them: the `tmux-coder agent-wrapper` subcommand reports lifecycle events (`started`, `exited`) derived from the OS process, and an **Agent Kind**'s own integration reports activity events (`busy`, `idle`, `waiting`) translated from that kind's native signals — the OpenCode plugin POSTs them directly, while Claude Code runs hooks that shell out to `tmux-coder agent-event <status>`. Every event type names a target **Agent Status**; the Daemon applies it under a fixed conflict policy (terminal `exited` wins; `started` records process identity but never downgrades a richer status).
_Avoid_: Message, signal, notification

**Desktop Notification**:
An outbound, user-facing OS alert the **Daemon** raises to the host desktop on a changed transition into `waiting` or `idle`. It is the mirror image of an **Event**: an Event flows *in* to the Daemon, a Desktop Notification flows *out* to the user. Platform-specific, always on, and best-effort — delivered only where the host supports it, and never allowed to block or fail the handling of the **Event** that triggered it. Optionally carries an audible cue (on by default, muted by `TMUX_CODER_NOTIFY_SOUND=0`), itself best-effort and Linux-only.
_Avoid_: Event, alert, toast, message

**Discord Notification**:
An outbound, one-shot message that a user arms for one **TC Agent** through the **Client**. The **Daemon** attempts best-effort delivery on that TC Agent's next `busy` to `waiting` or `busy` to `idle` transition, and consumes the armed request only when the agent enters `idle`. Non-qualifying transitions leave it armed; delivery failure does not restore or retry it. Unlike an always-on **Desktop Notification**, a Discord Notification is explicitly armed per TC Agent and exists only in the in-memory **Agent Registry**.
_Avoid_: Event, Desktop Notification, persistent subscription, channel

**Reconciliation**:
The process by which the **Daemon** heals drift between its in-memory record of a **Session** and the runtime resources it owns: the tmux session for every **Session**, and the git worktree for a **Worktree Session**. Triggered on write operations, never on plain reads.
_Avoid_: Sync, refresh, resync

**Worktree Hook**:
A **Project**-declared lifecycle script that customizes setup around a **Worktree Session**. It belongs to tmux-coder's lifecycle, not Git's hook system. On creation it runs asynchronously in that Session's `worktree-setup` tmux window; failure remains visible until acknowledged and then tears down the failed Session and worktree.
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
A TOML file inside a **Project** at `.tmux-coder/.tmux-coder.toml` that declares **Secondary Sessions**, **Menu Actions**, environment variables, and hooks. Checked into version control. Most runtime state (**Sessions**, **Agent Registry**) lives only in the **Daemon**'s memory and is rebuilt on start; durable persistence (eventually SQLite) is limited to **Projects**.
_Avoid_: Settings, project file, manifest

**Menu Action**:
A user-selectable workflow made available by tmux-coder for the current **Session**. It has a unique searchable name, may have a human-facing description, may accept a user-supplied argument or have a direct selection key for immediate execution, and runs attached by default so failures reach the user. A Menu Action may explicitly detach when its script can safely continue after the Client exits. A Project declaration overrides a global Menu Action with the same name.
_Avoid_: Menu item, quick action, command

**Action File** (`~/.tmux-coder/actions.toml`):
A user-owned file that declares global **Menu Actions** available across **Projects**. A Project's **Config File** can supplement or override these actions for its Sessions.
_Avoid_: Daemon Config, global config, script file

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
>
> **Dev**: If I choose a Menu Action from this Worktree Session, does it run against the whole Project?
>
> **Domain expert**: No. A **Menu Action** always targets the current Session, so this one targets the worktree checkout.
>
> **Dev**: Why does this Project's review action differ from the one I use elsewhere?
>
> **Domain expert**: Its **Config File** declares a **Menu Action** with the same name as the global one in your **Action File**, so the Project declaration overrides it.
