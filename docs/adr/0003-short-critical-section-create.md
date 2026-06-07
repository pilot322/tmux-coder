# 3. Short critical section for project creation

## Status

Accepted

## Context

The daemon guards all in-memory state with a single `sync.RWMutex` on `DaemonState`: reads (`GET`) take a shared lock, writes (`POST`/`DELETE`) take an exclusive one. Creating a project is a write that mixes fast in-memory mutations (reserve a unique main-session name, insert the Project and Session records) with a slow, fallible side effect (`CreateSession`, which shells out to tmux).

With one global mutex, an exclusive writer blocks readers too. So holding the write lock across the tmux exec would let a slow or hung `tmux new-session` stall every `GET` for its duration — directly contradicting the requirement that reads never be throttled.

Two options were considered:

1. **Lock across everything** — hold the write lock for both the in-memory mutations and the tmux exec. Fully atomic and trivially simple, but a slow tmux call freezes all reads and writes.
2. **Short critical section** — take the write lock only to reserve the name and insert the records, release it, exec tmux *unlocked*, then re-acquire the lock to roll back the records if `CreateSession` fails.

## Decision

Option 2. The lock is held only for in-memory work; the tmux subprocess never runs under the lock. On tmux failure the create re-acquires the lock and removes the Project and Session it inserted, returning 502.

## Consequences

- Reads are never blocked by a tmux exec, satisfying the read-latency goal.
- There is a brief window where a concurrent `GET` can observe a Project whose tmux session is still being created (or was just rolled back). This is harmless: the daemon is already reconciliation-tolerant of "record exists, tmux session missing" drift (see [Reconciliation in CONTEXT.md]) — the window is just a transient instance of that same state.
- The flow looks unusual ("reserve → unlock → exec → relock to undo") and a future reader may mistake it for a bug and "fix" it by holding the lock across the whole operation. That would silently reintroduce the read-latency stall. This ADR exists to prevent that.
- Create is not a true transaction; atomicity is enforced by explicit rollback (a `defer` that undoes the inserts unless a success flag is set), not by the lock.
