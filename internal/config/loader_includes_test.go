package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestLoad_MergesIncludeFragments(t *testing.T) {
	dir := t.TempDir()
	includePath := filepath.Join(dir, "tools.json")
	mainPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(includePath, []byte(`{
  "tools": {
    "rg": { "providers": [{ "provider": "brew", "package": "ripgrep" }] }
  },
  "groups": [{ "name": "extra", "tools": ["rg"] }]
}`), 0o600); err != nil {
		t.Fatalf("write include: %v", err)
	}
	if err := os.WriteFile(mainPath, []byte(`{
  "version": 17,
  "$include": ["tools.json"],
  "settings": { "auto_import": true },
  "groups": [{ "name": "base", "tools": ["jq"] }],
  "tools": {
    "jq": { "providers": [{ "provider": "brew", "package": "jq" }] }
  }
}`), 0o600); err != nil {
		t.Fatalf("write main: %v", err)
	}

	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Include) != 0 {
		t.Fatalf("Include = %v, want stripped after load", cfg.Include)
	}
	if !cfg.Settings.AutoImport {
		t.Fatal("expected merged settings.auto_import")
	}
	if _, ok := cfg.Tools["rg"]; !ok || cfg.Tools["jq"].Providers[0].Package != "jq" {
		t.Fatalf("tools = %+v, want merged jq and rg", cfg.Tools)
	}
	names := make([]string, 0, len(cfg.Groups))
	for _, g := range cfg.Groups {
		if g != nil {
			names = append(names, g.Name)
		}
	}
	if len(names) != 2 {
		t.Fatalf("groups = %v, want base and extra", names)
	}
}

func TestLoad_MergesIncludeFragmentsViaSymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	packageDir := filepath.Join(dir, "package", ".config", "omni")
	linkDir := filepath.Join(dir, "home", ".config", "omni")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	if err := os.MkdirAll(linkDir, 0o700); err != nil {
		t.Fatalf("mkdir link dir: %v", err)
	}
	fragmentPath := filepath.Join(packageDir, "settings.d", "tools.json")
	if err := os.MkdirAll(filepath.Dir(fragmentPath), 0o700); err != nil {
		t.Fatalf("mkdir settings.d: %v", err)
	}
	if err := os.WriteFile(fragmentPath, []byte(`{
  "tools": {
    "rg": { "providers": [{ "provider": "brew", "package": "ripgrep" }] }
  }
}`), 0o600); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	mainPath := filepath.Join(packageDir, "settings.json")
	if err := os.WriteFile(mainPath, []byte(`{
  "version": 17,
  "$include": ["settings.d/tools.json"],
  "tools": {
    "jq": { "providers": [{ "provider": "brew", "package": "jq" }] }
  }
}`), 0o600); err != nil {
		t.Fatalf("write main: %v", err)
	}
	linkPath := filepath.Join(linkDir, "settings.json")
	if err := os.Symlink(mainPath, linkPath); err != nil {
		t.Fatalf("symlink settings.json: %v", err)
	}

	cfg, err := config.Load(linkPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Tools["rg"]; !ok || cfg.Tools["jq"].Providers[0].Package != "jq" {
		t.Fatalf("tools = %+v, want merged jq and rg through symlinked config", cfg.Tools)
	}
}

func TestPatchTool_UpdatesOwningInclude(t *testing.T) {
	dir := t.TempDir()
	includePath := filepath.Join(dir, "tools.json")
	mainPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(includePath, []byte(`{
  "tools": {
    "pnpm": { "providers": [{ "provider": "brew" }] },
    "jq": { "providers": [{ "provider": "brew" }] }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(`{
  "version": 17,
  "$include": ["tools.json"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.PatchTool(mainPath, "pnpm", func(spec *config.ToolSpec) error {
		spec.Hosts = map[string]config.ToolInstallSpec{"topaz": {Provider: "bun"}}
		return nil
	}); err != nil {
		t.Fatalf("PatchTool: %v", err)
	}
	got, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if provider := got.Tools["pnpm"].Hosts["topaz"].Provider; provider != "bun" {
		t.Fatalf("pnpm host provider = %q, want bun", provider)
	}
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  \"version\": 17,\n  \"$include\": [\"tools.json\"]\n}" {
		t.Fatalf("root config was changed: %s", data)
	}
}
