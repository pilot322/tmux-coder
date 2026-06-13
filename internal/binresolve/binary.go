package binresolve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func ResolveSiblingThenPath(executable, name string, lookPath func(string) (string, error)) (string, error) {
	if executable != "" {
		sibling := filepath.Join(filepath.Dir(executable), name)
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(name)
	if err != nil {
		return "", fmt.Errorf("find %s: %w", name, err)
	}
	return path, nil
}
