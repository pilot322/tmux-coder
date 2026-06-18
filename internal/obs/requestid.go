package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

// ctxKey is unexported so the request-id value can't collide with another
// package's context keys.
type ctxKey int

const requestIDKey ctxKey = iota

// WithRequestID returns a context carrying id. The HTTP edge mints one per
// request and stores it here; ctxHandler then stamps it on every line logged
// downstream, so one request is traceable across layers without threading.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom returns the request id stored in ctx, if any.
func RequestIDFrom(ctx context.Context) (string, bool) {
	return requestIDFrom(ctx)
}

// requestIDFrom returns the request id stored in ctx, if any.
func requestIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey).(string)
	return id, ok && id != ""
}

// NewRequestID mints a short random hex id from crypto/rand.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// ctxHandler decorates a slog.Handler with values pulled from the context at
// emit time — currently the request id minted at the HTTP edge.
type ctxHandler struct {
	slog.Handler
}

func (h ctxHandler) Handle(ctx context.Context, rec slog.Record) error {
	if id, ok := requestIDFrom(ctx); ok {
		rec.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, rec)
}

func (h ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ctxHandler{h.Handler.WithAttrs(attrs)}
}

func (h ctxHandler) WithGroup(name string) slog.Handler {
	return ctxHandler{h.Handler.WithGroup(name)}
}
