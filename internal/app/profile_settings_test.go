package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
	systemprovider "github.com/lkshrk/omni/internal/provider/system"
)

// ─── HostGroups ───────────────────────────────────────────────────────────────

func TestHostGroups_HostWithAssignedGroup(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
			logicalTool("jq", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
			{Name: "dev", Tools: groupTools("jq")},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	groups, err := a.HostGroups(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostGroups: %v", err)
	}

	if len(groups) != 2 {
		t.Fatalf("HostGroups returned %d groups, want 2: %v", len(groups), groups)
	}
	names := make(map[string]bool)
	for _, g := range groups {
		names[g.BaseName()] = true
	}
	if !names["testhost"] || !names["work"] {
		t.Errorf("HostGroups returned wrong groups: %v, want testhost and work", names)
	}
	if names["dev"] {
		t.Error("HostGroups should not have returned the 'dev' group")
	}
}

func TestHostGroups_HostWithNoAssignedGroups(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.EnsureHost("testhost"); err != nil {
		t.Fatalf("EnsureHost: %v", err)
	}

	groups, err := a.HostGroups(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostGroups: %v", err)
	}
	if len(groups) != 1 || groups[0].BaseName() != "testhost" {
		t.Fatalf("HostGroups returned %v, want only testhost", groupNamesForTest(groups))
	}
}

func TestHostGroups_UnknownHost_ReturnsError(t *testing.T) {
	a, _ := newImportApp(t)

	_, err := a.HostGroups(context.Background(), "nonexistent")
	if err == nil {
		t.Error("HostGroups with unknown host should return an error, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error should mention 'not configured', got %q", err.Error())
	}
}

func TestHostGroups_EmptyHostName_ReturnsAllGroups(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	groups, err := a.HostGroups(context.Background(), "")
	if err != nil {
		t.Fatalf("HostGroups with empty name: %v", err)
	}
	if len(groups) != 2 {
		t.Errorf("HostGroups(\"\") returned %d groups, want 2 (all groups)", len(groups))
	}
}

func groupNamesForTest(groups []*config.GroupConfig) []string {
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		names = append(names, group.BaseName())
	}
	return names
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

func TestSetupProviderOptionsFromManagers(t *testing.T) {
	options := app.SetupProviderOptionsFromManagers(
		map[string]string{"system": "apt"},
		[]string{"uv", "pip3"},
		[]string{"bun", "npm"},
		config.Settings{DisabledProviders: []string{"node"}},
	)

	if len(options) != 3 {
		t.Fatalf("SetupProviderOptionsFromManagers returned %d options, want 3: %+v", len(options), options)
	}
	want := []app.SetupProviderOption{
		{Name: "system", Label: "system(apt)", Enabled: true},
		{Name: "node", Label: "node(bun • npm)", Enabled: false},
		{Name: "python", Label: "python(uv • pip3)", Enabled: true},
	}
	for i := range want {
		if options[i] != want[i] {
			t.Fatalf("option %d = %+v, want %+v", i, options[i], want[i])
		}
	}
}

func TestSetupProviderOptionsUsesResolvedProvidersAndAvailableManagers(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{"bun", "npm", "uv"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir)

	brew := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, systemprovider.New(brew), brew)
	settings := config.Settings{DisabledProviders: []string{provider.EcosystemPython}}
	settings.SetEcosystemManager(provider.EcosystemNode, "npm")

	got := a.SetupProviderOptions(context.Background(), settings)
	want := []app.SetupProviderOption{
		{Name: "system", Label: "system(brew)", Enabled: true},
		{Name: "node", Label: "node(npm • bun)", Enabled: true},
		{Name: "python", Label: "python(uv)", Enabled: false},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("SetupProviderOptions = %+v, want %+v", got, want)
	}
}

func TestSetupProviderEnabled(t *testing.T) {
	options := []app.SetupProviderOption{
		{Name: "system", Enabled: true},
		{Name: "node", Enabled: false},
		{Name: "python", Enabled: true},
	}

	if app.SetupProviderEnabled(options, "node") {
		t.Fatal("node should be disabled")
	}
	if !app.SetupProviderEnabled(options, "python") {
		t.Fatal("python should be enabled")
	}
	if app.SetupProviderEnabled(options, "missing") {
		t.Fatal("missing provider should be disabled")
	}
	if got := app.SetupDisabledProviders(options); !slices.Equal(got, []string{"node"}) {
		t.Fatalf("SetupDisabledProviders = %v, want [node]", got)
	}
}

