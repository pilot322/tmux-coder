# Handoff: Agent view sort/grouping + desktop notifications

**For:** an implementation agent driving the work with the **`tdd` skill**
(red → green → refactor, one behavior at a time — vertical slices, never write all
tests up front).
**Branch:** `feat/agent-state-sorting-and-notifications` (already checked out).
**Design status:** fully specified and agreed in a grilling session. Do **not**
re-litigate the design — implement it. Glossary term **Desktop Notification** is
already in `CONTEXT.md`. An ADR `0012-daemon-side-desktop-notifications.md` was
offered but may not be written yet; **this handoff is authoritative** for behavior.

Two independent features. They touch disjoint code (Feature 1 = TUI client only,
Feature 2 = daemon only) and can be done in either order.

---

## Feature 1 — Agent view sort + grouping (TUI client, presentation only)

### Model (authoritative)

- **Status sort is always on** in the Agents view (tab 3). There is no "unsorted"
  mode. Rank, top → bottom:

  | rank | status     | note |
  |:---:|------------|------|
  | 0 | `waiting`   | ◆ red — needs the user |
  | 1 | `idle`      | ○ green — ready/done |
  | 2 | `busy`      | ◐ — working |
  | 3 | `running`   | ● — alive, no activity reporting |
  | 4 | `starting`  | ◌ — not confirmed alive |
  | 5 | *(empty / unknown)* | `·` — anything else |

  **Tiebreaker within a rank: ascending agent ID.** Use a *stable* sort.

- **`s` toggles group-by-project**, in the **Agents view only**. `g`/`G`
  (jump-to-top/bottom) keep their meaning everywhere, including the Agents view —
  no collision, `s` (lowercase) is currently unbound (`S` = create secondary, a
  Sessions action, untouched).
- The grouping toggle is **ephemeral Model state**, default **flat (ungrouped)**.
  Not persisted, not sent to the daemon.
- **Grouped mode:** agents are status-sorted (same rank) *within each project
  group*; groups appear in the **existing project order** (the order
  `tabSessions`/`tabOverview` already iterate `m.projects`); each group gets a
  **non-selectable project header** line, exactly like the Sessions tab.
- Flat mode: one status-sorted list, no headers.

### Why no daemon involvement
Pure presentation, scoped to one view. The daemon keeps returning agents in its
own order; the TUI sorts for display. Sorting in the daemon would wrongly impose a
presentation order on every consumer.

### Change map — `internal/client/tui/model.go`
Line anchors will drift; treat as pointers.

- **Keys** (`keys` struct, ~181–197): add `group: key.NewBinding(key.WithKeys("s"))`.
  Update `helpText` (line 179) to mention `s group (Agents)`.
- **Model** (struct, ~93–139): add `groupAgents bool` (zero value = flat = correct
  default).
- **Key handling** (`Update`, the `tea.KeyMsg` switch ~336–370): on `keys.group`,
  toggle `m.groupAgents` **only when `m.tab == tabAgents`**; otherwise ignore.
  After toggling call `m.normalizeSelection()` so the cursor stays valid.
- **Single source of order.** Today two sites iterate `m.agents` independently:
  `rows()` for `tabAgents` (~860–866) and `writeAgentsView()` (~525–539). Introduce
  **one** helper, e.g. `func (m Model) agentRows() []viewRow`, that returns the
  fully ordered (and, when grouped, project-bucketed) agent `viewRow`s, and have
  **both** `rows()` and `writeAgentsView()` consume it so navigation and render can
  never disagree.
  - In grouped mode the **header lines are added only in `writeAgentsView`**, not in
    `agentRows()` — `rows()` must stay selectable-only (see its doc comment
    ~849–851). `writeAgentsView` walks the same ordered rows and emits a project
    header whenever `row.project.ID` changes.
- **Status rank** is a small pure helper in this package, e.g.
  `func agentStatusRank(status string) int` mapping the table above (default → 5).
  Sort with `sort.SliceStable` on `(rank, ID)`.

### Cursor behaviour (already works — add a guard test)
`currentIndex` resolves the selection by agent **ID** first (model.go:919–925), so
re-sorting on each 1 s poll keeps the cursor glued to its agent. Don't break this.

---

## Feature 2 — Desktop notifications (daemon)

### Model (authoritative)

