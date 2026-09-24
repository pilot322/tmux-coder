# 19. OpenCode v2 authenticated pane setup

## Status

Accepted

## Context

OpenCode v2 changes the CLI plugin, server authentication, session model, and
attached TUI contracts. Keeping v1 and v2 paths would require two separate
startup protocols and weaken verification of the selected conversation.

## Decision

Support v2 only. The Daemon owns one password-protected foreground server and
verifies its authenticated v2 `/api/info` before handing URL and credential to
pane wrappers over the loopback API. An external server must pass the same
check. A CLI-only plugin in each pane reports only its displayed conversation
and related activity. For requested setup, the plugin validates the exact
location-scoped model and variant; after the Daemon disables pane input, it
creates, displays, and verifies the target session model through v2's session
API. Only then does the Daemon paste an optional prompt once and restore input.
Browser deep links retain the exact conversation route but require prior v2
`/connect` pairing; credentials never appear in dashboard links.

## Consequences

- v1 OpenCode binaries and `tui.json` plugin entries are unsupported.
- Explicit startup setup no longer copies or edits the user's model state.
- Browser access requires a known password on the Daemon and OpenCode pairing.
