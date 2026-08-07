package menu

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pilot322/tmux-coder/internal/config"
)

func TestDirectKeySelectsNoneActionImmediately(t *testing.T) {
	m := NewModel([]Action{{MenuAction: config.MenuAction{Name: "commit", Key: "c", Argument: config.ArgumentNone}}})
	m, cmd := updateModel(m, runeKey('c'))
	if cmd == nil || m.selection == nil || m.selection.Action.Name != "commit" {
		t.Fatalf("model = %+v, cmd nil = %v", m, cmd == nil)
	}
}

func TestDirectRequiredArgumentValidatesAndSelects(t *testing.T) {
	m := NewModel([]Action{{MenuAction: config.MenuAction{Name: "fix", Key: "f", Argument: config.ArgumentRequired}}})
	m, _ = updateModel(m, runeKey('f'))
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.validationErr == "" || m.selection != nil {
		t.Fatalf("empty submit = %+v", m)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("  repair this  ")})
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.selection == nil || m.selection.Argument != "  repair this  " {
		t.Fatalf("selection = %+v", m.selection)
	}
}

func TestDirectOptionalArgumentMayBeEmpty(t *testing.T) {
	m := NewModel([]Action{{MenuAction: config.MenuAction{Name: "review", Key: "r", Argument: config.ArgumentOptional}}})
	m, _ = updateModel(m, runeKey('r'))
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.selection == nil || m.selection.Argument != "" {
		t.Fatalf("selection = %+v", m.selection)
	}
}

func TestPasteAndAltDoNotTriggerDirectKeys(t *testing.T) {
	actions := []Action{
		{MenuAction: config.MenuAction{Name: "commit", Key: "c", Argument: config.ArgumentNone}},
		{MenuAction: config.MenuAction{Name: "fix-code", Argument: config.ArgumentOptional}},
	}
	m := NewModel(actions)
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fx-c repair"), Paste: true})
	if cmd != nil || m.selection != nil || m.mode != modeFuzzy || m.query != "fx-c" || m.argument != "repair" {
		t.Fatalf("pasted model = %+v", m)
	}

	m = NewModel(actions)
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	if cmd != nil || m.selection != nil || m.mode != modeFuzzy || m.query != "c" {
		t.Fatalf("alt model = %+v", m)
	}
}

func TestFuzzySelectionSplitsAndPreservesArgument(t *testing.T) {
	actions := []Action{
		{MenuAction: config.MenuAction{Name: "commit", Key: "c", Argument: config.ArgumentNone}},
		{MenuAction: config.MenuAction{Name: "fix-issues", Argument: config.ArgumentRequired}},
	}
	m := NewModel(actions)
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fx- repair  API tests ")})
	if matches := m.matches(); len(matches) != 1 || actions[matches[0]].Name != "fix-issues" {
		t.Fatalf("matches = %v", matches)
	}
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.selection == nil || m.selection.Action.Name != "fix-issues" || m.selection.Argument != "repair  API tests" {
		t.Fatalf("selection = %+v", m.selection)
	}
}

func TestFuzzyExcludesDirectActionsAndNoMatchStaysOpen(t *testing.T) {
	m := NewModel([]Action{
		{MenuAction: config.MenuAction{Name: "commit", Key: "c", Argument: config.ArgumentNone}},
		{MenuAction: config.MenuAction{Name: "review", Argument: config.ArgumentNone}},
	})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ommit")})
	if len(m.matches()) != 0 {
		t.Fatalf("matches = %v", m.matches())
	}
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.selection != nil || m.validationErr == "" {
		t.Fatalf("model = %+v", m)
	}
}

func TestFuzzyArgumentRulesAndNavigation(t *testing.T) {
	actions := []Action{
		{MenuAction: config.MenuAction{Name: "alpha", Argument: config.ArgumentNone}},
		{MenuAction: config.MenuAction{Name: "alpine", Argument: config.ArgumentOptional}},
	}
	m := NewModel(actions)
	m, _ = updateModel(m, runeKey('a'))
	m, _ = updateModel(m, runeKey('j'))
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" value")})
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.selection == nil || m.selection.Action.Name != "alpine" || m.selection.Argument != "value" {
		t.Fatalf("selection = %+v", m.selection)
	}

	m = NewModel([]Action{{MenuAction: config.MenuAction{Name: "alpha", Argument: config.ArgumentNone}}})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a supplied")})
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.selection != nil || m.validationErr == "" {
		t.Fatalf("none argument model = %+v", m)
	}
}

func TestEscapeCancelsWithoutSelection(t *testing.T) {
	m := NewModel([]Action{{MenuAction: config.MenuAction{Name: "review", Argument: config.ArgumentNone}}})
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil || m.selection != nil {
		t.Fatalf("model = %+v, cmd nil = %v", m, cmd == nil)
	}
}

func updateModel(m Model, message tea.KeyMsg) (Model, tea.Cmd) {
	model, cmd := m.Update(message)
	return model.(Model), cmd
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}
