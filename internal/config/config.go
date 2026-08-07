// Package config reads and validates a Project's Config File
// (`.tmux-coder/.tmux-coder.toml`). It is pure: its only side effect is reading
// the file passed to Load. All static validation of declared Secondary Sessions
// (ADR-0007) happens here, before any tmux work, so a malformed Config File
// fails a create operation loudly instead of producing a partial session tree.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"
)

// DefaultWorktreeHookTimeout bounds the on-create worktree hook when the Config
// File does not specify one.
const DefaultWorktreeHookTimeout = 2 * time.Minute

const (
	ArgumentNone     = "none"
	ArgumentOptional = "optional"
	ArgumentRequired = "required"
)

// maxSessionDepth is the maximum total ancestry depth of a Session, counting a
// Main or Worktree root as depth one (ADR-0006).
const maxSessionDepth = 5

// ErrValidation marks a malformed or semantically invalid Config File. Callers
// test for it with errors.Is and translate it to their own validation error.
var ErrValidation = errors.New("invalid config file")

// File is a decoded, validated Config File.
type File struct {
	Worktree    Worktree
	Secondaries []Secondary // topologically ordered: a parent precedes its children
	MenuActions []MenuAction
}

// Worktree holds the [worktree] section: the on-create hook and its timeout.
type Worktree struct {
	OnCreateScript  string
	OnCreateTimeout time.Duration
}

// Secondary is one declared Secondary Session. ID and Parent are config-local
// handles only (ADR-0007); Parent resolves against another entry's ID.
type Secondary struct {
	Subdir   string
	Name     string
	OnDelete string
	ID       string
	Parent   string
}

// MenuAction is one validated declaration from a Config File or Action File.
type MenuAction struct {
	Name        string
	Description string
	Key         string
	Script      string
	Argument    string
}

// rawFile mirrors the on-disk TOML shape with kebab-case keys.
type rawFile struct {
	Worktree    rawWorktree     `toml:"worktree"`
	Secondaries []rawSecondary  `toml:"secondary-sessions"`
	MenuActions []rawMenuAction `toml:"menu-actions"`
}

type rawActionFile struct {
	MenuActions []rawMenuAction `toml:"menu-actions"`
}

type rawWorktree struct {
	OnCreateScript  string `toml:"on-create-script"`
	OnCreateTimeout string `toml:"on-create-timeout"`
}

type rawSecondary struct {
	Subdir   string `toml:"subdir"`
	Name     string `toml:"name"`
	OnDelete string `toml:"on-delete"`
	ID       string `toml:"id"`
	Parent   string `toml:"parent"`
}

type rawMenuAction struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Key         string `toml:"key"`
	Script      string `toml:"script"`
	Argument    string `toml:"argument"`
}

var menuActionName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ProjectPath returns the Config File path owned by projectRoot.
func ProjectPath(projectRoot string) string {
	return filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml")
}

// Load reads, decodes and validates the Config File under projectRoot. A
// missing file is not an error: it yields a zero File with the default hook
// timeout and no Secondary Sessions. A read failure (other than not-exist) is
// returned verbatim so the caller can distinguish I/O from validation.
func Load(projectRoot string) (File, error) {
	path := ProjectPath(projectRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{Worktree: Worktree{OnCreateTimeout: DefaultWorktreeHookTimeout}}, nil
		}
		return File{}, fmt.Errorf("read config file: %w", err)
	}
	file, err := Parse(data)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", path, err)
	}
	return file, nil
}

// LoadActionFile reads and strictly validates a global Action File. A missing
// file yields no actions.
func LoadActionFile(path string) ([]MenuAction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read action file %s: %w", path, err)
	}
	actions, err := ParseActionFile(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return actions, nil
}

// Parse strictly decodes and validates Config File bytes. Unknown keys are a
// hard error so a typo surfaces immediately.
func Parse(data []byte) (File, error) {
	var raw rawFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	timeout := DefaultWorktreeHookTimeout
	if raw.Worktree.OnCreateTimeout != "" {
		d, err := time.ParseDuration(raw.Worktree.OnCreateTimeout)
		if err != nil || d <= 0 {
			return File{}, fmt.Errorf("%w: invalid on-create-timeout %q", ErrValidation, raw.Worktree.OnCreateTimeout)
		}
		timeout = d
	}

	secondaries := make([]Secondary, len(raw.Secondaries))
	for i, rs := range raw.Secondaries {
		onDelete := rs.OnDelete
		if onDelete == "" {
			onDelete = "cascade"
		}
		secondaries[i] = Secondary{
			Subdir:   rs.Subdir,
			Name:     rs.Name,
			OnDelete: onDelete,
			ID:       rs.ID,
			Parent:   rs.Parent,
		}
	}

	ordered, err := validateSecondaries(secondaries)
	if err != nil {
		return File{}, err
	}
	actions, err := validateMenuActions(raw.MenuActions)
	if err != nil {
		return File{}, err
	}

	return File{
		Worktree: Worktree{
			OnCreateScript:  raw.Worktree.OnCreateScript,
			OnCreateTimeout: timeout,
		},
		Secondaries: ordered,
		MenuActions: actions,
	}, nil
}

