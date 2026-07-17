# 15. Daemon-owned shared OpenCode server

## Status

Accepted

## Context

Launching each OpenCode TC Agent as a standalone TUI also launches an embedded
server. The duplicated servers materially increase memory and CPU use as agent
count grows. A shared server must outlive any one agent pane, start without a
pane-specific agent identity, and work for both daemon-owned and borrowed panes.

## Decision

The Daemon lazily starts one `opencode serve` process when an OpenCode wrapper
requests `POST /resources/opencode-server`. Concurrent requests are serialized,
and the endpoint returns one loopback URL after the process accepts connections.
The server receives a parent-death signal if the Daemon exits.

Every OpenCode wrapper launches `opencode attach <url> --dir <working-directory>`.
`TMUX_CODER_OPENCODE_SERVER_URL` overrides the managed URL for an externally
owned server. Other Agent Kinds retain their existing launch command.

OpenCode activity reporting runs in a TUI plugin entrypoint. The headless server
has no `TMUX_CODER_AGENT_ID`; each attached TUI retains its pane-specific ID and
reports only events for the session currently displayed in that pane.

## Consequences

- All OpenCode TC Agents in one Daemon instance share server caches and runtime.
- The first OpenCode agent pays server startup latency; later agents reuse it.
- Server lifetime follows Daemon lifetime rather than any individual TC Agent.
- OpenCode's `tui.json` must reference the bundled TUI plugin directory instead
  of loading the old server-side plugin from `opencode.json`.
