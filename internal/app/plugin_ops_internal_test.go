package app

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func resolvePluginNames(cfg *config.RootConfig, group string) []string {
	plugins := resolvePlugins(cfg, group)
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	return names
}

func TestResolvePlugins_UngroupedPlugin_AppearsOnAllHosts(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Plugins: []config.Plugin{{Name: "global", Marketplace: "m"}}},
	}
	for _, group := range []string{"box", "work", ""} {
		got := resolvePluginNames(cfg, group)
		if len(got) != 1 || got[0] != "global" {
			t.Errorf("group=%q: ungrouped plugin must appear; got %v", group, got)
		}
	}
}

func TestResolvePlugins_GroupedPlugin_OnlyOnMatchingGroup(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Plugins: []config.Plugin{{Name: "work-only", Marketplace: "m"}}},
		Groups: []*config.GroupConfig{{Name: "work", Plugins: []string{"work-only"}}},
	}
	if got := resolvePluginNames(cfg, "work"); len(got) != 1 || got[0] != "work-only" {
		t.Fatalf("expected work-only on matching group, got %v", got)
	}
	if got := resolvePluginNames(cfg, "personal"); len(got) != 0 {
		t.Fatalf("expected work-only excluded on non-matching group, got %v", got)
	}
}
