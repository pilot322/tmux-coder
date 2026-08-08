package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
)

const (
	openCodeSetupTimeout     = 30 * time.Second
	openCodeModelVerifyDelay = 50 * time.Millisecond
)

var ErrAgentSetupNotFound = errors.New("agent startup setup not found")

type OpenCodeSetupTmuxGateway interface {
	SetPaneInput(ctx context.Context, paneID string, enabled bool) error
	PasteLiteral(ctx context.Context, paneID, text string) error
	ConfirmPickerSelection(ctx context.Context, paneID string) error
	SendEnter(ctx context.Context, paneID string) error
}

type OpenCodeSetupReady struct {
	Model       string
	Variant     string
	DisplayName string
	StatePath   string
	Version     string
	HasVariants bool
	Error       string
}

type openCodeSetup struct {
	model   string
	variant string
	prompt  *string

	pane    chan string
	state   chan string
	ready   chan OpenCodeSetupReady
	proceed chan struct{}
	opened  chan string
	done    chan struct{}
	cancel  context.CancelFunc
	err     error
}

// OpenCodeSetupCoordinator owns transient startup data and the one-shot tmux
// automation handshake. It deliberately remains separate from Agent Status.
type OpenCodeSetupCoordinator struct {
	mu      sync.Mutex
	setups  map[int]*openCodeSetup
	tmux    OpenCodeSetupTmuxGateway
	timeout time.Duration
	log     obs.Logger
}

func NewOpenCodeSetupCoordinator(tmux OpenCodeSetupTmuxGateway, log obs.Logger) *OpenCodeSetupCoordinator {
	return &OpenCodeSetupCoordinator{
		setups:  make(map[int]*openCodeSetup),
		tmux:    tmux,
		timeout: openCodeSetupTimeout,
		log:     log.With("component", "opencode-setup"),
	}
}

func (c *OpenCodeSetupCoordinator) Register(agentID int, model, variant string, prompt *string, paneID string) {
	var promptCopy *string
	if prompt != nil {
		value := *prompt
		promptCopy = &value
	}
	setup := &openCodeSetup{
		model: model, variant: variant, prompt: promptCopy,
		pane: make(chan string, 1), state: make(chan string, 1), ready: make(chan OpenCodeSetupReady, 1),
		proceed: make(chan struct{}), opened: make(chan string, 1), done: make(chan struct{}),
	}
	if paneID != "" {
		setup.pane <- paneID
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	setup.cancel = cancel
	c.mu.Lock()
	c.setups[agentID] = setup
	c.mu.Unlock()
	go c.run(ctx, agentID, setup)
}

func (c *OpenCodeSetupCoordinator) SetPane(agentID int, paneID string) {
	c.mu.Lock()
	setup := c.setups[agentID]
	c.mu.Unlock()
	if setup == nil {
		return
	}
	select {
	case setup.pane <- paneID:
	default:
	}
}

func (c *OpenCodeSetupCoordinator) SetStatePath(agentID int, statePath string) error {
	c.mu.Lock()
	setup := c.setups[agentID]
	c.mu.Unlock()
	if setup == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case setup.state <- statePath:
		return nil
	default:
		return fmt.Errorf("OpenCode startup state was already registered")
	}
}

func (c *OpenCodeSetupCoordinator) Ready(ctx context.Context, agentID int, ready OpenCodeSetupReady) error {
	c.mu.Lock()
	setup := c.setups[agentID]
	c.mu.Unlock()
	if setup == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case setup.ready <- ready:
	default:
		return fmt.Errorf("OpenCode startup setup was already attempted")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-setup.proceed:
		return nil
	case <-setup.done:
		return nil
	}
}

func (c *OpenCodeSetupCoordinator) Opened(agentID int, setupError string) error {
	c.mu.Lock()
	setup := c.setups[agentID]
	c.mu.Unlock()
	if setup == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case setup.opened <- setupError:
		return nil
	default:
		return fmt.Errorf("OpenCode startup UI was already opened")
	}
}

func (c *OpenCodeSetupCoordinator) Wait(ctx context.Context, agentID int) error {
	c.mu.Lock()
	setup := c.setups[agentID]
	c.mu.Unlock()
	if setup == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-setup.done:
		c.mu.Lock()
		err := setup.err
		c.mu.Unlock()
		return err
	}
}

func (c *OpenCodeSetupCoordinator) Forget(agentID int) {
	c.mu.Lock()
	if setup := c.setups[agentID]; setup != nil {
		setup.prompt = nil
	}
	delete(c.setups, agentID)
	c.mu.Unlock()
}

func (c *OpenCodeSetupCoordinator) Cancel(agentID int) {
	c.mu.Lock()
	setup := c.setups[agentID]
	c.mu.Unlock()
	if setup == nil {
		return
	}
	setup.cancel()
	<-setup.done
}

