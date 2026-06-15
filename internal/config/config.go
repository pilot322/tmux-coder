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
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// DefaultWorktreeHookTimeout bounds the on-create worktree hook when the Config
// File does not specify one.
const DefaultWorktreeHookTimeout = 2 * time.Minute

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

// rawFile mirrors the on-disk TOML shape with kebab-case keys.
type rawFile struct {
	Worktree    rawWorktree    `toml:"worktree"`
	Secondaries []rawSecondary `toml:"secondary-sessions"`
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

// Load reads, decodes and validates the Config File under projectRoot. A
// missing file is not an error: it yields a zero File with the default hook
// timeout and no Secondary Sessions. A read failure (other than not-exist) is
// returned verbatim so the caller can distinguish I/O from validation.
func Load(projectRoot string) (File, error) {
	path := filepath.Join(projectRoot, ".tmux-coder", ".tmux-coder.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{Worktree: Worktree{OnCreateTimeout: DefaultWorktreeHookTimeout}}, nil
		}
		return File{}, fmt.Errorf("read config file: %w", err)
	}
	return Parse(data)
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

	return File{
		Worktree: Worktree{
			OnCreateScript:  raw.Worktree.OnCreateScript,
			OnCreateTimeout: timeout,
		},
		Secondaries: ordered,
	}, nil
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
