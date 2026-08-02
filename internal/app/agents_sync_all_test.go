package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type unavailableMcpAdapter struct{ id string }

func (s *unavailableMcpAdapter) ID() string      { return s.id }
func (s *unavailableMcpAdapter) Available() bool { return false }
func (s *unavailableMcpAdapter) List(context.Context) ([]InstalledMcpServer, error) {
	return nil, nil
}
func (s *unavailableMcpAdapter) Add(context.Context, config.McpServer) error { return nil }
func (s *unavailableMcpAdapter) Remove(context.Context, string) error        { return nil }

type unavailablePluginAdapter struct{ id string }

func (s *unavailablePluginAdapter) ID() string      { return s.id }
func (s *unavailablePluginAdapter) Available() bool { return false }
func (s *unavailablePluginAdapter) ListPlugins(context.Context) ([]InstalledPlugin, error) {
	return nil, nil
}
func (s *unavailablePluginAdapter) InstallPlugin(context.Context, config.Plugin) error { return nil }
func (s *unavailablePluginAdapter) RemovePlugin(context.Context, config.Plugin) error  { return nil }
func (s *unavailablePluginAdapter) UpdatePlugin(context.Context, string, string) error { return nil }
func (s *unavailablePluginAdapter) ListMarketplaces(context.Context) ([]InstalledMarketplace, error) {
	return nil, nil
}
func (s *unavailablePluginAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *unavailablePluginAdapter) UpdateMarketplaces(context.Context) error { return nil }

type secondListFailingPluginAdapter struct {
	shadowTestPluginAdapter
	calls int
}

func (s *secondListFailingPluginAdapter) ListPlugins(context.Context) ([]InstalledPlugin, error) {
	s.calls++
	if s.calls == 2 {
		return nil, errors.New("transient plugin list failure")
	}
	return s.listedPlugins, nil
}

func legacySkillFixture(t *testing.T, home, name string) {
	t.Helper()
	writeAppSkill(t, filepath.Join(home, ".agents", "skills", name), name)
	writeAppSkill(t, filepath.Join(home, ".claude", "skills", name), name)
}

func TestImportSkills_SkipsPluginShadowedLockPackages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	stubBinariesOnPath(t, "claude")
	legacySkillFixture(t, home, "some-skill")
	legacySkillFixture(t, home, "other-skill")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"some-skill":  {Source: "owner/academic-research-skills"},
			"other-skill": {Source: "owner/other-skills"},
		},
	})
	pluginStub := &shadowTestPluginAdapter{
		id:            "claude-code",
		listedPlugins: []InstalledPlugin{{Name: "academic-research-skills", Marketplace: "some-marketplace"}},
	}
	a := newSkillsTestApp(t, config.AgentsConfig{}, WithPluginAdapters([]PluginAdapter{pluginStub}))
	ctx := context.Background()

	unmanaged, err := a.UnmanagedSkillPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.ImportSkills(ctx, ImportSkillsOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Added) != len(unmanaged) {
		t.Fatalf("dry-run Added = %v, want the %d package(s) UnmanagedSkillPackages offers", preview.Added, len(unmanaged))
	}
	if !hasSubstring(preview.Warnings, "academic-research-skills") {
		t.Fatalf("dry-run warnings = %v, want one naming the owning plugin", preview.Warnings)
	}

	diff, err := a.ImportSkills(ctx, ImportSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if hasSubstring(diff.Added, "owner/academic-research-skills") {
		t.Errorf("Added = %v, want the plugin-provided package skipped", diff.Added)
	}
	if !hasSubstring(diff.Added, "owner/other-skills") {
		t.Errorf("Added = %v, want the unshadowed package imported", diff.Added)
	}
	if !hasSubstring(diff.Warnings, `plugin "academic-research-skills" on claude-code`) {
		t.Errorf("warnings = %v, want one naming the owning plugin and agent", diff.Warnings)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range cfg.Agents.Packages {
		if pkg.Source == "owner/academic-research-skills" {
			t.Errorf("manifest = %+v, want the plugin-provided package left out", cfg.Agents.Packages)
		}
	}
	info, err := os.Lstat(filepath.Join(home, ".claude", "skills", "some-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("the plugin-provided directory must not be adopted into the package store")
	}
}

func TestRestoreSkills_ReportsDriftPerAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "restore-drift-host")
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "drift-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code"}}
	a := newSkillsTestApp(t, config.AgentsConfig{Packages: []config.SkillPackage{pkg}})
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := service.Install(ctx, pkg, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	writeAppSkill(t, link, "demo")
	if err := os.WriteFile(filepath.Join(link, "EXTRA.md"), []byte("another tool's copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _, err := a.RestoreSkills(ctx, RestoreSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("Failed = %+v, want the contested target degraded to drift", res.Failed)
	}
	if len(res.Installed) != 1 || res.Installed[0] != source {
		t.Fatalf("Installed = %v, want the package installed where it could be", res.Installed)
	}
	if len(res.Drift) != 1 {
		t.Fatalf("Drift = %v, want one line for the drifted target", res.Drift)
	}
	if !strings.Contains(res.Drift[0], source) || !strings.Contains(res.Drift[0], "claude-code") {
		t.Errorf("Drift[0] = %q, want it to name the package and the agent", res.Drift[0])
	}
	if _, err := os.Stat(filepath.Join(link, "EXTRA.md")); err != nil {
		t.Errorf("the foreign copy must be left untouched: %v", err)
	}
}

func TestRestoreSkills_DriftedTargetDoesNotBlockHealthyTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "restore-partial-drift-host")
	stubBinariesOnPath(t, "claude", "codex")
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(t.TempDir(), "drift-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code", "codex"}}
	a := newSkillsTestApp(t, config.AgentsConfig{Packages: []config.SkillPackage{pkg}})
	ctx := context.Background()

	foreign := filepath.Join(home, ".claude", "skills", "demo")
	writeAppSkill(t, foreign, "demo")
	if err := os.WriteFile(filepath.Join(foreign, "EXTRA.md"), []byte("another tool's copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, _, err := a.RestoreSkills(ctx, RestoreSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 || len(res.Installed) != 1 {
		t.Fatalf("installed = %v failed = %+v, want the package installed with no failure", res.Installed, res.Failed)
	}
	codexLink := filepath.Join(home, ".codex", "skills", "demo")
	if info, err := os.Lstat(codexLink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("codex entry = %v (%v), want omni's managed link", info, err)
	}
	if info, err := os.Lstat(foreign); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("claude-code entry = %v (%v), want the foreign directory untouched", info, err)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "claude-code") {
		t.Fatalf("Drift = %v, want exactly the contested target reported", res.Drift)
	}
}