func TestSetupNodeHelpers(t *testing.T) {
	a, _ := newImportApp(t)
	wantOptions := []app.SettingsManagerOption{
		{Value: "", Label: "auto", Description: "use the provider's preferred available manager"},
		{Value: "bun", Label: "bun", Description: "fast runtime + package manager"},
		{Value: "pnpm", Label: "pnpm", Description: "disk-efficient, workspace-native"},
		{Value: "npm", Label: "npm", Description: "bundled with every Node.js install"},
	}
	if got := app.DefaultSetupNodeManagerOptions(); !slices.Equal(got, wantOptions) {
		t.Fatalf("DefaultSetupNodeManagerOptions = %+v, want %+v", got, wantOptions)
	}
	if got := a.SetupNodeManagerOptions(); !slices.Equal(got, wantOptions) {
		t.Fatalf("SetupNodeManagerOptions = %+v, want %+v", got, wantOptions)
	}
	if app.SetupNodeProviderEnabled([]app.SetupProviderOption{{Name: "node", Enabled: false}}) {
		t.Fatal("disabled node setup provider should not be enabled")
	}
	if !app.SetupNodeProviderEnabled([]app.SetupProviderOption{{Name: "node", Enabled: true}}) {
		t.Fatal("enabled node setup provider should be enabled")
	}
}

func TestApplySettingsChange_DraftSemantics(t *testing.T) {
	a, _ := newImportApp(t)
	base := config.Settings{DisabledProviders: []string{"node"}}

	got, _, err := a.ApplySettingsChange(context.Background(), base, app.ToggleSettingBool("auto_import"))
	if err != nil {
		t.Fatalf("ApplySettingsChange auto_import: %v", err)
	}
	if !got.AutoImport {
		t.Fatal("auto_import should toggle on")
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.SetSettingValue("node.manager", "pnpm"))
	if err != nil {
		t.Fatalf("ApplySettingsChange node.manager: %v", err)
	}
	if manager := got.EcosystemManager(provider.EcosystemNode); manager != "pnpm" {
		t.Fatalf("node manager = %q, want pnpm", manager)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.SetSettingValue("python.manager", "uv"))
	if err != nil {
		t.Fatalf("ApplySettingsChange python.manager: %v", err)
	}
	if manager := got.EcosystemManager(provider.EcosystemPython); manager != "uv" {
		t.Fatalf("python manager = %q, want uv", manager)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.SetSettingValue("python.manager", "pip"))
	if err != nil {
		t.Fatalf("ApplySettingsChange python.manager alias: %v", err)
	}
	if manager := got.EcosystemManager(provider.EcosystemPython); manager != "pip3" {
		t.Fatalf("python manager alias = %q, want pip3", manager)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.SetSystemPriority([]string{"brew", "missing", "apt", "brew"}))
	if err != nil {
		t.Fatalf("ApplySettingsChange system priority: %v", err)
	}
	if priority := got.EcosystemPriority(provider.EcosystemSystem); strings.Join(priority, ",") != "brew,apt" {
		t.Fatalf("system priority = %v, want [brew apt]", priority)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.ToggleSettingsProvider(provider.EcosystemNode))
	if err != nil {
		t.Fatalf("ApplySettingsChange toggle node: %v", err)
	}
	if slices.Contains(got.DisabledProviders, provider.EcosystemNode) {
		t.Fatalf("node should be enabled, disabled providers = %v", got.DisabledProviders)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.ToggleSettingsProvider(provider.EcosystemSystem))
	if err != nil {
		t.Fatalf("ApplySettingsChange toggle system: %v", err)
	}
	if !slices.Contains(got.DisabledProviders, provider.EcosystemSystem) {
		t.Fatalf("system should be disabled, disabled providers = %v", got.DisabledProviders)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.ToggleSettingBool("dots_git.auto_commit"))
	if err != nil {
		t.Fatalf("ApplySettingsChange dots_git.auto_commit: %v", err)
	}
	if !got.DotsGit.AutoCommit {
		t.Fatal("dots_git.auto_commit should toggle on")
	}
}

func TestApplySettingsChange_SetProviderPriority(t *testing.T) {
	a, _ := newImportApp(t)

	got, _, err := a.ApplySettingsChange(context.Background(), config.Settings{}, app.SetProviderPriority(
		[]string{"pnpm", "bun", "npm", "uv", "pip", "brew", "bogus", "brew"},
	))
	if err != nil {
		t.Fatalf("ApplySettingsChange set provider priority: %v", err)
	}
	// Unknown names dropped, duplicates removed, order preserved.
	if want := "pnpm,bun,npm,uv,pip,brew"; strings.Join(got.ProviderPriority, ",") != want {
		t.Fatalf("provider_priority = %v, want %s", got.ProviderPriority, want)
	}
	// Node manager derived from the top-ranked node concrete in priority (pnpm).
	if m := got.EcosystemManager(provider.EcosystemNode); m != "pnpm" {
		t.Errorf("derived node manager = %q, want pnpm", m)
	}
	// Python manager derived from top-ranked python concrete (uv before pip).
	if m := got.EcosystemManager(provider.EcosystemPython); m != "uv" {
		t.Errorf("derived python manager = %q, want uv", m)
	}
}

