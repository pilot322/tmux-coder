package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
)

type setupTmuxFake struct {
	calls      []string
	statePath  string
	model      string
	variant    string
	enterCount int
	pasteErr   error
}

func (f *setupTmuxFake) SetPaneInput(_ context.Context, _ string, enabled bool) error {
	f.calls = append(f.calls, "input:"+map[bool]string{true: "on", false: "off"}[enabled])
	return nil
}

func (f *setupTmuxFake) PasteLiteral(_ context.Context, _ string, text string) error {
	f.calls = append(f.calls, "paste:"+text)
	return f.pasteErr
}

func (f *setupTmuxFake) SendEnter(_ context.Context, _ string) error {
	f.enterCount++
	f.calls = append(f.calls, "enter")
	if f.enterCount == 1 && f.model != "" {
		provider, model, _ := strings.Cut(f.model, "/")
		data := []byte(`{"recent":[{"providerID":"` + provider + `","modelID":"` + model + `"}]}`)
		return os.WriteFile(filepath.Join(f.statePath, "model.json"), data, 0o600)
	}
	if f.enterCount == 2 && f.model != "" {
		provider, model, _ := strings.Cut(f.model, "/")
		variant := f.variant
		if variant == "" {
			variant = "default"
		}
		data := []byte(`{"recent":[{"providerID":"` + provider + `","modelID":"` + model + `"}],"variant":{"` + f.model + `":"` + variant + `"}}`)
		return os.WriteFile(filepath.Join(f.statePath, "model.json"), data, 0o600)
	}
	return nil
}

func TestOpenCodeSetupOrdersModelVerificationBeforeLiteralPrompt(t *testing.T) {
	statePath := t.TempDir()
	fake := &setupTmuxFake{statePath: statePath, model: "anthropic/claude-haiku"}
	coordinator := NewOpenCodeSetupCoordinator(fake, obs.Nop())
	prompt := "quotes '$HOME' ; Unicode λ\nsecond line"
	coordinator.Register(7, fake.model, "", &prompt, "%4")
	if err := coordinator.SetStatePath(7, statePath); err != nil {
		t.Fatal(err)
	}
	readyDone := make(chan error, 1)
	go func() {
		readyDone <- coordinator.Ready(context.Background(), 7, OpenCodeSetupReady{
			Model: fake.model, DisplayName: "Claude Haiku", StatePath: statePath, Version: "1.18.14", HasVariants: true,
		})
	}()
	if err := <-readyDone; err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Opened(7, ""); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Wait(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"input:off", "paste:Claude Haiku", "enter", "paste:Default", "enter", "paste:" + prompt, "enter", "input:on",
	}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fake.calls, want)
	}
}

func TestOpenCodeSetupRestoresInputAfterFailure(t *testing.T) {
	fake := &setupTmuxFake{pasteErr: errors.New("paste failed")}
	coordinator := NewOpenCodeSetupCoordinator(fake, obs.Nop())
	prompt := "hello"
	coordinator.Register(8, "", "", &prompt, "%5")
	readyDone := make(chan error, 1)
	go func() {
		readyDone <- coordinator.Ready(context.Background(), 8, OpenCodeSetupReady{Version: "1.18.14"})
	}()
	if err := <-readyDone; err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Opened(8, ""); err != nil {
		t.Fatal(err)
	}
	err := coordinator.Wait(context.Background(), 8)
	if err == nil || !strings.Contains(err.Error(), "paste failed") {
		t.Fatalf("error = %v", err)
	}
	if got := fake.calls[len(fake.calls)-1]; got != "input:on" {
		t.Fatalf("last call = %q, want input restoration", got)
	}
}

func TestOpenCodeSetupDeadline(t *testing.T) {
	coordinator := NewOpenCodeSetupCoordinator(&setupTmuxFake{}, obs.Nop())
	coordinator.timeout = 20 * time.Millisecond
	prompt := "hello"
	coordinator.Register(9, "", "", &prompt, "%6")
	err := coordinator.Wait(context.Background(), 9)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenCodeSetupCancellationRestoresInputBeforePromptDisposal(t *testing.T) {
	fake := &setupTmuxFake{}
	coordinator := NewOpenCodeSetupCoordinator(fake, obs.Nop())
	prompt := "do not retain me"
	coordinator.Register(10, "", "", &prompt, "%7")
	readyDone := make(chan error, 1)
	go func() {
		readyDone <- coordinator.Ready(context.Background(), 10, OpenCodeSetupReady{Version: "1.18.14"})
	}()
	if err := <-readyDone; err != nil {
		t.Fatal(err)
	}
	coordinator.Cancel(10)
	if err := coordinator.Wait(context.Background(), 10); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	coordinator.mu.Lock()
	retained := coordinator.setups[10].prompt
	coordinator.mu.Unlock()
	if retained != nil {
		t.Fatal("prompt retained after cancellation")
	}
	if got := fake.calls[len(fake.calls)-1]; got != "input:on" {
		t.Fatalf("last call = %q, want input restoration", got)
	}
}

func TestValidateOpenCodeSetup(t *testing.T) {
	model := "provider/model/with/slash"
	prompt := " "
	if err := validateOpenCodeSetup("opencode", &model, nil, &prompt); err != nil {
		t.Fatalf("valid setup: %v", err)
	}
	empty := ""
	if err := validateOpenCodeSetup("opencode", nil, nil, &empty); err == nil {
		t.Fatal("expected empty prompt rejection")
	}
	if err := validateOpenCodeSetup("claude", &model, nil, nil); err == nil {
		t.Fatal("expected non-OpenCode model rejection")
	}
	variant := "high"
	if err := validateOpenCodeSetup("opencode", nil, &variant, nil); err == nil {
		t.Fatal("expected variant without model rejection")
	}
}

func TestOpenCodeSetupSelectsAndVerifiesRequestedVariant(t *testing.T) {
	statePath := t.TempDir()
	fake := &setupTmuxFake{statePath: statePath, model: "openai/gpt-5.6-luna", variant: "high"}
	coordinator := NewOpenCodeSetupCoordinator(fake, obs.Nop())
	coordinator.Register(11, fake.model, fake.variant, nil, "%8")
	if err := coordinator.SetStatePath(11, statePath); err != nil {
		t.Fatal(err)
	}
	readyDone := make(chan error, 1)
	go func() {
		readyDone <- coordinator.Ready(context.Background(), 11, OpenCodeSetupReady{
			Model: fake.model, Variant: fake.variant, DisplayName: "GPT-5.6 Luna", StatePath: statePath, Version: "1.18.14", HasVariants: true,
		})
	}()
	if err := <-readyDone; err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Opened(11, ""); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Wait(context.Background(), 11); err != nil {
		t.Fatal(err)
	}
	want := []string{"input:off", "paste:GPT-5.6 Luna", "enter", "paste:high", "enter", "input:on"}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("calls = %#v, want %#v", fake.calls, want)
	}
}
