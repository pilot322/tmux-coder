// Package obs is tmux-coder's logging gateway: a small key/value, context-aware
// Logger interface with one log/slog-backed implementation, so no other layer
// depends on slog directly. The composition root builds one base logger and
// injects it into the usecases and infra gateways; the domain stays pure. See
// ADR-0012.
package obs

import "context"

// Logger is the logging port. It is key/value (slog-native variadic) and
// context-aware so a request id stored in ctx surfaces on every line without
// manual threading. There is deliberately no Fatal (a hidden os.Exit is a
// footgun) and no level check — levels run hardcoded at DEBUG for now.
type Logger interface {
	Debug(ctx context.Context, msg string, kv ...any)
	Info(ctx context.Context, msg string, kv ...any)
	Warn(ctx context.Context, msg string, kv ...any)
	Error(ctx context.Context, msg string, kv ...any)
	// With returns a Logger that tags every line with the given key/value
	// pairs; constructors use it to stamp a "component" on their logs.
	With(kv ...any) Logger
}

// nopLogger discards everything. Nop is the logger tests pass when they don't
// assert on output, and the safe default anywhere a real logger is absent.
type nopLogger struct{}

// Nop returns a Logger that emits nothing.
func Nop() Logger { return nopLogger{} }

func (nopLogger) Debug(context.Context, string, ...any) {}
func (nopLogger) Info(context.Context, string, ...any)  {}
func (nopLogger) Warn(context.Context, string, ...any)  {}
func (nopLogger) Error(context.Context, string, ...any) {}
func (nopLogger) With(...any) Logger                    { return nopLogger{} }
