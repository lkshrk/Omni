package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestValidateRoot_PluginMissingMarketplace_IsHardError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Plugins: []config.Plugin{{Name: "caveman", Marketplace: "does-not-exist"}},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if e.Warn {
			continue
		}
		if e.Path == `$.agents.plugins[0].marketplace` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected hard error for unknown plugin marketplace ref, got %+v", errs)
	}
}

func TestValidateRoot_PluginWithDeclaredMarketplace_NoError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace"}},
			Plugins:      []config.Plugin{{Name: "caveman", Marketplace: "caveman"}},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	for _, e := range errs {
		if !e.Warn {
			t.Fatalf("unexpected hard error: %+v", e)
		}
	}
}

func TestValidateRoot_DuplicateMarketplaceName_IsError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{
				{Name: "caveman", Source: "a/b"},
				{Name: "caveman", Source: "c/d"},
			},
		},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if !e.Warn && e.Path == `$.agents.marketplaces[1].name` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate marketplace name error, got %+v", errs)
	}
}

func TestValidateRoot_GroupPluginRef_UnknownIsWarnOnly(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "work", Plugins: []string{"ghost"}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if e.Path == `$.groups[0].plugins[0]` {
			if !e.Warn {
				t.Fatalf("expected warn-level error for group plugin ref, got hard error: %+v", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected a warn-level error for unknown group plugin ref")
	}
}

func TestValidateRoot_GroupMarketplaceRef_UnknownIsWarnOnly(t *testing.T) {
	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "work", Marketplaces: []string{"ghost"}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	found := false
	for _, e := range errs {
		if e.Path == `$.groups[0].marketplaces[0]` {
			if !e.Warn {
				t.Fatalf("expected warn-level error for group marketplace ref, got hard error: %+v", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected a warn-level error for unknown group marketplace ref")
	}
}

func TestValidateRoot_GroupMarketplaceRef_DeclaredNoError(t *testing.T) {
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "caveman", Source: "lkshrk/agent-marketplace"}},
		},
		Groups: []*config.GroupConfig{{Name: "work", Marketplaces: []string{"caveman"}}},
	}
	errs := config.ValidateRoot(cfg, config.ProviderValidation{})
	for _, e := range errs {
		if e.Path == `$.groups[0].marketplaces[0]` {
			t.Fatalf("unexpected error for declared group marketplace ref: %+v", e)
		}
	}
}
