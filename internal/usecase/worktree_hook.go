package usecase

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultWorktreeHookTimeout = 2 * time.Minute

type WorktreeHookRequest struct {
	ScriptPath string
	WorkingDir string
	Timeout    time.Duration
	Env        map[string]string
}

type WorktreeHookResult struct {
	Output string
}

type WorktreeHookRunner interface {
	Run(ctx context.Context, req WorktreeHookRequest) (WorktreeHookResult, error)
}

type HookLeaseOwner struct {
	ProjectID       int
	SessionName     string
	TmuxSessionName string
	Branch          string
	WorktreePath    string
}

type ResourceLeaseOwnerKind string

const (
	ResourceLeaseOwnerHook    ResourceLeaseOwnerKind = "hook"
	ResourceLeaseOwnerSession ResourceLeaseOwnerKind = "session"
)

type PortLeaseRequest struct {
	ProjectID int
	OwnerKind ResourceLeaseOwnerKind
	HookToken string
	SessionID int
	Key       string
	Start     int
	End       int
}

type ResourceLeaseRepository interface {
	BeginHook(ctx context.Context, token string, owner HookLeaseOwner) error
	EndHook(ctx context.Context, token string) error
	AcquirePort(ctx context.Context, req PortLeaseRequest, portAvailable func(int) bool) (int, error)
	PromoteHookLeases(ctx context.Context, token string, sessionID int) error
	ReleaseHookLeases(ctx context.Context, token string) error
	ReleaseSessionLeases(ctx context.Context, sessionID int) error
}

type worktreeHookConfig struct {
	Script  string
	Timeout time.Duration
}

func loadWorktreeHookConfig(projectRoot string) (worktreeHookConfig, error) {
	path := filepath.Join(projectRoot, ".tmux-coder.toml")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return worktreeHookConfig{Timeout: defaultWorktreeHookTimeout}, nil
		}
		return worktreeHookConfig{}, fmt.Errorf("%w: read config file: %v", ErrGateway, err)
	}
	defer file.Close()

	cfg := worktreeHookConfig{Timeout: defaultWorktreeHookTimeout}
	section := ""
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(stripTomlComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if section != "worktree" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return worktreeHookConfig{}, fmt.Errorf("%w: invalid config line %d", ErrValidation, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return worktreeHookConfig{}, fmt.Errorf("%w: invalid config value for %s", ErrValidation, key)
		}
		switch key {
		case "on_create_script":
			cfg.Script = unquoted
		case "on_create_timeout":
			timeout, err := time.ParseDuration(unquoted)
			if err != nil || timeout <= 0 {
				return worktreeHookConfig{}, fmt.Errorf("%w: invalid on_create_timeout", ErrValidation)
			}
			cfg.Timeout = timeout
		}
	}
	if err := scanner.Err(); err != nil {
		return worktreeHookConfig{}, fmt.Errorf("%w: read config file: %v", ErrGateway, err)
	}
	return cfg, nil
}

func resolveWorktreeHookScript(projectRoot, configured string) (string, error) {
	if configured == "" {
		return "", nil
	}
	if filepath.IsAbs(configured) {
		return "", fmt.Errorf("%w: hook script path must be relative to project root", ErrValidation)
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: resolve project root: %v", ErrGateway, err)
	}
	candidate := filepath.Join(root, configured)
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: hook script path must stay inside project root", ErrValidation)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: hook script does not exist", ErrValidation)
		}
		return "", fmt.Errorf("%w: stat hook script: %v", ErrGateway, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: hook script must be a file", ErrValidation)
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%w: hook script is not executable", ErrValidation)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("%w: resolve project root: %v", ErrGateway, err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: resolve hook script: %v", ErrGateway, err)
	}
	resolvedRel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || resolvedRel == ".." || strings.HasPrefix(resolvedRel, ".."+string(filepath.Separator)) || filepath.IsAbs(resolvedRel) {
		return "", fmt.Errorf("%w: hook script path must stay inside project root", ErrValidation)
	}
	return candidate, nil
}

func newWorktreeHookToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("%w: generate hook token: %v", ErrGateway, err)
	}
	return hex.EncodeToString(b[:]), nil
}

func worktreeHookEnv(projectRoot, worktreeRoot string, projectID int, sessionName, tmuxSessionName, branch, hookToken string) map[string]string {
	return map[string]string{
		"TMUX_CODER_PROJECT_ROOT":      projectRoot,
		"TMUX_CODER_WORKTREE_ROOT":     worktreeRoot,
		"TMUX_CODER_PROJECT_ID":        strconv.Itoa(projectID),
		"TMUX_CODER_SESSION_NAME":      sessionName,
		"TMUX_CODER_TMUX_SESSION_NAME": tmuxSessionName,
		"TMUX_CODER_BRANCH":            branch,
		"TMUX_CODER_HOOK_TOKEN":        hookToken,
	}
}

func stripTomlComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

type missingWorktreeHookRunner struct{}

func (missingWorktreeHookRunner) Run(ctx context.Context, req WorktreeHookRequest) (WorktreeHookResult, error) {
	return WorktreeHookResult{}, fmt.Errorf("hook runner is not configured")
}

type noopResourceLeaseRepository struct{}

func (noopResourceLeaseRepository) BeginHook(ctx context.Context, token string, owner HookLeaseOwner) error {
	return nil
}

func (noopResourceLeaseRepository) EndHook(ctx context.Context, token string) error { return nil }

func (noopResourceLeaseRepository) AcquirePort(ctx context.Context, req PortLeaseRequest, portAvailable func(int) bool) (int, error) {
	return 0, fmt.Errorf("%w: resource leases are not configured", ErrGateway)
}

func (noopResourceLeaseRepository) PromoteHookLeases(ctx context.Context, token string, sessionID int) error {
	return nil
}

func (noopResourceLeaseRepository) ReleaseHookLeases(ctx context.Context, token string) error {
	return nil
}

func (noopResourceLeaseRepository) ReleaseSessionLeases(ctx context.Context, sessionID int) error {
	return nil
}
