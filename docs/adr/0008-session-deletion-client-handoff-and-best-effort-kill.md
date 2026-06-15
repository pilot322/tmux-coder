# 8. Session deletion: client hand-off and best-effort kill

## Status

Accepted

## Context

Deleting a Worktree or Secondary Session shells out to `tmux kill-session`. Two problems surfaced when a user deletes the very session their terminal is attached to.

First, the client gets ejected. tmux's default `detach-on-destroy on` detaches a client whose session is destroyed, and if that session was the last one on the server the server exits and the client process dies with it. The user is dropped to a bare shell with no hand-off, even though the daemon (which runs the deletion, outside tmux) is unaffected.

Second, and worse, deletion was left non-atomic. The old order was: remove the git worktree, then `kill-session`, then delete the in-memory records. Killing the attached/last session makes `kill-session` exit non-zero — the connection or server is torn down underneath it — so the usecase returned `ErrGateway` *before* deleting the records. The worktree was already gone from disk, but the Session record survived. `GetSessions` returned raw records with no validation, so the dead session stayed listed yet could not be attached (the create/heal path returns `ErrSessionNotFound` → 404). State persistence is pure in-memory, so a daemon restart "fixed" it — the signature of an uncommitted partial delete.

The daemon is already reconciliation-tolerant of "record exists, tmux session missing" drift (see ADR-0003 and Reconciliation in CONTEXT.md), but `reconcileWorktreeSessions` only runs at the *start* of create/delete, never on a plain list, so a freshly orphaned record was visible until the next mutation.

## Decision

Three changes, kept at the layer that owns each concern.

1. **Hand the client off before killing.** A new `SessionGateway.SwitchClients(from, to)` moves every client attached to `from` over to `to`. Deletion calls it to switch clients to the project's Main Session (which cannot be deleted, so it always exists) before the kill. The decision of *where* to send the client lives in the usecase, which knows the session graph; the mechanism (`list-clients` → `switch-client`) lives in the gateway.

2. **Kill is best-effort once deletion is committed.** After the worktree is removed (the only legitimately abortable step — a dirty worktree still returns `ErrConflict` and aborts cleanly), the kill no longer gates record deletion. The records are always removed, because a listed-but-unattachable session is worse than a stray shell-only tmux session. The real `Kill` is also made idempotent: an already-gone session returns nil rather than a non-zero-exit error.

3. **Reads filter unattachable sessions.** `GetSessions` omits any Worktree Session whose worktree directory no longer exists, so a crash-orphaned record is never shown as attachable. This is a filter on the view, not Reconciliation — it heals nothing and removes no record, keeping Reconciliation a write-only operation (ADR-0003). The stale record is still pruned by `reconcileWorktreeSessions` on the next create/delete.

## Consequences

- A user who deletes the session they are sitting in is reattached to the project's Main Session instead of being detached or having their client killed.
- Deletion is consistent in the common case and recoverable in the failure case: the record never outlives a successful worktree removal, and any record that does slip through (hard crash mid-delete) is hidden from listing and reaped by Reconciliation on the next mutation.
- If a kill genuinely fails *after* the worktree is removed — not merely "already gone" — the record is still deleted, which can leave a shell-only tmux session with no backing worktree. `reconcileWorktreeSessions` keys off worktree records, so it will not reap this stray. We accept this rare, harmless leak as the cost of never stranding a listed-but-404 session.
- `SwitchClients` and the best-effort kill are deliberately silent on tmux errors. A future reader may see the swallowed error and want to surface it; doing so would reintroduce the partial-delete bug this ADR exists to prevent.
