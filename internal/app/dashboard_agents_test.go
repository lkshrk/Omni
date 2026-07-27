package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestDashboardAgentsSummary_Enabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	installedSource := filepath.Join(t.TempDir(), "installed")
	writeAppSkill(t, filepath.Join(installedSource, "skills", "installed-skill"), "installed-skill")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"installed-skill": {Source: installedSource, UpdatedAt: "2026-06-01T00:00:00Z"},
			"unmanaged-skill": {Source: "o/unmanaged", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{
			{Source: installedSource},
			{Source: "o/missing"},
		},
		McpServers:   []config.McpServer{{Name: "srv1", Transport: "stdio", Command: "foo"}},
		Plugins:      []config.Plugin{{Name: "plug1", Marketplace: "mp1"}},
		Marketplaces: []config.Marketplace{{Name: "mp1", Source: "o/mp1"}},
	})
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: installedSource}, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if summary.SkillPackages != 2 {
		t.Errorf("SkillPackages = %d, want 2", summary.SkillPackages)
	}
	if summary.SkillsInstalled != 1 {
		t.Errorf("SkillsInstalled = %d, want 1", summary.SkillsInstalled)
	}
	if summary.SkillsMissing != 1 {
		t.Errorf("SkillsMissing = %d, want 1", summary.SkillsMissing)
	}
	if len(summary.SkillsMissingNames) != 1 || summary.SkillsMissingNames[0] != "o/missing" {
		t.Errorf("SkillsMissingNames = %v, want [o/missing]", summary.SkillsMissingNames)
	}
	if summary.SkillsUnmanaged != 1 {
		t.Errorf("SkillsUnmanaged = %d, want 1", summary.SkillsUnmanaged)
	}
	if len(summary.SkillsUnmanagedNames) != 1 || summary.SkillsUnmanagedNames[0] != "o/unmanaged" {
		t.Errorf("SkillsUnmanagedNames = %v, want [o/unmanaged]", summary.SkillsUnmanagedNames)
	}
	if summary.McpServers != 1 {
		t.Errorf("McpServers = %d, want 1", summary.McpServers)
	}
	if summary.Plugins != 1 {
		t.Errorf("Plugins = %d, want 1", summary.Plugins)
	}
	if summary.Marketplaces != 1 {
		t.Errorf("Marketplaces = %d, want 1", summary.Marketplaces)
	}
	if !summary.AgentsEnabled {
		t.Errorf("AgentsEnabled = false, want true")
	}
	if got, want := summary.Managed(), 2+1+1; got != want {
		t.Errorf("Managed() = %d, want %d", got, want)
	}
	if got, want := summary.OutOfSync(), 1+1; got != want {
		t.Errorf("OutOfSync() = %d, want %d", got, want)
	}
}

func TestDashboardAgentsSummary_CountsResolvedPackagesOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("OMNI_HOSTNAME", "box")
	writeSkillLockFixture(t, home, config.SkillLockFile{Version: 3, Skills: map[string]config.SkillLockEntry{}})
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{
			{Source: "glob/al"},
			{Source: "work/only"},
			{Source: "home/only"},
		},
	})
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Groups = []*config.GroupConfig{
			{Name: "box", Special: "host"},
			{Name: "work", Skills: []string{"work/only"}},
			{Name: "home", Skills: []string{"home/only"}},
		}
		cfg.Hosts = map[string][]string{"box": {"work"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkillPackages != 2 {
		t.Fatalf("SkillPackages = %d, want 2 resolved packages for host", summary.SkillPackages)
	}
}

func TestDashboardAgentsSummary_MissingNamesSortedAndPopulated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{Version: 3, Skills: map[string]config.SkillLockEntry{}})
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "o/no-lock-entry"}},
	})
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if summary.SkillsMissing != 1 {
		t.Fatalf("SkillsMissing = %d, want 1", summary.SkillsMissing)
	}
	if len(summary.SkillsMissingNames) != 1 || summary.SkillsMissingNames[0] != "o/no-lock-entry" {
		t.Fatalf("SkillsMissingNames = %v, want [o/no-lock-entry]", summary.SkillsMissingNames)
	}
}

func TestDashboardAgentsSummary_ShadowedPackage_NotCountedMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	stubBinariesOnPath(t, "claude")
	writeSkillLockFixture(t, home, config.SkillLockFile{Version: 3, Skills: map[string]config.SkillLockEntry{}})

	pluginStub := &shadowTestPluginAdapter{
		id:            "claude-code",
		listedPlugins: []InstalledPlugin{{Name: "academic-research-skills", Marketplace: "some-marketplace"}},
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path, WithPluginAdapters([]PluginAdapter{pluginStub}))
	brew := &availabilityCountingProvider{name: "brew", available: true}
	if err := a.InitTestMode(context.Background(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: "owner/academic-research-skills", Agents: []string{"claude-code"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if summary.SkillsMissing != 0 {
		t.Errorf("SkillsMissing = %d, want 0 (shadowed by installed plugin)", summary.SkillsMissing)
	}
	if len(summary.SkillsMissingNames) != 0 {
		t.Errorf("SkillsMissingNames = %v, want empty", summary.SkillsMissingNames)
	}
	if summary.SkillsInstalled != 1 {
		t.Errorf("SkillsInstalled = %d, want 1", summary.SkillsInstalled)
	}
}

func TestDashboardAgentsSummary_AgentsDisabled_ZeroValueNoLockRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"installed-skill": {Source: "o/installed", UpdatedAt: "2026-06-01T00:00:00Z"},
			"unmanaged-skill": {Source: "o/unmanaged", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	a := newSkillsGateTestApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents = config.AgentsConfig{
			Packages: []config.SkillPackage{
				{Source: "o/installed"},
				{Source: "o/missing"},
			},
			McpServers:   []config.McpServer{{Name: "srv1", Transport: "stdio", Command: "foo"}},
			Plugins:      []config.Plugin{{Name: "plug1", Marketplace: "mp1"}},
			Marketplaces: []config.Marketplace{{Name: "mp1", Source: "o/mp1"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected no error even without a lockfile on disk, got %v", err)
	}

	if summary.AgentsEnabled {
		t.Fatalf("AgentsEnabled = true, want false")
	}
	if summary.SkillPackages != 0 || summary.SkillsInstalled != 0 || summary.SkillsMissing != 0 ||
		len(summary.SkillsMissingNames) != 0 || summary.SkillsUnmanaged != 0 || summary.McpServers != 0 ||
		summary.Plugins != 0 || summary.Marketplaces != 0 {
		t.Fatalf("summary = %+v, want all-zero", summary)
	}
	if summary.Managed() != 0 || summary.OutOfSync() != 0 {
		t.Fatalf("Managed()=%d OutOfSync()=%d, want 0,0", summary.Managed(), summary.OutOfSync())
	}
}
