package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func testSettingsWithManagers(node, python string) config.Settings {
	var s config.Settings
	if node != "" {
		s.SetEcosystemManager("node", node)
	}
	if python != "" {
		s.SetEcosystemManager("python", python)
	}
	return s
}

// ─── EffectiveSettings ────────────────────────────────────────────────────────

func TestEffectiveSettings_NoHostEntry_ReturnsGlobal(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: testSettingsWithManagers("bun", "uv"),
	}
	got := cfg.EffectiveSettings("myhost")
	if got.EcosystemManager("node") != "bun" {
		t.Errorf("node manager = %q, want bun", got.EcosystemManager("node"))
	}
	if got.EcosystemManager("python") != "uv" {
		t.Errorf("python manager = %q, want uv", got.EcosystemManager("python"))
	}
}

func TestEffectiveSettings_HostEntryAllZero_GlobalFillsGaps(t *testing.T) {
	settings := testSettingsWithManagers("pnpm", "uv")
	settings.DotsRepo = "~/dotfiles"
	cfg := &config.RootConfig{
		Settings: settings,
		HostSettings: map[string]config.Settings{
			"myhost": {}, // all zero/empty — must not override global
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if got.EcosystemManager("node") != "pnpm" {
		t.Errorf("node manager = %q, want pnpm (global)", got.EcosystemManager("node"))
	}
	if got.EcosystemManager("python") != "uv" {
		t.Errorf("python manager = %q, want uv (global)", got.EcosystemManager("python"))
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
	// When the host key exists but DisabledProviders is nil, the global list is
	// preserved. Nil means "not set" — not "enable everything".
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
	// A host can explicitly opt back into dots sync by setting DotsDisabled=false
	// (*bool pointing to false), even when the global setting disables it (true).
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
	// A host entry that does NOT set DotsDisabled (nil *bool) must not override
	// the global true. This was the silent bug: bool zero-value (false) would
	// unconditionally overwrite global true.
	cfg := &config.RootConfig{
		Settings: config.Settings{
			DotsDisabled: config.BoolPtr(true),
		},
		HostSettings: map[string]config.Settings{
			"myhost": testSettingsWithManagers("bun", ""), // DotsDisabled not set → nil
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if !config.BoolVal(got.DotsDisabled) {
		t.Error("DotsDisabled: want true (global preserved when host nil), got false")
	}
}

func TestEffectiveSettings_Mixed_HostNodeManager_GlobalPythonManager(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: testSettingsWithManagers("npm", "uv"),
		HostSettings: map[string]config.Settings{
			"myhost": testSettingsWithManagers("bun", ""),
		},
	}
	got := cfg.EffectiveSettings("myhost")
	if got.EcosystemManager("node") != "bun" {
		t.Errorf("node manager = %q, want bun (host wins)", got.EcosystemManager("node"))
	}
	if got.EcosystemManager("python") != "uv" {
		t.Errorf("python manager = %q, want uv (global fills gap)", got.EcosystemManager("python"))
	}
}

func TestEffectiveSettings_MultipleHosts_OnlyCurrentApplies(t *testing.T) {
	cfg := &config.RootConfig{
		Settings: testSettingsWithManagers("npm", ""),
		HostSettings: map[string]config.Settings{
			"workhost": testSettingsWithManagers("bun", ""),
			"homehost": testSettingsWithManagers("pnpm", ""),
		},
	}
	got := cfg.EffectiveSettings("homehost")
	if got.EcosystemManager("node") != "pnpm" {
		t.Errorf("node manager = %q, want pnpm (homehost only)", got.EcosystemManager("node"))
	}

	got2 := cfg.EffectiveSettings("workhost")
	if got2.EcosystemManager("node") != "bun" {
		t.Errorf("node manager = %q, want bun (workhost only)", got2.EcosystemManager("node"))
	}

	got3 := cfg.EffectiveSettings("otherhost")
	if got3.EcosystemManager("node") != "npm" {
		t.Errorf("node manager = %q, want npm (global, no host entry)", got3.EcosystemManager("node"))
	}
}
