package app_test

import (
	"context"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestStartupSnapshotBuildsConfigDerivedState(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	system := &stubProvider{name: "system", available: true}
	a, cfgPath := newImportApp(t, system, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
			"fd":      {Provider: "system", InstallWith: "brew", Ignore: true},
		},
		Hosts: map[string][]string{host: {"dev"}},
		Ignore: config.GlobalIgnore{
			Tools: []string{"fd"},
		},
		Groups: []*config.GroupConfig{
			{Name: host, Special: "host"},
			{
				Name:  "dev",
				Taps:  []string{"homebrew/cask"},
				Tools: groupTools("ripgrep"),
				Dots:  []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	snapshot, err := a.StartupSnapshot(context.Background())
	if err != nil {
		t.Fatalf("StartupSnapshot: %v", err)
	}
	if !snapshot.Settings.AutoImport {
		t.Fatal("snapshot settings did not include config settings")
	}
	if !slices.Contains(snapshot.Taps, "homebrew/cask") {
		t.Fatalf("snapshot taps = %v, want homebrew/cask", snapshot.Taps)
	}
	if snapshot.HostInfo == nil || snapshot.HostInfo.Active != host {
		t.Fatalf("snapshot active host = %#v, want %q", snapshot.HostInfo, host)
	}
	if got := snapshot.HostInfo.Hosts[host].Ignore; !slices.Contains(got, "fd") {
		t.Fatalf("snapshot active host ignore = %v, want fd", got)
	}
	memberships := snapshot.ToolMemberships["ripgrep\x00system"]
	if !slices.Contains(memberships, "dev") {
		t.Fatalf("tool memberships = %v, want dev", memberships)
	}
	if got := snapshot.DotMemberships["nvim"]; !slices.Equal(got, []string{"dev"}) {
		t.Fatalf("dot memberships = %v, want [dev]", got)
	}
	if !snapshot.ToolIgnores["fd"] {
		t.Fatalf("tool ignores = %v, want fd", snapshot.ToolIgnores)
	}
	if got := snapshot.IgnoreLabels["fd"]; got != "tool" {
		t.Fatalf("ignore labels = %v, want fd=tool", snapshot.IgnoreLabels)
	}
	if !snapshot.GroupIgnores["fd"]["global"] {
		t.Fatalf("group ignores = %v, want fd global", snapshot.GroupIgnores)
	}
	if got := snapshot.ToolProviderPins["ripgrep"]; got != "brew" {
		t.Fatalf("tool provider pins = %v, want ripgrep=brew", snapshot.ToolProviderPins)
	}
}

func TestCreateEmptyConfigStartupStateCreatesConfigAndReturnsSetupState(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, _ := newImportApp(t, brew)
	if a.HasConfig() {
		t.Fatal("expected no config before startup setup")
	}

	state, err := a.CreateEmptyConfigStartupState(context.Background())
	if err != nil {
		t.Fatalf("CreateEmptyConfigStartupState: %v", err)
	}
	if !a.HasConfig() {
		t.Fatal("CreateEmptyConfigStartupState did not create settings.json")
	}
	if state.HostInfo == nil || state.HostInfo.Active != "" {
		t.Fatalf("startup host = %#v, want no active host", state.HostInfo)
	}
	if len(state.SetupProviders) == 0 {
		t.Fatal("setup providers must be populated from app startup state")
	}
	if len(state.EcosystemProviderNames) == 0 {
		t.Fatal("ecosystem provider names must be populated from app startup state")
	}
}

func TestToolScopeStateBuildsIgnoredToolsAndProviderPins(t *testing.T) {
	a, cfgPath := newImportApp(t)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd": {
				Provider: "system",
				Ignore:   true,
			},
			"ripgrep": {
				Provider:    "system",
				InstallWith: "brew",
				Fallback: &config.FallbackSpec{
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: config.FallbackStatusUnresolved,
				},
				Hosts: map[string]config.ToolInstallSpec{
					host: {Provider: "system", InstallWith: "apt"},
				},
			},
		},
		Hosts: map[string][]string{host: {}},
		Ignore: config.GlobalIgnore{
			Tools: []string{"fd"},
		},
		Groups: []*config.GroupConfig{{Name: host, Special: "host"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	state, err := a.ToolScopeState(context.Background())
	if err != nil {
		t.Fatalf("ToolScopeState: %v", err)
	}
	if !slices.Contains(state.GlobalIgnoredTools, "fd") {
		t.Fatalf("global ignored tools = %v, want fd", state.GlobalIgnoredTools)
	}
	if !slices.Contains(state.HostIgnoredTools, "fd") {
		t.Fatalf("host ignored tools = %v, want fd", state.HostIgnoredTools)
	}
	if !state.ToolIgnores["fd"] {
		t.Fatalf("tool ignores = %v, want fd", state.ToolIgnores)
	}
	if got := state.ToolProviderPins["ripgrep"]; got != "apt" {
		t.Fatalf("tool provider pins = %v, want ripgrep=apt", state.ToolProviderPins)
	}
	if got := state.ToolFallbacks["ripgrep"]; got.Source.Owner != "BurntSushi" || got.Source.Repo != "ripgrep" {
		t.Fatalf("tool fallbacks = %+v, want ripgrep BurntSushi/ripgrep", state.ToolFallbacks)
	}
}

func TestToolScopeDisplayStateBuildsTUIDerivedScopeMaps(t *testing.T) {
	a, cfgPath := newImportApp(t)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd": {
				Provider: "system",
				Ignore:   true,
			},
			"jq": {
				Provider: "system",
			},
			"ripgrep": {
				Provider:    "system",
				InstallWith: "brew",
				Hosts: map[string]config.ToolInstallSpec{
					host: {Provider: "system", InstallWith: "apt"},
				},
			},
		},
		Hosts: map[string][]string{host: {}},
		Ignore: config.GlobalIgnore{
			Tools: []string{"fd", "jq"},
		},
		Groups: []*config.GroupConfig{{Name: host, Special: "host"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	state, err := a.ToolScopeDisplayState(context.Background())
	if err != nil {
		t.Fatalf("ToolScopeDisplayState: %v", err)
	}
	if got := state.IgnoreLabels["fd"]; got != "tool" {
		t.Fatalf("ignore labels = %v, want fd=tool", state.IgnoreLabels)
	}
	if got := state.IgnoreLabels["jq"]; got != "global" {
		t.Fatalf("ignore labels = %v, want jq=global", state.IgnoreLabels)
	}
	if !state.ToolIgnores["fd"] || state.ToolIgnores["jq"] {
		t.Fatalf("tool ignores = %v, want only fd", state.ToolIgnores)
	}
	if !state.GroupIgnores["fd"]["global"] || !state.GroupIgnores["jq"]["global"] {
		t.Fatalf("group ignores = %v, want fd/jq global entries", state.GroupIgnores)
	}
	if got := state.ToolProviderPins["ripgrep"]; got != "apt" {
		t.Fatalf("tool provider pins = %v, want ripgrep=apt", state.ToolProviderPins)
	}
}

func TestToolScopeDisplayStateWithFallbackUsesFallbackOnlyWhenLoadedHostIgnoresAreEmpty(t *testing.T) {
	a, cfgPath := newImportApp(t)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{host: {}},
		Groups: []*config.GroupConfig{{Name: host, Special: "host"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	state, err := a.ToolScopeDisplayStateWithFallback(context.Background(), []string{"host-only"})
	if err != nil {
		t.Fatalf("ToolScopeDisplayStateWithFallback: %v", err)
	}
	if got := state.IgnoreLabels["host-only"]; got != "global" {
		t.Fatalf("ignore labels = %v, want host-only fallback", state.IgnoreLabels)
	}

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"current-host": {Provider: "system"},
		},
		Hosts: map[string][]string{host: {}},
		Ignore: config.GlobalIgnore{
			Tools: []string{"current-host"},
		},
		Groups: []*config.GroupConfig{{Name: host, Special: "host"}},
	}); err != nil {
		t.Fatalf("save config with current host ignore: %v", err)
	}

	state, err = a.ToolScopeDisplayStateWithFallback(context.Background(), []string{"stale-host"})
	if err != nil {
		t.Fatalf("ToolScopeDisplayStateWithFallback with current host ignore: %v", err)
	}
	if state.IgnoreLabels["stale-host"] != "" {
		t.Fatalf("ignore labels = %v, did not expect stale fallback", state.IgnoreLabels)
	}
	if got := state.IgnoreLabels["current-host"]; got != "global" {
		t.Fatalf("ignore labels = %v, want current-host", state.IgnoreLabels)
	}
}

func TestToolGroupStateBuildsHostFilteredLabelsAndGroupNames(t *testing.T) {
	a, cfgPath := newImportApp(t)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
			"fd":      {Provider: "system"},
		},
		Hosts: map[string][]string{host: {"work"}},
		Groups: []*config.GroupConfig{
			{Name: host, Special: "host"},
			{Name: "inactive", Tools: groupTools("fd")},
			{Name: "apps"},
			{Name: "work", Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	state, err := a.ToolGroupState(context.Background())
	if err != nil {
		t.Fatalf("ToolGroupState: %v", err)
	}
	if !slices.Equal(state.GroupNames, []string{"apps", "inactive", "work"}) {
		t.Fatalf("group names = %v, want apps/inactive/work", state.GroupNames)
	}
	if got := state.ToolGroups["ripgrep\x00system"]; got != "work" {
		t.Fatalf("tool groups = %v, want ripgrep=work", state.ToolGroups)
	}
	if got := state.ToolGroups["fd\x00system"]; got != "" {
		t.Fatalf("tool groups = %v, want fd hidden from active host labels", state.ToolGroups)
	}
	if got := state.ToolMemberships["fd\x00system"]; !slices.Contains(got, "inactive") {
		t.Fatalf("tool memberships = %v, want inactive", got)
	}
	if state.HostInfo == nil || state.HostInfo.Active != host {
		t.Fatalf("host info = %#v, want active host %q", state.HostInfo, host)
	}
}