func TestAgentsSyncAll_ClaimPhaseIsOptIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	stubBinariesOnPath(t, "claude")
	legacySkillFixture(t, home, "other-skill")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills:  map[string]config.SkillLockEntry{"other-skill": {Source: "owner/other-skills"}},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{})
	ctx := context.Background()

	converge, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(converge.Imported.Added) != 0 {
		t.Fatalf("Imported.Added = %v, want the claim phase skipped without ImportUnmanaged", converge.Imported.Added)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 0 {
		t.Fatalf("manifest = %+v, want it untouched by a converge-only run", cfg.Agents.Packages)
	}

	claimed, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{ImportUnmanaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstring(claimed.Imported.Added, "owner/other-skills") {
		t.Fatalf("Imported.Added = %v, want the unmanaged package claimed", claimed.Imported.Added)
	}
	cfg, err = a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "owner/other-skills" {
		t.Fatalf("manifest = %+v, want the claimed package tracked", cfg.Agents.Packages)
	}
}

func TestAgentsSyncAll_ProgressFollowsDependencyOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newSkillsTestApp(t, config.AgentsConfig{})
	var progress []string

	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{
		ImportUnmanaged: true,
		DryRun:          true,
		Progress:        func(text string) { progress = append(progress, text) },
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"would import unmanaged plugins…",
		"would restore plugins…",
		"would import unmanaged skills…",
		"would restore skills…",
		"would import unmanaged mcp servers…",
		"would restore mcp servers…",
	}
	if len(progress) != len(want) {
		t.Fatalf("progress = %v, want %v", progress, want)
	}
	for i := range want {
		if progress[i] != want[i] {
			t.Fatalf("progress = %v, want %v", progress, want)
		}
	}
}

func TestAgentsSyncAll_FirstDryRunProjectsPluginBeforeSkillImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	stubBinariesOnPath(t, "claude")
	legacySkillFixture(t, home, "some-skill")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"some-skill": {Source: "owner/academic-research-skills"},
		},
	})
	pluginStub := &shadowTestPluginAdapter{id: "claude-code"}
	a := newSkillsTestApp(t, config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "some-marketplace", Source: "owner/marketplace"}},
		Plugins: []config.Plugin{{
			Name: "academic-research-skills", Marketplace: "some-marketplace", Agents: []string{"claude-code"},
		}},
	}, WithPluginAdapters([]PluginAdapter{pluginStub}))

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{ImportUnmanaged: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstring(res.Plugins.WouldInstall, "claude-code/academic-research-skills") {
		t.Fatalf("Plugins.WouldInstall = %v, want first-run plugin projection", res.Plugins.WouldInstall)
	}
	if hasSubstring(res.Imported.Added, "owner/academic-research-skills") {
		t.Fatalf("Imported.Added = %v, want projected plugin-provided skill skipped", res.Imported.Added)
	}
	if !hasSubstring(res.Imported.Warnings, "academic-research-skills") {
		t.Fatalf("Imported.Warnings = %v, want projected plugin shadow warning", res.Imported.Warnings)
	}
}

