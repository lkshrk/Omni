package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func testSettingsWithProviderPriority(providers ...string) config.Settings {
	var s config.Settings
	if len(providers) > 0 {
		s.ProviderPriority = append([]string(nil), providers...)
	}
	return s
}

func TestEffectiveSettings_NoHostEntry_ReturnsGlobal(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: testSettingsWithProviderPriority("bun", "uv"),
	}
	got := cfg.EffectiveSettings("myhost")
	if len(got.ProviderPriority) < 2 || got.ProviderPriority[0] != "bun" || got.ProviderPriority[1] != "uv" {
		t.Errorf("ProviderPriority = %v, want [bun uv] (global)", got.ProviderPriority)
	}
}

func TestEffectiveSettings_HostEntryAllZero_GlobalFillsGaps(t *testing.T) {
	settings := testSettingsWithProviderPriority("pnpm", "uv")
	settings.DotsRepo = "~/dotfiles"
	cfg := &config.RootConfig{
		Settings: settings,
		HostSettings: map[string]config.Settings{
			"myhost": {}, // all zero/empty — must not override global
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if len(got.ProviderPriority) < 2 || got.ProviderPriority[0] != "pnpm" || got.ProviderPriority[1] != "uv" {
		t.Errorf("ProviderPriority = %v, want [pnpm uv] (global fills gaps)", got.ProviderPriority)
	}
	if got.DotsRepo != "~/dotfiles" {
		t.Errorf("DotsRepo = %q, want ~/dotfiles (global)", got.DotsRepo)
	}
}

func TestEffectiveSettings_HostDotsDisabled_Wins(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DotsDisabled: nil,
		},
		HostSettings: map[string]config.Settings{
			"myhost": {DotsDisabled: config.BoolPtr(true)},
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if !config.BoolVal(got.DotsDisabled) {
		t.Error("DotsDisabled: want true (host wins), got false")
	}
}

func TestEffectiveSettings_HostDisabledProviders_AppliedWhenSet(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: config.Settings{},
		HostSettings: map[string]config.Settings{
			"myhost": {DisabledProviders: []string{"pip"}},
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if len(got.DisabledProviders) != 1 || got.DisabledProviders[0] != "pip" {
		t.Errorf("DisabledProviders = %v, want [pip]", got.DisabledProviders)
	}
}

func TestEffectiveSettings_HostNilDisabledProviders_PreservesGlobal(t *testing.T) {
	// Nil means "not set", not "enable everything", so the global list survives.
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DisabledProviders: []string{"brew"},
		},
		HostSettings: map[string]config.Settings{
			"myhost": {DisabledProviders: nil},
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if len(got.DisabledProviders) != 1 || got.DisabledProviders[0] != "brew" {
		t.Errorf("DisabledProviders = %v, want [brew] (global preserved when host list is nil)", got.DisabledProviders)
	}
}

func TestEffectiveSettings_HostEmptyDisabledProviders_OverridesGlobal(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DisabledProviders: []string{"brew"},
		},
		HostSettings: map[string]config.Settings{
			"myhost": {DisabledProviders: []string{}},
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if got.DisabledProviders == nil {
		t.Fatal("DisabledProviders is nil, want explicit empty list")
	}
	if len(got.DisabledProviders) != 0 {
		t.Errorf("DisabledProviders = %v, want []", got.DisabledProviders)
	}
}

func TestEffectiveSettings_HostDotsDisabledFalse_OverridesGlobalTrue(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DotsDisabled: config.BoolPtr(true),
		},
		HostSettings: map[string]config.Settings{
			"myhost": {DotsDisabled: config.BoolPtr(false)},
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if config.BoolVal(got.DotsDisabled) {
		t.Error("DotsDisabled: want false (host opts back in), got true")
	}
}

func TestEffectiveSettings_HostEntryAbsentDotsDisabled_GlobalPreserved(t *testing.T) {
	// A nil host *bool must not override a global true.
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DotsDisabled: config.BoolPtr(true),
		},
		HostSettings: map[string]config.Settings{
			"myhost": testSettingsWithProviderPriority("bun"), // DotsDisabled not set → nil
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if !config.BoolVal(got.DotsDisabled) {
		t.Error("DotsDisabled: want true (global preserved when host nil), got false")
	}
}

func TestEffectiveSettings_AgentFeatureFlagsHostOverride(t *testing.T) {
	tr := config.BoolPtr(true)
	cfg := &config.RootConfig{
		Settings: config.Settings{SkillsDisabled: config.BoolPtr(false)},
		HostSettings: map[string]config.Settings{
			"h1": {SkillsDisabled: tr, McpDisabled: tr, PluginsDisabled: tr},
		},
	}
	s := cfg.EffectiveSettings("h1")
	if !config.BoolVal(s.SkillsDisabled) || !config.BoolVal(s.McpDisabled) || !config.BoolVal(s.PluginsDisabled) {
		t.Errorf("host overrides not applied: %+v", s)
	}
	other := cfg.EffectiveSettings("other")
	if config.BoolVal(other.SkillsDisabled) || other.McpDisabled != nil || other.PluginsDisabled != nil {
		t.Errorf("non-matching host must keep globals: %+v", other)
	}
}

func TestEffectiveSettings_Mixed_HostProviderPriority_OverridesGlobal(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: testSettingsWithProviderPriority("npm", "uv"),
		HostSettings: map[string]config.Settings{
			"myhost": testSettingsWithProviderPriority("bun"),
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if len(got.ProviderPriority) == 0 || got.ProviderPriority[0] != "bun" {
		t.Errorf("ProviderPriority = %v, want bun first (host wins)", got.ProviderPriority)
	}
}

func TestEffectiveSettings_MultipleHosts_OnlyCurrentApplies(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: testSettingsWithProviderPriority("npm"),
		HostSettings: map[string]config.Settings{
			"workhost": testSettingsWithProviderPriority("bun"),
			"homehost": testSettingsWithProviderPriority("pnpm"),
		},
	}
	got := cfg.EffectiveSettings("homehost")
	if len(got.ProviderPriority) == 0 || got.ProviderPriority[0] != "pnpm" {
		t.Errorf("ProviderPriority = %v, want pnpm first (homehost only)", got.ProviderPriority)
	}

	got2 := cfg.EffectiveSettings("workhost")
	if len(got2.ProviderPriority) == 0 || got2.ProviderPriority[0] != "bun" {
		t.Errorf("ProviderPriority = %v, want bun first (workhost only)", got2.ProviderPriority)
	}

	got3 := cfg.EffectiveSettings("otherhost")
	if len(got3.ProviderPriority) == 0 || got3.ProviderPriority[0] != "npm" {
		t.Errorf("ProviderPriority = %v, want npm first (global, no host entry)", got3.ProviderPriority)
	}
}
