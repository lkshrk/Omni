//go:build windows

package app_test

import "testing"

func makeIgnoredSpecialFile(t *testing.T, _ string) {
	t.Helper()
	t.Skip("FIFO special-file regression requires Unix")
}
