// Package daemonconfig reads and validates the daemon-wide YAML configuration.
package daemonconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pilot322/tmux-coder/internal/domain"
	"gopkg.in/yaml.v3"
)

const configRelativePath = ".tmux-coder/config.yaml"

// ErrInvalidConfig marks malformed YAML and invalid daemon configuration
// values. Details from parsers are deliberately omitted because they may
// contain a Discord webhook token.
var ErrInvalidConfig = errors.New("invalid daemon config")

type file struct {
	DiscordWebhookNotify string `yaml:"discord_webhook_notify"`
}

// Load reads the daemon configuration from $HOME/.tmux-coder/config.yaml. HOME
// is preferred so daemon startup resolves the same path as the launching user;
// os.UserHomeDir is used when HOME is blank.
func Load() (domain.DaemonConfig, error) {
	home, err := resolveHome(os.Getenv, os.UserHomeDir)
	if err != nil {
		return domain.DaemonConfig{}, fmt.Errorf("resolve daemon config home: %w", err)
	}
	return LoadFrom(filepath.Join(home, filepath.FromSlash(configRelativePath)))
}

func resolveHome(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return home, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is empty")
	}
	return home, nil
}

// LoadFrom reads one daemon configuration file. A missing file is equivalent
// to an empty file and returns the domain defaults with Discord disabled.
func LoadFrom(path string) (domain.DaemonConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.DefaultDaemonConfig(), nil
		}
		return domain.DaemonConfig{}, fmt.Errorf("read daemon config: %w", err)
	}
	return Parse(data)
}

// Parse strictly decodes and validates daemon configuration bytes. Blank input
// and a missing or blank discord_webhook_notify key disable Discord.
func Parse(data []byte) (domain.DaemonConfig, error) {
	config := domain.DefaultDaemonConfig()
	if len(bytes.TrimSpace(data)) == 0 {
		return config, nil
	}

	var decoded file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&decoded); errors.Is(err, io.EOF) {
		return config, nil
	} else if err != nil {
		return domain.DaemonConfig{}, fmt.Errorf("%w: malformed YAML", ErrInvalidConfig)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return domain.DaemonConfig{}, fmt.Errorf("%w: multiple YAML documents", ErrInvalidConfig)
	}

	webhook := strings.TrimSpace(decoded.DiscordWebhookNotify)
	if webhook == "" {
		return config, nil
	}
	if !validDiscordWebhook(webhook) {
		return domain.DaemonConfig{}, fmt.Errorf("%w: invalid discord_webhook_notify", ErrInvalidConfig)
	}
	config.DiscordWebhookNotify = webhook
	return config, nil
}

func validDiscordWebhook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Opaque != "" {
		return false
	}
	if u.User != nil || u.Host == "" || u.Host != u.Hostname() {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "discord.com", "discordapp.com":
	default:
		return false
	}
	if u.Fragment != "" {
		return false
	}

	const prefix = "/api/webhooks/"
	if !strings.HasPrefix(u.Path, prefix) || u.EscapedPath() != u.Path {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	return len(segments) == 2 && segments[0] != "" && segments[1] != ""
}
