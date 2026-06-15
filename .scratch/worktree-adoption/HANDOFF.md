# Handoff: Worktree Session creation modes + adoption

**For:** an implementation agent driving the work with the **`tdd` skill**
(red → green → refactor, one behavior at a time).
**Branch:** `feat/existing-worktree` (already checked out).
**Design status:** fully specified and agreed. Do **not** re-litigate the design —
implement it. Rationale lives in `docs/adr/0009-worktree-session-creation-modes-and-adoption.md`
and the glossary term **Worktree Adoption** in `CONTEXT.md`.

---

## 1. Goal

When creating a **Worktree Session** from the TUI ("the CLI"), the daemon
currently sends a single `create: true`. If the branch or worktree already
exists, creation just fails with an opaque 409. Replace this with:

1. Two explicit flags — `createWorktree`, `createBranch` — that encode three
   creation modes (fresh / existing-branch / **adopt**).
2. A machine-readable `code` on conflict 409s so the TUI can offer the right
   `y/n` prompt and re-issue.

### Target UX (the two new prompts)

- **Branch exists, no worktree** → `branch already exists. Create a worktree for it? y/n`
  → on `y`, re-issue with `createWorktree:true, createBranch:false` (**hooks DO run**).
- **Worktree exists on disk, no session** → `worktree already exists. Create a session? y/n`
  → on `y`, re-issue with `createWorktree:false, createBranch:false` (**hooks do NOT run** — adoption).

---

## 2. The model (authoritative)

### Flags → mode

| Mode | `createWorktree` | `createBranch` | Git action | Hooks |
|---|:---:|:---:|---|:---:|
| Fresh | `t` | `t` | `worktree add -b <branch> <path> [base]` | run |
| Existing branch, new worktree | `t` | `f` | `worktree add <path> <branch>` | run |
| **Adopt** existing worktree | `f` | `f` | *(none — wrap existing checkout)* | **skip** |
| Illegal | `f` | `t` | reject with `ErrValidation` | — |

