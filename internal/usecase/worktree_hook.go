package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pilot322/tmux-coder/internal/config"
)

// translateConfigErr maps a config.Load failure onto the usecase error taxonomy:
// a malformed or invalid Config File is a validation error; anything else (a
// read failure) is a gateway error.
func translateConfigErr(err error) error {
	if errors.Is(err, config.ErrValidation) {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return fmt.Errorf("%w: %v", ErrGateway, err)
}

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
