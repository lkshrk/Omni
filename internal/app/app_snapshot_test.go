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
	if snapshot.ProviderToolCounts["brew"] != 1 {
		t.Fatalf("provider tool counts = %v, want brew=1", snapshot.ProviderToolCounts)
	}
	if !slices.Contains(snapshot.ConfiguredProviders, "brew") {
		t.Fatalf("configured providers = %v, want brew", snapshot.ConfiguredProviders)
	}
	if !snapshot.ToolIgnores["fd"] {
		t.Fatalf("tool ignores = %v, want fd", snapshot.ToolIgnores)
	}
	if got := snapshot.ToolProviderPins["ripgrep"]; got != "brew" {
		t.Fatalf("tool provider pins = %v, want ripgrep=brew", snapshot.ToolProviderPins)
	}
}
