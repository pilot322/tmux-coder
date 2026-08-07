package menu

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pilot322/tmux-coder/internal/config"
	"github.com/sahilm/fuzzy"
)

type mode uint8

const (
	modeInitial mode = iota
	modeFuzzy
	modeArgument
)

type Selection struct {
	Action   Action
	Argument string
}

type Model struct {
	actions       []Action
	direct        map[rune]int
	unkeyed       []int
	mode          mode
	query         string
	argument      string
	hasArgument   bool
	promptAction  int
	cursor        int
	selection     *Selection
	validationErr string
}

var (
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func Run(ctx context.Context, actions []Action) (Selection, bool, error) {
	final, err := tea.NewProgram(NewModel(actions), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if err != nil {
		return Selection{}, false, err
	}
	model := final.(Model)
	if model.selection == nil {
		return Selection{}, false, nil
	}
	return *model.selection, true, nil
}

func NewModel(actions []Action) Model {
	m := Model{actions: actions, direct: make(map[rune]int), promptAction: -1}
	for i, action := range actions {
		if action.Key == "" {
			m.unkeyed = append(m.unkeyed, i)
			continue
		}
		m.direct[[]rune(action.Key)[0]] = i
	}
	return m
}

func (Model) Init() tea.Cmd { return nil }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.Type == tea.KeyEsc || key.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if m.mode == modeArgument {
		return m.updateArgument(key)
	}
	if m.mode == modeFuzzy {
		return m.updateFuzzy(key)
	}
	if key.Type != tea.KeyRunes && key.Type != tea.KeySpace {
		return m, nil
	}
	if !key.Alt && !key.Paste && len(key.Runes) == 1 {
		if i, ok := m.direct[key.Runes[0]]; ok {
			if m.actions[i].Argument == config.ArgumentNone {
				m.selection = &Selection{Action: m.actions[i]}
				return m, tea.Quit
			}
			m.mode = modeArgument
			m.promptAction = i
			return m, nil
		}
	}
	m.mode = modeFuzzy
	for _, r := range key.Runes {
		m.appendFuzzyRune(r)
	}
	return m, nil
}

func (m Model) updateArgument(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEnter:
		if m.actions[m.promptAction].Argument == config.ArgumentRequired && strings.TrimSpace(m.argument) == "" {
			m.validationErr = "argument is required"
			return m, nil
		}
		m.selection = &Selection{Action: m.actions[m.promptAction], Argument: m.argument}
		return m, tea.Quit
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.argument = removeLastRune(m.argument)
		m.validationErr = ""
	case tea.KeyRunes, tea.KeySpace:
		m.argument += string(key.Runes)
		m.validationErr = ""
	}
	return m, nil
}

func (m Model) updateFuzzy(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEnter:
		matches := m.matches()
		if len(matches) == 0 {
			m.validationErr = "no matching action"
			return m, nil
		}
		action := m.actions[matches[m.cursor]]
		argument := strings.TrimSpace(m.argument)
		if action.Argument == config.ArgumentNone && m.hasArgument {
			m.validationErr = fmt.Sprintf("%s does not accept an argument", action.Name)
			return m, nil
		}
		if action.Argument == config.ArgumentRequired && argument == "" {
			m.validationErr = "argument is required"
			return m, nil
		}
		m.selection = &Selection{Action: action, Argument: argument}
		return m, tea.Quit
	case tea.KeyUp:
		m.move(-1)
	case tea.KeyDown:
		m.move(1)
	case tea.KeyBackspace, tea.KeyCtrlH:
		if m.hasArgument {
			m.argument = removeLastRune(m.argument)
		} else {
			m.query = removeLastRune(m.query)
			m.cursor = 0
		}
		m.validationErr = ""
	case tea.KeyRunes, tea.KeySpace:
		for _, r := range key.Runes {
			if !m.hasArgument && !key.Alt && !key.Paste && len(key.Runes) == 1 && (r == 'j' || r == 'k') {
				if r == 'j' {
					m.move(1)
				} else {
					m.move(-1)
				}
				continue
			}
			m.appendFuzzyRune(r)
		}
	}
	return m, nil
}

func (m *Model) appendFuzzyRune(r rune) {
	if !m.hasArgument && r == ' ' {
		m.hasArgument = true
		m.validationErr = ""
		return
	}
	if m.hasArgument {
		m.argument += string(r)
	} else {
		m.query += string(r)
		m.cursor = 0
	}
	m.validationErr = ""
}

func (m *Model) move(delta int) {
	count := len(m.matches())
	if count == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= count {
		m.cursor = count - 1
	}
}

func (m Model) matches() []int {
	if m.query == "" {
		return append([]int(nil), m.unkeyed...)
	}
	names := make([]string, len(m.unkeyed))
	for i, actionIndex := range m.unkeyed {
		names[i] = m.actions[actionIndex].Name
	}
	found := fuzzy.Find(m.query, names)
	matches := make([]int, len(found))
	for i, match := range found {
		matches[i] = m.unkeyed[match.Index]
	}
	return matches
}

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(headingStyle.Render("Menu Actions") + "\n\n")
	if m.mode == modeArgument {
		action := m.actions[m.promptAction]
		b.WriteString(keyStyle.Render(action.Name) + " argument: " + m.argument + "▏\n")
		b.WriteString(mutedStyle.Render("enter run  esc cancel") + "\n")
		if m.validationErr != "" {
			b.WriteString(errorStyle.Render(m.validationErr) + "\n")
		}
		return b.String()
	}
	if m.mode == modeInitial {
		for _, action := range m.actions {
			if action.Key == "" {
				continue
			}
			b.WriteString(" " + keyStyle.Render(action.Key) + "  " + action.Name)
			if action.Description != "" {
				b.WriteString("  " + mutedStyle.Render(action.Description))
			}
			b.WriteByte('\n')
		}
		if len(m.unkeyed) > 0 {
			b.WriteString("\n" + mutedStyle.Render("Type to search other actions") + "\n")
		}
		b.WriteString(mutedStyle.Render("esc cancel") + "\n")
		return b.String()
	}

	b.WriteString(headingStyle.Render("/ ") + m.query)
	if m.hasArgument {
		b.WriteString(" " + m.argument)
	}
	b.WriteString("▏\n")
	matches := m.matches()
	if len(matches) == 0 {
		b.WriteString("  " + mutedStyle.Render("no matches") + "\n")
	}
	for i, actionIndex := range matches {
		action := m.actions[actionIndex]
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		b.WriteString(prefix + action.Name)
		if action.Description != "" {
			b.WriteString("  " + mutedStyle.Render(action.Description))
		}
		b.WriteByte('\n')
	}
	if m.validationErr != "" {
		b.WriteString(errorStyle.Render(m.validationErr) + "\n")
	}
	b.WriteString(mutedStyle.Render("up/down or j/k move  enter run  esc cancel") + "\n")
	return b.String()
}

func removeLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	return string(runes[:len(runes)-1])
}
