# Worktree Session adoption on project open

When a **Project** is opened (`POST /projects`) and its repo has git worktrees
that tmux-coder is not yet managing as **Worktree Sessions**, the **Daemon** must
learn the user's intent before adopting them. The first open request that detects
such worktrees and carries no decision is rejected with **428 Precondition
Required** and a machine-readable `code` of `worktrees_detected`; the **Client**
prompts the user and re-issues the open with a `createWorktreeSessions` decision.
On the re-issue, `true` bulk-**adopts** every detected worktree as a parentless
Worktree Session, `false` opens the project normally and skips them. The
parameter is only consulted when un-adopted worktrees exist — supplied otherwise,
it is a no-op.

This is bulk **Worktree Adoption** (ADR-0009) at open time, gated on explicit
consent. It keeps adoption explicit — never inferred from drift — consistent with
ADR-0009's rule that **Reconciliation** never auto-adopts orphan worktrees.

`createWorktreeSessions` is a tri-state `*bool` on the request: `nil` means "no
decision yet" (→ 428 when worktrees exist), `true` adopts, `false` skips. A plain
`bool` could not distinguish *absent* from an explicit *false*, which is the
distinction the whole flow turns on.

The "active worktrees" set is `git worktree list` minus: the primary working tree
(the project root), worktrees already wrapped by a Worktree Session, detached-HEAD
worktrees (a Worktree Session is branch-oriented), and prunable / missing-path
worktrees (adoption would fail).

## Considered options

- **Reuse the 409 `StateConflictError` machinery (ADR-0009)** — rejected. The
  existing codes (`worktree_exists`, `branch_exists`, …) signal *blocking* state;
  nothing here blocks. Modelling "I need a decision" as a conflict overloads the
  word. Instead a sibling `PreconditionRequiredError` maps to 428, reusing the
  identical client-side `code`-matching pattern.
- **A 200 response carrying `{created:false, worktreesPending:[…]}`** — rejected.
  A success status that silently did not do the thing is easy for a client to
  ignore; a rejection forces the decision.
- **Auto-adopt on open without asking** — rejected. ADR-0009 deliberately made
  adoption creation-time and explicit; silently wrapping every on-disk worktree
  on open would surprise users and resurrect declined worktrees.
- **Per-worktree consent** — rejected. A single bool / single Y/n matches the UX;
  selective adoption is already served by the per-session create/adopt flow.
- **Persisting a declined ("no") decision** — rejected for now. All Daemon state
  is in-memory (a persisted decline dies on restart anyway), declining is one
  keystroke, and the prompt only appears on an *explicit* open. Naturally
  revisitable once durable persistence (SQLite) lands.

## Consequences

- A new `PreconditionRequiredError{Code, Msg, Worktrees}` lives in the usecase
  layer beside `StateConflictError`; `writeUsecaseError` maps it to 428. The
  `worktrees_detected` code string is a wire contract duplicated in `httpclient`
  (it must not drift), exactly like the ADR-0009 codes.
- `createProjectRequest` / `CreateProjectInput` gain `CreateWorktreeSessions
  *bool`; `errorResponse` gains an optional `worktrees:[{path,branch}]` populated
  only for this 428 (the controller special-cases it, as it already does for
  `StateConflictError`).
- The first request has **zero side effects** — no Project or Main Session is
  created when it rejects. The whole open stays atomic: it either fully completes
  or does nothing. Detection runs a read-only `git worktree list` on every open.
- Core open (Project + Main Session) is atomic and must succeed; worktree
  adoption is layered on top as **best-effort per worktree**. A failed adoption
  does not roll back the open or its siblings. Combined with the stateless design,
  any worktree that fails to adopt — or is declined — simply stays un-adopted and
  is re-offered on the next open.
- Bulk-adopted Worktree Sessions are **parentless** (Project-level, `parent =
  -1`), identical to the `W` create path. **Provenance** is recorded as "no
  source" and is never reconstructed from git ancestry.
- The CLI `open` command becomes interactive: on 428 it prompts (`[Y/n]`, Enter =
  yes) and re-issues. A non-TTY stdin is treated as "no". Explicit
  `--create-worktree-sessions` / `--no-create-worktree-sessions` flags set the
  bool on the first request and bypass the prompt and the 428 round-trip.
