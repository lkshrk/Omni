package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestUnmanagedSkillPackages_NormalizesEquivalentSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"managed-skill": {Source: "https://github.com/o/managed.git"},
		},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "o/managed"}},
	})

	rows, err := a.UnmanagedSkillPackages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("equivalent source offered as unmanaged: %+v", rows)
	}
}

func TestUnmanagedSkillPackages_PopulatesPerAgentStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	stubBinariesOnPath(t, "claude", "cursor")
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
	if status["claude-code"] != SkillStatusInstalled {
		t.Error("claude-code should be installed on disk")
	}
	if status["cursor"] == SkillStatusInstalled {
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
	t.Parallel()
	a := newSkillsGateTestApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
	_, err := a.AdoptSkillPackage("o/unmanaged")
	if err == nil || err.Error() != "skills are disabled for this host" {
		t.Fatalf("err = %v, want skills-disabled error", err)
	}
}

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

func TestRestoreSkills_LegacyCollisionCarriesImportHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))

	source := filepath.Join(t.TempDir(), "legacy-source")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")
	legacy := filepath.Join(home, ".agents", "skills", "demo")
	writeAppSkill(t, legacy, "demo")
	if err := os.WriteFile(filepath.Join(legacy, "EXTRA.md"), []byte("diverged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills:  map[string]config.SkillLockEntry{"demo": {Source: source}},
	})

	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: source, Agents: []string{"cline"}}},
	})
	res, _, err := a.RestoreSkills(context.Background(), RestoreSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("failed = %+v, want the contested entry degraded to drift", res.Failed)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "cline") {
		t.Fatalf("drift = %v, want the contested target named", res.Drift)
	}
	hinted := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "omni agents skills import") {
			hinted = true
		}
	}
	if !hinted {
		t.Fatalf("warnings = %v, want an import hint", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(legacy, "EXTRA.md")); err != nil {
		t.Errorf("legacy directory must be left untouched: %v", err)
	}
}
