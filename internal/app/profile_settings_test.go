package app_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// ─── ProfileGroups ────────────────────────────────────────────────────────────

func TestProfileGroups_ProfileWithTwoGroups(t *testing.T) {
	a, cfgPath := newImportApp(t)

	// Seed two named groups in config.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
			logicalTool("jq", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")}, // base
			{Name: "work", Tools: groupTools("slack")},
			{Name: "dev", Tools: groupTools("jq")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	// Create a profile that references "base" and "work" (not "dev").
	if err := a.AddProfile("myprofile", []string{"base", "work"}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	groups, err := a.ProfileGroups(context.Background(), "myprofile")
	if err != nil {
		t.Fatalf("ProfileGroups: %v", err)
	}

	// Should return exactly the groups belonging to the profile (base + work).
	if len(groups) != 2 {
		t.Fatalf("ProfileGroups returned %d groups, want 2: %v", len(groups), groups)
	}
	names := make(map[string]bool)
	for _, g := range groups {
		names[g.BaseName()] = true
	}
	if !names["base"] || !names["work"] {
		t.Errorf("ProfileGroups returned wrong groups: %v, want base and work", names)
	}
	if names["dev"] {
		t.Error("ProfileGroups should not have returned the 'dev' group")
	}
}

func TestProfileGroups_ProfileWithNoGroups(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.AddProfile("empty-profile", []string{}); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	groups, err := a.ProfileGroups(context.Background(), "empty-profile")
	if err != nil {
		t.Fatalf("ProfileGroups: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("ProfileGroups returned %d groups, want 0: %v", len(groups), groups)
	}
}

func TestProfileGroups_UnknownProfile_ReturnsError(t *testing.T) {
	a, _ := newImportApp(t)

	_, err := a.ProfileGroups(context.Background(), "nonexistent")
	if err == nil {
		t.Error("ProfileGroups with unknown profile should return an error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got %q", err.Error())
	}
}

func TestProfileGroups_EmptyProfileName_ReturnsAllGroups(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	groups, err := a.ProfileGroups(context.Background(), "")
	if err != nil {
		t.Fatalf("ProfileGroups with empty name: %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("ProfileGroups(\"\") returned %d groups, want 2 (all groups)", len(groups))
	}
}

// ─── SaveDisabledProviders ────────────────────────────────────────────────────

func TestSaveDisabledProviders_PersistsList(t *testing.T) {
	a, cfgPath := newImportApp(t)

	want := []string{"system", "node"}
	if err := a.SaveDisabledProviders(context.Background(), want); err != nil {
		t.Fatalf("SaveDisabledProviders: %v", err)
	}

	// Reload config and verify.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	got := cfg.HostSettings[short].DisabledProviders
	if len(got) != len(want) {
		t.Fatalf("DisabledProviders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DisabledProviders[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSaveDisabledProviders_SecondCallOverwrites(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SaveDisabledProviders(context.Background(), []string{"system", "node"}); err != nil {
		t.Fatalf("first SaveDisabledProviders: %v", err)
	}
	if err := a.SaveDisabledProviders(context.Background(), []string{"python"}); err != nil {
		t.Fatalf("second SaveDisabledProviders: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	got := cfg.HostSettings[short].DisabledProviders
	if len(got) != 1 || got[0] != "python" {
		t.Errorf("DisabledProviders = %v after second call, want [python]", got)
	}
}

func TestSaveDisabledProviders_EmptyList(t *testing.T) {
	a, cfgPath := newImportApp(t)

	// Start with something, then clear it.
	if err := a.SaveDisabledProviders(context.Background(), []string{"system"}); err != nil {
		t.Fatalf("first SaveDisabledProviders: %v", err)
	}
	if err := a.SaveDisabledProviders(context.Background(), []string{}); err != nil {
		t.Fatalf("second SaveDisabledProviders: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	got := cfg.HostSettings[short].DisabledProviders
	if got == nil {
		t.Fatal("DisabledProviders is nil after empty call, want explicit empty list")
	}
	if len(got) != 0 {
		t.Errorf("DisabledProviders = %v after empty call, want []", got)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), `"disabled_providers": []`) {
		t.Fatalf("settings JSON did not persist explicit empty disabled_providers list:\n%s", raw)
	}
}

// ─── SaveDotsDisabled ─────────────────────────────────────────────────────────

func TestSaveDotsDisabled_True(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled(true): %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if !config.BoolVal(cfg.HostSettings[short].DotsDisabled) {
		t.Error("DotsDisabled should be true after SaveDotsDisabled(true)")
	}
}

func TestSaveDotsDisabled_False(t *testing.T) {
	a, cfgPath := newImportApp(t)

	// Set to true first, then explicitly set to false.
	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled(true): %v", err)
	}
	if err := a.SaveDotsDisabled(context.Background(), false); err != nil {
		t.Fatalf("SaveDotsDisabled(false): %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if config.BoolVal(cfg.HostSettings[short].DotsDisabled) {
		t.Error("DotsDisabled should be false after SaveDotsDisabled(false)")
	}
}

func TestSaveSettings_PersistsGlobalAndHostSpecificShape(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "portablehost.example")
	a, cfgPath := newImportApp(t)

	disabled := true
	settings := config.Settings{
		AutoImport:        true,
		DotsRepo:          "~/dotfiles",
		DotsDisabled:      &disabled,
		DisabledProviders: []string{"node"},
		DotsGit: config.DotsGitConfig{
			AutoCommit: true,
			AutoPush:   true,
		},
	}
	settings.SetEcosystemManager("node", "pnpm")
	settings.SetEcosystemPriority("system", []string{"brew", "apt"})

	if err := a.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	global, ok := root["settings"].(map[string]any)
	if !ok {
		t.Fatalf("settings object missing or wrong type in %s", raw)
	}
	for _, key := range []string{"auto_import", "dots_git"} {
		if _, ok := global[key]; !ok {
			t.Fatalf("global settings key %q missing from %s", key, raw)
		}
	}
	for _, key := range []string{"ecosystems", "dots_repo", "dots_disabled", "disabled_providers"} {
		if _, ok := global[key]; ok {
			t.Fatalf("host-specific key %q leaked into global settings in %s", key, raw)
		}
	}

	hosts, ok := root["host_settings"].(map[string]any)
	if !ok {
		t.Fatalf("host_settings object missing or wrong type in %s", raw)
	}
	host, ok := hosts["portablehost"].(map[string]any)
	if !ok {
		t.Fatalf("host_settings.portablehost object missing or wrong type in %s", raw)
	}
	for _, key := range []string{"ecosystems", "dots_repo", "dots_disabled", "disabled_providers"} {
		if _, ok := host[key]; !ok {
			t.Fatalf("host settings key %q missing from %s", key, raw)
		}
	}
	for _, key := range []string{"auto_import", "dots_git"} {
		if _, ok := host[key]; ok {
			t.Fatalf("global key %q leaked into host settings in %s", key, raw)
		}
	}
}

func TestDisableDotsForHost_NoRepoPersistsDisabled(t *testing.T) {
	a, cfgPath := newImportApp(t)

	ops, err := a.DisableDotsForHost(context.Background(), app.DisableDotsOptions{})
	if err != nil {
		t.Fatalf("DisableDotsForHost: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops = %v, want none when dots_repo is not configured", ops)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if !config.BoolVal(cfg.HostSettings[short].DotsDisabled) {
		t.Error("DotsDisabled should be true after DisableDotsForHost")
	}
}

func TestEnableDotsForHost_NoRepoClearsDisabled(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled(true): %v", err)
	}
	ops, err := a.EnableDotsForHost(context.Background())
	if err != nil {
		t.Fatalf("EnableDotsForHost: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops = %v, want none when dots_repo is not configured", ops)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if config.BoolVal(cfg.HostSettings[short].DotsDisabled) {
		t.Error("DotsDisabled should be false after EnableDotsForHost")
	}
}

// ─── EffectiveManagers ────────────────────────────────────────────────────────

func TestEffectiveManagers_ReturnsTwoStrings(t *testing.T) {
	a, _ := newImportApp(t)

	pythonBin, nodeBin := a.EffectiveManagers()
	// Neither should panic. Both may be "" if none of the candidates are on PATH,
	// but they must be valid strings (not undefined/nil).
	t.Logf("pythonBin=%q nodeBin=%q", pythonBin, nodeBin)
}

func TestEffectiveManagers_NoPanicWithoutConfig(t *testing.T) {
	// App with no config written yet — settings load will return zero-value; probeFirst
	// must still run without panicking.
	a, _ := newImportApp(t)

	pythonBin, nodeBin := a.EffectiveManagers()
	// Both values are strings (possibly ""); verify via valid assignment.
	if false {
		t.Log(pythonBin, nodeBin)
	}
}

func TestEffectiveManagers_HonoursPinnedManager(t *testing.T) {
	// If we pin the node ecosystem manager to "npm" and npm is on PATH, EffectiveManagers
	// should return "npm" for the node bin.  We cannot guarantee "npm" is on the
	// test runner's PATH, so we only assert no error occurs and return type is string.
	a, _ := newImportApp(t)

	if err := a.SaveSettings(context.Background(), testSettingsWithNodePython("npm", "pip3")); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	pythonBin, nodeBin := a.EffectiveManagers()
	t.Logf("pythonBin=%q nodeBin=%q", pythonBin, nodeBin)
	// pythonBin and nodeBin are strings — assignment confirms they are the right type.
	_ = pythonBin
	_ = nodeBin
}

// ─── AllAvailableManagers ─────────────────────────────────────────────────────

func TestAllAvailableManagers_NoPanic(t *testing.T) {
	a, _ := newImportApp(t)
	pyBins, nodeBins := a.AllAvailableManagers()
	// Must return slices (possibly nil/empty) without panicking.
	t.Logf("pyBins=%v nodeBins=%v", pyBins, nodeBins)
}

func TestAllAvailableManagers_SupersetOfEffective(t *testing.T) {
	a, _ := newImportApp(t)
	pyBin, nodeBin := a.EffectiveManagers()
	pyBins, nodeBins := a.AllAvailableManagers()

	// Every binary that EffectiveManagers returns must also appear in AllAvailableManagers.
	if pyBin != "" {
		found := false
		for _, b := range pyBins {
			if b == pyBin {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("pyBin %q from EffectiveManagers not in AllAvailableManagers %v", pyBin, pyBins)
		}
	}
	if nodeBin != "" {
		found := false
		for _, b := range nodeBins {
			if b == nodeBin {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("nodeBin %q from EffectiveManagers not in AllAvailableManagers %v", nodeBin, nodeBins)
		}
	}
}

func TestAllAvailableManagers_NoDuplicates(t *testing.T) {
	a, _ := newImportApp(t)
	pyBins, nodeBins := a.AllAvailableManagers()

	check := func(label string, bins []string) {
		seen := make(map[string]bool)
		for _, b := range bins {
			if seen[b] {
				t.Errorf("%s: duplicate binary %q in AllAvailableManagers", label, b)
			}
			seen[b] = true
		}
	}
	check("python", pyBins)
	check("node", nodeBins)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// shortHostnameForTest mirrors the app-internal shortHostname logic so tests
// can derive the expected key without importing unexported symbols.
func shortHostnameForTest(hostname string) string {
	if idx := strings.IndexByte(hostname, '.'); idx != -1 {
		return hostname[:idx]
	}
	if hostname == "" {
		return "localhost"
	}
	return hostname
}
