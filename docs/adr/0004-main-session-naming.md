# 4. Main session naming: `<basename>.main` with collision suffix

## Status

Accepted

## Context

Every Project has exactly one Main Session, whose `sessionName` is also the name of its tmux session on the **dedicated** tmux server (`-L tmux-coder`). Because that server is shared across all Projects, every Main Session name must be globally unique on it, and raw `fullPath` values are not suitable tmux session names.

The obvious readable choice, `<basename>.main`, is not unique: two Projects at different paths but the same basename (`/work/api` and `/personal/api`) both derive `api.main`, and the second `tmux new-session -s api.main` collides.

Three options were considered:

1. **`<basename>-<id>`** (e.g. `api-7`) — guaranteed unique via the monotonic project id, but less readable on `tmux attach`.
2. **`<basename>.main`, reject collisions** — return 409 when the derived name is already taken. Readable, but some legitimate Projects can't be created.
3. **`<basename>.main` with a numeric fallback suffix** — `api.main`, then `api.main-2`, `api.main-3`, ... on collision. Always succeeds and stays readable.

## Decision

Option 3. The create flow derives `<basename>.main`, checks the session repo for an existing name, and bumps a numeric suffix until it finds a free one. The chosen name is stored on the Session and the API response composes `mainSessionName` from a Main Session lookup.

## Consequences

- Names are human-readable when attaching, and creation never fails on a name collision.
- The name is no longer predictable from `fullPath` alone — given a path you cannot assume its main session is `<basename>.main`; you must look it up (the suffix depends on creation order).
- The free-name search and the name assignment must happen inside the same write-locked critical section, or two concurrent creates could both pick `api.main` (see ADR-0003).
- A future reader might "simplify" this to `<basename>-<id>` for predictability; that is a real alternative but trades away readability, which was the point of choosing this scheme.
