package config

// White-box tests for the unexported atomicWrite; must stay in package config.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAtomicWrite_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.json")
	data := []byte(`{"key":"value"}`)

	if err := atomicWrite(path, data); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestAtomicWriteRejectsEqualContentOutsideTestSandbox(t *testing.T) {
	path := filepath.Join("..", "config", "loader.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, data); err == nil || !strings.Contains(err.Error(), "config write") {
		t.Fatalf("atomicWrite equal outside content error = %v", err)
	}
}

func TestAcquireWriteLockRejectsOutsideBeforeCreatingLock(t *testing.T) {
	path := os.Getenv("OMNI_TEST_ROOT")
	lockPath := filepath.Join(filepath.Dir(path), ".omni-config.lock")
	if lock, err := AcquireWriteLock(path); err == nil || !strings.Contains(err.Error(), "config write lock") {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("AcquireWriteLock outside error = %v", err)
	}
	if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("outside lock was created: %v", err)
	}
}

func TestAtomicWrite_CreatesNestedDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "file.json")
	data := []byte(`{}`)

	if err := atomicWrite(path, data); err != nil {
		t.Fatalf("atomicWrite nested dirs: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not found after atomic write: %v", err)
	}
}

func TestAtomicWrite_MkdirAllError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 not meaningful on Windows")
	}

	dir := t.TempDir()
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("i am a file"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	path := filepath.Join(blocker, "subdir", "file.json")
	err := atomicWrite(path, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error when MkdirAll cannot create directory, got nil")
	}
	if !strings.Contains(err.Error(), "creating config directory") {
		t.Errorf("error message unexpected: %v", err)
	}
}

func TestAtomicWrite_CreateTempError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 not meaningful on Windows")
	}

	dir := t.TempDir()
	targetDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.Chmod(targetDir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0o755) })

	path := filepath.Join(targetDir, "settings.json")
	err := atomicWrite(path, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error when CreateTemp cannot create file, got nil")
	}
	if !strings.Contains(err.Error(), "creating temp file") {
		t.Errorf("error message unexpected: %v", err)
	}
}

func TestAtomicWrite_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := atomicWrite(path, []byte(`{"v":1}`)); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := atomicWrite(path, []byte(`{"v":2}`)); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != `{"v":2}` {
		t.Errorf("got %q, want {\"v\":2}", got)
	}
}

func TestAtomicWrite_PreservesSettingsSymlink(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config", "omni")
	repoDir := filepath.Join(dir, "repo", "dotfiles", "omni", ".config", "omni")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(repoDir, "settings.json")
	if err := os.WriteFile(target, []byte(`{"v":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(configDir, "settings.json")
	relTarget, err := filepath.Rel(configDir, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := atomicWrite(link, []byte(`{"v":2}`)); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}

	if info, err := os.Lstat(link); err != nil {
		t.Fatalf("Lstat link: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings path mode = %v, want symlink preserved", info.Mode())
	}
	if got, err := os.ReadFile(target); err != nil {
		t.Fatalf("ReadFile target: %v", err)
	} else if string(got) != `{"v":2}` {
		t.Fatalf("target content = %q, want updated content", got)
	}
	if got, err := os.ReadFile(link); err != nil {
		t.Fatalf("ReadFile link: %v", err)
	} else if string(got) != `{"v":2}` {
		t.Fatalf("link content = %q, want updated content through symlink", got)
	}
}

func TestAtomicWrite_NoTempFileLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := atomicWrite(path, []byte(`{}`)); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}
