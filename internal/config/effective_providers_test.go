package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestEffectiveSettings_ProvidersInheritedWhenHostNil(t *testing.T) {
	root := &config.RootConfig{
		Settings:     config.Settings{Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}}},
		HostSettings: map[string]config.Settings{"box": {}}, // Providers == nil
	}
	got := root.EffectiveSettings("box")
	if len(got.Providers) != 1 || got.Providers[0].Name != "uv" {
		t.Errorf("Providers = %+v, want inherited global [uv]", got.Providers)
	}
}

func TestEffectiveSettings_HostProvidersReplaceGlobal(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}}},
		HostSettings: map[string]config.Settings{
			"box": {Providers: []config.ProviderEntry{{Name: "bun", Provider: "brew"}}},
		},
	}
	got := root.EffectiveSettings("box")
	if len(got.Providers) != 1 || got.Providers[0].Name != "bun" {
		t.Errorf("Providers = %+v, want host-replaced [bun]", got.Providers)
	}
}

func TestEffectiveSettings_EmptyHostProvidersClearGlobal(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{Providers: []config.ProviderEntry{{Name: "uv", Provider: "brew"}}},
		HostSettings: map[string]config.Settings{
			"box": {Providers: []config.ProviderEntry{}},
		},
	}
	got := root.EffectiveSettings("box")
	if len(got.Providers) != 0 {
		t.Errorf("Providers = %+v, want cleared by empty host list", got.Providers)
	}
}
