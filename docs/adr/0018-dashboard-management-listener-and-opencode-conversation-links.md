# 18. Dashboard management listener and OpenCode conversation links

## Status

Accepted

## Context

The terminal **Client** uses a loopback Daemon API that also carries trusted
agent-integration and resource operations. A browser **Client** must expose only
the management operations it needs, remain usable at mobile widths, and update
without a persistent transport or a frontend build toolchain.

OpenCode TC Agents attach to one Daemon-owned shared server through an internal
URL. A browser may reach that server through a Tailscale IP, MagicDNS name, or
reverse proxy instead. The **Web Dashboard** must open the conversation currently
displayed by one exact agent pane without deriving OpenCode routes itself or
allowing delayed plugin reports to associate another conversation with that TC
Agent.

## Decision

The Daemon owns two HTTP listeners. The existing internal listener remains on
loopback and retains the complete API used by terminal Clients, wrappers, agent
integrations, and resource helpers. A separate dashboard management listener
defaults to `127.0.0.1:39356` and serves the **Web Dashboard** plus a `/api`
allowlist for Project, Session, and TC Agent listing and management, including
Discord Notification control. Agent events, OpenCode setup and conversation
reports, and resource endpoints remain internal-only. Both listeners share the
same usecases and in-memory state, start as one Daemon lifecycle, and stop
together.

The dashboard's HTML, CSS, and JavaScript are embedded in the Daemon binary. The
browser polls the three management collections once per second, coalesces
overlapping refreshes, suppresses stale responses around mutations, and keeps
the last successful resource data when one feed fails. It uses same-origin
`/api` requests and does not reproduce OpenCode conversation or terminal-pane
features.

Internal and browser-facing URLs are separate configuration concerns. Agent
creation always passes the fixed internal loopback Daemon address to the pane,
not the browser request's Host. Daemon-managed OpenCode TC Agents continue to
attach to the shared server's internal loopback URL, while
`opencode_public_url` is a validated browser root origin used only for generated
web links. `dashboard_public_url` describes the dashboard's browser root origin
without changing its listener. Public origins cannot contain a path prefix.

The OpenCode TUI plugin observes the conversation displayed by its own pane and
reports a nullable **OpenCode Conversation Identity** immediately and when the
route changes. Each report includes the stable tmux pane ID, a reporter epoch,
and an increasing sequence. The Daemon accepts reports only for an OpenCode TC
Agent whose pane matches, compares epoch before sequence, and ignores stale or
duplicate reports. Conversation identity and its ordering metadata live in the
**Agent Registry** independently of **Agent Status**.

When both identity and `opencode_public_url` are available, the Daemon generates
OpenCode's canonical route from that origin and returns it in the Agent response:

```
<origin>/server/<base64url-origin>/session/<escaped-session-id>
```

The browser renders this server-generated URL and never constructs an OpenCode
route. Missing identity or a missing public OpenCode origin leaves the exact
conversation link unavailable.

Version 1 adds no application authentication, login, or authorization layer.
Tailnet ACLs and the Daemon host's firewall are the deployment access boundary.

## Consequences

- Browser exposure does not expose internal integration or resource routes, but
  every peer allowed to reach the management listener can perform its mutations.
- Dashboard and OpenCode reverse proxies require separate root origins; mounting
  either application below a shared path prefix is unsupported.
- Exact links follow conversation switches and tolerate reordered reports, while
  a newly started or currently route-less OpenCode pane clearly has no link.
- Compatible existing OpenCode TC Agents can report identity without recreation;
  non-OpenCode Agent Kinds remain manageable without conversation links.
- Embedded assets and polling keep deployment dependency-free at the cost of
  three periodic management requests per connected dashboard.
- The managed OpenCode server still binds all interfaces. Its internal loopback
  attachment URL does not remove the operator's firewall and Tailnet ACL
  responsibility for the browser-facing port.
