package discordnotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/usecase"
)

const testWebhook = "https://discord.com/api/webhooks/123/super-secret-token"

// bare wraps a plain-text payload so the non-shape tests stay focused on
// transport behavior and don't have to repeat the embed struct.
func bare(s string) usecase.DiscordMessage { return usecase.DiscordMessage{Content: s} }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func clientWithTransport(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func response(status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("response body"))}
}

func TestNotifierPostsJSONContent(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody struct {
		Content string `json:"content"`
		Embeds  []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Color       int    `json:"color"`
			Fields      []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Inline bool   `json:"inline"`
			} `json:"fields"`
			Footer struct {
				Text string `json:"text"`
			} `json:"footer"`
		} `json:"embeds"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	client := clientWithTransport(func(req *http.Request) (*http.Response, error) {
		gotMethod = req.Method
		gotContentType = req.Header.Get("Content-Type")
		if err := json.NewDecoder(req.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusNoContent), nil
	})
	n := NewNotifierWithClient(testWebhook, client)

	msg := usecase.DiscordMessage{
		Content: "reviewer needs input · api · api.main",
		Embed: usecase.DiscordEmbed{
			Title:       "reviewer needs input",
			Description: "api · api.main",
			Color:       0xE74C3C,
			Fields: []usecase.DiscordField{
				{Name: "Project", Value: "api", Inline: true},
				{Name: "Session", Value: "api.main", Inline: true},
			},
		},
	}
	if err := n.Notify(context.Background(), msg); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody.Content != "reviewer needs input · api · api.main" {
		t.Errorf("content = %q, want summary line shown in mobile push", gotBody.Content)
	}
	if len(gotBody.Embeds) != 1 {
		t.Fatalf("embeds = %#v, want exactly one", gotBody.Embeds)
	}
	embed := gotBody.Embeds[0]
	if embed.Title != "reviewer needs input" {
		t.Errorf("embed title = %q, want the title", embed.Title)
	}
	if embed.Description != "api · api.main" {
		t.Errorf("embed description = %q, want the body", embed.Description)
	}
	if embed.Color != 0xE74C3C {
		t.Errorf("embed color = %#x, want %#x", embed.Color, 0xE74C3C)
	}
	if len(embed.Fields) != 2 ||
		embed.Fields[0].Name != "Project" || embed.Fields[0].Value != "api" || !embed.Fields[0].Inline ||
		embed.Fields[1].Name != "Session" || embed.Fields[1].Value != "api.main" || !embed.Fields[1].Inline {
		t.Errorf("embed fields = %#v, want inline Project/Session", embed.Fields)
	}
	if embed.Footer.Text != "tmux-coder" {
		t.Errorf("embed footer = %q, want tmux-coder", embed.Footer.Text)
	}
	if gotBody.AllowedMentions.Parse == nil || len(gotBody.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed mention parse = %#v, want an empty list", gotBody.AllowedMentions.Parse)
	}
}

func TestNotifierOmitsEmbedWhenBare(t *testing.T) {
	var gotBody struct {
		Content string `json:"content"`
		Embeds  []struct {
			Title string `json:"title"`
		} `json:"embeds"`
	}
	n := NewNotifierWithClient(testWebhook, clientWithTransport(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return response(http.StatusNoContent), nil
	}))
	if err := n.Notify(context.Background(), usecase.DiscordMessage{Content: "agent is idle"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if gotBody.Content != "agent is idle" {
		t.Errorf("content = %q, want plain text", gotBody.Content)
	}
	if gotBody.Embeds != nil {
		t.Errorf("embeds = %#v, want omitted for a bare message", gotBody.Embeds)
	}
}

func TestNotifierAcceptsAny2xxResponse(t *testing.T) {
	for _, status := range []int{200, 201, 204, 299} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			n := NewNotifierWithClient(testWebhook, clientWithTransport(func(*http.Request) (*http.Response, error) {
				return response(status), nil
			}))
			if err := n.Notify(context.Background(), bare("content")); err != nil {
				t.Fatalf("Notify status %d: %v", status, err)
			}
		})
	}
}

func TestNotifierReturnsRedactedErrorForNon2xx(t *testing.T) {
	n := NewNotifierWithClient(testWebhook, clientWithTransport(func(*http.Request) (*http.Response, error) {
		return response(http.StatusBadRequest), nil
	}))

	err := n.Notify(context.Background(), bare("content"))
	if !errors.Is(err, ErrDelivery) {
		t.Fatalf("Notify error = %v, want ErrDelivery", err)
	}
	assertRedacted(t, err)
}

func TestNotifierReturnsRedactedTransportError(t *testing.T) {
	n := NewNotifierWithClient(testWebhook, clientWithTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request to " + testWebhook + " failed")
	}))

	err := n.Notify(context.Background(), bare("content"))
	if !errors.Is(err, ErrDelivery) {
		t.Fatalf("Notify error = %v, want ErrDelivery", err)
	}
	assertRedacted(t, err)
}

func TestNotifierBoundsRequestContext(t *testing.T) {
	client := clientWithTransport(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok {
			t.Fatal("request context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > requestTimeout {
			t.Fatalf("request deadline remaining = %v, want within %v", remaining, requestTimeout)
		}
		return response(http.StatusNoContent), nil
	})
	n := NewNotifierWithClient(testWebhook, client)
	if err := n.Notify(context.Background(), bare("content")); err != nil {
		t.Fatalf("Notify: %v", err)
	}
}

func TestNotifierIgnoresCallerCancellationButKeepsItsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n := NewNotifierWithClient(testWebhook, clientWithTransport(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			t.Fatalf("request inherited caller cancellation: %v", err)
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Fatal("request has no delivery deadline")
		}
		return response(http.StatusNoContent), nil
	}))

	if err := n.Notify(ctx, bare("content")); err != nil {
		t.Fatalf("Notify error = %v", err)
	}
}

func TestNotifierRejectsRedirects(t *testing.T) {
	var calls int
	n := NewNotifierWithClient(testWebhook, clientWithTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls > 1 {
			t.Fatal("followed redirect away from configured Discord webhook")
		}
		resp := response(http.StatusFound)
		resp.Header = http.Header{"Location": []string{"https://example.com/collect"}}
		return resp, nil
	}))
	if err := n.Notify(context.Background(), bare("content")); !errors.Is(err, ErrDelivery) {
		t.Fatalf("Notify error = %v, want ErrDelivery", err)
	}
	if calls != 1 {
		t.Fatalf("requests = %d, want one", calls)
	}
}

func TestBlankAndExplicitNoopNotifier(t *testing.T) {
	for _, notifier := range []usecase.DiscordNotifier{NewNotifier(""), NewNotifier("  "), NoopNotifier{}} {
		if err := notifier.Notify(context.Background(), bare("content")); err != nil {
			t.Fatalf("noop Notify: %v", err)
		}
	}
	if _, ok := NewNotifier(" ").(NoopNotifier); !ok {
		t.Fatalf("blank webhook should return NoopNotifier")
	}
}

func assertRedacted(t *testing.T, err error) {
	t.Helper()
	for _, secret := range []string{testWebhook, "super-secret-token"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked webhook secret: %v", err)
		}
	}
}
