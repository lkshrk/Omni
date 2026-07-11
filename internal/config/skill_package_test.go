package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestLoadMigratesLegacySkillsToPackages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := `{"version":1,"agents":{"skills":[{"name":"a","source":"o/r","ref":"main"},{"name":"b","source":"o/r"}]}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.Skills != nil {
		t.Errorf("legacy Skills should be cleared on load, got %+v", cfg.Agents.Skills)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "o/r" || cfg.Agents.Packages[0].Ref != "main" {
		t.Fatalf("Packages = %+v, want single o/r#main", cfg.Agents.Packages)
	}
}

func TestValidateRootSkillPackages(t *testing.T) {
	t.Run("negative: empty source and unknown ref", func(t *testing.T) {
		root := &config.RootConfig{
			Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: ""}}},
			Groups: []*config.GroupConfig{{Name: "g", Skills: []string{"unknown/pkg"}}},
		}
		errs := config.ValidateRoot(root, config.ProviderValidation{})
		var sawSource, sawRef bool
		for _, e := range errs {
			if e.Path == "$.agents.packages[0].source" {
				sawSource = true
			}
			if e.Path == `$.groups[0].skills[0]` {
				sawRef = true
			}
		}
		if !sawSource {
			t.Error("expected empty package source error")
		}
		if !sawRef {
			t.Error("expected unknown group skill ref error")
		}
	})

	t.Run("positive: matching ref is accepted", func(t *testing.T) {
		root := &config.RootConfig{
			Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: "vercel-labs/agent-skills"}}},
			Groups: []*config.GroupConfig{{Name: "g", Skills: []string{"vercel-labs/agent-skills"}}},
		}
		errs := config.ValidateRoot(root, config.ProviderValidation{})
		for _, e := range errs {
			if e.Path == `$.groups[0].skills[0]` {
				t.Errorf("unexpected group skill ref error: %v", e)
			}
		}
	})
}

func TestMigrateLegacySkillsToPackages(t *testing.T) {
	root := &config.RootConfig{
		Agents: config.AgentsConfig{
			Skills: []config.ManifestSkill{
				{Name: "a", Source: "o/r", Ref: "main", Agents: []string{"codex"}},
				{Name: "b", Source: "o/r"},
				{Name: "c", Source: "x/y", Ref: "v2"},
			},
		},
	}
	config.MigrateSkillPackages(root)
	if root.Agents.Skills != nil {
		t.Errorf("legacy Skills should be cleared, got %+v", root.Agents.Skills)
	}
	if len(root.Agents.Packages) != 2 {
		t.Fatalf("packages = %+v, want 2 deduped by source", root.Agents.Packages)
	}
	if root.Agents.Packages[0].Source != "o/r" || root.Agents.Packages[0].Ref != "main" {
		t.Errorf("first package = %+v", root.Agents.Packages[0])
	}
	config.MigrateSkillPackages(root)
	if len(root.Agents.Packages) != 2 {
		t.Errorf("second migrate changed packages: %+v", root.Agents.Packages)
	}
}

func TestSkillPackageJSONRoundTrip(t *testing.T) {
	root := &config.RootConfig{
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{
				{Source: "vercel-labs/agent-skills", Ref: "main", Agents: []string{"codex"}},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", Skills: []string{"vercel-labs/agent-skills"}},
		},
	}
	b, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	var got config.RootConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Agents.Packages) != 1 || got.Agents.Packages[0].Source != "vercel-labs/agent-skills" {
		t.Fatalf("packages = %+v", got.Agents.Packages)
	}
	if len(got.Groups[0].Skills) != 1 || got.Groups[0].Skills[0] != "vercel-labs/agent-skills" {
		t.Fatalf("group skills = %+v", got.Groups[0].Skills)
	}
}