// ParseActionFile strictly decodes and validates Action File bytes.
func ParseActionFile(data []byte) ([]MenuAction, error) {
	var raw rawActionFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	return validateMenuActions(raw.MenuActions)
}

func validateMenuActions(raw []rawMenuAction) ([]MenuAction, error) {
	actions := make([]MenuAction, len(raw))
	seen := make(map[string]bool, len(raw))
	for i, action := range raw {
		if !menuActionName.MatchString(action.Name) {
			return nil, fmt.Errorf("%w: menu-action %d has invalid name %q", ErrValidation, i, action.Name)
		}
		if seen[action.Name] {
			return nil, fmt.Errorf("%w: duplicate menu-action name %q", ErrValidation, action.Name)
		}
		seen[action.Name] = true
		if action.Script == "" {
			return nil, fmt.Errorf("%w: menu-action %q needs a script", ErrValidation, action.Name)
		}
		argument := action.Argument
		if argument == "" {
			argument = ArgumentNone
		}
		if argument != ArgumentNone && argument != ArgumentOptional && argument != ArgumentRequired {
			return nil, fmt.Errorf("%w: menu-action %q has invalid argument %q", ErrValidation, action.Name, action.Argument)
		}
		if action.Key != "" {
			runes := []rune(action.Key)
			if len(runes) != 1 || !unicode.IsPrint(runes[0]) {
				return nil, fmt.Errorf("%w: menu-action %q key must be one printable rune", ErrValidation, action.Name)
			}
		}
		actions[i] = MenuAction{
			Name:        action.Name,
			Description: action.Description,
			Key:         action.Key,
			Script:      action.Script,
			Argument:    argument,
		}
	}
	return actions, nil
}

// validateSecondaries enforces every static rule from ADR-0007 and returns the
// entries topologically ordered (a parent precedes its children). It first runs
// the order-independent checks — a usable handle per entry, unique ids, an
// explicit on-delete policy, parent references that resolve without self-loops —
// then sorts, which also detects cycles.
func validateSecondaries(secondaries []Secondary) ([]Secondary, error) {
	byID := make(map[string]int, len(secondaries))
	for i, s := range secondaries {
		if s.Subdir == "" && s.Name == "" {
			return nil, fmt.Errorf("%w: secondary-session %d needs a subdir or a name", ErrValidation, i)
		}
		if s.OnDelete != "cascade" && s.OnDelete != "inherit" {
			return nil, fmt.Errorf("%w: secondary-session %d has invalid on-delete %q", ErrValidation, i, s.OnDelete)
		}
		if s.ID != "" {
			if _, dup := byID[s.ID]; dup {
				return nil, fmt.Errorf("%w: duplicate secondary-session id %q", ErrValidation, s.ID)
			}
			byID[s.ID] = i
		}
	}

	for _, s := range secondaries {
		if s.Parent == "" {
			continue
		}
		if s.Parent == s.ID {
			return nil, fmt.Errorf("%w: secondary-session %q cannot parent itself", ErrValidation, s.ID)
		}
		if _, ok := byID[s.Parent]; !ok {
			return nil, fmt.Errorf("%w: secondary-session parent %q matches no declared id", ErrValidation, s.Parent)
		}
	}

	return topoSort(secondaries, byID)
}

// topoSort returns the entries with every parent ahead of its children. Each
// entry has at most one parent, so a child is ready the moment its parent is
// emitted; processing roots first in declaration order keeps the output stable.
// Any entry left unemitted sits on a cycle.
func topoSort(secondaries []Secondary, byID map[string]int) ([]Secondary, error) {
	children := make(map[int][]int, len(secondaries))
	depth := make(map[int]int, len(secondaries))
	var queue []int
	for i, s := range secondaries {
		if s.Parent == "" {
			// A root-parented secondary sits one level under the root Session,
			// which is itself depth 1 (ADR-0006).
			depth[i] = 2
			queue = append(queue, i)
			continue
		}
		p := byID[s.Parent]
		children[p] = append(children[p], i)
	}

	ordered := make([]Secondary, 0, len(secondaries))
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		if depth[i] > maxSessionDepth {
			return nil, fmt.Errorf("%w: secondary-session %q exceeds the maximum session depth of %d", ErrValidation, secondaries[i].ID, maxSessionDepth)
		}
		ordered = append(ordered, secondaries[i])
		for _, c := range children[i] {
			depth[c] = depth[i] + 1
		}
		queue = append(queue, children[i]...)
	}

	if len(ordered) != len(secondaries) {
		return nil, fmt.Errorf("%w: secondary-sessions contain a parent cycle", ErrValidation)
	}
	return ordered, nil
}
