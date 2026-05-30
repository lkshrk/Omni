package app_test

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// ─── SaveNodeManager ──────────────────────────────────────────────────────────

// TestSaveNodeManager_HappyPath calls SaveNodeManager and verifies the value
// is persisted to host_settings[shortHostname].ecosystems.node.manager.
func TestSaveNodeManager_HappyPath(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SaveNodeManager(context.Background(), "bun"); err != nil {
		t.Fatalf("SaveNodeManager: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	got := cfg.HostSettings[short].EcosystemManager("node")
	if got != "bun" {
		t.Errorf("node manager = %q after SaveNodeManager(bun), want bun", got)
	}
}

// TestSaveNodeManager_SecondCallOverwrites verifies that calling SaveNodeManager
// twice leaves only the value from the second call.
func TestSaveNodeManager_SecondCallOverwrites(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SaveNodeManager(context.Background(), "pnpm"); err != nil {
		t.Fatalf("first SaveNodeManager: %v", err)
	}
	if err := a.SaveNodeManager(context.Background(), "npm"); err != nil {
		t.Fatalf("second SaveNodeManager: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	got := cfg.HostSettings[short].EcosystemManager("node")
	if got != "npm" {
		t.Errorf("node manager = %q after two calls, want npm", got)
	}
}

// TestSaveNodeManager_PreservesOtherHostSettings verifies that SaveNodeManager
// does not overwrite other host-specific settings (e.g. PythonManager) already
// in the file for the same host.
func TestSaveNodeManager_PreservesOtherHostSettings(t *testing.T) {
	a, cfgPath := newImportApp(t)

	// Set python manager via SaveSettings first.
	if err := a.SaveSettings(context.Background(), testSettingsWithManager("python", "uv")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Now save node manager — python manager must survive.
	if err := a.SaveNodeManager(context.Background(), "bun"); err != nil {
		t.Fatalf("SaveNodeManager: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	hs := cfg.HostSettings[short]
	if hs.EcosystemManager("node") != "bun" {
		t.Errorf("node manager = %q, want bun", hs.EcosystemManager("node"))
	}
	if hs.EcosystemManager("python") != "uv" {
		t.Errorf("python manager = %q after SaveNodeManager, want uv (should be preserved)", hs.EcosystemManager("python"))
	}
}

// TestSaveNodeManager_EmptyString verifies that an empty manager value can be
// persisted (clears the pin, falling back to auto-detect).
func TestSaveNodeManager_EmptyString(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SaveNodeManager(context.Background(), "bun"); err != nil {
		t.Fatalf("first SaveNodeManager: %v", err)
	}
	if err := a.SaveNodeManager(context.Background(), ""); err != nil {
		t.Fatalf("second SaveNodeManager (clear): %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	got := cfg.HostSettings[short].EcosystemManager("node")
	// empty string means "omit from JSON" (omitempty); the field will be "" after reload.
	if got != "" {
		t.Errorf("node manager = %q after clearing, want empty string", got)
	}
}

func TestSaveNodeManager_RejectsInvalidManager(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := a.SaveNodeManager(context.Background(), "bun"); err != nil {
		t.Fatalf("SaveNodeManager(bun): %v", err)
	}

	err := a.SaveNodeManager(context.Background(), "uv")
	if err == nil {
		t.Fatal("SaveNodeManager(uv) succeeded, want validation error")
	}

	cfg, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if got := cfg.HostSettings[short].EcosystemManager("node"); got != "bun" {
		t.Fatalf("node manager = %q after invalid save, want bun", got)
	}
}

// TestSaveNodeManager_ConfigFileUnwritable verifies that SaveNodeManager
// returns an error when the config file cannot be written.
func TestSaveNodeManager_ConfigFileUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod not meaningful on Windows")
	}

	a, cfgPath := newImportApp(t)

	// Write a valid config first so the file and directory exist.
	if err := a.SaveSettings(context.Background(), config.Settings{}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Make the config directory read-only so atomic rename fails.
	cfgDir := cfgPath[:len(cfgPath)-len("/settings.json")]
	if err := os.Chmod(cfgDir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o755) })

	err := a.SaveNodeManager(context.Background(), "bun")
	if err == nil {
		t.Error("expected error when config directory is read-only, got nil")
	}
}
