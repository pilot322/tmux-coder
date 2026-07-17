# 15. Per-Agent One-Shot Discord Notifications

## Status

Accepted

## Context

Desktop Notifications alert the local host whenever a TC Agent changes into
`waiting` or `idle`. Users also need a remote alert when they leave the machine,
but an always-on webhook would be noisy and could expose activity they did not
intend to send. Discord delivery also has stricter security and failure concerns:
the webhook token is a secret, network requests can stall or fail, and retries can
duplicate an alert.

## Decision

The Client lets the user arm one **Discord Notification** for a selected TC Agent.
Pressing `n` in Overview or Agents opens a focused confirmation. The Client sends
the explicit desired state to the Daemon rather than invoking a non-idempotent
toggle. Confirming while already armed disables the pending notification.

Armed state belongs to the TC Agent in the in-memory Agent Registry. It is not
persisted and disappears when the agent exits or the Daemon restarts. Arming an
agent that is already `waiting` or `idle` does not send immediately.

The Daemon consumes an armed notification only on these transitions:

| transition | Discord content |
| ---------- | --------------- |
| `busy` to `waiting` | Agent needs input, with Project and Session |
| `busy` to `idle` | Agent is idle, with Project and Session |

All other status and lifecycle transitions leave it armed. This strict predicate
is intentionally different from Desktop Notifications, which retain their broader
changed `*` to `waiting` or `idle` policy because integrations do not always report
a preceding `busy` event.

Status update and one-shot consumption happen atomically under the Daemon state
write lock. Delivery happens afterward and outside the lock. It is bounded and
best-effort: failure cannot fail Agent Event handling, and a failed attempt remains
consumed without retry.

The webhook is configured as `discord_webhook_notify` in
`~/.tmux-coder/config.yaml`, loaded once at Daemon startup. A missing file, missing
key, or blank value disables Discord without preventing startup. Malformed YAML,
unknown keys, or a non-empty invalid webhook prevents startup. Validation accepts
only HTTPS URLs on official Discord hosts with an `/api/webhooks/{id}/{token}`
path. Errors and logs never include the configured URL or token.

## Consequences

- Multiple Clients observe one authoritative armed state and cannot duplicate a
  notification through polling or retries.
- A transient delivery failure loses that one-shot request by design; this avoids
  hidden retry state and duplicate messages.
- The webhook applies Daemon-wide, while the decision to send remains per agent.
- Editing the config requires a Daemon restart.
- Desktop Notification behavior is unchanged.
