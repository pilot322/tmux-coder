package obs

import (
	"bytes"
	"context"
	"testing"
)

func TestRequestIDSurfacesFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := newWithWriter(&buf, RoleDaemon)

	ctx := WithRequestID(context.Background(), "req-abc123")
	log.Info(ctx, "handled")

	line := decodeLines(t, &buf)[0]
	if line["request_id"] != "req-abc123" {
		t.Fatalf("line[request_id] = %v, want %q", line["request_id"], "req-abc123")
	}
}

func TestRequestIDAbsentWhenNotInContext(t *testing.T) {
	var buf bytes.Buffer
	log := newWithWriter(&buf, RoleDaemon)

	log.Info(context.Background(), "handled")

	line := decodeLines(t, &buf)[0]
	if _, ok := line["request_id"]; ok {
		t.Fatalf("request_id should be absent when not in context, got %v", line["request_id"])
	}
}

func TestNewRequestIDIsNonEmptyAndUnique(t *testing.T) {
	a, b := NewRequestID(), NewRequestID()
	if a == "" {
		t.Fatal("NewRequestID returned empty")
	}
	if a == b {
		t.Fatalf("NewRequestID not unique: %q == %q", a, b)
	}
}
