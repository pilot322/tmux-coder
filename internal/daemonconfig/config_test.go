package daemonconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
)

func TestLoadFromMissingFileReturnsDefaults(t *testing.T) {
	config, err := LoadFrom(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom missing file: %v", err)
	}
	if config.ProjectTitleLimit() != domain.DefaultMaxProjectTitleLength {
		t.Errorf("project title limit = %d, want default", config.ProjectTitleLimit())
	}
	if config.DiscordWebhookNotify != "" {
		t.Errorf("DiscordWebhookNotify = %q, want disabled", config.DiscordWebhookNotify)
	}
}

func TestLoadUsesHOMEConfigPath(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".tmux-coder")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const webhook = "https://discord.com/api/webhooks/123/token"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("discord_webhook_notify: "+webhook+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.DiscordWebhookNotify != webhook {
		t.Errorf("DiscordWebhookNotify = %q, want configured webhook", config.DiscordWebhookNotify)
	}
}

func TestResolveHomeFallsBackWhenHOMEIsBlank(t *testing.T) {
	called := false
	home, err := resolveHome(func(string) string { return "  " }, func() (string, error) {
		called = true
		return "/fallback/home", nil
	})
	if err != nil {
		t.Fatalf("resolveHome: %v", err)
	}
	if !called || home != "/fallback/home" {
		t.Fatalf("resolveHome = %q, fallback called = %v", home, called)
	}
}

func TestParseDisabledConfigurations(t *testing.T) {
	for _, input := range []string{"", "  \n", "# no settings\n", "{}\n", "discord_webhook_notify: \"\"\n", "discord_webhook_notify: \"  \"\n"} {
		config, err := Parse([]byte(input))
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if config.DiscordWebhookNotify != "" {
			t.Errorf("Parse(%q) webhook = %q, want disabled", input, config.DiscordWebhookNotify)
		}
	}
}

func TestParseAcceptsOfficialDiscordWebhookURLs(t *testing.T) {
	for _, webhook := range []string{
		"https://discord.com/api/webhooks/123/token",
		"https://discordapp.com/api/webhooks/456/legacy-token",
		"https://DISCORD.COM/api/webhooks/123/token?wait=true",
	} {
		t.Run(webhook, func(t *testing.T) {
			config, err := Parse([]byte("discord_webhook_notify: \"" + webhook + "\"\n"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if config.DiscordWebhookNotify != webhook {
				t.Errorf("DiscordWebhookNotify = %q, want %q", config.DiscordWebhookNotify, webhook)
			}
		})
	}
}

func TestParseRejectsMalformedAndInvalidConfiguration(t *testing.T) {
	const secret = "super-secret-token"
	tests := map[string]string{
		"malformed YAML":  "discord_webhook_notify: [\n",
		"unknown key":     "discord_webhook_notify: \"\"\ndiscord_webhok_notify: value\n",
		"multiple docs":   "discord_webhook_notify: \"\"\n---\n{}\n",
		"HTTP":            "http://discord.com/api/webhooks/123/" + secret,
		"malformed URL":   "https://discord.com/%zz/api/webhooks/123/" + secret,
		"other host":      "https://example.com/api/webhooks/123/" + secret,
		"lookalike host":  "https://discord.com.evil.example/api/webhooks/123/" + secret,
		"www host":        "https://www.discord.com/api/webhooks/123/" + secret,
		"userinfo":        "https://attacker@discord.com/api/webhooks/123/" + secret,
		"port":            "https://discord.com:443/api/webhooks/123/" + secret,
		"missing id":      "https://discord.com/api/webhooks//" + secret,
		"missing token":   "https://discord.com/api/webhooks/123/",
		"extra segment":   "https://discord.com/api/webhooks/123/" + secret + "/extra",
		"wrong path":      "https://discord.com/not-webhooks/123/" + secret,
		"encoded segment": "https://discord.com/api/webhooks/123%2F456/" + secret,
		"fragment":        "https://discord.com/api/webhooks/123/" + secret + "#fragment",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			input := value
			if name != "malformed YAML" && name != "unknown key" && name != "multiple docs" {
				input = "discord_webhook_notify: \"" + value + "\"\n"
			}
			_, err := Parse([]byte(input))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Parse error = %v, want ErrInvalidConfig", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked webhook token: %v", err)
			}
		})
	}
}
