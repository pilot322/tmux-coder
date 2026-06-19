# ADR-0014: Session Topology Planning and Reconcile Healing Semantics

## Status

Accepted. Refines ADR-0006 (Secondary Session hierarchy) and ADR-0010 (Worktree Session provenance, nesting, and lifecycle).

## Context

Session deletion and reconciliation both mutate Session Topology: they delete subtrees, reparent surviving Worktree Sessions, and apply Secondary Session lifecycle rules. The rules are easy to duplicate incorrectly because topology decisions are mixed with tmux, git, lock, lease, agent, and repository side effects.

Explicit deletion and reconciliation also have different failure modes. Explicit deletion is user-initiated while the target worktree still exists, so malformed parent chains are likely data-integrity bugs. Reconciliation is daemon self-healing after the filesystem has already diverged from recorded Session state, so a missing ancestor can be part of the condition being repaired.

## Decision

Topology changes are planned as pure, ID-based instructions before usecases perform side effects. The domain planner describes which Session IDs to delete and which surviving Session IDs to reparent. It does not construct replacement Sessions and does not depend on repositories, locks, tmux, git, leases, agents, context, or logging.

Explicit Worktree deletion is strict. If the removed Worktree Session has a malformed positive parent chain, planning fails and no git removal or repository mutation is attempted. Worktree children are reparented to the removed Worktree Session's parent, and Secondary descendants under the removed Worktree Session cascade.

Reconcile prune is healing-oriented. Missing Worktree Sessions and their Secondary descendants are deleted. Surviving Worktree Sessions whose parent is pruned are reparented to the nearest non-pruned ancestor. If that ancestor chain reaches a missing parent, the surviving Worktree Session becomes parentless (`-1`) instead of failing the prune.

Secondary deletion follows the selected Secondary Session's `onDelete` policy. `cascade` deletes descendant Secondary Sessions child-before-parent. `inherit` deletes only the selected Secondary Session and reparents only direct Secondary children to the selected Session's parent; descendants remain under their current parent, and relative working directories are not recalculated.

A Worktree Session under a Secondary Session is malformed topology and fails planning. Delete and prune plans emit delete IDs child-before-parent so repository deletion remains safe.

## Consequences

- The strict delete versus tolerant reconcile split is intentional: delete is fail-fast data protection; reconcile is daemon self-healing after out-of-band filesystem changes.
- Usecases remain responsible for side-effect ordering, including tmux kills, git worktree removal, lease release, agent deletion, locks, and repository updates.
- Future durable storage can rely on the planner contract: it receives stable ID-based instructions rather than replacement Session values.
- The behavior is harder to reverse later because persisted Session Topology and reconcile behavior may depend on this contract.
