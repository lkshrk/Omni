package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestEffectiveSettings_ClonesProviders(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{
			Providers: []config.ProviderEntry{{
				Name:     "uv",
				Provider: "brew",
				Options:  map[string]string{"k": "v"},
				Variants: []config.ToolInstallSpec{{Provider: "pip", Options: map[string]string{"a": "b"}}},
				Hosts:    map[string]config.ToolInstallSpec{"h": {Provider: "apt", Options: map[string]string{"c": "d"}}},
			}},
		},
	}

	clone := root.EffectiveSettings("nohost")
	clone.Providers[0].Name = "mutated"
	clone.Providers[0].Options["k"] = "mutated"
	clone.Providers[0].Variants[0].Options["a"] = "mutated"
	clone.Providers[0].Hosts["h"].Options["c"] = "mutated"

	src := root.Settings.Providers[0]
	if src.Name != "uv" {
		t.Errorf("Name mutated through clone: %q", src.Name)
	}
	if src.Options["k"] != "v" {
		t.Errorf("Options mutated through clone: %q", src.Options["k"])
	}
	if src.Variants[0].Options["a"] != "b" {
		t.Errorf("Variant Options mutated through clone: %q", src.Variants[0].Options["a"])
	}
	if src.Hosts["h"].Options["c"] != "d" {
		t.Errorf("Host Options mutated through clone: %q", src.Hosts["h"].Options["c"])
	}
}
