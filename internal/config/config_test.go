package config_test

import (
	"errors"
	"testing"
	"time"

	"github.com/pilot322/tmux-coder/internal/config"
)

func TestParseDecodesWorktreeAndSecondaries(t *testing.T) {
	data := []byte(`
[worktree]
on-create-script = "setup.sh"
on-create-timeout = "30s"

[[secondary-sessions]]
subdir = "backend"
id = "backend"

[[secondary-sessions]]
subdir = "tools"
parent = "backend"
on-delete = "inherit"
`)

	file, err := config.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if file.Worktree.OnCreateScript != "setup.sh" {
		t.Errorf("OnCreateScript = %q, want setup.sh", file.Worktree.OnCreateScript)
	}
	if file.Worktree.OnCreateTimeout != 30*time.Second {
		t.Errorf("OnCreateTimeout = %v, want 30s", file.Worktree.OnCreateTimeout)
	}
	if len(file.Secondaries) != 2 {
		t.Fatalf("len(Secondaries) = %d, want 2", len(file.Secondaries))
	}
	if file.Secondaries[0].ID != "backend" || file.Secondaries[0].Subdir != "backend" {
		t.Errorf("first secondary = %+v", file.Secondaries[0])
	}
	// on-delete defaults to cascade when omitted.
	if file.Secondaries[0].OnDelete != "cascade" {
		t.Errorf("first secondary OnDelete = %q, want cascade", file.Secondaries[0].OnDelete)
	}
	if file.Secondaries[1].OnDelete != "inherit" {
		t.Errorf("second secondary OnDelete = %q, want inherit", file.Secondaries[1].OnDelete)
	}
}

func TestParseRejectsStaticErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{
			name: "unknown key",
			toml: "[worktree]\nunknown-key = \"x\"\n",
		},
		{
			name: "unknown secondary key",
			toml: "[[secondary-sessions]]\nsubdir = \"a\"\nbogus = \"x\"\n",
		},
		{
			name: "bad on-delete value",
			toml: "[[secondary-sessions]]\nsubdir = \"a\"\non-delete = \"reparent\"\n",
		},
		{
			name: "missing subdir and name",
			toml: "[[secondary-sessions]]\nid = \"a\"\n",
		},
		{
			name: "duplicate id",
			toml: "[[secondary-sessions]]\nsubdir = \"a\"\nid = \"dup\"\n[[secondary-sessions]]\nsubdir = \"b\"\nid = \"dup\"\n",
		},
		{
			name: "self parent",
			toml: "[[secondary-sessions]]\nsubdir = \"a\"\nid = \"a\"\nparent = \"a\"\n",
		},
		{
			name: "dangling parent",
			toml: "[[secondary-sessions]]\nsubdir = \"a\"\nparent = \"ghost\"\n",
		},
		{
			name: "invalid timeout",
			toml: "[worktree]\non-create-timeout = \"soon\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tt.toml))
			errValidation(t, err)
		})
	}
}

func TestParseTopologicallyOrdersForwardReferences(t *testing.T) {
	// The child is declared before the parent; Parse must still resolve the
	// reference and emit the parent ahead of the child.
	data := []byte(`
[[secondary-sessions]]
subdir = "tools"
id = "tools"
parent = "backend"

[[secondary-sessions]]
subdir = "backend"
id = "backend"
`)

	file, err := config.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	order := []string{file.Secondaries[0].ID, file.Secondaries[1].ID}
	if want := []string{"backend", "tools"}; order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("topo order = %v, want %v", order, want)
	}
}

func TestParseRejectsCycle(t *testing.T) {
	data := []byte(`
[[secondary-sessions]]
subdir = "a"
id = "a"
parent = "b"

[[secondary-sessions]]
subdir = "b"
id = "b"
parent = "a"
`)
	_, err := config.Parse(data)
	errValidation(t, err)
}

// chainConfig builds n secondaries chained parent->child (s1 has no parent).
func chainConfig(n int) []byte {
	var b []byte
	for i := 1; i <= n; i++ {
		entry := "\n[[secondary-sessions]]\nsubdir = \"s" + itoa(i) + "\"\nid = \"s" + itoa(i) + "\"\n"
		if i > 1 {
			entry += "parent = \"s" + itoa(i-1) + "\"\n"
		}
		b = append(b, entry...)
	}
	return b
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestParseAcceptsDepthAtLimit(t *testing.T) {
	// Four chained secondaries reach depth 5 counting the root — the limit.
	if _, err := config.Parse(chainConfig(4)); err != nil {
		t.Fatalf("Parse depth-5 chain: %v", err)
	}
}

func TestParseRejectsDepthOverLimit(t *testing.T) {
	// Five chained secondaries reach depth 6 counting the root.
	_, err := config.Parse(chainConfig(5))
	errValidation(t, err)
}

// errValidation asserts an error chains to config.ErrValidation.
func errValidation(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want validation error, got nil")
	}
	if !errors.Is(err, config.ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
}
