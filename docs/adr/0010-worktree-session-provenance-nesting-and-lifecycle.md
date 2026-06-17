# ADR-0010: Worktree Session Provenance, Nesting, and Lifecycle

## Status

Accepted. Extends ADR-0005 (worktree paths/lifecycle) and ADR-0006 (session hierarchy/depth). Orthogonal to ADR-0009 (creation modes and adoption): modes decide *how* a worktree comes to be (`createWorktree`/`createBranch`), provenance decides *what it is parented to*; the two axes compose on the same create request.

## Context

Worktree Sessions were parentless and always branched off the Project's main checkout. Users want to branch a worktree off *another* worktree, and off an arbitrary base branch that no Session represents — and they want the Session tree to reflect where each worktree came from (e.g. `feat1` under main, `feat1-backend` under `feat1`, while a worktree cut from a different base sits at the Project level).

## Decision

A Worktree Session records its **Provenance** — the Session it was created *from* — in its `parent` field. Provenance is fixed at creation; it is structural metadata, not a live Git merge-base, and it does not move when Git later moves branches.

Two creation gestures, both of which start as a fresh worktree with a new branch (`createWorktree` + `createBranch`, per ADR-0009):

- **`'w'` — branch from a Session.** The selected entity resolves to a source Session: the Project's Main Session (Projects view), the selected session (Sessions view), or the selected session / the selected agent's owning session (Overview view). The new worktree branches off that source's **committed branch tip** (Main → `git.CurrentBranch`; Worktree → stored `branch`). A Secondary Session is **not** a valid source (it shares its root's worktree) and is **hard-rejected**. A detached-HEAD Main Session has no base branch and is **rejected**. Dirty/uncommitted work in the source is not inherited (standard `git worktree add`). **Only a Worktree Session source becomes the new Session's parent; a Main Session source produces a parentless / Project-level Worktree Session**, so worktrees created from main do not "belong" to main.
- **`'W'` — branch from a bare base ref.** Prompts for a new branch name, then a base ref, validated permissively with `git.ResolveCommit` against the Project checkout (any commit-ish: local branch, remote-tracking branch, tag, or SHA — reject only if nothing resolves). The result is **parentless / Project-level**, since no Session represents the base.

Both gestures are offered in the Projects, Sessions, and Overview views; not the Agents tab.

**Deletion is reparent, fixed.** When a Worktree Session is removed — by explicit delete *or* by reconcile-prune when its directory vanishes out-of-band — its **Worktree** children are **reparented to its parent** (or made parentless if it had no parent), since they are independent checkouts unaffected by the removal, while its **Secondary** children **cascade** (their subdirs physically vanish with the worktree, as today). Worktree Sessions get no user-selectable `onDelete` policy; reparent is always the behavior.

**Depth budget resets at each Worktree/Main root.** ADR-0006's depth-5 cap on Secondary nesting is measured from the Secondary's nearest Worktree/Main root (that root = depth 1), so a deeply-nested worktree does not consume the Secondary budget. Worktree nesting itself is uncapped.

## Consequences

- Naming and paths are unchanged from ADR-0005: every worktree is a flat sibling directory `<projectbase>.<branchslug>` with a flat tmux name. Provenance lives only in `parent`; it is never encoded into the directory or name (branch names are repo-unique, so flat names never collide on branch, and physical nesting would reintroduce the worktree-inside-worktree footgun ADR-0005 avoided).
- `sessionDepth` must stop climbing at the first Worktree/Main ancestor instead of walking to the root, or the new worktree parent chain silently shrinks the Secondary depth budget.
- The TUI renders each Project as Main + its full subtree (nested worktrees and secondaries) first, then parentless `'W'` worktrees at Project level. Tree building walks `parent` pointers rather than the old "main then flat worktrees" layout.
- The worktree create path/API gains a parent (source session) input; `'W'` passes none.
- Provenance is recorded for *every* creation mode, not just a fresh branch-from-source. When a `'w'` from a Worktree Session or a `'W'` create hits a conflict and the Client re-issues in the existing-branch or adopt mode (ADR-0009), the original source is carried through unchanged, so the resulting Worktree Session still nests under it (or stays parentless for Main/bare-base sources). The recorded parent reflects the source type, independent of how the worktree was ultimately materialized.
- Provenance is in-memory metadata only — consistent with all current Session state, which does not survive a Daemon restart (the memory repos persist nothing). No new persistence work; when durable storage lands, `parent` is one more column.