- Hooks run **iff `createWorktree`**. No `runWorktreeHooks` flag.
- `baseBranch` is valid **only when `createBranch:true`**; reject otherwise with
  `ErrValidation`. (A future CLI feature will start sending `baseBranch` — keep the
  rule, don't remove the field.)

### Conflict `code` set (carried on 409s)

Evaluated against `git worktree list --porcelain` for the derived worktree path,
**in this order**:

| `code` | State detected | TUI behavior |
|---|---|---|
| `session_exists` | a Worktree Session already bound to this branch | hard error |
| `worktree_exists` | derived path **is** this repo's worktree, on this branch, no session | prompt → **adopt** |
| `path_blocked` | path occupied but not adoptable (stray dir, or worktree on a *different* branch) | hard error |
| `branch_exists` | branch exists locally, no worktree at the derived path | prompt → **new worktree** |
| *(none)* | clean | proceed (fresh) |

`worktree_exists` wins over `branch_exists` because an existing worktree always
implies its branch exists, and "adopt the worktree" is the more specific offer.

### Daemon decision logic per incoming mode

Let `S` = session-for-branch exists, `W` = derived path is a worktree of this
repo (with branch `B_w`), `P` = path exists on disk but is not this repo's
worktree, `L` = branch exists locally.

**Mode `(t,t)` fresh:**
`S` → `session_exists` · else `W && B_w==branch` → `worktree_exists` ·
`W && B_w!=branch` → `path_blocked` · `P` → `path_blocked` ·
`L` → `branch_exists` · else → **proceed** (`AddWorktree` with `-b`, hooks, tmux, record).

**Mode `(t,f)` existing-branch:**
`S` → `session_exists` · `W && B_w==branch` → `worktree_exists` ·
`W && B_w!=branch` → `path_blocked` · `P` → `path_blocked` ·
`!L` → `ErrConflict` "branch does not exist" · else → **proceed**
(`AddWorktree` **without** `-b`, hooks, tmux, record).

**Mode `(f,f)` adopt:**
`S` → `session_exists` · `W && B_w==branch` → **proceed adopt**
(no `AddWorktree`, **no hooks**, tmux create, record with `branch=<branch>`) ·
`W && B_w!=branch` → `path_blocked` ("worktree on <B_w>, not <branch>") ·
else (no worktree at path) → `path_blocked` ("no worktree to adopt at <path>").

**Mode `(f,t)`:** `ErrValidation` up front ("cannot create a branch without a worktree").

> Path comparison must canonicalize both sides (`filepath.EvalSymlinks` + `Clean`)
> before matching — `git worktree list` emits symlink-resolved absolute paths.

---

## 3. Out of scope (do not touch)

- `reconcileWorktreeSessions` — stays prune-only; no auto-adoption.
- Any new `/inspect` endpoint — the `code` on the create 409 is the whole mechanism.
- Secondary-session materialization still runs for **all** modes including adopt
  (it creates runtime tmux sessions, independent of worktree creation). Only
  `AddWorktree` and the **Worktree Hook** are gated on `createWorktree`.

---

## 4. Change map (where each piece lives)

Layer order below is also a sane TDD order (inside-out: gateway → usecase →
http adapter → client → TUI). Line numbers are anchors; they will drift.

### 4.1 Git gateway — `internal/usecase/ports.go`, `internal/infra/git/gateway.go`
- Add to `GitWorktreeGateway` (ports.go:59): `ListWorktrees(ctx, repoPath) ([]WorktreeRef, error)`
  and a `WorktreeRef{ Path, Branch string; Detached bool }` type (usecase package).
- Implement in `gateway.go` via `git -C <repoPath> worktree list --porcelain`
  (parse `worktree `, `branch refs/heads/<x>`, `detached` records).
- `AddWorktree`'s existing `create bool` param already means "use `-b`" — it now
  receives `createBranch`. Rename the param `create`→`createBranch` for honesty
  (cosmetic; behavior identical) and only call it when `createWorktree` is true.

### 4.2 Usecase — `internal/usecase/create_session.go`
- `CreateSessionInput` (line 15): replace `Create bool` with
  `CreateWorktree bool` and `CreateBranch bool`.
- Add a typed conflict error in the usecase package (e.g. `ports.go` or a new
  small file):
  ```go
  type StateConflictError struct{ Code, Msg string }
  func (e *StateConflictError) Error() string { return e.Msg }
  func (e *StateConflictError) Is(t error) bool { return t == ErrConflict } // keeps 409 mapping
  ```
  Plus exported code constants: `CodeSessionExists = "session_exists"`, etc.
- Rewrite the validation/decision block (roughly lines 61–146) to implement §2's
  per-mode logic against `ListWorktrees`. Replace the `os.Stat`-based
  `WorktreePathExists` branch detection for the conflict codes.
- Gate `AddWorktree` (line 142) and `runConfiguredWorktreeHook` (line 149) on
  `in.CreateWorktree`. In adopt mode `hookToken == ""`, so the lease/promote
  block (155–193) is already skipped.
- **Rollback (lines 141–146, 417–424):** `branchCreated := in.CreateBranch`;
  `worktreeCreated` stays false in adopt mode (we skip `AddWorktree`), so
  `rollbackCreatedWorktree` must not remove the adopted worktree or delete its
  branch. Verify this with a test.

### 4.3 HTTP adapter — `internal/adapter/httpapi/dto.go`, `controller.go`, `response.go`
- `createSessionRequest` (dto.go:23): replace `Create` with `CreateWorktree`,
  `CreateBranch`. `errorResponse` (dto.go:55): add `Code string \`json:"code,omitempty"\``.
- `SessionController.Create` (controller.go:159): pass the two new flags through.
- `writeUsecaseError` (response.go:23): before the generic `ErrConflict` branch,
  `errors.As(err, &*StateConflictError)` and write `{error, code}` with 409.

### 4.4 HTTP client — `internal/client/httpclient/client.go`
- `CreateSessionInput` (line 66): replace `Create` with `CreateWorktree`,
  `CreateBranch` (keep `omitempty`).
- Add a typed `APIError{ Status int; Code, Message string }` (with `Error()`),
  and mirror the four code constants (wire contract — must equal the usecase
  strings). Make `statusError` (line 316) parse `code` and return `*APIError`.

### 4.5 TUI — `internal/client/tui/model.go`
- `createWorktreeCmd` (line 573): parameterize the two flags; the initial attempt
  sends `(t,t)`.
- `createSessionMsg` handler (line 220): on error, `errors.As` to `*httpclient.APIError`;
  if `Code` is `worktree_exists` or `branch_exists`, enter a new conflict-confirm
  state (reuse the still-set `m.worktreeBranch` / `m.worktreeProjectID` — they are
  not cleared until success). Other codes / errors → status line as today.
- Add the confirm state + `View` rendering (near the delete-confirm block ~320–335)
  and `y`/`n` handling in `Update` (near ~246). `y` re-issues `createWorktreeCmd`
  with the mode from §1; `n`/Esc cancels and clears.

---

## 5. TDD plan (suggested red→green sequence)

Existing fakes to extend (do **not** rewrite):
- `internal/usecase/create_session_test.go` → `fakeWorktreeGit` (line 637): add a
  `ListWorktrees` method + a programmable `worktrees []WorktreeRef` field;
  `AddWorktree` signature gains the rename.
- `internal/adapter/httpapi/httpapi_test.go` → `stubGit` (line 72): add `ListWorktrees`.
- `internal/client/httpclient/client_test.go` (lines 89–117): update the create
  payload assertion to the two flags; add a test that a 409 body with `code`
  decodes to `*APIError`.
- `internal/client/tui/model_test.go`: drive the prompt→confirm→re-issue flow.

Scenario checklist (each is one red→green cycle):

**Usecase**
1. `(t,t)` clean → fresh path: `AddWorktree(-b)` called, hooks run, session recorded.
2. `(t,t)` with session-for-branch → `StateConflictError{session_exists}`.
3. `(t,t)` with adoptable worktree (in list, same branch) → `worktree_exists`.
4. `(t,t)` with worktree-on-different-branch → `path_blocked`.
5. `(t,t)` with stray dir at path (not in list) → `path_blocked`.
6. `(t,t)` with branch only (no worktree) → `branch_exists`.
7. `(t,f)` with branch present, no worktree → proceeds, `AddWorktree` **without** `-b`, hooks run.
8. `(t,f)` with no branch → `ErrConflict` "branch does not exist".
9. `(f,f)` adopt happy path → **no** `AddWorktree`, **no** hook run, tmux create + record; branch recorded = requested branch.
10. `(f,f)` worktree on different branch → `path_blocked`.
11. `(f,f)` nothing at path → `path_blocked`.
12. `(f,t)` → `ErrValidation`.
13. `baseBranch` set with `createBranch:false` → `ErrValidation`.
14. Adopt rollback: force a later failure (tmux create errors) → assert the
    adopted worktree is **not** removed and branch **not** deleted.

**HTTP adapter**: a `StateConflictError` → 409 body carries the `code`.
**HTTP client**: 409 `{code}` → `*APIError` with `Code` populated; create payload
serializes the two flags.
**TUI**: `branch_exists` error → branch prompt; `y` re-issues `(t,f)`.
`worktree_exists` error → session prompt; `y` re-issues `(f,f)`. `n` cancels.

---

## 6. Conventions

- Production-clean comments only — this is a public repo; no teaching/lesson
  asides in code (per `CLAUDE.md` / project memory). Explain *why*, not *what*.
- Match surrounding Go style; small focused gateway methods like the existing ones.
- Run `go test ./...` green before considering a slice done. `go vet ./...` too.
- Keep the wire `code` strings identical across usecase ↔ httpclient (it's the contract).

## 7. Done when
- All §5 scenarios pass; `go test ./...` green.
- TUI shows both `y/n` prompts and re-issues correctly (manual check via `./dev` or
  the `run` skill).
- No changes to reconciliation behavior.
