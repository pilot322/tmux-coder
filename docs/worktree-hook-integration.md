# Making your project play nicely with tmux-coder worktrees

This guide is for **consumer projects** — any repository you open with tmux-coder
and from which you create [Worktree Sessions](../CONTEXT.md). It explains how to
wire up **Worktree Hooks** so that every worktree comes up as an independent,
runnable copy of your project with no shared mutable state, and its external
resources can be removed when the Session is deleted.

The single most important idea: **your project's runnable infrastructure must be
fully configurable from `.env` (or an equivalent file the hook can write).** The
hook is just a script — it can only isolate the things you have made configurable.
Anything hard-coded (a fixed database name, a fixed port, a fixed cache prefix)
will be shared across every worktree and will cause collisions. The work this guide
asks of you is mostly *making your project parameterizable*; the hook itself is
small.

---

## Background: what a Worktree Hook is

A **Worktree Hook** is a Project-declared lifecycle script that tmux-coder runs
when it creates or deletes a Worktree Session. It belongs to tmux-coder's lifecycle, not
Git's hook system — it has nothing to do with `.git/hooks`.

When you create a worktree, tmux-coder:

1. Creates the git worktree at a new path.
2. Records the Worktree Session and starts its tmux session.
3. **Runs your create hook** in a dedicated `worktree-setup` window in that Session.

The Worktree Session is listed immediately, so you can attach to it or create a
TC Agent while setup continues. A successful hook closes `worktree-setup`
automatically. If the hook exits non-zero or times out, tmux-coder raises a
critical Desktop Notification and leaves the window open with its output. Press
Enter there after inspecting the failure; tmux-coder then removes the Session,
its TC Agents, the worktree, and a branch created for it. A failing hook therefore
remains inspectable without leaving a half-configured Session behind. Deletion
runs a separate destroy hook synchronously, before removing the checkout (Step 6).

---

## Step 1 — Declare the hook in your Config File

tmux-coder reads a per-project **Config File** at:

```
.tmux-coder/.tmux-coder.toml
```

Add a `[worktree]` section:

```toml
[worktree]
on-create-script  = ".tmux-coder/setup-worktree.sh"   # relative to project root
on-create-timeout = "60s"                             # optional; default 2m
on-destroy-script  = ".tmux-coder/teardown-worktree.sh" # for external resources
on-destroy-timeout = "60s"                             # optional; default 2m
```

Rules enforced by tmux-coder (invalid create configuration blocks creation;
invalid destroy configuration blocks deletion):

- **`on-create-script`** — path to your hook, **relative to the project root**.
  Absolute paths are rejected. The path may not escape the project root (no `..`),
  and symlinks are resolved and re-checked against the same rule.
- The script must **exist** and be **executable** (`chmod +x`).
- **`on-create-timeout`** — a Go duration string (`"30s"`, `"2m"`, `"90s"`). If
  omitted it defaults to **2 minutes**. The hook is killed if it exceeds this;
  budget for a cold `npm install` / `go mod download` if those run here.
- **`on-destroy-script`** follows the same relative-path, containment, and
  executable rules as `on-create-script`. Configure it when setup creates
  external resources. **`on-destroy-timeout`** is a positive Go duration string
  with the same 2-minute default.

Unknown keys are a hard error, so a typo surfaces immediately rather than being
silently ignored.

The setup window supervisor requires `bash`, `timeout` (GNU coreutils), and
`tee` on the host in addition to tmux. These commands enforce the configured
timeout while preserving combined stdout/stderr in both the window and hook log.

Check this file into version control — that's how every clone and every worktree
inherits the same setup behavior.

---

## Step 2 — Know what the hook receives

The create hook runs with its **working directory set to the new worktree's root**,
and with these environment variables set by tmux-coder:

| Variable | Meaning |
| --- | --- |
| `TMUX_CODER_PROJECT_ROOT` | Absolute path to the project root (the main checkout). |
| `TMUX_CODER_WORKTREE_ROOT` | Absolute path to the **new worktree** (also your `cwd`). |
| `TMUX_CODER_PROJECT_ID` | Integer project id, as a string (e.g. `"42"`). |
| `TMUX_CODER_SESSION_NAME` | User-facing session name (e.g. `myproject.auth`). |
| `TMUX_CODER_TMUX_SESSION_NAME` | Internal tmux target (dots → underscores, e.g. `myproject_auth`). |
| `TMUX_CODER_BRANCH` | The git branch checked out in this worktree. |
| `TMUX_CODER_HOOK_TOKEN` | Opaque token used to acquire ports (see Step 4). Treat as read-only and pass it through unchanged. |

`TMUX_CODER_SESSION_NAME` and `TMUX_CODER_BRANCH` are your best **stable,
human-meaningful uniqueness keys** for naming databases, schemas, prefixes, etc.
`TMUX_CODER_PROJECT_ID` is a stable numeric discriminator if you need one.
The destroy hook's environment differs; see Step 6.

---

## Step 3 — Make your infrastructure configurable (the real work)

