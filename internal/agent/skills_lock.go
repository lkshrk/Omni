package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// Each step is atomic alone, but the backup-rename dances interleave destructively across processes; the lock is held by the open file so a kill can never strand it.
type storeLock struct{ file *os.File }

// Blocks so concurrent omni invocations queue instead of failing.
func (s *Skills) lockStore() (*storeLock, error) {
	if s.dataDir == "" {
		return nil, fmt.Errorf("skills data dir is not configured")
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating skills data dir: %w", err)
	}
	path := filepath.Join(s.dataDir, ".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening skills store lock %s: %w", path, err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("waiting for the skills store lock %s: %w", path, err)
	}
	return &storeLock{file: file}, nil
}

// A fixer prunes target skill dirs that exist whether or not the store does, so a merely absent store must not drop the lock an install holds while staging into those same dirs.
// An unusable store is the one safe exception: Install takes this lock before it touches a target dir, so a store nobody can create is a store nobody can be racing in.
func (s *Skills) lockStoreForWrite(dryRun bool) (*storeLock, error) {
	if dryRun {
		return nil, nil
	}
	if s.dataDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return nil, nil
	}
	return s.lockStore()
}

func (l *storeLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlockFile(l.file)
	_ = l.file.Close()
}
