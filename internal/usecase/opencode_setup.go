package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
)

const openCodeSetupTimeout = 30 * time.Second

var ErrAgentSetupNotFound = errors.New("agent startup setup not found")

type OpenCodeSetupTmuxGateway interface {
	SetPaneInput(ctx context.Context, paneID string, enabled bool) error
	PasteLiteral(ctx context.Context, paneID, text string) error
	SendEnter(ctx context.Context, paneID string) error
}

type OpenCodeSetupReady struct {
	Model   string
	Variant string
	Version string
	Error   string
}

type openCodeSetup struct {
	model, variant string
	prompt         *string
	pane           chan string
	ready          chan OpenCodeSetupReady
	proceed        chan struct{}
	opened         chan OpenCodeSetupReady
	done           chan struct{}
	cancel         context.CancelFunc
	err            error
}

// OpenCodeSetupCoordinator owns transient startup data. The plugin validates
// the catalog before Ready, then creates, displays and verifies the session
// after pane input is disabled and before Opened allows the prompt to be sent.
type OpenCodeSetupCoordinator struct {
	mu      sync.Mutex
	setups  map[int]*openCodeSetup
	tmux    OpenCodeSetupTmuxGateway
	timeout time.Duration
}

func NewOpenCodeSetupCoordinator(tmux OpenCodeSetupTmuxGateway, _ obs.Logger) *OpenCodeSetupCoordinator {
	return &OpenCodeSetupCoordinator{setups: make(map[int]*openCodeSetup), tmux: tmux, timeout: openCodeSetupTimeout}
}

func (c *OpenCodeSetupCoordinator) Register(id int, model, variant string, prompt *string, paneID string) {
	var copyPrompt *string
	if prompt != nil {
		value := *prompt
		copyPrompt = &value
	}
	s := &openCodeSetup{model: model, variant: variant, prompt: copyPrompt,
		pane: make(chan string, 1), ready: make(chan OpenCodeSetupReady, 1),
		proceed: make(chan struct{}), opened: make(chan OpenCodeSetupReady, 1), done: make(chan struct{})}
	if paneID != "" {
		s.pane <- paneID
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	s.cancel = cancel
	c.mu.Lock()
	c.setups[id] = s
	c.mu.Unlock()
	go c.run(ctx, s)
}

func (c *OpenCodeSetupCoordinator) get(id int) *openCodeSetup {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.setups[id]
}

func (c *OpenCodeSetupCoordinator) SetPane(id int, paneID string) {
	if s := c.get(id); s != nil {
		select {
		case s.pane <- paneID:
		default:
		}
	}
}

func (c *OpenCodeSetupCoordinator) Ready(ctx context.Context, id int, ready OpenCodeSetupReady) error {
	s := c.get(id)
	if s == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case s.ready <- ready:
	default:
		return errors.New("OpenCode startup setup was already attempted")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.proceed:
		return nil
	case <-s.done:
		return nil
	}
}

func (c *OpenCodeSetupCoordinator) Opened(id int, result OpenCodeSetupReady) error {
	s := c.get(id)
	if s == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case s.opened <- result:
		return nil
	default:
		return errors.New("OpenCode startup session was already reported")
	}
}

func (c *OpenCodeSetupCoordinator) Wait(ctx context.Context, id int) error {
	s := c.get(id)
	if s == nil {
		return ErrAgentSetupNotFound
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return s.err
	}
}

func (c *OpenCodeSetupCoordinator) Forget(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.setups[id]; s != nil {
		s.prompt = nil
	}
	delete(c.setups, id)
}

func (c *OpenCodeSetupCoordinator) Cancel(id int) {
	if s := c.get(id); s != nil {
		s.cancel()
		<-s.done
	}
}

func (c *OpenCodeSetupCoordinator) run(ctx context.Context, s *openCodeSetup) {
	defer s.cancel()
	var pane string
	var ready OpenCodeSetupReady
	gotReady := false
	for pane == "" || !gotReady {
		select {
		case <-ctx.Done():
			c.finish(s, fmt.Errorf("startup setup deadline exceeded: %w", ctx.Err()))
			return
		case pane = <-s.pane:
		case ready = <-s.ready:
			gotReady = true
		}
	}
	if ready.Error != "" {
		c.finish(s, errors.New(ready.Error))
		return
	}
	if ready.Model != s.model || ready.Variant != s.variant {
		c.finish(s, fmt.Errorf("plugin validated model/variant %q/%q, requested %q/%q", ready.Model, ready.Variant, s.model, s.variant))
		return
	}
	c.finish(s, c.automate(ctx, pane, s))
}

func (c *OpenCodeSetupCoordinator) automate(ctx context.Context, pane string, s *openCodeSetup) (result error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.tmux.SetPaneInput(cleanupCtx, pane, true); err != nil {
			result = errors.Join(result, fmt.Errorf("restore pane input: %w", err))
		}
	}()
	if err := c.tmux.SetPaneInput(ctx, pane, false); err != nil {
		return fmt.Errorf("disable pane input: %w", err)
	}
	close(s.proceed)
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for OpenCode session verification: %w", ctx.Err())
	case opened := <-s.opened:
		if opened.Error != "" {
			return errors.New(opened.Error)
		}
		if opened.Model != s.model || opened.Variant != s.variant {
			return fmt.Errorf("verified session model/variant %q/%q differs from requested %q/%q", opened.Model, opened.Variant, s.model, s.variant)
		}
	}
	if s.prompt != nil {
		if err := c.tmux.PasteLiteral(ctx, pane, *s.prompt); err != nil {
			return fmt.Errorf("enter initial prompt: %w", err)
		}
		s.prompt = nil
		if err := c.tmux.SendEnter(ctx, pane); err != nil {
			return fmt.Errorf("submit initial prompt: %w", err)
		}
	}
	return nil
}

func (c *OpenCodeSetupCoordinator) finish(s *openCodeSetup, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s.prompt = nil
	s.err = err
	close(s.done)
}
