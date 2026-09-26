# What tmux-coder is

`tmux-coder` is a CLI/TUI for running coding agents inside an isolated tmux server.

It manages projects, git worktrees, tmux sessions, and agent panes from one place.

# Why

Working with several agent-driven branches gets messy fast: shared tmux sessions, clashing ports, missing agent status, and half-configured worktrees.

`tmux-coder` keeps each project/worktree/session separated and gives the daemon one place to track agent lifecycle and status, while still letting you use your tmux config you're already used to.

# Disclaimer
This project is SLOP! It was supposed to be a Go learning project, but I don't have the time nowadays and I really wanted to experience what this could feel like.

# Who it is for

Developers who use tmux, git worktrees, and terminal coding agents such as OpenCode, Pi, Claude Code, or Codex.

# Installation
No one-liner yet- WIP.

From this repo:

```sh
./dev install
```

That builds and installs `tmux-coder` and `tmux-coderd` into your Go bin dir, installs the OpenCode plugin, and installs Claude Code activity hooks.

Manual install:

```sh
go install github.com/pilot322/tmux-coder/cmd/tmux-coder@latest
go install github.com/pilot322/tmux-coder/cmd/tmux-coderd@latest
```

Make sure your Go bin dir is on `PATH`:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

# Dependencies

System/runtime dependencies:

- `tmux`
- `git`
- one or more agent executables, for example `opencode`, `claude`, or `codex`

Build dependency:

- Go `1.22.2+`

Notification dependencies:

- desktop notifications work only on Linux
- visual notifications need `notify-send`
- sound is optional and needs `paplay`
- disable sound with `TMUX_CODER_NOTIFY_SOUND=0`

Daemon-wide settings live in `~/.tmux-coder/config.yaml`:

```yaml
discord_webhook_notify: https://discord.com/api/webhooks/WEBHOOK_ID/WEBHOOK_TOKEN
opencode_server_port: 39155
dashboard_listen_address: 127.0.0.1:39356
dashboard_public_url: http://127.0.0.1:39356
opencode_public_url: http://127.0.0.1:39155
```

Create the webhook under your Discord server's **Server Settings > Integrations > Webhooks**, then replace the example value with its copied URL. Only official HTTPS Discord webhook URLs are accepted. A missing webhook leaves Discord notifications disabled.

The network settings and their non-empty environment overrides are:

| Config key | Default | Environment override | Purpose |
| --- | --- | --- | --- |
| `opencode_server_port` | `39155` | `TMUX_CODER_OPENCODE_SERVER_PORT` | Port for the Daemon-owned shared OpenCode server. |
| `dashboard_listen_address` | `127.0.0.1:39356` | `TMUX_CODER_DASHBOARD_LISTEN_ADDRESS` | Concrete address on which the Web Dashboard listens. |
| `dashboard_public_url` | unset | `TMUX_CODER_DASHBOARD_PUBLIC_URL` | Browser-facing root origin recorded in Daemon startup logs. It does not change the listen address or browser routing. |
| `opencode_public_url` | unset | `TMUX_CODER_OPENCODE_PUBLIC_URL` | Browser-facing root origin from which the Daemon generates exact OpenCode conversation links. It does not replace the internal OpenCode attachment URL. |

Inherited environment values take precedence over `~/.tmux-coder/.env`, and both take precedence over `config.yaml`. The Daemon reads configuration once at startup, so stop and restart `tmux-coderd` after changing the file or environment; a later Client invocation auto-starts it when needed. The internal and dashboard listeners start together. An invalid or unavailable address prevents startup, and either HTTP server stopping causes the Daemon to close the other and exit.

This precedence applies unchanged to installed binaries. Binaries built with `./dev build` are marked as development builds: they ignore daemon-wide file values for their baked daemon port, OpenCode port, dashboard listener, public URLs, and tmux server label so separate worktrees cannot collapse onto the production instance. Environment values inherited by an explicitly launched development binary remain final overrides.

