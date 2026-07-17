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

Discord notifications use a webhook configured in `~/.tmux-coder/config.yaml`:

```yaml
discord_webhook_notify: https://discord.com/api/webhooks/WEBHOOK_ID/WEBHOOK_TOKEN
```

Create the webhook under your Discord server's **Server Settings > Integrations > Webhooks**, then replace the example value with its copied URL. Only official HTTPS Discord webhook URLs are accepted. The daemon reads this file once at startup, so restart it after changing the configuration. A missing file or key leaves Discord notifications disabled; invalid YAML, unknown keys, and invalid webhook URLs prevent daemon startup.

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
- `a`: create agent
- `n`: arm or disable a one-shot Discord notification for the selected agent
- `w`: create worktree from selected session
- `W`: create worktree from a base ref
- `s`: create secondary session
- `X`: delete selected item
- `f`: fuzzyfind
- `?`: help
- `q`: quit

Pressing `n` in Overview or Agents opens an enable/disable confirmation. An armed notification is consumed by the selected agent's next `busy` to `waiting` (needs input) or `busy` to `idle` transition. Other transitions leave it armed. Confirming again while armed disables it.

Start an agent from inside a tmux-coder-managed session:

```sh
tmux-coder new opencode --name frontend
```

If no executable is given, `opencode` is used.

You can use any executable really, but it needs to have an extension or hooks set up so that it passes the agent's state to the daemon. Currently only opencode and claude code have been set up (opencode works much better). More coming soon.

OpenCode agents share one headless server owned by the daemon. Each agent pane
runs an attached TUI in its own working directory, so concurrent agents avoid
duplicating the server process. Set `TMUX_CODER_OPENCODE_SERVER_URL` to use an
already-running server instead.

Configure the bundled OpenCode plugin in `~/.config/opencode/tui.json` so
activity remains associated with the attached TUI's agent ID. Remove any older
`tmux-coder.js` entry from `opencode.json`:

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
```

Create the script and make it executable:

```sh
mkdir -p .tmux-coder
$EDITOR .tmux-coder/setup-worktree.sh
chmod +x .tmux-coder/setup-worktree.sh
```

The hook runs in the new worktree root after `git worktree add` and before the session is recorded. If it fails or times out, tmux-coder rolls the worktree creation back.

The hook receives:

- `TMUX_CODER_PROJECT_ROOT`
- `TMUX_CODER_WORKTREE_ROOT`
- `TMUX_CODER_PROJECT_ID`
- `TMUX_CODER_SESSION_NAME`
- `TMUX_CODER_TMUX_SESSION_NAME`
- `TMUX_CODER_BRANCH`
- `TMUX_CODER_HOOK_TOKEN`

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