func TestAgentsSyncAll_DryRunProjectsPluginsAcrossMcpPhases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pluginStub := &shadowTestPluginAdapter{id: "claude-code"}
	mcpStub := &listingMcpAdapter{id: "claude-code", listed: []InstalledMcpServer{{
		Name: "adopt-shadow", Transport: "http", URL: "https://example.com/mcp", HeadersKnown: true,
	}}}
	a := newSkillsTestApp(t, config.AgentsConfig{
		Marketplaces: []config.Marketplace{{Name: "market", Source: "owner/market"}},
		Plugins: []config.Plugin{
			{Name: "adopt-shadow", Marketplace: "market", Agents: []string{"claude-code"}},
			{Name: "restore-shadow", Marketplace: "market", Agents: []string{"claude-code"}},
		},
		McpServers: []config.McpServer{{
			Name: "restore-shadow", Transport: "http", URL: "https://example.com/restore", Agents: []string{"claude-code"},
		}},
	}, WithPluginAdapters([]PluginAdapter{pluginStub}), WithMcpAdapters([]McpAdapter{mcpStub}))

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{ImportUnmanaged: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.McpAdopted.WouldAdopt) != 0 {
		t.Fatalf("McpAdopted.WouldAdopt = %v, want projected plugin-provided server omitted", res.McpAdopted.WouldAdopt)
	}
	if len(res.Mcp.WouldInstall) != 0 {
		t.Fatalf("Mcp.WouldInstall = %v, want no duplicate planned plugin server", res.Mcp.WouldInstall)
	}
	if !hasSubstring(res.Mcp.ShadowedByPlugin, "claude-code/restore-shadow") {
		t.Fatalf("Mcp.ShadowedByPlugin = %v, want projected plugin shadow", res.Mcp.ShadowedByPlugin)
	}
}

func TestAgentsSyncAll_ProjectionKeepsKnownPluginsAfterRestoreListFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pluginStub := &secondListFailingPluginAdapter{shadowTestPluginAdapter: shadowTestPluginAdapter{
		id: "claude-code", listedPlugins: []InstalledPlugin{{Name: "shadow-plugin"}},
	}}
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "owner/shadow-plugin", Agents: []string{"claude-code"}}},
	}, WithPluginAdapters([]PluginAdapter{pluginStub}))

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstring(res.Plugins.Warnings, "transient plugin list failure") {
		t.Fatalf("Plugins.Warnings = %v, want restore list failure preserved", res.Plugins.Warnings)
	}
	if !hasSubstring(res.Skills.ShadowedByPlugin, "owner/shadow-plugin") {
		t.Fatalf("Skills.ShadowedByPlugin = %v, want known installed plugin retained in projection", res.Skills.ShadowedByPlugin)
	}
}

func TestAgentsSyncAll_ContinuesPastFeatureFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "sync-all-agents-host")
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "collide-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")
	collision := filepath.Join(home, ".claude", "skills", "demo")
	writeAppSkill(t, collision, "demo")
	if err := os.WriteFile(filepath.Join(collision, "EXTRA.md"), []byte("diverged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(t.TempDir(), "absent-skills")

	a := newSkillsTestApp(t,
		config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: absent, Agents: []string{"claude-code"}},
			{Source: source, Agents: []string{"claude-code"}},
		}},
		WithMcpAdapters([]McpAdapter{&unavailableMcpAdapter{id: "claude-code"}}),
		WithPluginAdapters([]PluginAdapter{&unavailablePluginAdapter{id: "claude-code"}}),
	)

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatalf("per-feature failures must stay in the result, got %v", err)
	}
	if !hasFeatureError(res.Errors, AgentsFeatureSkills) {
		t.Fatalf("Errors = %+v, want the unreachable source reported", res.Errors)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "claude-code") {
		t.Errorf("Drift = %v, want the drifted target reported", res.Drift)
	}
	if !hasSubstring(res.Warnings, AgentsFeatureMcp+": agent claude-code not available") {
		t.Errorf("Warnings = %v, want the mcp phase to have run after the skills failure", res.Warnings)
	}
	if !hasSubstring(res.Warnings, AgentsFeaturePlugins+": agent claude-code not available") {
		t.Errorf("Warnings = %v, want the plugins phase to have run after the skills failure", res.Warnings)
	}
}

func TestAgentsSyncAll_DisabledFeaturesWarnInsteadOfFailing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newSkillsGateTestApp(t, config.Settings{
		SkillsDisabled:  config.BoolPtr(true),
		McpDisabled:     config.BoolPtr(true),
		PluginsDisabled: config.BoolPtr(true),
	})

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{ImportUnmanaged: true})
	if err != nil {
		t.Fatalf("disabled features must warn, not fail: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("Errors = %+v, want none when features are merely disabled", res.Errors)
	}
	if len(res.Imported.Added) != 0 {
		t.Errorf("Imported.Added = %v, want the claim phase skipped while skills are disabled", res.Imported.Added)
	}
	for _, want := range []string{"skills are disabled", "mcp servers are disabled", "plugins are disabled"} {
		if !hasSubstring(res.Warnings, want) {
			t.Errorf("Warnings = %v, want one containing %q", res.Warnings, want)
		}
	}
}

func TestAgentsSyncAll_MasterSwitchStillHardErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := newSkillsGateTestApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err == nil {
		t.Fatal("AgentsSyncAll must fail while agents_disabled is set")
	}
}

func hasSubstring(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func hasFeatureError(errs []AgentsFeatureError, feature string) bool {
	for _, e := range errs {
		if e.Feature == feature {
			return true
		}
	}
	return false
}
