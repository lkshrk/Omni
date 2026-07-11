package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func writeSkillLockFixture(t *testing.T, home string, lock config.SkillLockFile) {
	t.Helper()
	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, ".skill-lock.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// shadowTestPluginAdapter is a minimal local stub for shadow-detection tests
// in this file's package (app), which cannot see app_test's stubPluginAdapter
// (defined in the external app_test package for the plugin_ops_test.go
// suite).
type shadowTestPluginAdapter struct {
	id            string
	listedPlugins []InstalledPlugin
}

func (s *shadowTestPluginAdapter) ID() string      { return s.id }
func (s *shadowTestPluginAdapter) Available() bool { return true }
func (s *shadowTestPluginAdapter) ListPlugins(context.Context) ([]InstalledPlugin, error) {
	return s.listedPlugins, nil
}
func (s *shadowTestPluginAdapter) InstallPlugin(context.Context, config.Plugin) error { return nil }
func (s *shadowTestPluginAdapter) RemovePlugin(context.Context, config.Plugin) error  { return nil }
func (s *shadowTestPluginAdapter) UpdatePlugin(context.Context, string, string) error { return nil }
func (s *shadowTestPluginAdapter) ListMarketplaces(context.Context) ([]InstalledMarketplace, error) {
	return nil, nil
}
func (s *shadowTestPluginAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *shadowTestPluginAdapter) UpdateMarketplaces(context.Context) error { return nil }

func newSkillsTestApp(t *testing.T, agents config.AgentsConfig, opts ...func(*App)) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents = agents
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestUnmanagedSkillPackages_LockMinusManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"managed-skill":   {Source: "o/managed", UpdatedAt: "2026-06-01T00:00:00Z"},
			"unmanaged-skill": {Source: "o/unmanaged", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "o/managed"}},
	})

	rows, err := a.UnmanagedSkillPackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 unmanaged row, got %+v", rows)
	}
	if rows[0].Source != "o/unmanaged" || rows[0].Name != "o/unmanaged" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
	if rows[0].Updated != "2026-07-05" {
		t.Fatalf("Updated = %q, want 2026-07-05", rows[0].Updated)
	}
	if !rows[0].Installed {
		t.Fatalf("expected Installed=true, got %+v", rows[0])
	}
}

func TestUnmanagedSkillPackages_PopulatesPerAgentStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "unmanaged-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cursor", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"unmanaged-skill": {Source: "o/unmanaged", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{})

	rows, err := a.UnmanagedSkillPackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 unmanaged row, got %+v", rows)
	}
	status := rows[0].PerAgentStatus
	if !status["claude-code"] {
		t.Error("claude-code should be installed on disk")
	}
	if status["cursor"] {
		t.Error("cursor should not be installed on disk")
	}
}

func TestAdoptSkillPackage_PersistsAndClearsFromUnmanaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"unmanaged-skill": {Source: "o/unmanaged", Ref: "v1.2.3", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{})

	pkg, err := a.AdoptSkillPackage("o/unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Source != "o/unmanaged" || pkg.Ref != "v1.2.3" {
		t.Fatalf("unexpected adopted package: %+v", pkg)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "o/unmanaged" {
		t.Fatalf("expected package persisted to manifest, got %+v", cfg.Agents.Packages)
	}

	rows, err := a.UnmanagedSkillPackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected package to disappear from unmanaged after adopt, got %+v", rows)
	}
}

func TestAdoptSkillPackage_GatedBySkillsDisabled(t *testing.T) {
	a := newSkillsGateTestApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
	_, err := a.AdoptSkillPackage("o/unmanaged")
	if err == nil || err.Error() != "skills are disabled for this host" {
		t.Fatalf("err = %v, want skills-disabled error", err)
	}
}

// TestUnmanagedSkillPackages_SuppressedByInstalledPlugin is a root-cause
// regression test for the "academic-research-skills plugin surfaces as an
// unmanaged skill row" report: a plugin's skills land on disk under the
// agent's skills dir just like an omni-managed package would, so the lockfile
// scan picks them up as an unmanaged source. Once installedPluginNames
// reports a same-named plugin on the agent where the package is present, that
// source must be suppressed rather than offered for claim.
func TestUnmanagedSkillPackages_SuppressedByInstalledPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "some-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"some-skill": {Source: "owner/academic-research-skills", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	pluginStub := &shadowTestPluginAdapter{
		id:            "claude-code",
		listedPlugins: []InstalledPlugin{{Name: "academic-research-skills", Marketplace: "some-marketplace"}},
	}
	a := newSkillsTestApp(t, config.AgentsConfig{}, WithPluginAdapters([]PluginAdapter{pluginStub}))

	rows, err := a.UnmanagedSkillPackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected plugin-provided package suppressed from unmanaged, got %+v", rows)
	}
}

func newSkillsGateTestApp(t *testing.T, s config.Settings) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Settings = s
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a
}
