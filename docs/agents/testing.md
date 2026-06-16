# Testing

How humans and agents test changes in this repo. Two layers: fast unit tests, and end-to-end runs against a real, isolated tmux-coder instance.

## Unit tests

```
go test ./...
```

Run from the repo root. This is the default after any change — fast, hermetic, no daemon or tmux server required. Use the standard `go test` flags as needed (`-run`, `-v`, `-race`, `./internal/...`).

## End-to-end testing

Unit tests don't cover the daemon ↔ client ↔ tmux wiring. For anything touching session lifecycle, agent orchestration, the TUI, or tmux behaviour, **build the binaries and drive a real instance**. Don't reason about it from the source alone.

### Why this is safe to do

Every dev build is isolated. `./dev build` bakes a per-worktree daemon port and tmux server label into the binary via `-ldflags`, both derived from the worktree path. So the instance you spin up here **cannot touch the installed (prod) tmux-coder or any other worktree's instance** — it gets its own daemon and its own tmux server. The installed binary keeps the shipped `tmux-coder` / port `64357` defaults; your dev build does not.

### 1. Build

```
./dev build daemon
./dev build client
```

Build both. `./dev build` prints the port and tmux server label it baked in, e.g.:

```
  bin/ dev build → port 64481, tmux server tmux-coder-<worktree>
```

### 2. Get the binary paths and the server label

```
./dev path client    # absolute path to bin/tmux-coder
./dev path daemon    # absolute path to bin/tmux-coderd
```

The tmux server label is `tmux-coder-<worktree-dir-name>` (the same string `./dev build` printed). You need it to inspect or send keys into tmux-coder's own sessions and agent panes:

```
tmux -L tmux-coder-<worktree> list-sessions
```

You do **not** need to start the daemon by hand — running the built client auto-starts its matching daemon (it resolves `tmux-coderd` sitting next to it in `bin/`). Building the daemon first just guarantees the fresh binary is the one that gets started.

### 3. Drive it with tmux send-keys

The client is a TUI. To drive it non-interactively, run it inside a tmux pane you control, send keys to navigate, and capture the pane to read state:

```
CLIENT="$(./dev path client)"
tmux new-session -d -s tc-test "$CLIENT"        # client boots the isolated daemon
tmux send-keys  -t tc-test j Enter              # navigate the TUI
tmux capture-pane -p -t tc-test                 # read what's on screen
```

There are two tmux layers — keep them straight:

- The **outer** session above (`tc-test`) just hosts the client process so you can drive its TUI.
- The **inner**, isolated server `tmux -L tmux-coder-<worktree>` is the one tmux-coder itself manages — its Sessions, agent panes, etc. Use the `-L <label>` form to list those sessions, capture an agent's pane, or `send-keys` directly into a running agent.

### Test thoroughly — but don't let tests stall

Cover the real behaviour. At the same time, take active steps to keep the loop short:

- **Keep waits tight.** When polling live status (agent state, TUI refresh), cap waits at ~15s and poll in small increments rather than sleeping for a long fixed block. Most state settles in seconds; if it hasn't after ~15s, something is wrong — investigate, don't wait longer.
- **Spinning up a live agent? Use a fast, cheap model.** A TC Agent is a real coding-agent process (Claude Code, Codex, …), so a careless test can burn through expensive model calls. Before launching one, **prompt the human to configure the agent instance to use a fast, cheap model** (e.g. Haiku) for the test run. Don't drive a live agent on an expensive default model just to exercise the plumbing.

## Cleanup

When you're done, tear down **this worktree's** instance from the worktree's working directory:

```
./dev kill
```

That kills only the daemon built from this worktree's `bin/tmux-coderd` and its matching tmux server.

> **NEVER run `./dev kill -a`.** The `-a` flag kills *every* tmux-coder daemon across *all* worktrees and the prod install, taking down instances other humans and agents are actively using. Always use the bare `./dev kill` scoped to your worktree.
