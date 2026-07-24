package dots_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestValidateHomeTargetPathWithSymlinkHome(t *testing.T) {
	tmp := t.TempDir()
	realHome := filepath.Join(tmp, "real-home")
	if err := os.MkdirAll(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	if err := os.Symlink(realHome, home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	if err := dots.ValidateHomeTargetPath(home); err != nil {
		t.Fatalf("exact symlink HOME rejected: %v", err)
	}
	if err := dots.ValidateHomeTargetPath(filepath.Join(home, ".config")); err != nil {
		t.Fatalf("child of symlink HOME rejected: %v", err)
	}
	if err := dots.ValidateHomeTargetPath(filepath.Join(tmp, "outside")); err == nil {
		t.Fatal("external sibling accepted as inside symlink HOME")
	}
}
