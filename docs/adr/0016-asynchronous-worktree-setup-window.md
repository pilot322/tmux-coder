# 16. Asynchronous Worktree Hook in a dedicated tmux window

## Status

Accepted

## Context

Worktree Hooks can spend minutes installing dependencies or provisioning local
infrastructure. Running the hook before creating the Worktree Session made that
Session unavailable for attachment or TC Agent creation during setup. Captured
hook output also forced users to open a separate log when setup failed.

## Decision

After materializing the git worktree, the Daemon creates and records the
Worktree Session, then starts its Worktree Hook in a dedicated tmux window named
`worktree-setup`. Session creation returns once that window has started; the
Daemon supervises the hook independently of the originating Client request.

On success, the setup window exits automatically, provisional Resource Leases
are promoted to the Session, and configured Secondary Sessions are materialized.
On failure or timeout, the window prints an acknowledgement prompt and remains
open. The Daemon sends a critical Desktop Notification, waits for Enter in that
window, and then removes the Worktree Session, its TC Agents and dependent
Secondary Sessions, its Resource Leases, its git worktree, and any branch created
for it.

## Consequences

- A Worktree Session is usable while its setup is still running.
- A TC Agent created during setup is intentionally provisional and is removed if
  setup later fails.
- Configured Secondary Sessions appear only after setup succeeds because their
  directories may be created by the hook.
- Hook supervision must outlive the HTTP request context.
- Failures are inspectable in place, while the existing hook log remains
  available for later diagnosis.
