package obs

import (
	"net/http"
	"time"
)

// AccessLog wraps a handler so every request mints a request id, carries it in
// the request context (where ctxHandler picks it up for downstream lines), and
// emits one access line on completion with method, path, status and latency.
func AccessLog(log Logger) func(http.Handler) http.Handler {
	log = log.With("component", "http")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithRequestID(r.Context(), NewRequestID())
			r = r.WithContext(ctx)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			start := time.Now()
			next.ServeHTTP(rec, r)

			log.Info(ctx, "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"latency_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// statusRecorder captures the response status code so the access line can
// report it. A handler that writes a body without calling WriteHeader implies
// 200, which is the zero-configuration default.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}
