package opencodeurl_test

import (
	"testing"

	"github.com/pilot322/tmux-coder/internal/opencodeurl"
)

func TestSession(t *testing.T) {
	tests := []struct {
		name      string
		origin    string
		sessionID string
		want      string
	}{
		{
			name:      "MagicDNS origin with trailing slash",
			origin:    "http://coder.tailnet.ts.net:4096/",
			sessionID: "ses_magic",
			want:      "http://coder.tailnet.ts.net:4096/server/aHR0cDovL2NvZGVyLnRhaWxuZXQudHMubmV0OjQwOTY/session/ses_magic",
		},
		{
			name:      "Tailscale IP origin",
			origin:    "http://100.101.102.103:4096",
			sessionID: "ses_ip",
			want:      "http://100.101.102.103:4096/server/aHR0cDovLzEwMC4xMDEuMTAyLjEwMzo0MDk2/session/ses_ip",
		},
		{
			name:      "HTTPS origin and opaque escaped session ID",
			origin:    "https://opencode.example.com///",
			sessionID: "ses/a+b ?#%",
			want:      "https://opencode.example.com/server/aHR0cHM6Ly9vcGVuY29kZS5leGFtcGxlLmNvbQ/session/ses%2Fa+b%20%3F%23%25",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := opencodeurl.Session(tt.origin, tt.sessionID); got != tt.want {
				t.Fatalf("Session() = %q, want %q", got, tt.want)
			}
		})
	}
}
