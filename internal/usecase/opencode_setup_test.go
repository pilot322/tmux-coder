package usecase

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/obs"
)

type setupTmuxFake struct {
	calls    []string
	pasteErr error
}

func (f *setupTmuxFake) SetPaneInput(_ context.Context, _ string, on bool) error {
	if on {
		f.calls = append(f.calls, "input:on")
	} else {
		f.calls = append(f.calls, "input:off")
	}
	return nil
}
func (f *setupTmuxFake) PasteLiteral(_ context.Context, _ string, text string) error {
	f.calls = append(f.calls, "paste:"+text)
	return f.pasteErr
}
func (f *setupTmuxFake) SendEnter(context.Context, string) error {
	f.calls = append(f.calls, "enter")
	return nil
}

func ready(t *testing.T, c *OpenCodeSetupCoordinator, id int, model, variant string) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- c.Ready(context.Background(), id, OpenCodeSetupReady{Model: model, Variant: variant, Version: "2.0.16"})
	}()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestSetupVerifiesBeforeLiteralPromptExactlyOnce(t *testing.T) {
	f := &setupTmuxFake{}
	c := NewOpenCodeSetupCoordinator(f, obs.Nop())
	prompt := "quotes '$HOME' ; Unicode λ\nsecond line"
	c.Register(7, "anthropic/haiku", "high", &prompt, "%4")
	ready(t, c, 7, "anthropic/haiku", "high")
	if len(f.calls) != 1 || f.calls[0] != "input:off" {
		t.Fatalf("early input: %#v", f.calls)
	}
	if err := c.Opened(7, OpenCodeSetupReady{Model: "anthropic/haiku", Variant: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	want := []string{"input:off", "paste:" + prompt, "enter", "input:on"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %#v, want %#v", f.calls, want)
	}
	if c.get(7).prompt != nil {
		t.Fatal("prompt retained")
	}
}

func TestSetupRejectsUnverifiedModelWithoutPrompt(t *testing.T) {
	f := &setupTmuxFake{}
	c := NewOpenCodeSetupCoordinator(f, obs.Nop())
	prompt := "secret"
	c.Register(8, "provider/model", "", &prompt, "%5")
	ready(t, c, 8, "provider/model", "")
	_ = c.Opened(8, OpenCodeSetupReady{Model: "other/model"})
	if err := c.Wait(context.Background(), 8); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("error: %v", err)
	}
	if !reflect.DeepEqual(f.calls, []string{"input:off", "input:on"}) {
		t.Fatalf("calls: %#v", f.calls)
	}
}

func TestSetupRestoresInputAfterPasteFailure(t *testing.T) {
	f := &setupTmuxFake{pasteErr: errors.New("paste failed")}
	c := NewOpenCodeSetupCoordinator(f, obs.Nop())
	prompt := "hello"
	c.Register(8, "", "", &prompt, "%5")
	ready(t, c, 8, "", "")
	_ = c.Opened(8, OpenCodeSetupReady{})
	err := c.Wait(context.Background(), 8)
	if err == nil || !strings.Contains(err.Error(), "paste failed") {
		t.Fatalf("error: %v", err)
	}
	if f.calls[len(f.calls)-1] != "input:on" {
		t.Fatalf("calls: %#v", f.calls)
	}
}

func TestSetupDeadlineAndCancellation(t *testing.T) {
	f := &setupTmuxFake{}
	c := NewOpenCodeSetupCoordinator(f, obs.Nop())
	c.timeout = 20 * time.Millisecond
	prompt := "secret"
	c.Register(9, "", "", &prompt, "%6")
	if err := c.Wait(context.Background(), 9); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("deadline: %v", err)
	}
	if c.get(9).prompt != nil {
		t.Fatal("prompt retained on deadline")
	}
	c.Register(10, "", "", &prompt, "%7")
	ready(t, c, 10, "", "")
	c.Cancel(10)
	if err := c.Wait(context.Background(), 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if c.get(10).prompt != nil || f.calls[len(f.calls)-1] != "input:on" {
		t.Fatal("cancellation did not restore input/discard prompt")
	}
}

func TestValidateOpenCodeSetup(t *testing.T) {
	model := "provider/model/with/slash"
	prompt := " "
	if err := validateOpenCodeSetup("opencode", &model, nil, &prompt); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if err := validateOpenCodeSetup("opencode", nil, nil, &empty); err == nil {
		t.Fatal("empty prompt accepted")
	}
	if err := validateOpenCodeSetup("claude", &model, nil, nil); err == nil {
		t.Fatal("non-OpenCode model accepted")
	}
	variant := "high"
	if err := validateOpenCodeSetup("opencode", nil, &variant, nil); err == nil {
		t.Fatal("variant without model accepted")
	}
}
