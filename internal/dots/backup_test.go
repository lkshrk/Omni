package dots_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestMain(m *testing.M) {
	origHome, hadHome := os.LookupEnv("HOME")
	testHome, err := os.MkdirTemp("", "omni-dots-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", testHome); err != nil {
		panic(err)
	}
	code := m.Run()
	if hadHome {
		_ = os.Setenv("HOME", origHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	_ = os.RemoveAll(testHome)
	os.Exit(code)
}

func TestBackupLocalPath_CopiesHomeRelativePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(home, ".config", "nvim", "init.lua")
	writeFile(t, src, "-- local")

	backup, err := dots.BackupLocalPath(src)
	if err != nil {
		t.Fatalf("BackupLocalPath: %v", err)
	}
	want := filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")
	if backup != want {
		t.Fatalf("backup path = %q, want %q", backup, want)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "-- local" {
		t.Fatalf("backup content = %q, want -- local", string(got))
	}
}

func TestBackupLocalPath_UsesSuffixWhenBackupExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(home, ".zshrc")
	writeFile(t, src, "# local")
	first := filepath.Join(home, dots.BackupDirName, ".zshrc")
	writeFile(t, first, "# old backup")

	backup, err := dots.BackupLocalPath(src)
	if err != nil {
		t.Fatalf("BackupLocalPath: %v", err)
	}
	want := first + ".1"
	if backup != want {
		t.Fatalf("backup path = %q, want %q", backup, want)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "# local" {
		t.Fatalf("backup content = %q, want # local", string(got))
	}
}
