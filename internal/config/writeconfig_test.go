package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func loaderFor(path string) func() (*config.RootConfig, error) {
	return func() (*config.RootConfig, error) { return config.Load(path) }
}

func TestWriteConfig_RoutesChangedKeyToOwnerFragment(t *testing.T) {
	dir := t.TempDir()
	main := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/tools.json"],
  "settings": { "auto_import": true }
}`,
		"settings.d/tools.json": `{
  "tools": { "jq": { "providers": [{ "provider": "brew", "package": "jq" }] } }
}`,
	})

	err := config.WriteConfig(main, loaderFor(main), nil, func(cfg *config.RootConfig) error {
		cfg.Tools["rg"] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "ripgrep"}}}
		return nil
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	mainRaw := rawKeys(t, main)
	if _, ok := mainRaw["tools"]; ok {
		t.Fatal("tools must not be re-inlined into main; it belongs to the fragment")
	}
	if _, ok := mainRaw["$include"]; !ok {
		t.Fatal("$include chain must survive the write")
	}
	fragRaw := rawKeys(t, filepath.Join(dir, "settings.d", "tools.json"))
	if !strings.Contains(string(fragRaw["tools"]), "ripgrep") || !strings.Contains(string(fragRaw["tools"]), "jq") {
		t.Fatalf("fragment tools = %s, want both jq and ripgrep", fragRaw["tools"])
	}
}

func TestWriteConfig_PreservesUnknownTopLevelKeys(t *testing.T) {
	dir := t.TempDir()
	main := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "settings": { "auto_import": false },
  "experimental_flag": 42
}`,
	})

	err := config.WriteConfig(main, loaderFor(main), nil, func(cfg *config.RootConfig) error {
		cfg.Settings.AutoImport = true
		return nil
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	raw := rawKeys(t, main)
	if got, ok := raw["experimental_flag"]; !ok || strings.TrimSpace(string(got)) != "42" {
		t.Fatalf("unknown top-level key dropped: got %q ok=%v", got, ok)
	}
	if !strings.Contains(string(raw["settings"]), "true") {
		t.Fatalf("settings not updated: %s", raw["settings"])
	}
}

func TestWriteConfig_ValidateRejectsBadMutation(t *testing.T) {
	dir := t.TempDir()
	main := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{ "version": 17, "settings": {} }`,
	})
	before, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}

	providers := &config.ProviderValidation{Known: []string{"brew"}}
	err = config.WriteConfig(main, loaderFor(main), providers, func(cfg *config.RootConfig) error {
		if cfg.Tools == nil {
			cfg.Tools = map[string]config.ToolSpec{}
		}
		cfg.Tools["bad"] = config.ToolSpec{Providers: []config.ToolInstallSpec{{Provider: "bogus"}}}
		return nil
	})
	if err == nil {
		t.Fatal("WriteConfig must reject a mutation that fails validation")
	}

	after, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("rejected write must not touch the file:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestWriteConfig_SkipSaveAborts(t *testing.T) {
	dir := t.TempDir()
	main := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{ "version": 17, "settings": { "auto_import": true } }`,
	})
	before, _ := os.ReadFile(main)

	err := config.WriteConfig(main, loaderFor(main), nil, func(cfg *config.RootConfig) error {
		cfg.Settings.AutoImport = false // change, then abort
		return config.ErrSkipSave
	})
	if err != nil {
		t.Fatalf("ErrSkipSave should abort with no error, got %v", err)
	}
	after, _ := os.ReadFile(main)
	if string(before) != string(after) {
		t.Fatal("ErrSkipSave must leave the file untouched")
	}
}

func TestWriteConfig_NoOpWritesNothing(t *testing.T) {
	dir := t.TempDir()
	main := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{ "version": 17, "settings": { "auto_import": true } }`,
	})
	info, err := os.Stat(main)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(main)

	err = config.WriteConfig(main, loaderFor(main), nil, func(cfg *config.RootConfig) error {
		return nil // no change
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	after, _ := os.ReadFile(main)
	if string(before) != string(after) {
		t.Fatalf("no-op mutate must not rewrite the file:\nbefore=%s\nafter=%s", before, after)
	}
	if info2, err := os.Stat(main); err == nil && !info2.ModTime().Equal(info.ModTime()) {
		t.Fatal("no-op mutate must not rewrite the file (mtime changed)")
	}
}
