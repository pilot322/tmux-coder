package menu

import (
	"path/filepath"
	"testing"

	"github.com/pilot322/tmux-coder/internal/client/httpclient"
)

func TestResolveSessionContext(t *testing.T) {
	project := httpclient.Project{ID: 2, Title: "api", FullPath: "/repo/api"}
	main := httpclient.Session{ID: 1, Type: "main", Branch: "main", Project: project}
	worktree := httpclient.Session{ID: 2, Parent: 1, Type: "worktree", Branch: "feature", Worktree: "/work/api.feature", Project: project}
	secondary := httpclient.Session{ID: 3, Parent: 2, Type: "secondary", RelativeWorkingDirectory: "packages/web", Project: project}
	nested := httpclient.Session{ID: 4, Parent: 3, Type: "secondary", RelativeWorkingDirectory: "tools", Project: project}
	sessions := []httpclient.Session{main, worktree, secondary, nested}

	context, err := ResolveSessionContext(nested, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if context.SessionRoot != "/work/api.feature" || context.WorktreeRoot != "/work/api.feature" || context.Branch != "feature" {
		t.Fatalf("root context = %+v", context)
	}
	if want := filepath.Join("/work/api.feature", "tools"); context.WorkingDirectory != want {
		t.Fatalf("working directory = %q, want %q", context.WorkingDirectory, want)
	}
}

func TestResolveMainSessionContext(t *testing.T) {
	project := httpclient.Project{FullPath: "/repo/api"}
	main := httpclient.Session{ID: 1, Type: "main", Branch: "main", Project: project}
	context, err := ResolveSessionContext(main, []httpclient.Session{main})
	if err != nil {
		t.Fatal(err)
	}
	if context.SessionRoot != project.FullPath || context.WorkingDirectory != project.FullPath || context.WorktreeRoot != "" {
		t.Fatalf("context = %+v", context)
	}
}

func TestResolveSessionContextRejectsMissingParent(t *testing.T) {
	secondary := httpclient.Session{ID: 3, Parent: 99, Type: "secondary", Project: httpclient.Project{FullPath: "/repo"}}
	if _, err := ResolveSessionContext(secondary, []httpclient.Session{secondary}); err == nil {
		t.Fatal("expected missing-parent error")
	}
}