Configuration is strict. `dashboard_listen_address` must contain a concrete IP address or valid hostname and a numeric port from 1 to 65535. Wildcard addresses such as `0.0.0.0` and `[::]`, missing hosts, and malformed numeric addresses are rejected. Public URLs must be absolute `http` or `https` browser origins with a valid non-wildcard host and optional port. User information, queries, fragments, and non-root paths are rejected; a trailing slash is removed and default HTTP/HTTPS ports are normalized. Invalid YAML, unknown keys, webhook URLs, listen addresses, public origins, and ports prevent Daemon startup.

# How to use

Open a project from inside a git repo:

```sh
tmux-coder open
```

or:

```sh
tmux-coder o
```

> Note: I recommend aliasing tmux-coder with something shorter like `tmxc` on your .(whatever)rc

Open the TUI:

```sh
tmux-coder
```

> Note: I recommend creating a tmux keybinding to open this on a 'popup' pane. I like prefix + tab

Useful TUI keys:

- `0-3`: switch tabs
- `j/k`: move
- `enter`: attach
- `a`: immediately create a default OpenCode agent, then optionally rename it
- `A`: create an agent with executable and OpenCode yolo options
- `n`: arm or disable a one-shot Discord notification for the selected agent
- `w`: create worktree from selected session
- `W`: create worktree from a base ref
- `s`: create secondary session
- `X`: delete selected item
- `f`: fuzzyfind
- `?`: help
- `q`: quit

Pressing `n` in Overview or Agents opens an enable/disable confirmation. An armed notification is consumed by the selected agent's next `busy` to `waiting` (needs input) or `busy` to `idle` transition. Other transitions leave it armed. Confirming again while armed disables it.

## Web Dashboard

Open the mobile-first browser Client at `http://127.0.0.1:39356` by default. It polls the Daemon management API and presents Projects, their Session Topology, and active TC Agents. From the dashboard you can open an existing absolute Project path on the Daemon host; create or adopt Worktree Sessions; create Secondary Sessions; delete managed resources; and create, rename, destroy, or change the Discord notification for a TC Agent. It does not clone repositories, browse the host filesystem, expose tmux panes, or reimplement OpenCode chat, tools, permissions, or history.

For an OpenCode TC Agent, the bundled pane-scoped plugin reports the OpenCode conversation currently displayed by that pane and reports again when it changes. The Daemon returns the canonical OpenCode web route, and the dashboard opens that exact conversation rather than an OpenCode home page. Compatible running agents become linkable as soon as they report; they do not need to be recreated. The dashboard shows **Conversation link unavailable** until both `opencode_public_url` is configured and the TC Agent has reported a current conversation. A non-OpenCode Agent Kind has no OpenCode link.

> **Warning:** version 1 has no application login or authentication. Any network peer that can reach the dashboard can invoke its management operations. Use Tailnet ACLs and the Daemon host's firewall as the access boundary, and do not expose the dashboard or OpenCode origins to an untrusted network.

For direct access through a Tailscale IP, bind the dashboard to that specific host address and publish both ports:

```yaml
dashboard_listen_address: 100.101.102.103:39356
dashboard_public_url: http://100.101.102.103:39356
opencode_public_url: http://100.101.102.103:39155
```

A direct MagicDNS deployment uses the Daemon host's concrete MagicDNS name in the same way:

```yaml
dashboard_listen_address: coder.example-tailnet.ts.net:39356
dashboard_public_url: http://coder.example-tailnet.ts.net:39356
opencode_public_url: http://coder.example-tailnet.ts.net:39155
```

For a reverse proxy running on the Daemon host, keep the dashboard on loopback and use dedicated dashboard and OpenCode root origins, preferably restricted to the tailnet:

```yaml
dashboard_listen_address: 127.0.0.1:39356
dashboard_public_url: https://dashboard.coder.example
opencode_public_url: https://opencode.coder.example
```

