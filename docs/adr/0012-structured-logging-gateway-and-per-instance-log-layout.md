# 12. Structured logging gateway and per-instance log layout

## Status

Accepted

## Context

The daemon had effectively no observability: three stdlib `log.Printf`/`log.Fatal`
calls in the composition root and scattered `fmt.Fprintf(os.Stderr, …)` elsewhere.
The daemon's own output, when it was auto-launched by the **Client**
(`internal/client/daemon`), was redirected to a throwaway `/tmp/tmux-coderd-*.log`
temp file — the only crash channel that existed. None of the usecase or infra
layers logged anything; failures in the tmux/git subprocesses the daemon's whole
job depends on were invisible.

Two facts about how this project is developed shaped the design more than anything
else. First, dev builds are already isolated from the installed build: `scripts/build`
bakes a per-worktree daemon port and tmux server **label** into each binary via
`-ldflags -X` (`daemonaddr.DefaultPort`, `tmuxserver.DefaultLabel`), so a worktree's
**Daemon**, **Client**, wrapper, and agent-event subcommand all share the label
`tmux-coder-<worktree>` while the installed binaries default to `tmux-coder`. Second,
several `tmux-coder` instances run in parallel during agent/worktree development, so
their logs must not interleave on disk.

## Decision

Add a logging gateway as a port, following the existing ports/adapters style.
A new leaf package `internal/obs` declares the interface and provides a single
`log/slog`-backed implementation; no layer depends on slog directly.

```go
type Logger interface {
	Debug(ctx context.Context, msg string, kv ...any)
	Info(ctx context.Context, msg string, kv ...any)
	Warn(ctx context.Context, msg string, kv ...any)
	Error(ctx context.Context, msg string, kv ...any)
	With(kv ...any) Logger
}
```

The interface is **key/value** (slog-native variadic `...any`) and **ctx-aware**.
There is no `Fatal` (a hidden `os.Exit` in a logging library is a footgun) and no
`Enabled` level check. Levels run **hardcoded at DEBUG** for now — a single named
constant in `obs`, no env var — so everything is visible while the feature beds in;
making it configurable later is a one-liner.

**Library choice.** `log/slog` (stdlib, Go 1.22) for the structured surface and
`gopkg.in/natefinch/lumberjack.v2` for size-based rotation. slog is key/value-native
with a JSON handler and adds no dependency; lumberjack is the de-facto Go rotation
writer. zap/zerolog were rejected as unjustified dependencies for this scale.

**Injection.** The composition root (`cmd/tmux-coderd`) builds one base logger and
injects it into all 11 usecases and all infra gateways (`tmux`, `git`, `hookexec`,
`process`, `netport`) as a required constructor param; each constructor tags itself
with `.With("component", …)`. `domain` stays pure — logging is a side effect and has
no place there. Tests pass a no-op `obs.Nop()`. The HTTP edge gains access-log
middleware that also mints a `request_id` (`crypto/rand`), stores it in the request
`ctx`, and a context-aware slog handler appends it to every line logged downstream —
so a single request is traceable across layers without manual threading.

**Per-instance file layout.** The log path is derived from the instance identity the
binaries already carry — `tmuxserver.Label(os.Getenv)` — so no new config is needed
and every process of an instance agrees on the path for free:

```
~/.tmux-coder/logs/
├── daemon/        tui/        agent-event/        wrapper/     ← installed (label "tmux-coder")
└── dev-<worktree>/
    └── daemon/    tui/        agent-event/        wrapper/     ← dev build (label "tmux-coder-<worktree>")
```

The installed build (`label == "tmux-coder"`) logs at the top level; a dev build logs
under `dev-<worktree>` (the label with its `tmux-coder-` prefix stripped). The four
roles map to processes: `daemon` = `tmux-coderd`; `tui` = the default `tmux-coder`
client mode (TUI, `open`, `new`, `acquire-port`); `agent-event` and `wrapper` = the
respective subcommands. `install-claude-hooks` writes no file (it prints to the user's
stdout). Each process selects its role once, at startup, from argv.

**Hybrid file granularity** — because the roles have very different lifetimes:

- `daemon`, `tui`, `wrapper` are long-lived (one process at a time, or one per
  agent), so each owns a `<pid>.log` rotated in place by lumberjack
  (50 MB, 5 backups, gzip). Single-writer, so lumberjack rotation is safe.
- `agent-event` is spawned fresh on **every Claude Code hook fire** (sub-second,
  frequent). A `<pid>.log` per invocation would carpet the directory with thousands
  of one-line files. Instead it appends to a **shared per-day** file
  `<YYYY-MM-DD>.log` opened `O_APPEND`: no renames, so concurrent short-lived
  processes are safe by construction, and "rotation" is simply a new file each day.

`pid` is a field on every line regardless of role, so the shared agent-event file
stays sliceable by process. Cross-run cleanup (which lumberjack cannot do — it only
manages backups of its *own* base name, never a previous PID's file or a past day's
file) is a 14-day age sweep each process runs over its own role directory at startup.

**Raw output channel.** slog cannot capture Go panics, runtime crashes, or stray
prints — those exit via the process's OS-level stderr. The Client's launcher
(`internal/client/daemon`) is changed to redirect the daemon's stdout/stderr into the
instance's `daemon/` directory (a plain `boot-<starttime>.log`, named by start time
because the child PID is unknown before fork) instead of `/tmp`. So structured logs
and crash output now live in the same per-instance tree.

## Consequences

- Every usecase and infra constructor signature gains a `Logger` param; existing
  tests are updated to pass `obs.Nop()`. The change is mechanical but wide.
- Log location is a pure function of the tmux server label. A future reader who
  changes the label scheme, or expects logs in a fixed place, must know the path
  tracks the label — this ADR records why. The upside is that dev isolation, daemon
  discovery, and log routing all derive from one identity with zero extra config.
- The two file-granularity rules are a deliberate deviation from a uniform
  `<pid>.log` scheme. A reader may try to "simplify" agent-event to per-PID files and
  silently reintroduce the file-explosion this avoids.
- Atomic append for the shared agent-event file holds only while each record is a
  single write under ~4 KB (PIPE_BUF). Normal status records are far smaller;
  a pathologically large DEBUG payload could interleave. Accepted for this role.
- DEBUG is hardcoded, so logs are voluminous; the 50 MB/5-backup rotation and the
  14-day sweep bound disk use, which matters because many instances run in parallel.
- `internal/obs` is a leaf with no domain/usecase deps, so any layer can import it
  without cycles.
