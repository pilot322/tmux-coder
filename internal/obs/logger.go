package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"
)

// defaultLevel is the single, hardcoded verbosity. Everything is logged at
// DEBUG while the feature beds in; making this configurable later is a one-liner.
const defaultLevel = slog.LevelDebug

// slogLogger adapts a *slog.Logger to the Logger port. The pid and role base
// attributes are baked in at construction; callers add a component with With.
type slogLogger struct {
	l *slog.Logger
}

// newWithWriter builds a Logger that writes JSON Lines to w. It is the seam the
// file-sink wiring and the format tests share: the sink decides where bytes go,
// this decides how they are shaped.
func newWithWriter(w io.Writer, role Role) Logger {
	handler := ctxHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource: true,
		Level:     defaultLevel,
	})}
	base := slog.New(handler).With("pid", os.Getpid(), "role", string(role))
	return &slogLogger{l: base}
}

func (s *slogLogger) Debug(ctx context.Context, msg string, kv ...any) {
	s.log(ctx, slog.LevelDebug, msg, kv...)
}

func (s *slogLogger) Info(ctx context.Context, msg string, kv ...any) {
	s.log(ctx, slog.LevelInfo, msg, kv...)
}

func (s *slogLogger) Warn(ctx context.Context, msg string, kv ...any) {
	s.log(ctx, slog.LevelWarn, msg, kv...)
}

func (s *slogLogger) Error(ctx context.Context, msg string, kv ...any) {
	s.log(ctx, slog.LevelError, msg, kv...)
}

func (s *slogLogger) With(kv ...any) Logger {
	return &slogLogger{l: s.l.With(kv...)}
}

// log builds the record by hand so AddSource reports the original call site
// rather than this wrapper. The PC is captured three frames up: past
// runtime.Callers, past log, and past the exported Debug/Info/... method.
func (s *slogLogger) log(ctx context.Context, level slog.Level, msg string, kv ...any) {
	if !s.l.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	rec := slog.NewRecord(time.Now(), level, msg, pcs[0])
	rec.Add(kv...)
	_ = s.l.Handler().Handle(ctx, rec)
}
