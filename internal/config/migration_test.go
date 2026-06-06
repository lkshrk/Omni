package config

import (
	"encoding/json"
	"testing"
)

func TestConfigMigrationsCoverCurrentVersion(t *testing.T) {
	seen := make(map[int]struct{}, len(configMigrations))
	version := 0
	for version < CurrentVersion {
		step, ok := configMigrationFrom(version)
		if !ok {
			t.Fatalf("missing migration from version %d to %d", version, version+1)
		}
		if _, dup := seen[step.from]; dup {
			t.Fatalf("duplicate migration from version %d", step.from)
		}
		seen[step.from] = struct{}{}
		if step.to <= step.from {
			t.Fatalf("migration from version %d to %d does not advance", step.from, step.to)
		}
		if step.apply == nil {
			t.Fatalf("migration from version %d to %d missing typed migration", step.from, step.to)
		}
		if step.applyRaw == nil {
			t.Fatalf("migration from version %d to %d missing raw migration", step.from, step.to)
		}
		version = step.to
	}
	if version != CurrentVersion {
		t.Fatalf("migration chain ends at version %d, want %d", version, CurrentVersion)
	}
}

func TestMigrate_UsesRegisteredMigrationChain(t *testing.T) {
	cfg := &RootConfig{}
	migrated, err := Migrate(cfg)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !migrated {
		t.Fatal("Migrate migrated = false, want true for legacy config")
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
}

func TestMigrateRawVersion_UsesRegisteredMigrationChain(t *testing.T) {
	raw := map[string]json.RawMessage{
		"settings": json.RawMessage(`{}`),
	}
	if err := migrateRawVersion(raw); err != nil {
		t.Fatalf("migrateRawVersion: %v", err)
	}
	version, err := rawConfigVersion(raw)
	if err != nil {
		t.Fatalf("rawConfigVersion: %v", err)
	}
	if version != CurrentVersion {
		t.Fatalf("raw version = %d, want %d", version, CurrentVersion)
	}
}

func TestMigrateV5ToV6_ConvertsToolsToProviderLists(t *testing.T) {
	cfg := &RootConfig{
		Version: 5,
		Settings: Settings{
			Ecosystems: map[string]EcosystemSettings{
				"node": {Manager: "pnpm"},
			},
		},
		Tools: map[string]ToolSpec{
			"docker": {
				Provider: "brew",
				Package:  "docker-desktop",
				Options:  map[string]string{"brew_kind": "cask"},
			},
			"eslint": {
				Provider:    "node",
				Package:     "eslint",
				InstallWith: "pnpm",
				Variants: []ToolInstallSpec{
					{Provider: "npm", Package: "eslint"},
				},
			},
			"unresolved": {Provider: "system"},
		},
		Groups: []*GroupConfig{{
			Name:  "base",
			Tools: []ToolEntry{{Name: "docker"}, {Name: "eslint"}, {Name: "unresolved"}},
		}},
	}

	migrated, err := Migrate(cfg)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !migrated {
		t.Fatal("Migrate migrated = false, want true")
	}
	if cfg.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", cfg.Version, CurrentVersion)
	}
	if cfg.Settings.Ecosystems != nil {
		t.Fatalf("settings ecosystems = %#v, want nil", cfg.Settings.Ecosystems)
	}

	docker := cfg.Tools["docker"]
	if docker.Provider != "" || docker.Package != "" || docker.InstallWith != "" || docker.Options != nil || docker.Variants != nil || docker.Hosts != nil {
		t.Fatalf("deprecated docker fields still populated: %+v", docker)
	}
	if len(docker.Providers) != 1 {
		t.Fatalf("docker providers = %+v, want one", docker.Providers)
	}
	if got := docker.Providers[0]; got.Provider != "brew" || got.Package != "docker-desktop" || got.Bin != "" || got.Options["brew_kind"] != "cask" {
		t.Fatalf("docker provider = %+v, want brew/docker-desktop cask", got)
	}

	eslint := cfg.Tools["eslint"]
	if len(eslint.Providers) != 2 {
		t.Fatalf("eslint providers = %+v, want pnpm+npm", eslint.Providers)
	}
	if got := eslint.Providers[0]; got.Provider != "pnpm" || got.Package != "eslint" || got.InstallWith != "" {
		t.Fatalf("eslint primary provider = %+v, want pnpm/eslint without install_with", got)
	}
	if got := eslint.Providers[1]; got.Provider != "npm" || got.Package != "eslint" {
		t.Fatalf("eslint variant provider = %+v, want npm/eslint", got)
	}

	if providers := cfg.Tools["unresolved"].Providers; len(providers) != 0 {
		t.Fatalf("unresolved providers = %+v, want empty", providers)
	}
}

func TestMigrateRawV5ToV6_RemovesDeprecatedToolFields(t *testing.T) {
	raw := map[string]json.RawMessage{
		"version": json.RawMessage(`5`),
		"settings": json.RawMessage(`{
			"ecosystems": {"node": {"manager": "pnpm"}},
			"disabled_providers": ["pip"]
		}`),
		"tools": json.RawMessage(`{
			"docker": {
				"provider": "brew",
				"package": "docker-desktop",
				"options": {"brew_kind": "cask"}
			},
			"eslint": {
				"provider": "node",
				"package": "eslint",
				"install_with": "pnpm",
				"variants": [{"provider": "npm", "package": "eslint"}]
			}
		}`),
	}
	if err := migrateRawVersion(raw); err != nil {
		t.Fatalf("migrateRawVersion: %v", err)
	}
	var out struct {
		Version  int `json:"version"`
		Settings struct {
			Ecosystems        map[string]EcosystemSettings `json:"ecosystems,omitempty"`
			DisabledProviders []string                     `json:"disabled_providers,omitempty"`
		} `json:"settings"`
		Tools map[string]ToolSpec `json:"tools"`
	}
	merged, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	if err := json.Unmarshal(merged, &out); err != nil {
		t.Fatalf("unmarshal migrated raw: %v\n%s", err, merged)
	}
	if out.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", out.Version, CurrentVersion)
	}
	if out.Settings.Ecosystems != nil {
		t.Fatalf("settings ecosystems = %#v, want nil", out.Settings.Ecosystems)
	}
	if len(out.Settings.DisabledProviders) != 1 || out.Settings.DisabledProviders[0] != "pip" {
		t.Fatalf("disabled providers = %#v, want [pip]", out.Settings.DisabledProviders)
	}
	docker := out.Tools["docker"]
	if docker.Provider != "" || docker.Package != "" || docker.Options != nil || len(docker.Providers) != 1 {
		t.Fatalf("docker migrated shape = %+v, want only providers[]", docker)
	}
	eslint := out.Tools["eslint"]
	if eslint.Provider != "" || eslint.InstallWith != "" || eslint.Variants != nil || len(eslint.Providers) != 2 {
		t.Fatalf("eslint migrated shape = %+v, want only providers[]", eslint)
	}
}
