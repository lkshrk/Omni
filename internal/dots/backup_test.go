package dots_test

import (
	"os"
	"os/exec"
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

func TestBackupLocalPath_SkipsCacheDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(home, ".config", "myapp")
	writeFile(t, filepath.Join(src, "config.toml"), "key = true")
	writeFile(t, filepath.Join(src, ".cache", "big.bin"), "huge cache")
	writeFile(t, filepath.Join(src, "node_modules", "pkg", "index.js"), "module")
	writeFile(t, filepath.Join(src, "__pycache__", "mod.pyc"), "bytecode")
	writeFile(t, filepath.Join(src, "sub", "real.txt"), "keep me")

	backup, err := dots.BackupLocalPath(src)
	if err != nil {
		t.Fatalf("BackupLocalPath: %v", err)
	}

	// Tracked file should be backed up.
	if _, err := os.Stat(filepath.Join(backup, "config.toml")); err != nil {
		t.Errorf("config.toml should be in backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "sub", "real.txt")); err != nil {
		t.Errorf("sub/real.txt should be in backup: %v", err)
	}

	// Cache dirs should be excluded.
	for _, excluded := range []string{".cache", "node_modules", "__pycache__"} {
		if _, err := os.Stat(filepath.Join(backup, excluded)); !os.IsNotExist(err) {
			t.Errorf("%s should NOT be in backup, got err=%v", excluded, err)
		}
	}
}

func TestBackupLocalPathFrom_GitTrackedOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a git repo with tracked + untracked files.
	repo := filepath.Join(home, "dotfiles", "nvim", ".config", "nvim")
	writeFile(t, filepath.Join(repo, "init.lua"), "-- config")
	writeFile(t, filepath.Join(repo, "lua", "plugins.lua"), "-- plugins")
	writeFile(t, filepath.Join(repo, ".cache", "huge.bin"), "100MB of cache")
	writeFile(t, filepath.Join(repo, "node_modules", "dep", "index.js"), "module")

	// git init + add only the tracked files
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "t@t.com")
	runGit(t, repo, "config", "user.name", "T")
	runGit(t, repo, "add", "init.lua", "lua/plugins.lua")
	runGit(t, repo, "commit", "-m", "init")

	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, target); err != nil {
		t.Fatal(err)
	}

	backup, err := dots.BackupLocalPathFrom(target, repo)
	if err != nil {
		t.Fatalf("BackupLocalPathFrom: %v", err)
	}

	// Tracked files present.
	if _, err := os.Stat(filepath.Join(backup, "init.lua")); err != nil {
		t.Errorf("init.lua should be in backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "lua", "plugins.lua")); err != nil {
		t.Errorf("lua/plugins.lua should be in backup: %v", err)
	}

	// Untracked cache dirs absent.
	for _, excluded := range []string{".cache", "node_modules"} {
		if _, err := os.Stat(filepath.Join(backup, excluded)); !os.IsNotExist(err) {
			t.Errorf("%s should NOT be in backup (untracked), got err=%v", excluded, err)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	baseArgs := []string{
		"-C", dir,
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "core.hooksPath=" + os.DevNull,
	}
	cmd := exec.Command("git", append(baseArgs, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
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

func TestBackupLocalPathFiltered_HonorsEntryIgnores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := filepath.Join(home, ".claude")
	writeFile(t, filepath.Join(target, "settings.json"), "{}")
	writeFile(t, filepath.Join(target, "plugins", "installed_plugins.json"), "{}")
	writeFile(t, filepath.Join(target, "plugins", "plugin-catalog-cache.json"), "huge")
	writeFile(t, filepath.Join(target, "projects", "session.jsonl"), "huge")

	ignores := append(dots.DefaultIgnores(),
		"*", "!/settings.json", "!/plugins", "!/plugins/installed_plugins.json")
	backup, err := dots.BackupLocalPathFilteredWithExecutor(t.Context(), nil, target, ignores)
	if err != nil {
		t.Fatalf("BackupLocalPathFilteredWithExecutor: %v", err)
	}

	for _, want := range []string{"settings.json", "plugins/installed_plugins.json"} {
		if _, err := os.Stat(filepath.Join(backup, want)); err != nil {
			t.Errorf("expected %s in backup: %v", want, err)
		}
	}
	for _, skip := range []string{"plugins/plugin-catalog-cache.json", "projects"} {
		if _, err := os.Stat(filepath.Join(backup, skip)); !os.IsNotExist(err) {
			t.Errorf("expected %s absent from backup, err = %v", skip, err)
		}
	}
}

func TestBackupLocalPathFiltered_NilIgnoresKeepsDefaultFiltering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := filepath.Join(home, ".config", "tool")
	writeFile(t, filepath.Join(target, "config.toml"), "x")
	writeFile(t, filepath.Join(target, "cache", "blob"), "huge")

	backup, err := dots.BackupLocalPathFilteredWithExecutor(t.Context(), nil, target, nil)
	if err != nil {
		t.Fatalf("BackupLocalPathFilteredWithExecutor: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "config.toml")); err != nil {
		t.Errorf("expected config.toml in backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "cache")); !os.IsNotExist(err) {
		t.Errorf("expected cache absent from backup, err = %v", err)
	}
}
