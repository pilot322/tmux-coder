# 1. Daemon with auto-launch and optional systemd service

## Status

Accepted

## Context

tmux-coder follows a server-client architecture where a daemon manages the tmux server, runtime state, and agent registry. We needed to decide how the daemon starts and stays running.

Three options were considered:

1. **Auto-launch only** — the CLI starts the daemon on first invocation if it's not already running. Simple, portable, zero setup. But the daemon dies when no sessions are active or on logout, and it's not reachable from remote machines.

2. **Systemd service only** — installed as a user service, always running, survives reboots, reachable over SSH. But requires systemd (Linux-only), adds setup ceremony, and doesn't work on macOS.

3. **Auto-launch by default, optional systemd install** — combines both. Casual use requires zero setup. A `tmux-coder service install` command generates and enables a systemd user unit for always-on/remote-access scenarios.

## Decision

Option 3: auto-launch by default, with an optional `tmux-coder service install` command for systemd.

## Consequences

- First-run experience has zero friction — no daemon management required.
- Users who want remote access (e.g. SSH from a laptop to a personal server) run a one-time setup command.
- The daemon code itself is identical in both modes — only the process supervisor differs.
- macOS and non-systemd Linux users are not locked out (they get auto-launch; launchd support could be added later).
- We need to handle the case where both modes could conflict (auto-launch detecting an existing systemd-managed instance).
