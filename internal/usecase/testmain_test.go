package usecase_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tmux-coder-usecase-test-*")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "tmux-coder")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		panic(err)
	}
	oldPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
	code := m.Run()
	_ = os.Setenv("PATH", oldPath)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
