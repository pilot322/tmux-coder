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
	if config.OpenCodeServerPort != 39155 {
		t.Errorf("OpenCodeServerPort = %d, want 39155", config.OpenCodeServerPort)
	}
}

func TestDefaultConfigUsesLoopbackDashboardAndDisabledPublicURLs(t *testing.T) {
	config, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if config.DashboardListenAddress != "127.0.0.1:39356" {
		t.Errorf("DashboardListenAddress = %q, want loopback default", config.DashboardListenAddress)
	}
	if config.DashboardPublicURL != "" {
		t.Errorf("DashboardPublicURL = %q, want disabled", config.DashboardPublicURL)
	}
	if config.OpenCodePublicURL != "" {
		t.Errorf("OpenCodePublicURL = %q, want disabled", config.OpenCodePublicURL)
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
	t.Setenv(OpenCodeServerPortEnv, "")
	t.Setenv(DashboardListenAddressEnv, "")
	t.Setenv(DashboardPublicURLEnv, "")
	t.Setenv(OpenCodePublicURLEnv, "")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.DiscordWebhookNotify != webhook {
		t.Errorf("DiscordWebhookNotify = %q, want configured webhook", config.DiscordWebhookNotify)
	}
}

func TestDevelopmentBuildUsesBakedNetworkDefaultsInsteadOfGlobalConfig(t *testing.T) {
	previousDevelopmentBuild := domain.DevelopmentBuild
	previousOpenCodePort := domain.DefaultOpenCodeServerPort
	previousDashboardPort := domain.DefaultDashboardPort
	domain.DevelopmentBuild = "true"
	domain.DefaultOpenCodeServerPort = "42001"
	domain.DefaultDashboardPort = "42002"
	t.Cleanup(func() {
		domain.DevelopmentBuild = previousDevelopmentBuild
		domain.DefaultOpenCodeServerPort = previousOpenCodePort
		domain.DefaultDashboardPort = previousDashboardPort
	})

	home := t.TempDir()
	dir := filepath.Join(home, ".tmux-coder")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("opencode_server_port: 41001\ndashboard_listen_address: 127.0.0.1:41002\ndashboard_public_url: https://production-dashboard.example\nopencode_public_url: https://production-opencode.example\n")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(OpenCodeServerPortEnv, "")
	t.Setenv(DashboardListenAddressEnv, "")
	t.Setenv(DashboardPublicURLEnv, "")
	t.Setenv(OpenCodePublicURLEnv, "")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.OpenCodeServerPort != 42001 {
		t.Errorf("OpenCodeServerPort = %d, want baked development port 42001", config.OpenCodeServerPort)
	}
	if config.DashboardListenAddress != "127.0.0.1:42002" {
		t.Errorf("DashboardListenAddress = %q, want baked development address", config.DashboardListenAddress)
	}
	if config.DashboardPublicURL != "" || config.OpenCodePublicURL != "" {
		t.Errorf("public URLs = %q, %q, want global production URLs ignored", config.DashboardPublicURL, config.OpenCodePublicURL)
	}
}

func TestDevelopmentBuildStillAcceptsInheritedNetworkOverrides(t *testing.T) {
	previousDevelopmentBuild := domain.DevelopmentBuild
	previousOpenCodePort := domain.DefaultOpenCodeServerPort
	previousDashboardPort := domain.DefaultDashboardPort
	domain.DevelopmentBuild = "true"
	domain.DefaultOpenCodeServerPort = "42001"
	domain.DefaultDashboardPort = "42002"
	t.Cleanup(func() {
		domain.DevelopmentBuild = previousDevelopmentBuild
		domain.DefaultOpenCodeServerPort = previousOpenCodePort
		domain.DefaultDashboardPort = previousDashboardPort
	})

	t.Setenv("HOME", t.TempDir())
	t.Setenv(OpenCodeServerPortEnv, "43001")
	t.Setenv(DashboardListenAddressEnv, "127.0.0.1:43002")
	t.Setenv(DashboardPublicURLEnv, "http://127.0.0.1:43002")
	t.Setenv(OpenCodePublicURLEnv, "http://127.0.0.1:43001")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.OpenCodeServerPort != 43001 {
		t.Errorf("OpenCodeServerPort = %d, want inherited override 43001", config.OpenCodeServerPort)
	}
	if config.DashboardListenAddress != "127.0.0.1:43002" {
		t.Errorf("DashboardListenAddress = %q, want inherited override", config.DashboardListenAddress)
	}
	if config.DashboardPublicURL != "http://127.0.0.1:43002" || config.OpenCodePublicURL != "http://127.0.0.1:43001" {
		t.Errorf("public URLs = %q, %q, want inherited overrides", config.DashboardPublicURL, config.OpenCodePublicURL)
	}
}

func TestParseAcceptsOpenCodeServerPort(t *testing.T) {
	config, err := Parse([]byte("opencode_server_port: 41000\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if config.OpenCodeServerPort != 41000 {
		t.Fatalf("OpenCodeServerPort = %d, want 41000", config.OpenCodeServerPort)
	}
}

func TestParseAcceptsConcreteDashboardListenAddress(t *testing.T) {
	for _, address := range []string{"100.64.0.8:41000", "daemon.tailnet.ts.net:41000", "[fd7a:115c:a1e0::1]:41000"} {
		t.Run(address, func(t *testing.T) {
			config, err := Parse([]byte("dashboard_listen_address: \"" + address + "\"\n"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if config.DashboardListenAddress != address {
				t.Fatalf("DashboardListenAddress = %q, want %q", config.DashboardListenAddress, address)
			}
		})
	}
}

func TestParseRejectsInvalidDashboardListenAddress(t *testing.T) {
	for name, address := range map[string]string{
		"blank":              "",
		"missing host":       ":41000",
		"missing port":       "100.64.0.8",
		"zero port":          "100.64.0.8:0",
		"port above maximum": "100.64.0.8:65536",
		"non-numeric port":   "100.64.0.8:http",
		"IPv4 wildcard":      "0.0.0.0:41000",
		"IPv6 wildcard":      "[::]:41000",
		"malformed hostname": "bad..host:41000",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte("dashboard_listen_address: \"" + address + "\"\n"))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Parse error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestParseRejectsMalformedNumericDashboardHost(t *testing.T) {
	_, err := Parse([]byte("dashboard_listen_address: 999.999.999.999:41000\n"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Parse error = %v, want ErrInvalidConfig", err)
	}
}

func TestParseRejectsSignedDashboardPort(t *testing.T) {
	_, err := Parse([]byte("dashboard_listen_address: daemon.tailnet.ts.net:+41000\n"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Parse error = %v, want ErrInvalidConfig", err)
	}
}

func TestParseNormalizesDashboardPublicURLOrigin(t *testing.T) {
	config, err := Parse([]byte("dashboard_public_url: \"  https://dashboard.tailnet.ts.net/  \"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if config.DashboardPublicURL != "https://dashboard.tailnet.ts.net" {
		t.Fatalf("DashboardPublicURL = %q, want normalized origin", config.DashboardPublicURL)
	}
}

func TestParseNormalizesDefaultHTTPPortToBrowserOrigin(t *testing.T) {
	config, err := Parse([]byte("dashboard_public_url: \"http://dashboard.tailnet.ts.net:80/\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if config.DashboardPublicURL != "http://dashboard.tailnet.ts.net" {
		t.Fatalf("DashboardPublicURL = %q, want browser origin without default port", config.DashboardPublicURL)
	}
}

func TestParseNormalizesDefaultHTTPSPortToBrowserOrigin(t *testing.T) {
	config, err := Parse([]byte("opencode_public_url: \"https://opencode.tailnet.ts.net:443/\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if config.OpenCodePublicURL != "https://opencode.tailnet.ts.net" {
		t.Fatalf("OpenCodePublicURL = %q, want browser origin without default port", config.OpenCodePublicURL)
	}
}

func TestParseRejectsInvalidPublicURLOrigins(t *testing.T) {
	for name, publicURL := range map[string]string{
		"relative":           "dashboard.tailnet.ts.net",
		"unsupported scheme": "ftp://dashboard.tailnet.ts.net",
		"missing host":       "https:///",
		"userinfo":           "https://user@dashboard.tailnet.ts.net",
		"query":              "https://dashboard.tailnet.ts.net?view=all",
		"fragment":           "https://dashboard.tailnet.ts.net#projects",
		"non-root path":      "https://dashboard.tailnet.ts.net/app",
		"invalid port":       "https://dashboard.tailnet.ts.net:65536",
		"malformed hostname": "https://bad..host",
	} {
		for _, key := range []string{"dashboard_public_url", "opencode_public_url"} {
			t.Run(key+"/"+name, func(t *testing.T) {
				_, err := Parse([]byte(key + ": \"" + publicURL + "\"\n"))
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("Parse error = %v, want ErrInvalidConfig", err)
				}
			})
		}
	}
}

func TestParseNormalizesOpenCodePublicURLOrigin(t *testing.T) {
	config, err := Parse([]byte("opencode_public_url: \"http://100.64.0.8:39155/\"\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if config.OpenCodePublicURL != "http://100.64.0.8:39155" {
		t.Fatalf("OpenCodePublicURL = %q, want normalized origin", config.OpenCodePublicURL)
	}
}

func TestEnvironmentOverridesOpenCodeServerPort(t *testing.T) {
	config, err := applyEnv(domain.DaemonConfig{OpenCodeServerPort: 41000}, func(key string) string {
		if key == OpenCodeServerPortEnv {
			return "42000"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("applyEnv: %v", err)
	}
	if config.OpenCodeServerPort != 42000 {
		t.Fatalf("OpenCodeServerPort = %d, want environment override 42000", config.OpenCodeServerPort)
	}
}

func TestEnvironmentOverridesBrowserConfiguration(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".tmux-coder")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("dashboard_listen_address: 127.0.0.1:40000\ndashboard_public_url: https://file-dashboard.example\nopencode_public_url: https://file-opencode.example\n")
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(OpenCodeServerPortEnv, "")
	t.Setenv(DashboardListenAddressEnv, "daemon.tailnet.ts.net:41000")
	t.Setenv(DashboardPublicURLEnv, "https://dashboard.tailnet.ts.net/")
	t.Setenv(OpenCodePublicURLEnv, "https://opencode.tailnet.ts.net/")

	config, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.DashboardListenAddress != "daemon.tailnet.ts.net:41000" {
		t.Errorf("DashboardListenAddress = %q, want environment override", config.DashboardListenAddress)
	}
	if config.DashboardPublicURL != "https://dashboard.tailnet.ts.net" {
		t.Errorf("DashboardPublicURL = %q, want normalized environment override", config.DashboardPublicURL)
	}
	if config.OpenCodePublicURL != "https://opencode.tailnet.ts.net" {
		t.Errorf("OpenCodePublicURL = %q, want normalized environment override", config.OpenCodePublicURL)
	}
}

func TestEnvironmentRejectsInvalidBrowserConfiguration(t *testing.T) {
	for env, value := range map[string]string{
		DashboardListenAddressEnv: "0.0.0.0:41000",
		DashboardPublicURLEnv:     "https://dashboard.example/app",
		OpenCodePublicURLEnv:      "ssh://opencode.example",
	} {
		t.Run(env, func(t *testing.T) {
			_, err := applyEnv(domain.DefaultDaemonConfig(), func(key string) string {
				if key == env {
					return value
				}
				return ""
			})
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("applyEnv error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestRejectsInvalidOpenCodeServerPorts(t *testing.T) {
	for _, input := range []string{"0", "65536", "-1"} {
		t.Run("yaml_"+input, func(t *testing.T) {
			_, err := Parse([]byte("opencode_server_port: " + input + "\n"))
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Parse error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	for _, input := range []string{"0", "65536", "not-a-port"} {
		t.Run("env_"+input, func(t *testing.T) {
			_, err := applyEnv(domain.DefaultDaemonConfig(), func(string) string { return input })
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("applyEnv error = %v, want ErrInvalidConfig", err)
			}
		})
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
