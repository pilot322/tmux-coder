package menu

import (
	"fmt"

	"github.com/pilot322/tmux-coder/internal/config"
)

type Scope uint8

const (
	GlobalScope Scope = iota
	ProjectScope
)

// Action retains declaration ownership because relative scripts resolve
// differently for global and Project actions.
type Action struct {
	config.MenuAction
	Scope      Scope
	SourcePath string
}

func Merge(global, project []config.MenuAction, globalPath, projectPath string) ([]Action, error) {
	merged := make([]Action, 0, len(global)+len(project))
	byName := make(map[string]int, len(global)+len(project))
	for _, declaration := range global {
		byName[declaration.Name] = len(merged)
		merged = append(merged, Action{MenuAction: declaration, Scope: GlobalScope, SourcePath: globalPath})
	}
	for _, declaration := range project {
		action := Action{MenuAction: declaration, Scope: ProjectScope, SourcePath: projectPath}
		if i, ok := byName[declaration.Name]; ok {
			merged[i] = action
			continue
		}
		byName[declaration.Name] = len(merged)
		merged = append(merged, action)
	}

	byKey := make(map[string]Action, len(merged))
	for _, action := range merged {
		if action.Key == "" {
			continue
		}
		if previous, ok := byKey[action.Key]; ok {
			return nil, fmt.Errorf("duplicate menu-action key %q: %q from %s and %q from %s", action.Key, previous.Name, previous.SourcePath, action.Name, action.SourcePath)
		}
		byKey[action.Key] = action
	}
	return merged, nil
}
