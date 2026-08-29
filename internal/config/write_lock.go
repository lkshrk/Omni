package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/flock"
)

type WriteLock struct {
	file *os.File
}

// AcquireWriteLock serializes all config-root writers across processes.
func AcquireWriteLock(configPath string) (*WriteLock, error) {
	dir := configLockRoot(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config root: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".omni-config.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := flock.Lock(f); err != nil {
		return nil, errors.Join(fmt.Errorf("lock config root: %w", err), f.Close())
	}
	return &WriteLock{file: f}, nil
}

func configLockRoot(configPath string) string {
	dir := filepath.Dir(configPath)
	for current := dir; current != filepath.Dir(current); current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, "settings.json")); err == nil {
			return current
		}
	}
	return dir
}

func (l *WriteLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := flock.Unlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(err, closeErr)
}
