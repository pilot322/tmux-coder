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

	"github.com/pilot322/tmux-coder/internal/usecase"
)

const requestTimeout = 3 * time.Second

// ErrDelivery is returned for every failed Discord delivery. The underlying
// HTTP error and webhook response are intentionally hidden because either can
// contain the secret webhook URL or token.
var ErrDelivery = errors.New("discord notification delivery failed")

type Notifier struct {
	webhookURL string
	client     *http.Client
}

// NewNotifier returns a webhook-backed notifier, or a no-op notifier when the
// webhook is blank.
func NewNotifier(webhook string) usecase.DiscordNotifier {
	return NewNotifierWithClient(webhook, http.DefaultClient)
}

// NewNotifierWithClient is NewNotifier with an injectable HTTP client for
// tests and custom transports.
func NewNotifierWithClient(webhook string, client *http.Client) usecase.DiscordNotifier {
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

// webhookPayload is the JSON body for a Discord webhook execute. Content is the
// short text Discord mobile push notifications render; Embeds render the styled
// embed in the Discord client. AllowedMentions keeps the webhook from causing
// pings, matching the prior plain-text behavior.
type webhookPayload struct {
	Content         string         `json:"content"`
	Embeds          []webhookEmbed `json:"embeds,omitempty"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

type webhookEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color,omitempty"`
	Fields      []webhookField `json:"fields,omitempty"`
	Footer      webhookFooter  `json:"footer,omitempty"`
}

type webhookField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type webhookFooter struct {
	Text string `json:"text"`
}

const discordFooter = "tmux-coder"

func (n *Notifier) Notify(ctx context.Context, msg usecase.DiscordMessage) error {
	payload := webhookPayload{
		Content: msg.Content,
	}
	payload.AllowedMentions.Parse = []string{}

	embed := webhookEmbed{
		Title:       msg.Embed.Title,
		Description: msg.Embed.Description,
		Color:       msg.Embed.Color,
		Footer:      webhookFooter{Text: discordFooter},
	}
	for _, f := range msg.Embed.Fields {
		embed.Fields = append(embed.Fields, webhookField{
			Name:   f.Name,
			Value:  f.Value,
			Inline: f.Inline,
		})
	}
	// Append the embed only when it carries something to render; the plain
	// Content alone is enough for a bare delivery.
	if embed.Title != "" || embed.Description != "" || embed.Color != 0 || len(embed.Fields) > 0 {
		payload.Embeds = []webhookEmbed{embed}
	}

	body, err := json.Marshal(payload)
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

func (NoopNotifier) Notify(context.Context, usecase.DiscordMessage) error { return nil }