A worktree is only "independent" to the extent that its running services don't
touch the same external state as another worktree. The hook can rename things only
if your tooling reads those names from configuration. So, before writing the hook,
**audit your project for every shared resource and make each one overridable from a
single file** — conventionally `.env`.

### The rule

> For every external or host-level resource your dev environment touches, there
> must be an environment variable (or config key) that fully determines *which*
> instance/name/path/port is used — and **all** of your tooling must honor it.

"All your tooling" is the part teams get wrong. It is not enough for the app server
to read `DATABASE_NAME`. Your migration runner, seed scripts, test runner, ORM CLI,
`docker-compose`, Makefile targets, and any `psql`/`redis-cli` helper must read the
*same* variable. If even one tool has the database name baked in, two worktrees will
fight over that database.

### Checklist of things to make configurable

| Resource | Make configurable as | Why it collides otherwise |
| --- | --- | --- |
| **Database (dev)** | `DATABASE_NAME` or `DATABASE_SCHEMA` (or a full `DATABASE_URL`) | Two worktrees running migrations/seeds against one DB corrupt each other's state. |
| **Ports** | `PORT`, `API_PORT`, `VITE_PORT`, … | Only one process can bind a port; the second worktree's server won't start. See Step 4 for allocation. |
| **Redis / cache** | `REDIS_URL` **or** a `CACHE_PREFIX` / key namespace | Shared keys mean one worktree reads another's cached/session data. |
| **Message queues / topics** | queue or topic name | Workers in worktree A consume jobs meant for worktree B. |
| **Object storage / uploads** | bucket name or local upload dir | Files clobber each other. |
| **Search indexes** | index name | Reindexing one worktree wipes the other's documents. |
| **Container/project names** | `COMPOSE_PROJECT_NAME` | `docker-compose` reuses the same containers/volumes/networks across worktrees. |
| **Lockfiles / sockets / PID files** | path under the worktree | Host-global paths serialize or crash parallel runs. |

Prefer **schema-per-worktree** or **database-name-per-worktree** over a shared
database. Prefer key **prefixes** over shared Redis/cache namespaces. The goal is
that two worktrees can run their full stack simultaneously and never observe each
other.

### Why `.env` specifically

A single, git-ignored `.env` (or `.env.local`) at the worktree root is the natural
seam for the hook, because:

- It is the one place the hook has to write — it does not need to understand your
  app's internals.
- Most ecosystems already load it (`dotenv`, Vite, Next.js, `docker-compose`,
  `direnv`, etc.), so one file reconfigures the whole stack at once.
- It lives **inside the worktree**, so it is naturally per-worktree and disappears
  when the worktree is deleted.

Keep a checked-in **`.env.example`** documenting every overridable key. That file
doubles as the contract the hook fills in.

---

## Step 4 — Allocate ports without collisions

Hard-coding a per-worktree port offset (worktree 1 → 3000, worktree 2 → 3001, …)
breaks down: worktrees are created and deleted, and you can't predict a free port.
tmux-coder gives you a **Port Lease** mechanism so the daemon hands you a free port
and remembers it for the session's lifetime.

From inside the hook, run:

```sh
tmux-coder acquire-port KEY --start N --end M
```

- `KEY` is a semantic label for the port (`web`, `api`, `db`, `vite`, …). Use a
  distinct key per port you need.
- `--start` / `--end` bound the range to search within.
- The command **prints the chosen port to stdout** (one integer, newline-terminated).

Because `TMUX_CODER_HOOK_TOKEN` is present in the hook's environment, `acquire-port`
automatically leases the port to *this in-progress worktree creation*. When the
Worktree Session is finalized, the lease is promoted to that session; if your hook
fails, the lease is released along with everything else in the rollback. You do not
pass the token explicitly — just don't unset it.

Example — capture two ports into `.env`:

```sh
web_port=$(tmux-coder acquire-port web --start 3000 --end 3099)
api_port=$(tmux-coder acquire-port api --start 4000 --end 4099)
```

> Outside a hook (e.g. a plain shell inside a managed session), the same command
> leases against the current session instead, inferred from the tmux session you're
> in.

---

## Step 5 — Write the hook

Putting it together. Keep the hook with the rest of your tmux-coder project
configuration (here `.tmux-coder/setup-worktree.sh`), make it executable, and it
runs in the new worktree:

```sh
#!/usr/bin/env bash
set -euo pipefail

# We start in the new worktree's root (TMUX_CODER_WORKTREE_ROOT).

# 1. Derive the same safe resource identity on create and destroy.
slug="${TMUX_CODER_PROJECT_ID}_$(printf '%s' "$TMUX_CODER_BRANCH" | sha256sum | cut -c1-12)"

# 2. Lease ports from the daemon (free ports, remembered for this session).
web_port="$(tmux-coder acquire-port web --start 3000 --end 3099)"
api_port="$(tmux-coder acquire-port api --start 4000 --end 4099)"

# 3. Write a per-worktree .env. Every tool in the project reads these.
cat > .env <<EOF
# Generated by tmux-coder worktree hook — do not edit by hand.
DATABASE_NAME=myapp_${slug}
REDIS_PREFIX=myapp:${slug}:
COMPOSE_PROJECT_NAME=myapp_${slug}
WEB_PORT=${web_port}
API_PORT=${api_port}
EOF

# 4. Install dependencies for this worktree (node_modules is per-worktree).
npm ci

# 5. Provision the isolated resources the .env now points at.
createdb "myapp_${slug}"
npm run db:migrate          # reads DATABASE_NAME from .env
npm run db:seed             # same
```

