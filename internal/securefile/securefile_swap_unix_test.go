//go:build !windows

package securefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDescriptorRelativeWriteResistsRootSwap(t *testing.T) {
	base := t.TempDir()
	logical := filepath.Join(base, "private")
	moved := filepath.Join(base, "moved")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot(logical)
	if err != nil {
		t.Fatal(err)
	}
	securefileSwapTestHook = func() error {
		if err := os.Rename(logical, moved); err != nil {
			return err
		}
		return os.Symlink(outside, logical)
	}
	defer func() { securefileSwapTestHook = nil }()
	if err := root.WriteFileAtomic("secret", []byte("canary")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(moved, "secret")); err != nil || string(data) != "canary" {
		t.Fatalf("anchored write missing: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); !os.IsNotExist(err) {
		t.Fatalf("write escaped root: %v", err)
	}
}
