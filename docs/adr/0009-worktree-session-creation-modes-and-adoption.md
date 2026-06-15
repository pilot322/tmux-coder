# Worktree Session creation modes and worktree adoption

Worktree Session creation is driven by two orthogonal booleans on the create
request — `createWorktree` (materialize the git worktree on disk and run
**Worktree Hooks**) and `createBranch` (create a new branch with `git worktree
add -b` vs. check out an existing one) — replacing the single overloaded
`create` flag. This yields three valid modes: **fresh** (`t,t`),
**existing-branch / new-worktree** (`t,f`), and **adoption** (`f,f`) which wraps
a Session around a worktree already on disk without re-creating it or running
hooks. The fourth combination (`f,t` — "branch without a worktree") is rejected
as a validation error.

When a fresh create conflicts with existing state, the **Daemon** returns `409`
with a machine-readable `code` (`session_exists`, `worktree_exists`,
`branch_exists`, `path_blocked`) so the **Client** can prompt the user and
re-issue with the appropriate mode, rather than the Client having to parse error
prose or pre-probe Git.

## Considered options

- **A `branchExistsOk` flag** for the existing-branch case — folded into
  `createBranch:false`, which already means exactly that.
- **A separate `runWorktreeHooks` flag** — rejected; hooks key off
  `createWorktree` (we materialized the worktree ⇒ we set it up). A flat default
  of `true` would have made adoption re-run setup hooks by surprise. Can be added
  later as an optional `*bool` (defaulting to `createWorktree`) if a
  cross-combination is ever needed.
- **Keeping `create:false` as "add a worktree for an existing branch"** —
  reassigned to adoption; the old meaning is now `createWorktree:true,
  createBranch:false`. Safe because only tests exercised `create:false`.
- **An `/inspect` endpoint** for the Client to pre-check state — rejected in
  favour of the error `code` on the existing create call (one fewer endpoint, one
  fewer race).

## Consequences

- Hooks run iff `createWorktree`. **Adoption never runs hooks**, and its rollback
  must **not** remove the worktree or delete the branch (tmux-coder did not create
  them).
- Adoption validates against `git worktree list --porcelain` (the path is a
  worktree of *this* repo, checked out on the named branch) via a new
  `ListWorktrees` gateway method — authoritative in one call, unlike
  `IsWorktreeRoot` + `CurrentBranch`.
- Worktree **Reconciliation** does **not** auto-adopt orphan worktrees: adoption
  is creation-time only, never inferred from drift. (Reconcile does prune Sessions
  whose worktree vanished and, per ADR-0010, reparents the pruned worktree's
  surviving Worktree children — but it never wraps an unmanaged on-disk worktree
  in a new Session.)
