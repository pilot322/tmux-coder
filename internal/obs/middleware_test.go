package obs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessLogEmitsOneLinePerRequest(t *testing.T) {
	rec := Recording()
	var sawID string
	handler := AccessLog(rec)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := requestIDFrom(r.Context()); ok {
			sawID = id
		}
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodPost, "/agents", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	records := rec.Records()
	if len(records) != 1 {
		t.Fatalf("got %d access lines, want 1: %v", len(records), records)
	}
	line := records[0]
	if line["method"] != http.MethodPost {
		t.Errorf("method = %v, want POST", line["method"])
	}
	if line["path"] != "/agents" {
		t.Errorf("path = %v, want /agents", line["path"])
	}
	if line["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v, want %d", line["status"], http.StatusTeapot)
	}
	if _, ok := line["latency_ms"]; !ok {
		t.Error("access line missing latency_ms")
	}
	if line["component"] != "http" {
		t.Errorf("component = %v, want http", line["component"])
	}

	if sawID == "" {
		t.Fatal("downstream handler saw no request_id in context")
	}
	if line["request_id"] != sawID {
		t.Errorf("access line request_id = %v, want %q (same id the handler saw)", line["request_id"], sawID)
	}
}

func TestAccessLogDefaultsStatusToOK(t *testing.T) {
	rec := Recording()
	handler := AccessLog(rec)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler that never calls WriteHeader: status should record as 200.
		_, _ = w.Write([]byte("ok"))
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/projects", nil))

	line := rec.Records()[0]
	if line["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want 200 when handler writes body without WriteHeader", line["status"])
	}
}