func (c *OpenCodeSetupCoordinator) run(ctx context.Context, agentID int, setup *openCodeSetup) {
	defer setup.cancel()
	var paneID string
	var statePath string
	var ready OpenCodeSetupReady
	readyReceived := false
	for paneID == "" || !readyReceived || (setup.model != "" && statePath == "" && ready.Error == "") {
		select {
		case <-ctx.Done():
			c.finish(setup, fmt.Errorf("startup setup deadline exceeded: %w", ctx.Err()))
			return
		case paneID = <-setup.pane:
		case statePath = <-setup.state:
		case ready = <-setup.ready:
			readyReceived = true
		}
	}

	if ready.Error != "" {
		c.finish(setup, errors.New(ready.Error))
		return
	}
	if ready.Version != "" && ready.Version != "1.18.14" {
		c.log.Warn(ctx, "untested OpenCode version; attempting startup automation", "agent_id", agentID, "version", ready.Version, "tested_version", "1.18.14")
	}
	if setup.model != "" {
		if ready.Model != setup.model {
			c.finish(setup, fmt.Errorf("plugin validated model %q, requested %q", ready.Model, setup.model))
			return
		}
		if ready.DisplayName == "" || ready.StatePath == "" {
			c.finish(setup, fmt.Errorf("plugin omitted model picker setup data"))
			return
		}
		if ready.StatePath != statePath {
			c.finish(setup, fmt.Errorf("plugin reported unexpected isolated state path"))
			return
		}
		if ready.Variant != setup.variant {
			c.finish(setup, fmt.Errorf("plugin validated variant %q, requested %q", ready.Variant, setup.variant))
			return
		}
	}

	err := c.automate(ctx, paneID, setup, ready)
	c.finish(setup, err)
}

func (c *OpenCodeSetupCoordinator) automate(ctx context.Context, paneID string, setup *openCodeSetup, ready OpenCodeSetupReady) (result error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.tmux.SetPaneInput(cleanupCtx, paneID, true); err != nil {
			result = errors.Join(result, fmt.Errorf("restore pane input: %w", err))
		}
	}()
	if err := c.tmux.SetPaneInput(ctx, paneID, false); err != nil {
		return fmt.Errorf("disable pane input: %w", err)
	}
	close(setup.proceed)
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for OpenCode startup UI: %w", ctx.Err())
	case setupError := <-setup.opened:
		if setupError != "" {
			return errors.New(setupError)
		}
	}

	if setup.model != "" {
		if err := c.tmux.PasteLiteral(ctx, paneID, ready.DisplayName); err != nil {
			return fmt.Errorf("enter model picker value: %w", err)
		}
		if err := c.tmux.ConfirmPickerSelection(ctx, paneID); err != nil {
			return fmt.Errorf("confirm model picker value: %w", err)
		}
		if err := verifySelectedModel(ctx, ready.StatePath, setup.model); err != nil {
			return err
		}
		if ready.HasVariants {
			pickerValue := "Default"
			verifiedVariant := "default"
			if setup.variant != "" {
				pickerValue = setup.variant
				verifiedVariant = setup.variant
			}
			if err := c.tmux.PasteLiteral(ctx, paneID, pickerValue); err != nil {
				return fmt.Errorf("enter model variant: %w", err)
			}
			if err := c.tmux.ConfirmPickerSelection(ctx, paneID); err != nil {
				return fmt.Errorf("confirm model variant: %w", err)
			}
			if err := verifySelectedVariant(ctx, ready.StatePath, setup.model, verifiedVariant); err != nil {
				return err
			}
		}
	}

	if setup.prompt != nil {
		if err := c.tmux.PasteLiteral(ctx, paneID, *setup.prompt); err != nil {
			return fmt.Errorf("enter initial prompt: %w", err)
		}
		setup.prompt = nil
		if err := c.tmux.SendEnter(ctx, paneID); err != nil {
			return fmt.Errorf("submit initial prompt: %w", err)
		}
	}
	return nil
}

func verifySelectedVariant(ctx context.Context, statePath, canonical, variant string) error {
	modelPath := filepath.Join(statePath, "model.json")
	for {
		data, err := os.ReadFile(modelPath)
		if err == nil {
			var state struct {
				Variant map[string]string `json:"variant"`
			}
			if json.Unmarshal(data, &state) == nil && state.Variant[canonical] == variant {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("verify selected variant %q for model %q: %w", variant, canonical, ctx.Err())
		case <-time.After(openCodeModelVerifyDelay):
		}
	}
}

func (c *OpenCodeSetupCoordinator) finish(setup *openCodeSetup, err error) {
	c.mu.Lock()
	setup.prompt = nil
	setup.err = err
	close(setup.done)
	c.mu.Unlock()
}

func verifySelectedModel(ctx context.Context, statePath, canonical string) error {
	modelPath := filepath.Join(statePath, "model.json")
	for {
		data, err := os.ReadFile(modelPath)
		if err == nil {
			var state struct {
				Recent []struct {
					ProviderID string `json:"providerID"`
					ModelID    string `json:"modelID"`
				} `json:"recent"`
			}
			if json.Unmarshal(data, &state) == nil && len(state.Recent) > 0 {
				selected := state.Recent[0].ProviderID + "/" + state.Recent[0].ModelID
				if selected == canonical {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("verify selected model %q: %w", canonical, ctx.Err())
		case <-time.After(openCodeModelVerifyDelay):
		}
	}
}
