// Package discordnotify delivers one-shot Discord notifications through a
// configured Discord webhook. Notification policy and content live in usecase;
// this package only implements bounded HTTP delivery.
package discordnotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const requestTimeout = 3 * time.Second

// ErrDelivery is returned for every failed Discord delivery. The underlying
// HTTP error and webhook response are intentionally hidden because either can
// contain the secret webhook URL or token.
var ErrDelivery = errors.New("discord notification delivery failed")

// DiscordNotifier is the transport shape consumed by the usecase package.
type DiscordNotifier interface {
	Notify(ctx context.Context, content string) error
}

type Notifier struct {
	webhookURL string
	client     *http.Client
}

// NewNotifier returns a webhook-backed notifier, or a no-op notifier when the
// webhook is blank.
func NewNotifier(webhook string) DiscordNotifier {
	return NewNotifierWithClient(webhook, http.DefaultClient)
}

// NewNotifierWithClient is NewNotifier with an injectable HTTP client for
// tests and custom transports.
func NewNotifierWithClient(webhook string, client *http.Client) DiscordNotifier {
	webhook = strings.TrimSpace(webhook)
	if webhook == "" {
		return NoopNotifier{}
	}
	if client == nil {
		client = http.DefaultClient
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Notifier{webhookURL: webhook, client: &clientCopy}
}

func (n *Notifier) Notify(ctx context.Context, content string) error {
	body, err := json.Marshal(struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}{Content: content, AllowedMentions: struct {
		Parse []string `json:"parse"`
	}{Parse: []string{}}})
	if err != nil {
		return ErrDelivery
	}

	requestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return ErrDelivery
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil || resp == nil {
		return ErrDelivery
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ErrDelivery
	}
	return nil
}

// NoopNotifier discards Discord notifications when no webhook is configured.
type NoopNotifier struct{}

func (NoopNotifier) Notify(context.Context, string) error { return nil }
