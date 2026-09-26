---
name: setup-tmxc-worktrees
description: Set up isolated tmux-coder worktrees in a consumer repository, including creation and teardown hooks. Use when asked to make a repository tmux-coder-worktree friendly, configure worktree hooks, or fix resource leaks or collisions between worktree sessions.
---

# Set up tmux-coder worktrees

Adapt the **repository being opened by tmux-coder**, not tmux-coder itself. Read [the Worktree Hook integration guide](https://github.com/pilot322/tmux-coder/blob/main/docs/worktree-hook-integration.md) for the creation and deletion contracts, config syntax, port leasing, and lifecycle before making changes. If working in a different repository, locate this guide in the tmux-coder checkout or ask for it.

## 1. Inventory the runnable stack

Trace the actual development path: app and worker entry points, service definitions, `.env` loading, scripts, migrations, seeds, tests, and local tooling. Record every shared database/schema, listening port, cache namespace, queue/topic, upload directory or bucket, search index, container project/volume, and host-level socket/lock/PID path that the stack uses. For each resource, identify **every** consumer, the variable or config key controlling it, and whether it outlives the worktree. Identify credentials and other existing local values that a generated file must preserve. Finish when every shared mutable resource has an owner, all its consumers are known, and any resource that cannot be isolated is explicitly flagged to the user.

## 2. Make the stack parameterizable

Give each resource a per-worktree name, path, or port from one local configuration seam (usually a git-ignored `.env`), and make every consumer use that same value, including migrations, test helpers, CLI tasks, and container configuration. Keep a checked-in `.env.example` documenting the required keys and defaults; preserve existing config and secrets instead of overwriting them. Use identifiers safe for each service, stable for a session and distinct across projects and worktrees; account for sanitized names that could collide. Put worktree-local files under the worktree root. Finish when no active tool silently falls back to a shared mutable resource while running the worktree stack.

## 3. Wire the creation hook

Add or extend `.tmux-coder/.tmux-coder.toml` with `[worktree].on-create-script` pointing to a checked-in executable script inside the project (for example `.tmux-coder/setup-worktree.sh`). Preserve other config sections. Set `on-create-timeout` based on the real cold setup cost. The hook runs with its working directory at the new worktree root and receives the `TMUX_CODER_*` values documented in the guide. It should:

1. Resolve the required per-worktree identities and obtain each listening port with `tmux-coder acquire-port KEY --start N --end M` in the hook environment; propagate `TMUX_CODER_HOOK_TOKEN` unchanged.
2. Create the local configuration from the project's template without losing necessary credentials or machine-specific values. Ensure every tool actually loads the generated values (a file existing on disk is not enough).
3. Install dependencies before invoking tools that need them; provision only the isolated resources, then run migrations/seeds or other project-specific setup as needed.
4. Exit non-zero when setup fails. Make repeated provisioning safe without swallowing genuine failures. Clean up partial external state on failure or provide a reaper: failed-creation rollback does not run the destroy hook. Never log secrets.

Finish when the declared hook is executable in Git, works from a freshly checked-out worktree, and has an adequate timeout.

## 4. Configure teardown

When setup provisions external resources, add `[worktree].on-destroy-script` and an executable, checked-in teardown script; set `on-destroy-timeout` to cover the actual work. Use the same per-worktree identities as setup (for example, from the generated `.env`), **validate each deletion target** against the worktree's identity, and remove only its external resources. Make partial teardown retryable and propagate real failures; do not mask them or print secrets. The destroy hook runs synchronously in the worktree root before Git removes it, including for adopted Worktree Sessions; it has `TMUX_CODER_SESSION_ID` but no `TMUX_CODER_HOOK_TOKEN` or provisional port leases. Ordinary deletion stops on hook failure; Force still runs the hook but proceeds on execution failure. The current project's config is read at deletion, so keep the configured script available and executable. External removal and failed-creation rollback bypass this hook: plan a manual or periodic reaper. If setup creates no external resources, explicitly record why no destroy hook is needed. Finish when every external resource provisioned by setup has a matching safe teardown or a documented exception.

## 5. Verify the lifecycle

Run the repository's relevant tests and static checks. Check both hooks' syntax and executable bits, config validity, `.env` ignore rule, and absence of committed secrets. Where tmux-coder and the required services are available, create **two disposable worktree sessions through tmux-coder**, run their normal stacks concurrently, and confirm distinct ports and resource names plus independent migrations/data. Delete one normally; confirm its external resources are gone and the other's remain usable. Use only known disposable resources for teardown. If a live check is unavailable, report exactly what remains unverified rather than claiming isolation or cleanup. Finish by summarizing the resource mapping, changed files, tests, and any manual setup or cleanup required.
