# ADR-0007: Config File schema for declarative Secondary Sessions

## Status

Accepted. Amends ADR-0006 (Secondary Session naming).

## Context

A Project's Config File (`.tmux-coder/.tmux-coder.toml`) should declare Secondary
Sessions that are materialized automatically when a root Session is created. Until
now the file was read by a hand-rolled line scanner (`loadWorktreeHookConfig`) that
only understood a flat `[worktree]` section and cannot parse arrays-of-tables. The
declarative-secondaries feature forces both a real parser and a schema.

## Decision

**Parser.** Adopt `github.com/pelletier/go-toml/v2` with `DisallowUnknownFields`,
reversing the prior "no TOML dependency" stance. The whole file decodes through one
strict decoder; the hand-rolled scanner and `stripTomlComment` are deleted, and the
existing `[worktree]` keys move onto the same decoder. Unknown keys are a hard error
so a typo in a checked-in config surfaces immediately instead of being silently
ignored.

**Schema.** Kebab-case throughout (matching TOML's most common convention; the
`[worktree]` keys are renamed to `on-create-script` / `on-create-timeout` to match).
Secondary Sessions are an array-of-tables:

```toml
[[secondary-sessions]]
subdir = "backend"        # relative to the session root; ADR-0006 normalization applies
name = "backend"          # optional — overrides the derived preferredName
on-delete = "inherit"     # optional — cascade | inherit, default cascade
id = "backend"            # optional — handle another entry references via `parent`
parent = "backend"        # optional — the `id` of the entry this nests under
```

- `subdir` is required **unless** `name` is set (mirrors ADR-0006: an empty directory
  is valid only with a `preferredName`).
- `on-delete` defaults to `cascade` (the existing `createSecondary` default; least
  surprise — deleting a node deletes its subtree). `inherit` is opt-in.

**Template semantics.** The declaration set is applied once to the **Main Session**
(at Project creation) and once to **each Worktree Session** (at worktree creation).
`subdir` resolves against the Project path for Main-rooted trees and the worktree path
for Worktree-rooted trees. A declaration therefore means "this sub-context exists in
every checkout of the Project."

**References and validation.** `id`/`parent` are config-local handles only; `parent`
resolves against an explicit `id` (never a `name` or `subdir` basename, which are
unstable under collision suffixing). Entries without `parent` attach to the root being
provisioned. Validation runs statically, up front, before any tmux work: unique `id`s,
every `parent` matches a declared `id`, no self-parent, no cycles, order-independent
(forward references allowed), `subdir`-or-`name` present, and total ancestry depth ≤ 5
counting the root (ADR-0006). `on-delete` values are checked explicitly (the decoder
only rejects unknown *keys*, not bad *values*).

**Atomicity.** Materialization is all-or-nothing. Static validation failure, a missing
`subdir`, or any tmux failure rolls back every secondary created so far, then the root
Session, worktree, and branch — reusing the existing create rollback. A malformed
Config File therefore fails the create operation loudly rather than producing a partial
tree. Materialization is the final in-flow step of `CreateProject` (the `Created` branch)
and `CreateSession` (the worktree branch), so it sits inside their rollback scope; it is
implemented as a `usecase`-package helper (the shape `reconcileWorktreeSessions` already
uses), not a new application-service layer.

**Lifecycle.** Secondary *records* are created exactly once, at the birth of their root
Session. Reconciliation never creates records; it only heals missing tmux sessions for
records that exist — and must recompute a secondary's working directory from its stored
`relativeWorkingDirectory` + root rather than the Project root.

## Consequences

- First non-TUI runtime dependency enters the module.
- A typo or unknown key in `.tmux-coder.toml` blocks worktree/project creation until fixed.
- `CreateProject.reconcile` now heals a Secondary Session's tmux at its
  `relativeWorkingDirectory` joined to the root it is anchored to, resolved through the
  shared `secondaryParentRoot` helper (`create_session.go`). Previously it recreated every
  non-worktree session at the Project root, which was wrong for a Worktree-rooted
  secondary's working directory.
- Secondary naming changes per the ADR-0006 amendment below.