Proxy the complete `https://dashboard.coder.example` origin to `http://127.0.0.1:39356` and the complete `https://opencode.coder.example` origin to `http://127.0.0.1:39155`. Path-prefix deployments such as `https://coder.example/tmux-coder` or `/opencode` are not supported: both public URL settings accept only root origins, the dashboard uses root `/api` and asset paths, and OpenCode conversation routes are generated from the OpenCode origin root.

The OpenCode URL used by TC Agents remains separate. Daemon-managed agents attach through the internal loopback URL returned by the shared server manager; `TMUX_CODER_OPENCODE_SERVER_URL` can instead select an externally owned server. Neither internal attachment path is replaced by `opencode_public_url`.

Start an agent from inside a tmux-coder-managed session:

```sh
tmux-coder new opencode --name frontend
tmux-coder new --model anthropic/claude-haiku --prompt "Review the current changes"
tmux-coder new --model openai/gpt-5.6-luna --variant high
tmux-coder new --yolo
```

If no executable is given, `opencode` is used.

OpenCode creation accepts a canonical `provider/model` through `--model`, an
OpenCode model variant through `--variant`, a non-empty initial `--prompt`, and
`--yolo` to auto-approve permissions that are not explicitly denied.
`--variant` requires `--model`; model and prompt can otherwise be used alone.
Model and variant selection on the displayed conversation are verified before
an initial prompt is submitted.
Omitting `--variant` uses OpenCode's default variant. Startup failure stops
the new agent rather than falling back to another model or variant or dropping
the prompt. These options are per creation and are not supported by other Agent
Kinds.

The TUI asks the same explicit yolo question when creating an OpenCode agent.
Yolo mode is per creation and is never stored as a default.

`--session-id ID` targets any managed Session and derives its Project. Supplying
`--project-id` as well validates that it matches. An explicit Session creates a
new owned window unless `--pane %ID` explicitly identifies a pane in that
Session.

## Menu Actions

Open the action menu from inside a tmux-coder-managed Session:

```sh
tmux-coder menu
tmux-coder m
```

Global Menu Actions live in `~/.tmux-coder/actions.toml`. Project actions live
in the Project's `.tmux-coder/.tmux-coder.toml` Config File. Both use the same
declaration shape:

```toml
[[menu-actions]]
name = "commit"
description = "Commit the current changes"
key = "c"
script = "actions/commit"
argument = "none"
detach = false

[[menu-actions]]
name = "fix-issues"
description = "Fix the described issues"
script = ".tmux-coder/actions/fix-issues"
argument = "required"
detach = true
```

`name` must be a unique lowercase kebab-case token and `script` is required.
`key` is an optional single printable character. `argument` is `none`,
`optional`, or `required`, and defaults to `none`. `detach` defaults to `false`;
set it to `true` only when the script can safely continue after the menu exits.
Unknown fields and invalid or duplicate declarations prevent the menu from
opening.

Press a displayed direct key to select that action. Any other printable
character starts fuzzy search across actions without keys. In fuzzy search,
the first space separates the query from one argument value:

```text
fx- repair the failing API tests
```

Use up/down or `j`/`k` to select a fuzzy result and Enter to run it. Escape or
Ctrl-C cancels. A direct-key action that accepts an argument opens a separate
prompt.

Project declarations replace global declarations with the same name while
retaining the global action's list position; Project-only actions append in
declaration order. Overrides replace the complete declaration without
field-level inheritance. Direct keys must be unique after this merge.

Relative global scripts resolve from `~/.tmux-coder`. Relative Project scripts
resolve from the current checkout root: the Project base directory for a Main
Session, or the owning worktree root for a Worktree Session and its Secondary
Sessions. The executable itself runs in the exact managed Session directory, so
a Secondary can resolve a checkout-level script while running in a nested
directory. Absolute script paths are also accepted. Scripts must be executable
regular files and are launched directly through their shebang.