- Fire a desktop notification **only** on `busy → waiting` and `busy → idle`.
  No other transition notifies (not `running→waiting`, not `idle→waiting`, not
  `starting→*`, not exit). Only departures **from `busy`** to those two targets.
- Detected **daemon-side** in `usecase/agent_event.go` `handleActivity`, where old
  status and target status are both known. Consequence (intended): notifications
  fire **even when no TUI is open**, and never double-fire across multiple TUIs.
- **Content & urgency** (mirror the TUI's visual semantics):

  | transition | title | body | `notify-send -u` |
  |---|---|---|---|
  | `busy → waiting` | `{name} needs input` | `{project} · {session}` | **critical** |
  | `busy → idle`    | `{name} is idle`     | `{project} · {session}` | **normal** |

  `{name}` = display name, else fallback `agent-{id}` (same fallback
  `agentRowLabel` uses). `{project}` = project title, `{session}` = session name.
- **Best-effort, never disruptive.** A notification failing must not affect
  agent-event processing. Errors are swallowed (debug log at most).

### The gateway (interface + impl)
Mirror the existing `GitWorktreeGateway` pattern: **port in `usecase`, dumb
mechanism in `infra`, policy in the usecase, wired in `main.go`.**

- **Port** — `internal/usecase/ports.go` (alongside `WorktreeRef` etc.):
  ```go
  type NotificationUrgency int
  const (
      UrgencyNormal NotificationUrgency = iota
      UrgencyCritical
  )

  type Notification struct {
      Title   string
      Body    string
      Urgency NotificationUrgency
  }

  // Notifier delivers a Desktop Notification to the host. Best-effort: a
  // returned error is logged and otherwise ignored by callers.
  type Notifier interface {
      Notify(ctx context.Context, n Notification) error
  }
  ```
  The interface is **generic / not status-aware** — the usecase composes the
  content and decides which transitions notify.

- **Impl** — new package `internal/infra/desktopnotify/`:
  - `NewNotifier()` returns a `notify-send` impl when `runtime.GOOS == "linux"`
    **and** `notify-send` resolves on `PATH`; otherwise a `NoopNotifier{}`.
    (Runtime selection, *not* build tags — shelling out needs no Linux-only
    imports, and this also covers "Linux without libnotify".)
  - The Linux impl runs `notify-send -u <normal|critical> -a tmux-coder <title>
    <body>` via `exec.CommandContext` with a short timeout (~2 s); swallow errors.
  - **For testability**, inject the exec + lookup functions on the struct like
    `cmd/tmux-coder/wrapper.go:30` does (`CommandContext: exec.CommandContext`),
    so a test can assert the argv (urgency flag, title, body) without spawning a
    process. `NewNotifier()` wires the real ones.
  - Add `var _ usecase.Notifier = (*…)(nil)` assertions for both impls.

### Wiring the policy into the usecase — `internal/usecase/agent_event.go`
`AgentEvent` currently holds only `agents` + `lock`. To compose
`{project} · {session}` it must look those up, so **inject the project & session
repos and the notifier**:

- Change `NewAgentEvent(a IAgentRepository, l StateLock)` →
  `NewAgentEvent(a IAgentRepository, p IProjectRepository, s ISessionRepository, n Notifier, l StateLock)`
  and store them.
- In `main.go` (cmd/tmux-coderd/main.go:46) the call becomes
  `usecase.NewAgentEvent(state.Agents(), state.Projects(), state.Sessions(), notifier, state)`,
  with `notifier := desktopnotify.NewNotifier()` created near the other gateways
  (~main.go:31–36).
- **`handleActivity` (lines 66–77) — the only place that changes behaviourally:**
  1. `old := agent.Status()` (already read by `readAgent`).
  2. Run the existing `WithWrite` update unchanged.
  3. **After `WithWrite` returns** (lock released) and only if `err == nil`:
     compute `shouldNotify := old == domain.AgentBusy && (status == domain.AgentWaiting || status == domain.AgentIdle)`.
     If so, fetch project title + session name (a `WithRead` over the project /
     session repos using `agent.ProjectID()` / `agent.SessionID()`; tolerate
     missing lookups by degrading the body), build the `Notification`, and call
     `uc.notifier.Notify(ctx, n)` — **outside any lock**, error swallowed.
  - Keep a tiny pure helper for the predicate + content mapping so it's unit
    testable, e.g. `func notificationFor(old, new domain.AgentStatus, name, project, session string) (usecase.Notification, bool)`.
- **Do not** notify from `handleStarted` or `handleExited`.

### The DBUS limitation (accepted, document in code)
The daemon is launched with `exec.Command(binary)` and no `cmd.Env`
(`internal/client/daemon/daemon.go:98`), so it inherits the launching client's
session env, including `DBUS_SESSION_BUS_ADDRESS` — which `notify-send` needs.
If the daemon was first auto-launched from a context with no session bus (bare
SSH), `notify-send` fails and we swallow it. Don't engineer around this; note it
in a comment on the Linux impl.

---

## Out of scope (do not build)
- No persistence of the grouping toggle; no daemon config flag to enable/disable
  notifications (always on where `notify-send` is available).
- No coalescing / debounce of rapid transitions (YAGNI for now).
- No notifications for any transition other than the two specified.
- No client-side (TUI) notification path.
- No changes to the poll interval, the HTTP API, or the wire DTOs.

---

## TDD plan (suggested red → green sequences)

Reuse existing fakes — don't rewrite them:
- **Usecase tests:** shared fakes live in `internal/usecase/helpers_test.go`
  (fake repos + `StateLock`). There is **no** `agent_event_test.go` yet — create it.
  Add a `fakeNotifier` that records the `Notification`s it received.
- **TUI tests:** `internal/client/tui/model_test.go` — use the existing
  `fakeAPI` and the `loaded(t, listMsg{...})` helper to seed agents/projects, then
  assert on `m.rows(tabAgents)` order and on `m.View()` output.

### Feature 2 — usecase (inside-out: port+policy → infra → wiring)
1. `busy → waiting` → `Notify` called once with `Urgency=critical`,
   title `"{name} needs input"`, body `"{project} · {session}"`.
2. `busy → idle` → one `Notify`, `Urgency=normal`, title `"{name} is idle"`.
3. Name fallback: agent with empty display name → title uses `agent-{id}`.
4. **Negatives (each its own cycle, assert zero `Notify` calls):**
   `running → waiting`; `idle → waiting`; `starting → idle`; `busy → busy`;
   `waiting`/`idle` arriving via `started`/`exited`.
5. Notify failure is swallowed: `fakeNotifier` returns an error → `Execute`
   still returns nil and the status update still happened.
6. Status update happens **before** the notify (and notify is outside the lock —
   assert via ordering or that the repo already reflects the new status when the
   fake notifier runs).

### Feature 2 — infra (`desktopnotify`)
7. Linux impl builds argv `notify-send -u critical -a tmux-coder "<title>" "<body>"`
   for critical, `-u normal` for normal (assert via injected exec func).
8. `NoopNotifier.Notify` returns nil and runs nothing.
9. Selection: missing `notify-send` (injected lookup fails) → `NewNotifier`
   yields the noop impl.

### Feature 1 — TUI
10. Flat order: agents with mixed statuses → `agentRows()` returns
    waiting, idle, busy, running, starting, unknown.
11. Tiebreaker: two `waiting` agents (IDs 30, 12) → 12 before 30.
12. `s` on the Agents tab toggles `groupAgents`; `s` on another tab is a no-op.
13. Grouped: agents across two projects → rows bucketed by project (existing
    project order), status-sorted within each bucket; `View()` shows a header per
    project; headers are **not** in `rows(tabAgents)` (navigation skips them).
14. Cursor follows its agent across a re-sort: select an agent, feed a `listMsg`
    that reorders it, assert the selection still points at the same agent ID.

---

## Conventions
- Production-clean comments only (public repo; per `CLAUDE.md` + project memory).
  Explain *why*, not *what*. No teaching asides in code.
- Match surrounding Go style; small focused gateway methods like the existing ones.
- Vocabulary: use **Desktop Notification** / **Agent Status** / **TC Agent** from
  `CONTEXT.md`; respect ADR 0008 (single agent status, two writers) — notifications
  observe transitions, they must not change the status conflict policy.
- `go test ./...` and `go vet ./...` green before a slice is done.

## Done when
- All scenarios above pass; `go test ./...` green.
- Agents view is status-sorted; `s` toggles grouping with headers; `g`/`G` still
  work in that view.
- Triggering `busy→waiting` / `busy→idle` on a real agent raises a `notify-send`
  alert with the right urgency (manual check via `./dev` or the `run` skill on a
  Linux desktop session); other transitions raise nothing.