func TestApplySettingsChange_SetProviderPriority_DerivesManagerSkippingDisabled(t *testing.T) {
	a, _ := newImportApp(t)
	base := config.Settings{DisabledProviders: []string{"bun", "pnpm", "uv"}}

	got, _, err := a.ApplySettingsChange(context.Background(), base, app.SetProviderPriority(
		[]string{"bun", "pnpm", "npm", "uv", "pip"},
	))
	if err != nil {
		t.Fatalf("ApplySettingsChange: %v", err)
	}
	// bun+pnpm disabled → first available node concrete is npm.
	if m := got.EcosystemManager(provider.EcosystemNode); m != "npm" {
		t.Errorf("derived node manager = %q, want npm (bun/pnpm disabled)", m)
	}
	// uv disabled → pip wins; pip stores as its settings value pip3.
	if m := got.EcosystemManager(provider.EcosystemPython); m != "pip3" {
		t.Errorf("derived python manager = %q, want pip3 (uv disabled)", m)
	}
}

func TestApplySettingsChange_SetProviderLayout_SetsOrderAndDisabled(t *testing.T) {
	a, _ := newImportApp(t)

	got, _, err := a.ApplySettingsChange(context.Background(), config.Settings{}, app.SetProviderLayout(
		[]string{"pnpm", "bun", "npm", "uv", "pip", "brew"},
		[]string{"bun", "bogus", "uv"},
	))
	if err != nil {
		t.Fatalf("SetProviderLayout: %v", err)
	}
	if want := "pnpm,bun,npm,uv,pip,brew"; strings.Join(got.ProviderPriority, ",") != want {
		t.Errorf("provider_priority = %v, want %s", got.ProviderPriority, want)
	}
	// "bogus" filtered out; valid concretes kept in order.
	if want := "bun,uv"; strings.Join(got.DisabledProviders, ",") != want {
		t.Errorf("disabled = %v, want %s", got.DisabledProviders, want)
	}
	// pnpm leads node order and is enabled → node manager pnpm; uv disabled → python pip3.
	if m := got.EcosystemManager(provider.EcosystemNode); m != "pnpm" {
		t.Errorf("node manager = %q, want pnpm", m)
	}
	if m := got.EcosystemManager(provider.EcosystemPython); m != "pip3" {
		t.Errorf("python manager = %q, want pip3", m)
	}
}

func TestConcreteProviderPriorityDraft_SeedsThenAppendsAll(t *testing.T) {
	a, _ := newImportApp(t)
	draft := a.ConcreteProviderPriorityDraft([]string{"uv", "brew"})
	if len(draft) < 2 || draft[0] != "uv" || draft[1] != "brew" {
		t.Fatalf("draft = %v, want leading [uv brew]", draft)
	}
	if !slices.Contains(draft, "bun") || !slices.Contains(draft, "pip") {
		t.Errorf("draft = %v, want all concretes appended", draft)
	}
}

func TestDefaultConcreteProviderPriorityDraft_NoApp(t *testing.T) {
	draft := app.DefaultConcreteProviderPriorityDraft(nil)
	want := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip"}
	if strings.Join(draft, ",") != strings.Join(want, ",") {
		t.Fatalf("draft = %v, want %v", draft, want)
	}
}

func TestAvailableConcreteProviderSet(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, brew)
	set := a.AvailableConcreteProviderSet(context.Background())
	if !set["brew"] {
		t.Errorf("available set = %v, want brew present", set)
	}
}

func TestApplySettingsChange_ToggleConcreteProvider(t *testing.T) {
	a, _ := newImportApp(t)

	got, _, err := a.ApplySettingsChange(context.Background(), config.Settings{}, app.ToggleSettingsProvider("bun"))
	if err != nil {
		t.Fatalf("toggle concrete bun: %v", err)
	}
	if !slices.Contains(got.DisabledProviders, "bun") {
		t.Fatalf("bun should be disabled, got %v", got.DisabledProviders)
	}

	got, _, err = a.ApplySettingsChange(context.Background(), got, app.ToggleSettingsProvider("bun"))
	if err != nil {
		t.Fatalf("re-toggle concrete bun: %v", err)
	}
	if slices.Contains(got.DisabledProviders, "bun") {
		t.Fatalf("bun should be re-enabled, got %v", got.DisabledProviders)
	}
}

func TestSystemProviderPriorityPlanning(t *testing.T) {
	a, _ := newImportApp(t, &stubProvider{name: "brew"}, &stubProvider{name: "apt"})

	if got := a.SystemProviderPriorityDisplay([]string{"brew", "missing", "apt", "brew"}); !slices.Equal(got, []string{"brew", "apt"}) {
		t.Fatalf("display priority = %v, want [brew apt]", got)
	}
	if got := a.SystemProviderPriorityDraft([]string{"brew", "missing", "brew"}); !slices.Equal(got, []string{"brew", "apt", "apk", "dnf", "zypper", "pacman"}) {
		t.Fatalf("draft priority = %v, want brew followed by remaining valid providers", got)
	}
	if got := app.DefaultSystemProviderPriorityDraft([]string{"missing"}); !slices.Equal(got, app.DefaultSystemProviderPriorityOptions()) {
		t.Fatalf("default draft = %v, want defaults %v", got, app.DefaultSystemProviderPriorityOptions())
	}
}