The script inherits the menu process environment. tmux-coder overwrites these
context variables with authoritative values:

- `TMUX_CODER_ACTION_NAME`
- `TMUX_CODER_ACTION_ARGUMENT`
- `TMUX_CODER_PROJECT_ID`
- `TMUX_CODER_PROJECT_ROOT`
- `TMUX_CODER_PROJECT_TITLE`
- `TMUX_CODER_SESSION_ID`
- `TMUX_CODER_SESSION_NAME`
- `TMUX_CODER_SESSION_TYPE`
- `TMUX_CODER_TMUX_SESSION_NAME`
- `TMUX_CODER_SESSION_ROOT`
- `TMUX_CODER_WORKTREE_ROOT`
- `TMUX_CODER_WORKING_DIRECTORY`
- `TMUX_CODER_BRANCH`

The action argument is available only through `TMUX_CODER_ACTION_ARGUMENT`; it
is not interpolated into a shell command or passed as shell source. By default,
the script runs interactively in the current terminal and its exit status
becomes the exit status of `tmux-coder menu`. With `detach = true`, the script
starts with the current terminal streams and `tmux-coder menu` exits without
waiting for it.

You can use any executable really, but it needs to have an extension or hooks set up so that it passes the agent's state to the daemon. Currently only opencode and claude code have been set up (opencode works much better). More coming soon.

OpenCode agents share one headless server owned by the daemon. Each agent pane
runs an attached TUI in its own working directory, so concurrent agents avoid
duplicating the server process. Set `TMUX_CODER_OPENCODE_SERVER_URL` to use an
already-running **v2** server instead. Install OpenCode v2 globally and confirm
`opencode --version` reports v2 before starting the Daemon:

```sh
npm install -g @opencode/cli@2.0.16
opencode --version
```

If another `opencode` shadows the global install on PATH, select the v2
executable with `TMUX_CODER_OPENCODE_BINARY` on the Daemon. The Daemon passes
that path to its agent panes.

The managed server generates a password per Daemon unless `OPENCODE_PASSWORD`
is set on the Daemon. For a browser, configure a known `OPENCODE_PASSWORD` on
the Daemon and enter the OpenCode origin and password on its `/connect` page
before following dashboard conversation links. The password is passed to pane
clients through the internal loopback API; it is never placed in dashboard links.
An external server must be reachable with the Daemon's `OPENCODE_PASSWORD`
(or `OPENCODE_SERVER_PASSWORD`), and must return v2 `/api/info`.
The managed server still listens on `0.0.0.0` at
`opencode_server_port`, even though Daemon-managed TC Agents receive its loopback
attachment URL. Its embedded web UI is therefore available through localhost,
LAN, and Tailscale addresses that the host firewall and Tailnet ACLs permit.
tmux-coder does not configure either boundary; the operator is responsible for
preventing unintended access.
Model-selected agents select a model and optional variant on their new v2
conversation and verify the session's model before submitting the prompt;
they do not rewrite durable OpenCode model preferences.

`./dev install` installs and configures the bundled OpenCode v2 CLI plugin so
activity remains associated with the attached TUI's agent ID. For a manual
installation, add the bundled plugin directory URL to
`~/.config/opencode/cli.json` under `plugins`. Reinstalling removes only the
old tmux-coder entry from `tui.json` and does not duplicate the CLI entry.
Remove any older `tmux-coder.js` entry from `opencode.json`:

```json
{
  "plugin": ["file:///path/to/tmux-coder/integrations/opencode"]
}
```

Install Claude Code activity hooks again if the binary path changes:

```sh
tmux-coder install-claude-hooks
```

# How it works

## daemon (http server)

`tmux-coderd` is auto-started by the CLI when needed.

It listens on `127.0.0.1:${TMUX_CODERD_PORT:-64357}`, owns runtime state, manages the dedicated tmux server, creates/removes git worktrees, runs worktree hooks, and tracks agent status.

