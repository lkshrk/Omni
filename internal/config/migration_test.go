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
