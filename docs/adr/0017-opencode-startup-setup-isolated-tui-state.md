# 17. OpenCode startup setup uses isolated TUI state

## Status

Accepted

## Context

OpenCode's attached TUI does not accept its standalone `--model` and `--prompt`
options, and its public TUI plugin API does not expose the currently selected
model. Model selection and initial prompt submission therefore require TUI
automation without replacing the Daemon-owned shared OpenCode server.

OpenCode persists model recents, favorites, and variants in TUI-local state.
Using the user's global state for verification would allow concurrent TUIs to
produce false positives and would make startup automation mutate durable user
preferences.

## Decision

Model-, variant-, and prompt-enabled OpenCode creation uses a dedicated Agent
Startup Setup handshake, separate from Agent Status events. The TUI plugin
validates an exact canonical provider/model catalog entry, optional model
variant, and a unique picker display name. The Daemon disables input before
allowing the plugin to open the picker, enters literal values through tmux
buffers, verifies the selected model and variant, submits an optional prompt
once, and restores input on every completion path.

The wrapper continues to launch `opencode attach` against the Daemon-owned
shared server. For model selection only, it copies the user's OpenCode state to
a private temporary `XDG_STATE_HOME`, removes the requested model from recents
and its saved variant, and registers that exact local state path with the
Daemon. The Daemon verifies selection from that path and explicitly chooses the
requested variant when present, or the Default variant otherwise. The wrapper
removes the state copy when the TC Agent exits; no changes are merged back.

Initial prompts exist only in the creation request and transient setup
coordinator. They are never put in process arguments, shell commands, logs, the
Agent Registry, HTTP Agent responses, or listings, and are cleared after
submission, failure, cancellation, or timeout.

## Consequences

- Startup automation has one 30-second attempt and fails closed.
- Model-selected attached TUIs preserve seeded favorites and plugin state while
  remaining isolated from concurrent global model changes.
- Pane input is unavailable only during requested automation.
- OpenCode version 1.18.14 is the tested baseline; other versions warn and use
  the same validation and failure rules.