Daemon environment variables can be inherited from the process supervisor or set in `~/.tmux-coder/.env`. Inherited values take precedence. The daemon never reads a Project's `.env`.

Project/session/agent state is currently in memory and rebuilt on daemon start.

## cli

`tmux-coder` is the client.

It starts the daemon if needed, calls its HTTP API, renders the TUI, attaches to tmux sessions, and provides helper subcommands such as `new` or `acquire-port`

Hidden helper modes report agent lifecycle/activity back to the daemon:

- `agent-wrapper`: starts and watches an agent process
- `agent-event`: used by agent hooks to report `busy`, `idle`, or `waiting`

## isolated tmux server (for session separation)

tmux-coder uses a dedicated tmux server instead of your default tmux server:

```sh
tmux -L "${TMUX_CODER_TMUX_SERVER:-tmux-coder}"
```

Each tmux-coder Session maps to one tmux session on that server. This keeps project/worktree sessions separate from normal tmux usage.

# Worktree hooks

Worktree hooks are tmux-coder hooks, not Git hooks.

Add this file to a project you open with tmux-coder:

```toml
# .tmux-coder/.tmux-coder.toml
[worktree]
on-create-script = ".tmux-coder/setup-worktree.sh"
on-create-timeout = "2m"
on-destroy-script = ".tmux-coder/teardown-worktree.sh" # optional
on-destroy-timeout = "2m"                      # optional
```

Create the script and make it executable:

```sh
mkdir -p .tmux-coder
$EDITOR .tmux-coder/setup-worktree.sh
chmod +x .tmux-coder/setup-worktree.sh
```

The create hook runs in the new worktree root after the Session is recorded, in its `worktree-setup` window. Setup continues asynchronously; a failure remains visible until acknowledged, then tmux-coder rolls back the worktree creation.

The optional destroy hook runs synchronously in the worktree root before `git worktree remove`, including for adopted Worktree Sessions. It does not run for Secondary Sessions, external-removal reconciliation, or failed-creation rollback. Non-Force deletion stops on hook failure or timeout; Force still waits for the hook, but continues removing the worktree if it fails. Invalid config or a missing/non-executable script blocks even Force deletion. Without Force, a dirty worktree is rejected before the hook runs. Hook output is retained in daemon hook logs.

The hook receives:

- `TMUX_CODER_PROJECT_ROOT`
- `TMUX_CODER_WORKTREE_ROOT`
- `TMUX_CODER_PROJECT_ID`
- `TMUX_CODER_SESSION_NAME`
- `TMUX_CODER_TMUX_SESSION_NAME`
- `TMUX_CODER_BRANCH`
- `TMUX_CODER_HOOK_TOKEN`

The destroy hook receives the same descriptive metadata plus `TMUX_CODER_SESSION_ID`, but **not** a hook token or new provisional port leases. Its script and timeout are read from the project's current Config File at deletion. See [the worktree hook guide](docs/worktree-hook-integration.md) for the full lifecycle.

Use `tmux-coder acquire-port` inside the hook to reserve ports without collisions:

```sh
web_port="$(tmux-coder acquire-port web --start 3001 --end 3099)"
api_port="$(tmux-coder acquire-port api --start 4001 --end 4099)"
```

Minimal hook example:

```sh
#!/usr/bin/env bash
set -euo pipefail

slug="$(printf '%s' "$TMUX_CODER_BRANCH" | tr -c 'a-zA-Z0-9' '_' | tr 'A-Z' 'a-z')"
web_port="$(tmux-coder acquire-port web --start 3000 --end 3099)"

cat > .env <<EOF
DATABASE_NAME=myapp_${slug}
WEB_PORT=${web_port}
COMPOSE_PROJECT_NAME=myapp_${slug}
EOF
```

The hook can only isolate what your project makes configurable. Put ports, database names, cache prefixes, compose project names, etc. behind env/config values and have all local tooling read them.
