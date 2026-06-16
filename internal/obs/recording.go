package obs

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

// Recorder is a Logger that keeps every emitted line in memory so a test can
// assert on what was logged. It satisfies Logger through the embedded value, so
// it drops straight into any constructor that wants one; loggers derived from it
// with With share the same buffer, so Records sees their output too.
type Recorder struct {
	Logger
	buf *syncBuffer
}

// Recording returns a Recorder. Tests that assert on output pass it where a
// Logger is expected and read it back with Records.
func Recording() *Recorder {
	buf := &syncBuffer{}
	return &Recorder{Logger: newWithWriter(buf, RoleDaemon), buf: buf}
}

// Records parses and returns every JSON line emitted so far.
func (r *Recorder) Records() []map[string]any {
	var out []map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(r.buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

// syncBuffer is a bytes.Buffer guarded by a mutex so a test reading Records
// while a handler is still writing can't race the buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
