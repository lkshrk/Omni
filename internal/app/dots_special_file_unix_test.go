//go:build !windows

package app_test

import (
	"syscall"
	"testing"
)

func makeIgnoredSpecialFile(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo %q: %v", path, err)
	}
}