func TestPinEcosystemForHostPersistsManagerAndSystemPriority(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.local")
	a, cfgPath := newImportApp(t)
	initialSettings := config.Settings{}
	initialSettings.SetEcosystemPriority(provider.EcosystemSystem, []string{"apt", "dnf", "brew"})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		HostSettings: map[string]config.Settings{
			"desk": initialSettings,
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := a.PinEcosystemForHost(context.Background(), provider.EcosystemNode, "bun"); err != nil {
		t.Fatalf("PinEcosystemForHost node: %v", err)
	}
	if err := a.PinEcosystemForHost(context.Background(), provider.EcosystemSystem, "brew"); err != nil {
		t.Fatalf("PinEcosystemForHost system: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	settings := cfg.HostSettings["desk"]
	if got := settings.EcosystemManager(provider.EcosystemNode); got != "bun" {
		t.Fatalf("node manager = %q, want bun", got)
	}
	wantPriority := []string{"brew", "apt", "apk", "dnf", "zypper", "pacman"}
	if got := settings.EcosystemPriority(provider.EcosystemSystem); !slices.Equal(got, wantPriority) {
		t.Fatalf("system priority = %v, want %v", got, wantPriority)
	}
}

func TestPinEcosystemForHostRejectsInvalidProviderPins(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.local")
	a, _ := newImportApp(t)

	tests := []struct {
		name      string
		ecosystem string
		concrete  string
		want      string
	}{
		{
			name:      "wrong node manager",
			ecosystem: provider.EcosystemNode,
			concrete:  "uv",
			want:      `"uv" is not a manager for ecosystem "node"`,
		},
		{
			name:      "ecosystem is not system concrete",
			ecosystem: provider.EcosystemSystem,
			concrete:  provider.EcosystemNode,
			want:      `"node" is not a system provider`,
		},
		{
			name:      "unknown family",
			ecosystem: "ruby",
			concrete:  "gem",
			want:      `unknown provider family "ruby"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := a.PinEcosystemForHost(context.Background(), tt.ecosystem, tt.concrete)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("PinEcosystemForHost error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSettingsProviderSummary(t *testing.T) {
	a, _ := newImportApp(t)
	settings := config.Settings{DisabledProviders: []string{"node", "brew", "node"}}

	got := a.SettingsProviderSummary(settings)
	want := app.SettingsProviderSummary{Enabled: 2, Total: 3}
	if got != want {
		t.Fatalf("SettingsProviderSummary = %+v, want %+v", got, want)
	}

	if defaultSummary := app.DefaultSettingsProviderSummary(settings); defaultSummary != want {
		t.Fatalf("DefaultSettingsProviderSummary = %+v, want %+v", defaultSummary, want)
	}
}

func TestSettingsProviderStateFrom(t *testing.T) {
	var settings config.Settings
	settings.DisabledProviders = []string{"node"}
	settings.SetEcosystemManager("node", "bun")
	settings.SetEcosystemManager("python", "uv")
	settings.SetEcosystemPriority("system", []string{"brew", "apt"})

	got := app.SettingsProviderStateFrom(settings)
	if !got.SystemEnabled || got.NodeEnabled || !got.PythonEnabled {
		t.Fatalf("provider enabled flags = system:%v node:%v python:%v, want true false true", got.SystemEnabled, got.NodeEnabled, got.PythonEnabled)
	}
	if got.NodeManager != "bun" || got.PythonManager != "uv" {
		t.Fatalf("managers = node:%q python:%q, want bun uv", got.NodeManager, got.PythonManager)
	}
	if !slices.Equal(got.SystemPriority, []string{"brew", "apt"}) {
		t.Fatalf("SystemPriority = %v, want [brew apt]", got.SystemPriority)
	}
	got.SystemPriority[0] = "mutated"
	if app.SettingsProviderStateFrom(settings).SystemPriority[0] == "mutated" {
		t.Fatal("SettingsProviderStateFrom returned mutable settings priority")
	}
}

func TestSettingsChangeForAction(t *testing.T) {
	a, _ := newImportApp(t)
	tests := []struct {
		name   string
		action app.SettingsActionID
		assert func(t *testing.T, settings config.Settings)
	}{
		{
			name:   "auto import",
			action: app.SettingsActionToggleAutoImport,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if !settings.AutoImport {
					t.Fatal("AutoImport should be enabled")
				}
			},
		},
		{
			name:   "system provider",
			action: app.SettingsActionToggleSystemProvider,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if !slices.Contains(settings.DisabledProviders, "system") {
					t.Fatalf("DisabledProviders = %v, want system", settings.DisabledProviders)
				}
			},
		},
		{
			name:   "node provider",
			action: app.SettingsActionToggleNodeProvider,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if !slices.Contains(settings.DisabledProviders, "node") {
					t.Fatalf("DisabledProviders = %v, want node", settings.DisabledProviders)
				}
			},
		},
		{
			name:   "python provider",
			action: app.SettingsActionTogglePythonProvider,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if !slices.Contains(settings.DisabledProviders, "python") {
					t.Fatalf("DisabledProviders = %v, want python", settings.DisabledProviders)
				}
			},
		},
		{
			name:   "node manager",
			action: app.SettingsActionCycleNodeManager,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if got := settings.EcosystemManager("node"); got != "bun" {
					t.Fatalf("node manager = %q, want bun", got)
				}
			},
		},
		{
			name:   "python manager",
			action: app.SettingsActionCyclePythonManager,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if got := settings.EcosystemManager("python"); got != "uv" {
					t.Fatalf("python manager = %q, want uv", got)
				}
			},
		},
		{
			name:   "dots commit",
			action: app.SettingsActionToggleDotsCommit,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if !settings.DotsGit.AutoCommit {
					t.Fatal("DotsGit.AutoCommit should be enabled")
				}
			},
		},
		{
			name:   "dots push",
			action: app.SettingsActionToggleDotsPush,
			assert: func(t *testing.T, settings config.Settings) {
				t.Helper()
				if !settings.DotsGit.AutoPush {
					t.Fatal("DotsGit.AutoPush should be enabled")
				}
			},
		},
	}
	for _, tt := range tests {
		change, err := app.SettingsChangeForAction(tt.action)
		if err != nil {
			t.Fatalf("%s SettingsChangeForAction: %v", tt.name, err)
		}
		settings, _, err := a.ApplySettingsChange(context.Background(), config.Settings{}, change)
		if err != nil {
			t.Fatalf("%s ApplySettingsChange: %v", tt.name, err)
		}
		tt.assert(t, settings)
	}

	if _, err := app.SettingsChangeForAction("missing"); err == nil || !strings.Contains(err.Error(), `unknown settings action "missing"`) {
		t.Fatalf("SettingsChangeForAction(missing) err = %v, want unknown action", err)
	}
}

func TestSettingsActionAvailable(t *testing.T) {
	if !app.SettingsActionAvailable(config.Settings{}, app.SettingsActionToggleDotsCommit) {
		t.Fatal("dots commit action should be available when auto push is disabled")
	}
	settings := config.Settings{DotsGit: config.DotsGitConfig{AutoPush: true}}
	if app.SettingsActionAvailable(settings, app.SettingsActionToggleDotsCommit) {
		t.Fatal("dots commit action should be unavailable when auto push implies commits")
	}
	if !app.SettingsActionAvailable(settings, app.SettingsActionToggleDotsPush) {
		t.Fatal("dots push action should remain available")
	}
}

func TestDefaultEcosystemProviderNames(t *testing.T) {
	got := app.DefaultEcosystemProviderNames()
	want := []string{"system", "node", "python"}
	if !slices.Equal(got, want) {
		t.Fatalf("DefaultEcosystemProviderNames = %v, want %v", got, want)
	}
}

func TestSettingsManagerOptions(t *testing.T) {
	a, _ := newImportApp(t)

	node := a.SettingsManagerOptions(provider.EcosystemNode)
	wantNode := []app.SettingsManagerOption{
		{Value: "", Label: "auto", Description: "use the provider's preferred available manager"},
		{Value: "bun", Label: "bun", Description: "fast runtime + package manager"},
		{Value: "pnpm", Label: "pnpm", Description: "disk-efficient, workspace-native"},
		{Value: "npm", Label: "npm", Description: "bundled with every Node.js install"},
	}
	if !slices.Equal(node, wantNode) {
		t.Fatalf("node manager options = %#v, want %#v", node, wantNode)
	}
	if defaultNode := app.DefaultSettingsManagerOptions(provider.EcosystemNode); !slices.Equal(defaultNode, wantNode) {
		t.Fatalf("default node manager options = %#v, want %#v", defaultNode, wantNode)
	}

	python := a.SettingsManagerOptions(provider.EcosystemPython)
	wantPython := []app.SettingsManagerOption{
		{Value: "", Label: "auto", Description: "use the provider's preferred available manager"},
		{Value: "uv", Label: "uv", Description: "fast Python tool manager"},
		{Value: "pip3", Label: "pip3", Description: "Python package installer"},
	}
	if !slices.Equal(python, wantPython) {
		t.Fatalf("python manager options = %#v, want %#v", python, wantPython)
	}

	if got := a.SettingsManagerOptions("missing"); got != nil {
		t.Fatalf("unknown ecosystem manager options = %#v, want nil", got)
	}
}

func TestSystemProviderPriorityDisplayFiltersInvalidAndDuplicateProviders(t *testing.T) {
	a, _ := newImportApp(t)
	priority := []string{"apt", "missing", "brew", "apt", "dnf"}
	want := []string{"apt", "brew", "dnf"}

	if got := app.DefaultSystemProviderPriorityDisplay(priority); !slices.Equal(got, want) {
		t.Fatalf("DefaultSystemProviderPriorityDisplay = %v, want %v", got, want)
	}
	if got := a.SystemProviderPriorityDisplay(priority); !slices.Equal(got, want) {
		t.Fatalf("SystemProviderPriorityDisplay = %v, want %v", got, want)
	}
}

func TestSettingsManagerHelp(t *testing.T) {
	a, _ := newImportApp(t)

	tests := []struct {
		name      string
		ecosystem string
		want      string
	}{
		{
			name:      "node",
			ecosystem: provider.EcosystemNode,
			want:      "JS package manager (auto = bun preferred, then pnpm, then npm).",
		},
		{
			name:      "python",
			ecosystem: provider.EcosystemPython,
			want:      "Python tool manager (auto = uv preferred, then pip3).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := app.DefaultSettingsManagerHelp(tt.ecosystem); got != tt.want {
				t.Fatalf("DefaultSettingsManagerHelp(%q) = %q, want %q", tt.ecosystem, got, tt.want)
			}
			if got := a.SettingsManagerHelp(tt.ecosystem); got != tt.want {
				t.Fatalf("SettingsManagerHelp(%q) = %q, want %q", tt.ecosystem, got, tt.want)
			}
		})
	}

	if got := app.DefaultSettingsManagerHelp("missing"); got != "" {
		t.Fatalf("DefaultSettingsManagerHelp(missing) = %q, want empty", got)
	}
	if got := (*app.App)(nil).SettingsManagerHelp(provider.EcosystemNode); got != "JS package manager (auto = bun preferred, then pnpm, then npm)." {
		t.Fatalf("nil SettingsManagerHelp(node) = %q, want default node help", got)
	}
}

func TestSettingsProviderHelp(t *testing.T) {
	a, _ := newImportApp(t)

	tests := []struct {
		name      string
		ecosystem string
		want      string
	}{
		{
			name:      "system",
			ecosystem: provider.EcosystemSystem,
			want:      "Track system tools on this machine (apt/apk/dnf/zypper/pacman/brew).",
		},
		{
			name:      "node",
			ecosystem: provider.EcosystemNode,
			want:      "Track node tools on this machine (bun/pnpm/npm).",
		},
		{
			name:      "python",
			ecosystem: provider.EcosystemPython,
			want:      "Track python tools on this machine (uv/pip3).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := app.DefaultSettingsProviderHelp(tt.ecosystem); got != tt.want {
				t.Fatalf("DefaultSettingsProviderHelp(%q) = %q, want %q", tt.ecosystem, got, tt.want)
			}
			if got := a.SettingsProviderHelp(tt.ecosystem); got != tt.want {
				t.Fatalf("SettingsProviderHelp(%q) = %q, want %q", tt.ecosystem, got, tt.want)
			}
		})
	}

	if got := app.DefaultSettingsProviderHelp("missing"); got != "" {
		t.Fatalf("DefaultSettingsProviderHelp(missing) = %q, want empty", got)
	}
	if got := (*app.App)(nil).SettingsProviderHelp(provider.EcosystemSystem); got != "Track system tools on this machine (apt/apk/dnf/zypper/pacman/brew)." {
		t.Fatalf("nil SettingsProviderHelp(system) = %q, want default system help", got)
	}
}

func TestSettingsProviderAndManagerMembershipHelpers(t *testing.T) {
	a, _ := newImportApp(t)

	if !a.IsManagerForEcosystem(provider.EcosystemNode, "bun") {
		t.Fatal("IsManagerForEcosystem(node, bun) = false, want true")
	}
	if a.IsManagerForEcosystem(provider.EcosystemNode, "uv") {
		t.Fatal("IsManagerForEcosystem(node, uv) = true, want false")
	}
	if !a.IsConcreteProviderForEcosystem(provider.EcosystemSystem, "brew") {
		t.Fatal("IsConcreteProviderForEcosystem(system, brew) = false, want true")
	}
	if a.IsConcreteProviderForEcosystem(provider.EcosystemSystem, provider.EcosystemSystem) {
		t.Fatal("IsConcreteProviderForEcosystem(system, system) = true, want false for provider family")
	}
	if a.IsConcreteProviderForEcosystem(provider.EcosystemNode, "brew") {
		t.Fatal("IsConcreteProviderForEcosystem(node, brew) = true, want false")
	}
}

func TestSettingsManagerCycle(t *testing.T) {
	a, _ := newImportApp(t)

	settings := config.Settings{}
	for _, tt := range []struct {
		ecosystem string
		want      string
	}{
		{provider.EcosystemNode, "bun"},
		{provider.EcosystemNode, "pnpm"},
		{provider.EcosystemNode, "npm"},
		{provider.EcosystemNode, ""},
	} {
		var err error
		settings, _, err = a.ApplySettingsChange(context.Background(), settings, app.CycleSettingsManager(tt.ecosystem))
		if err != nil {
			t.Fatalf("CycleSettingsManager(%s): %v", tt.ecosystem, err)
		}
		if got := settings.EcosystemManager(tt.ecosystem); got != tt.want {
			t.Fatalf("%s manager = %q, want %q", tt.ecosystem, got, tt.want)
		}
	}

	for _, tt := range []struct {
		ecosystem string
		want      string
	}{
		{provider.EcosystemPython, "uv"},
		{provider.EcosystemPython, "pip3"},
		{provider.EcosystemPython, ""},
	} {
		var err error
		settings, _, err = a.ApplySettingsChange(context.Background(), settings, app.CycleSettingsManager(tt.ecosystem))
		if err != nil {
			t.Fatalf("CycleSettingsManager(%s): %v", tt.ecosystem, err)
		}
		if got := settings.EcosystemManager(tt.ecosystem); got != tt.want {
			t.Fatalf("%s manager = %q, want %q", tt.ecosystem, got, tt.want)
		}
	}
}

func TestSaveSettingsChange_PersistsHostAndGlobalSettings(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "settings-change-host")
	a, cfgPath := newImportApp(t)

	for _, change := range []app.SettingsChange{
		app.SetSettingValue("auto_import", "true"),
		app.SetSettingValue("node.manager", "bun"),
		app.SetSettingValue("python.manager", "uv"),
		app.SetSystemPriority([]string{"brew"}),
		app.ToggleSettingsProvider(provider.EcosystemNode),
		app.ToggleSettingBool("dots_git.auto_push"),
	} {
		if _, err := a.SaveSettingsChange(context.Background(), change); err != nil {
			t.Fatalf("SaveSettingsChange(%+v): %v", change, err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Settings.AutoImport {
		t.Fatal("global settings.auto_import should persist")
	}
	if !cfg.Settings.DotsGit.AutoPush {
		t.Fatal("global settings.dots_git.auto_push should persist")
	}
	host := cfg.HostSettings["settings-change-host"]
	if manager := host.EcosystemManager(provider.EcosystemNode); manager != "bun" {
		t.Fatalf("host node manager = %q, want bun", manager)
	}
	if manager := host.EcosystemManager(provider.EcosystemPython); manager != "uv" {
		t.Fatalf("host python manager = %q, want uv", manager)
	}
	if priority := host.EcosystemPriority(provider.EcosystemSystem); strings.Join(priority, ",") != "brew" {
		t.Fatalf("host system priority = %v, want [brew]", priority)
	}
	if !slices.Contains(host.DisabledProviders, provider.EcosystemNode) {
		t.Fatalf("host disabled providers = %v, want node disabled", host.DisabledProviders)
	}
}

func TestSaveSettingsChanges_AppliesBatchToCurrentConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "settings-batch-host")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
	}); err != nil {
		t.Fatalf("saveAppConfig: %v", err)
	}

	settings, _, err := a.SaveSettingsChanges(
		context.Background(),
		app.SetSettingValue("node.manager", "pnpm"),
		app.ToggleSettingBool("dots_git.auto_push"),
	)
	if err != nil {
		t.Fatalf("SaveSettingsChanges: %v", err)
	}
	if !settings.AutoImport {
		t.Fatal("returned settings should preserve current config auto_import")
	}
	if manager := settings.EcosystemManager(provider.EcosystemNode); manager != "pnpm" {
		t.Fatalf("returned node manager = %q, want pnpm", manager)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Settings.AutoImport {
		t.Fatal("settings.auto_import should not be overwritten by the batch save")
	}
	if !cfg.Settings.DotsGit.AutoPush {
		t.Fatal("settings.dots_git.auto_push should persist")
	}
	if manager := cfg.HostSettings["settings-batch-host"].EcosystemManager(provider.EcosystemNode); manager != "pnpm" {
		t.Fatalf("host node manager = %q, want pnpm", manager)
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

func TestSaveDisabledProviders_RejectsNonEcosystemProvider(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := a.SaveDisabledProviders(context.Background(), []string{provider.EcosystemNode}); err != nil {
		t.Fatalf("SaveDisabledProviders(node): %v", err)
	}

	err := a.SaveDisabledProviders(context.Background(), []string{"brew"})
	if err == nil {
		t.Fatal("SaveDisabledProviders(brew) succeeded, want validation error")
	}

	cfg, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if got := cfg.HostSettings[short].DisabledProviders; len(got) != 1 || got[0] != provider.EcosystemNode {
		t.Fatalf("disabled providers = %v after invalid save, want [node]", got)
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

// ─── Effective managers ──────────────────────────────────────────────────────

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

func TestSetSetting_PersistsParsedValues(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)

	tests := []struct {
		key   string
		value string
	}{
		{key: "auto-import", value: "true"},
		{key: "node.manager", value: "pnpm"},
		{key: "python.manager", value: "uv"},
		{key: "system.priority", value: "brew,apt"},
		{key: "dots_repo", value: "$HOME/dotfiles"},
		{key: "dots_git.auto_commit", value: "true"},
		{key: "dots_git.auto_push", value: "true"},
	}
	for _, tt := range tests {
		if _, err := a.SetSetting(context.Background(), tt.key, tt.value); err != nil {
			t.Fatalf("SetSetting(%s): %v", tt.key, err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	settings := cfg.Settings
	if !settings.AutoImport || !settings.DotsGit.AutoCommit || !settings.DotsGit.AutoPush {
		t.Fatalf("global settings = %+v, want enabled auto import and dots git flags", settings)
	}
	hostSettings := cfg.HostSettings["testhost"]
	if got := hostSettings.EcosystemManager(provider.EcosystemNode); got != "pnpm" {
		t.Fatalf("node manager = %q, want pnpm", got)
	}
	if got := hostSettings.EcosystemManager(provider.EcosystemPython); got != "uv" {
		t.Fatalf("python manager = %q, want uv", got)
	}
	if got := hostSettings.EcosystemPriority(provider.EcosystemSystem); len(got) != 2 || got[0] != "brew" || got[1] != "apt" {
		t.Fatalf("system priority = %v, want [brew apt]", got)
	}
	if got := hostSettings.DotsRepo; got != "~/dotfiles" {
		t.Fatalf("dots repo = %q, want ~/dotfiles", got)
	}
}

func TestSetSetting_RejectsInvalidValues(t *testing.T) {
	a, _ := newImportApp(t)
	tests := []struct {
		key   string
		value string
		want  string
	}{
		{key: "auto_import", value: "yes please", want: "auto_import must be true or false"},
		{key: "node.manager", value: "uv", want: `"uv" is not a manager for ecosystem "node"`},
		{key: "system.priority", value: "system", want: `"system" is not a concrete provider for ecosystem "system"`},
		{key: "missing", value: "true", want: `unknown setting "missing"`},
	}
	for _, tt := range tests {
		_, err := a.SetSetting(context.Background(), tt.key, tt.value)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("SetSetting(%s) err = %v, want %q", tt.key, err, tt.want)
		}
	}
}

func TestEnableDisableProvider_ValidatesAndPersists(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)

	disabled, err := a.DisableProvider(context.Background(), provider.EcosystemNode)
	if err != nil {
		t.Fatalf("DisableProvider: %v", err)
	}
	if len(disabled) != 1 || disabled[0] != provider.EcosystemNode {
		t.Fatalf("disabled = %v, want [node]", disabled)
	}
	enabled, err := a.EnableProvider(context.Background(), provider.EcosystemNode)
	if err != nil {
		t.Fatalf("EnableProvider: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("enabled list = %v, want none disabled", enabled)
	}
	// Concrete providers are disablable under the provider-priority model.
	concreteDisabled, err := a.DisableProvider(context.Background(), "brew")
	if err != nil {
		t.Fatalf("DisableProvider(brew): %v", err)
	}
	if !slices.Contains(concreteDisabled, "brew") {
		t.Fatalf("disabled = %v, want brew present", concreteDisabled)
	}
	if _, err := a.DisableProvider(context.Background(), "bogus-pm"); err == nil || !strings.Contains(err.Error(), "is not a known provider") {
		t.Fatalf("DisableProvider(bogus-pm) err = %v, want unknown-provider validation", err)
	}
	if _, err := a.EnableProvider(context.Background(), "brew"); err != nil {
		t.Fatalf("EnableProvider(brew): %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.HostSettings["testhost"].DisabledProviders; got == nil || len(got) != 0 {
		t.Fatalf("disabled providers = %v, want explicit empty list", got)
	}
}

func TestEffectiveManagers_UsesActiveNVMBin(t *testing.T) {
	home := t.TempDir()
	activeBin := filepath.Join(home, ".nvm", "versions", "node", "v24.15.0", "bin")
	if err := os.MkdirAll(activeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeBin, "npm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("NVM_BIN", activeBin)
	t.Setenv("PATH", "/usr/bin:/bin")

	a, _ := newImportApp(t)
	_, nodeBin := a.EffectiveManagers()
	if nodeBin != "npm" {
		t.Fatalf("EffectiveManagers node = %q, want npm from active NVM_BIN", nodeBin)
	}
	_, nodeBins := a.AllAvailableManagers()
	if len(nodeBins) != 1 || nodeBins[0] != "npm" {
		t.Fatalf("AllAvailableManagers node = %v, want [npm] from active NVM_BIN", nodeBins)
	}
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
