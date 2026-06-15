package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// TestWithConfigPersistsAgentsSkills guards the patch-write: the agents key must
// be part of topLevelKeys, otherwise manifest changes (e.g. import) are computed
// in memory but silently dropped on save.
func TestWithConfigPersistsAgentsSkills(t *testing.T) {
	ctx := context.Background()
	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(ctx, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Skills = []config.ManifestSkill{{Name: "frontend-design", Source: "vercel-labs/agent-skills"}}
		return nil
	}); err != nil {
		t.Fatalf("withConfig: %v", err)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if len(cfg.Agents.Skills) != 1 || cfg.Agents.Skills[0].Name != "frontend-design" {
		t.Fatalf("agents.skills not persisted across save/load: %+v", cfg.Agents.Skills)
	}
}
