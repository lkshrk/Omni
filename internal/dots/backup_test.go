package dots_test

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestBackupLocalPathFrom_UsesTargetPathAndSourceContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := filepath.Join(home, ".config", "nvim")
	source := filepath.Join(home, "dotfiles", "dotfiles", "nvim", ".config", "nvim")
	writeFile(t, filepath.Join(source, "init.lua"), "-- repo")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}

	backup, err := dots.BackupLocalPathFrom(target, source)
	if err != nil {
		t.Fatalf("BackupLocalPathFrom: %v", err)
	}
	want := filepath.Join(home, dots.BackupDirName, ".config", "nvim")
	if backup != want {
		t.Fatalf("backup path = %q, want %q", backup, want)
	}
	if info, err := os.Lstat(backup); err != nil {
		t.Fatalf("Lstat backup: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("backup should contain copied source content, got symlink")
	}
	got, err := os.ReadFile(filepath.Join(backup, "init.lua"))
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "-- repo" {
		t.Fatalf("backup content = %q, want -- repo", string(got))
	}
}

func TestBackupAndRemoveLocalPath_BacksUpBeforeRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	src := filepath.Join(home, ".config", "wezterm", "wezterm.lua")
	writeFile(t, src, "return {}")

	backup, err := dots.BackupAndRemoveLocalPath(src)
	if err != nil {
		t.Fatalf("BackupAndRemoveLocalPath: %v", err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still exists after remove: %v", err)
	}
	want := filepath.Join(home, dots.BackupDirName, ".config", "wezterm", "wezterm.lua")
	if backup != want {
		t.Fatalf("backup path = %q, want %q", backup, want)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "return {}" {
		t.Fatalf("backup content = %q, want return {}", string(got))
	}
	trash := filepath.Join(expectedTrashRoot(t, home), "wezterm.lua")
	got, err = os.ReadFile(trash)
	if err != nil {
		t.Fatalf("ReadFile trash: %v", err)
	}
	if string(got) != "return {}" {
		t.Fatalf("trash content = %q, want return {}", string(got))
	}
}

func TestBackupAndRemoveLocalPath_UnlinksSymlinkOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	target := filepath.Join(home, "outside", "config")
	writeFile(t, filepath.Join(target, "init.lua"), "-- target")
	link := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	backup, err := dots.BackupAndRemoveLocalPath(link)
	if err != nil {
		t.Fatalf("BackupAndRemoveLocalPath: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup path = %q, want empty for symlink unlink", backup)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link should be unlinked: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "init.lua"))
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(got) != "-- target" {
		t.Fatalf("target content = %q, want -- target", string(got))
	}
	if _, err := os.Lstat(filepath.Join(expectedTrashRoot(t, home), "nvim")); !os.IsNotExist(err) {
		t.Fatalf("symlink should not be moved to trash: %v", err)
	}
}

func TestRemoveLocalPathAfterBackup_RefusesExistingTargetWithoutBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(home, ".zshrc")
	writeFile(t, src, "# local")

	err := dots.RemoveLocalPathAfterBackup(src, "")
	if err == nil {
		t.Fatal("RemoveLocalPathAfterBackup succeeded without backup")
	}
	if _, statErr := os.Lstat(src); statErr != nil {
		t.Fatalf("source should remain after refused remove: %v", statErr)
	}
	got, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatalf("ReadFile source: %v", readErr)
	}
	if string(got) != "# local" {
		t.Fatalf("source content = %q, want # local", string(got))
	}
}

func TestRemoveLocalPathAfterBackup_AllowsSymlinkWithoutBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := filepath.Join(home, "repo", "zshrc")
	writeFile(t, target, "# repo")
	link := filepath.Join(home, ".zshrc")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := dots.RemoveLocalPathAfterBackup(link, ""); err != nil {
		t.Fatalf("RemoveLocalPathAfterBackup: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link should be unlinked: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "# repo" {
		t.Fatalf("target changed: body=%q err=%v", got, err)
	}
}

func expectedTrashRoot(t *testing.T, home string) string {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, ".Trash")
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "Trash", "files")
		}
		return filepath.Join(home, ".Trash")
	default:
		if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
			return filepath.Join(xdgDataHome, "Trash", "files")
		}
		return filepath.Join(home, ".local", "share", "Trash", "files")
	}
}
