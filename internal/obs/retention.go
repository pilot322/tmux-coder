package obs

import (
	"os"
	"path/filepath"
	"time"
)

// retentionAge bounds how long a role directory keeps files. lumberjack only
// manages backups of its own current base name — never a previous pid's file or
// a past day's file — so cross-run cleanup is this separate age sweep, run by
// every process over its own role directory at startup.
const retentionAge = 14 * 24 * time.Hour

// sweep removes every regular file in dir whose mtime is older than maxAge,
// measured from now. A missing directory is not an error: the first process of
// an instance sweeps before anything has been written. Subdirectories and
// per-file removal errors are skipped so one undeletable file can't abort the
// sweep.
func sweep(dir string, maxAge time.Duration, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}