Notes:

- **Exit non-zero to abort.** `set -euo pipefail` means any failed step rolls back
  the whole worktree creation — which is what you want; a worktree that can't be
  provisioned should not exist.
- **Stay inside the timeout.** If `npm ci` / migrations are slow, raise
  `on-create-timeout` accordingly.
- **Make provisioning retryable.** If a resource already exists, handle only that
  case explicitly; do not mask a connection or permission error. A failed create
  hook rolls back the worktree without running the destroy hook, so clean up any
  partially provisioned external state on failure or via a reaper.
- **Don't touch shared state by name.** Notice every external resource above is
  derived from `${slug}` — that is the whole point. Include a project-specific
  prefix if multiple projects use the same external service.

---

## Step 6 — Configure teardown for external resources

Anything **inside** the worktree is removed by Git; external databases, buckets,
or indexes are not. If setup provisions external resources, configure
`on-destroy-script` in Step 1 and check in an executable teardown script alongside
the setup script. Use the same identities that setup generated, while the checkout
and its `.env` still exist. For example, `.tmux-coder/teardown-worktree.sh` for
the database in Step 5:

```sh
#!/usr/bin/env bash
set -euo pipefail
# Read the generated value as data, and verify it before deleting anything.
db_name="$(sed -n 's/^DATABASE_NAME=//p' .env)"
slug="${TMUX_CODER_PROJECT_ID}_$(printf '%s' "$TMUX_CODER_BRANCH" | sha256sum | cut -c1-12)"
[[ "$db_name" == "myapp_${slug}" ]] || { echo 'unexpected database name' >&2; exit 1; }
dropdb --if-exists "$db_name"
```

Use your project's actual resource list: stop per-worktree containers and remove
external databases, queues, buckets, or indexes created by setup. Validate each
target before deleting it; never delete a shared service or an unrelated resource.
Make teardown safe to retry if only some resources were removed on the first run.
Keep it within `on-destroy-timeout`, and keep secrets out of stdout/stderr.

The script's cwd is the worktree root. It receives the project root, worktree
root, project id, session name, tmux session name, and branch listed in Step 2,
plus `TMUX_CODER_SESSION_ID`. It receives **no** `TMUX_CODER_HOOK_TOKEN` or new
provisional Port Leases. Existing session leases remain until removal succeeds.

The destroy hook runs **synchronously before `git worktree remove`**, including
for adopted Worktree Sessions. The current Config File in the Project root is
read at deletion, not a copy from creation. Invalid config or a missing or
non-executable configured script blocks deletion, even with Force. Without Force,
tmux-coder first rejects an already-dirty checkout without running the hook; Git
can still reject removal afterward if the hook dirties the checkout. On a hook
failure or timeout, ordinary deletion retains the Session and worktree for retry.
Force still runs and waits for the hook, but continues removal on execution
failure. Make teardown idempotent so a retry is safe.

Hook stdout/stderr is retained under the daemon's `logs/.../daemon/hooks/`
directory, with `worktree-on-destroy` in the log name and header. No destroy hook
runs for Secondary Session deletion, reconciliation after external worktree
removal, or rollback of a failed worktree creation. No configured destroy script
preserves the previous deletion behavior; use that only when setup leaves no
external resources. Keep a manual or periodic reaper for failed creations and
checkouts removed outside tmux-coder. Verify by creating two disposable Worktree
Sessions, confirming distinct resource identities, then deleting one and checking
that its resources are gone while the other's remain.

---

## Summary

1. Declare `[worktree].on-create-script` and, when provisioning external state,
   `[worktree].on-destroy-script` in `.tmux-coder/.tmux-coder.toml`; keep both
   scripts under `./.tmux-coder`.
2. **Make every shared resource configurable from `.env`** — database name/schema,
   ports, cache prefixes, queue/topic names, compose project name — and ensure
   *all* tooling reads those same values.
3. In the create hook, derive a unique resource identity, lease ports with
   `tmux-coder acquire-port`, write `.env`, and provision the isolated resources.
4. Fail the create hook (exit non-zero) if provisioning can't complete — tmux-coder
   rolls the worktree back after acknowledgement.
5. Tear down only this worktree's external resources in the destroy hook, making
   removal safe to retry; account separately for failed creations and external removal.

The hook is small. The investment is in step 2: a project whose infrastructure is
fully parameterizable gets per-worktree isolation almost for free.
