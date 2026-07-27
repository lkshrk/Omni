package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestLoad_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 not meaningful on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unreadable file, got nil")
	}
	if !strings.Contains(err.Error(), "reading config file") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPatch_InvalidExistingJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{invalid json}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	type patch struct {
		Settings config.Settings `json:"settings"`
	}
	err := config.Patch(path, patch{})
	if err == nil {
		t.Fatal("expected error for invalid existing JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parsing existing config") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPatch_UnmarshalablePatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	type badPatch struct {
		Ch chan int `json:"ch"`
	}
	err := config.Patch(path, badPatch{Ch: make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error for channel field, got nil")
	}
	if !strings.Contains(err.Error(), "encoding patch") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDefaultConfigPath_XDGContainsOmniSettingsJSON(t *testing.T) {
	base := t.TempDir()
	t.Setenv("OMNI_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", base)

	p, err := config.DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if !strings.HasSuffix(p, filepath.Join("omni", "settings.json")) {
		t.Errorf("path %q does not end with omni/settings.json", p)
	}
}

func TestDefaultCacheDir_XDGContainsOmni(t *testing.T) {
	base := t.TempDir()
	t.Setenv("OMNI_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", base)

	p, err := config.DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir: %v", err)
	}
	if filepath.Base(p) != "omni" {
		t.Errorf("path %q does not end in 'omni'", p)
	}
}
