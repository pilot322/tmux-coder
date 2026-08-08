// Package daemonconfig reads and validates the daemon-wide YAML configuration.
package daemonconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pilot322/tmux-coder/internal/domain"
	"gopkg.in/yaml.v3"
)

const configRelativePath = ".tmux-coder/config.yaml"

const (
	OpenCodeServerPortEnv     = "TMUX_CODER_OPENCODE_SERVER_PORT"
	DashboardListenAddressEnv = "TMUX_CODER_DASHBOARD_LISTEN_ADDRESS"
	DashboardPublicURLEnv     = "TMUX_CODER_DASHBOARD_PUBLIC_URL"
	OpenCodePublicURLEnv      = "TMUX_CODER_OPENCODE_PUBLIC_URL"
)

// ErrInvalidConfig marks malformed YAML and invalid daemon configuration
// values. Details from parsers are deliberately omitted because they may
// contain a Discord webhook token.
var ErrInvalidConfig = errors.New("invalid daemon config")

type file struct {
	DiscordWebhookNotify   string  `yaml:"discord_webhook_notify"`
	OpenCodeServerPort     *int    `yaml:"opencode_server_port"`
	DashboardListenAddress *string `yaml:"dashboard_listen_address"`
	DashboardPublicURL     string  `yaml:"dashboard_public_url"`
	OpenCodePublicURL      string  `yaml:"opencode_public_url"`
}

// Load reads the daemon configuration from $HOME/.tmux-coder/config.yaml. HOME
// is preferred so daemon startup resolves the same path as the launching user;
// os.UserHomeDir is used when HOME is blank.
func Load() (domain.DaemonConfig, error) {
	home, err := resolveHome(os.Getenv, os.UserHomeDir)
	if err != nil {
		return domain.DaemonConfig{}, fmt.Errorf("resolve daemon config home: %w", err)
	}
	config, err := LoadFrom(filepath.Join(home, filepath.FromSlash(configRelativePath)))
	if err != nil {
		return domain.DaemonConfig{}, err
	}
	return applyEnv(config, os.Getenv)
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
	if decoded.OpenCodeServerPort != nil {
		if !validPort(*decoded.OpenCodeServerPort) {
			return domain.DaemonConfig{}, fmt.Errorf("%w: invalid opencode_server_port", ErrInvalidConfig)
		}
		config.OpenCodeServerPort = *decoded.OpenCodeServerPort
	}
	if decoded.DashboardListenAddress != nil {
		address := strings.TrimSpace(*decoded.DashboardListenAddress)
		if !validDashboardListenAddress(address) {
			return domain.DaemonConfig{}, fmt.Errorf("%w: invalid dashboard_listen_address", ErrInvalidConfig)
		}
		config.DashboardListenAddress = address
	}
	dashboardPublicURL, ok := normalizePublicURL(decoded.DashboardPublicURL)
	if !ok {
		return domain.DaemonConfig{}, fmt.Errorf("%w: invalid dashboard_public_url", ErrInvalidConfig)
	}
	config.DashboardPublicURL = dashboardPublicURL
	opencodePublicURL, ok := normalizePublicURL(decoded.OpenCodePublicURL)
	if !ok {
		return domain.DaemonConfig{}, fmt.Errorf("%w: invalid opencode_public_url", ErrInvalidConfig)
	}
	config.OpenCodePublicURL = opencodePublicURL

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

func applyEnv(config domain.DaemonConfig, getenv func(string) string) (domain.DaemonConfig, error) {
	raw := strings.TrimSpace(getenv(OpenCodeServerPortEnv))
	if raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || !validPort(port) {
			return domain.DaemonConfig{}, fmt.Errorf("%w: invalid %s", ErrInvalidConfig, OpenCodeServerPortEnv)
		}
		config.OpenCodeServerPort = port
	}

	if address := strings.TrimSpace(getenv(DashboardListenAddressEnv)); address != "" {
		if !validDashboardListenAddress(address) {
			return domain.DaemonConfig{}, fmt.Errorf("%w: invalid %s", ErrInvalidConfig, DashboardListenAddressEnv)
		}
		config.DashboardListenAddress = address
	}
	if raw := strings.TrimSpace(getenv(DashboardPublicURLEnv)); raw != "" {
		publicURL, ok := normalizePublicURL(raw)
		if !ok {
			return domain.DaemonConfig{}, fmt.Errorf("%w: invalid %s", ErrInvalidConfig, DashboardPublicURLEnv)
		}
		config.DashboardPublicURL = publicURL
	}
	if raw := strings.TrimSpace(getenv(OpenCodePublicURLEnv)); raw != "" {
		publicURL, ok := normalizePublicURL(raw)
		if !ok {
			return domain.DaemonConfig{}, fmt.Errorf("%w: invalid %s", ErrInvalidConfig, OpenCodePublicURLEnv)
		}
		config.OpenCodePublicURL = publicURL
	}
	return config, nil
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func validDashboardListenAddress(address string) bool {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	for _, char := range rawPort {
		if char < '0' || char > '9' {
			return false
		}
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || !validPort(port) {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsUnspecified()
	}
	return validHostname(host)
}

func validHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	numericAddress := strings.Contains(host, ".")
	for _, char := range host {
		if (char < '0' || char > '9') && char != '.' {
			numericAddress = false
			break
		}
	}
	if numericAddress {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func normalizePublicURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	if strings.Contains(raw, "#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if u.RawQuery != "" || u.ForceQuery || (u.Path != "" && u.Path != "/") {
		return "", false
	}

	hostname := strings.ToLower(u.Hostname())
	if hostname == "" || strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsUnspecified() {
			return "", false
		}
		hostname = ip.String()
	} else if !validHostname(hostname) {
		return "", false
	}

	port := u.Port()
	if port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || !validPort(parsedPort) {
			return "", false
		}
		if (scheme == "http" && parsedPort == 80) || (scheme == "https" && parsedPort == 443) {
			port = ""
		} else {
			port = strconv.Itoa(parsedPort)
		}
	}
	if port != "" {
		u.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		u.Host = "[" + hostname + "]"
	} else {
		u.Host = hostname
	}
	u.Scheme = scheme
	u.Path = ""
	u.RawPath = ""
	return u.String(), true
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
