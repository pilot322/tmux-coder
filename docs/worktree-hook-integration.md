# Making your project play nicely with tmux-coder worktrees

This guide is for **consumer projects** — any repository you open with tmux-coder
and from which you create [Worktree Sessions](../CONTEXT.md). It explains how to
wire up the **Worktree Hook** so that every worktree comes up as a fully
independent, runnable copy of your project, with no shared mutable state between
worktrees.

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
when it creates a new Worktree Session. It belongs to tmux-coder's lifecycle, not
Git's hook system — it has nothing to do with `.git/hooks`.

When you create a worktree, tmux-coder:

1. Creates the git worktree at a new path.
2. **Runs your hook** in that new worktree's directory (this guide's subject).
3. Records the Worktree Session and starts its tmux session.

If the hook exits non-zero or times out, tmux-coder **rolls the whole creation
back**: the worktree is removed, a newly created branch is deleted, and no session
is recorded. So a failing hook fails safe — you never end up with a half-configured
worktree.

---

## Step 1 — Declare the hook in your Config File

tmux-coder reads a per-project **Config File** at:

```
.tmux-coder/.tmux-coder.toml
```

Add a `[worktree]` section:

```toml
[worktree]
on-create-script  = "scripts/tmux-coder-setup.sh"   # relative to project root
on-create-timeout = "60s"                            # optional; default 2m
```

Rules enforced by tmux-coder (a violation fails worktree creation loudly):

- **`on-create-script`** — path to your hook, **relative to the project root**.
  Absolute paths are rejected. The path may not escape the project root (no `..`),
  and symlinks are resolved and re-checked against the same rule.
- The script must **exist** and be **executable** (`chmod +x`).
- **`on-create-timeout`** — a Go duration string (`"30s"`, `"2m"`, `"90s"`). If
  omitted it defaults to **2 minutes**. The hook is killed if it exceeds this;
  budget for a cold `npm install` / `go mod download` if those run here.

Unknown keys are a hard error, so a typo surfaces immediately rather than being
silently ignored.

Check this file into version control — that's how every clone and every worktree
inherits the same setup behavior.

---

## Step 2 — Know what the hook receives

The hook runs with its **working directory set to the new worktree's root**, and
with these environment variables set by tmux-coder:

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

Putting it together. The hook lives at the path you declared (here
`scripts/tmux-coder-setup.sh`), is executable, and runs in the new worktree:

```sh
#!/usr/bin/env bash
set -euo pipefail

# We start in the new worktree's root (TMUX_CODER_WORKTREE_ROOT).

# 1. Derive a safe, unique slug for this worktree from the branch name.
slug="$(printf '%s' "${TMUX_CODER_BRANCH}" | tr -c 'a-zA-Z0-9' '_' | tr 'A-Z' 'a-z')"

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

# 4. Provision the isolated resources the .env now points at.
createdb "myapp_${slug}" 2>/dev/null || true
npm run db:migrate          # reads DATABASE_NAME from .env
npm run db:seed             # same

# 5. Install dependencies for this worktree (node_modules is per-worktree).
npm ci
```

Notes:

- **Exit non-zero to abort.** `set -euo pipefail` means any failed step rolls back
  the whole worktree creation — which is what you want; a worktree that can't be
  provisioned should not exist.
- **Stay inside the timeout.** If `npm ci` / migrations are slow, raise
  `on-create-timeout` accordingly.
- **Make it idempotent / forgiving.** `createdb … || true` tolerates a pre-existing
  database so re-runs don't fail spuriously.
- **Don't touch shared state by name.** Notice every external resource above is
  derived from `${slug}` — that is the whole point.

---

## Step 6 — Clean up on delete (optional)

The Worktree Hook runs on *create*. Anything that lives **inside** the worktree
(its `.env`, `node_modules`, build artifacts) is removed automatically when the
worktree is deleted. But **external** resources you provisioned — a dev database
named `myapp_<slug>`, an uploads bucket, a search index — are not tmux-coder's to
know about, so they will linger.

If you provision external state in the hook, plan its teardown:

- Make creation idempotent (as above) so a recreated worktree reuses cleanly.
- Provide a manual reaper (e.g. a `make db:drop-worktree` target) or a periodic
  job that drops databases/prefixes whose worktree no longer exists.

---

## Summary

1. Declare `[worktree].on-create-script` in `.tmux-coder/.tmux-coder.toml`.
2. **Make every shared resource configurable from `.env`** — database name/schema,
   ports, cache prefixes, queue/topic names, compose project name — and ensure
   *all* tooling reads those same values.
3. In the hook, derive a unique slug (from `TMUX_CODER_BRANCH`), lease ports with
   `tmux-coder acquire-port`, write `.env`, and provision the isolated resources.
4. Fail the hook (exit non-zero) if provisioning can't complete — tmux-coder rolls
   the worktree back cleanly.

The hook is small. The investment is in step 2: a project whose infrastructure is
fully parameterizable gets per-worktree isolation almost for free.
