package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestRestoreSkills_ContinuesAfterPackageFailure(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{
		{SkillPackage: config.SkillPackage{Source: "o/r", Agents: []string{"claude-code"}}},
		{SkillPackage: config.SkillPackage{Source: "o/r2", Agents: []string{"claude-code"}}},
	}
	inst := stubInstaller{fail: map[string]bool{"o/r": true}}
	res := restoreSkills(context.Background(), pkgs, nil, inst)
	if len(res.Installed) != 1 || res.Installed[0] != "o/r2" {
		t.Fatalf("installed = %v", res.Installed)
	}
	if len(res.Failed) != 1 || res.Failed[0].Name != "o/r" {
		t.Fatalf("failed = %v", res.Failed)
	}
}

type stubInstaller struct {
	fail     map[string]bool
	warnings []string
}

func (s stubInstaller) Sync(_ context.Context, pkg config.SkillPackage, _ []string) ([]string, error) {
	if s.fail[pkg.Source] {
		return s.warnings, fmt.Errorf("boom")
	}
	return s.warnings, nil
}

func (s stubInstaller) Upgrade(ctx context.Context, pkg config.SkillPackage, agents []string) ([]string, error) {
	return s.Sync(ctx, pkg, agents)
}

func (s stubInstaller) PackageStore(string) (string, string, error) {
	return "", "", nil
}

func TestUpdateSkillsKeepsPreludeAndPerPackageWarnings(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{
		{SkillPackage: config.SkillPackage{Source: "o/r", Agents: []string{"claude-code"}}},
	}
	inst := stubInstaller{warnings: []string{"legacy install remains"}}
	out, err := updateSkills(context.Background(), pkgs, []string{"warning: plugins unavailable"}, inst)
	if err != nil {
		t.Fatal(err)
	}
	want := "warning: plugins unavailable\nwarning: legacy install remains\nupdated o/r"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

func TestRestoreSkillsOptionsDryRun(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{{
		SkillPackage: config.SkillPackage{
			Source: "o/r",
			Ref:    "main",
			Agents: []string{"claude-code"},
		},
	}}
	lines := dryRunLines(t.Context(), pkgs, nil, stubInventory{})
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	want := "install o/r at ref main all discovered skills for targets claude-code"
	if lines[0] != want {
		t.Fatalf("line = %q want %q", lines[0], want)
	}
}

func TestRestoreSkillsOptionsDryRunEmptyAgentSetIsNoOp(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{{SkillPackage: config.SkillPackage{Source: "o/r"}}}
	if lines := dryRunLines(t.Context(), pkgs, []string{}, stubInventory{}); len(lines) != 0 {
		t.Fatalf("lines = %v, want no install actions", lines)
	}
}

func TestFilterShadowedSkillPackages_SkipsPluginProvided(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{
		{SkillPackage: config.SkillPackage{Source: "owner/academic-research-skills", Agents: []string{"claude-code"}}},
		{SkillPackage: config.SkillPackage{Source: "o/normal-package", Agents: []string{"claude-code"}}},
	}
	pluginNames := map[string]map[string]bool{
		"claude-code": {"academic-research-skills": true},
	}
	keep, shadowed := filterShadowedSkillPackages(pkgs, nil, pluginNames)
	if len(keep) != 1 || keep[0].Source != "o/normal-package" {
		t.Fatalf("keep = %+v, want only o/normal-package", keep)
	}
	if len(shadowed) != 1 || shadowed[0] != "owner/academic-research-skills" {
		t.Fatalf("shadowed = %v, want [owner/academic-research-skills]", shadowed)
	}
}

func TestResolveRestoreTargets_NilUseFallsBackToDetectedAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &App{}
	cfg := &config.RootConfig{}
	pkgs := []resolvedPackage{
		{SkillPackage: config.SkillPackage{Source: "owner/academic-research-skills"}},
		{SkillPackage: config.SkillPackage{Source: "o/normal-package"}},
	}
	pluginNames := map[string]map[string]bool{
		"claude-code": {"academic-research-skills": true},
	}

	pkgs = resolveRestoreTargets(pkgs, nil, a.EnabledAgentIDs(cfg))
	keep, shadowed := filterShadowedSkillPackages(pkgs, nil, pluginNames)
	if len(shadowed) != 1 || shadowed[0] != "owner/academic-research-skills" {
		t.Fatalf("shadowed = %v, want [owner/academic-research-skills]", shadowed)
	}
	if len(keep) != 1 || keep[0].Source != "o/normal-package" {
		t.Fatalf("keep = %+v, want only o/normal-package", keep)
	}
}

func TestResolveRestoreTargets_PreservesPackageAgentsWithoutHostSelection(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{{
		SkillPackage: config.SkillPackage{Source: "o/r", Agents: []string{"claude-code"}},
	}}
	got := resolveRestoreTargets(pkgs, nil, []string{"codex"})
	if !reflect.DeepEqual(got[0].Agents, []string{"claude-code"}) {
		t.Fatalf("agents = %v, want package target [claude-code]", got[0].Agents)
	}
}

func TestResolveRestoreTargets_ExplicitEmptyHostSelectionDisablesPackages(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{{
		SkillPackage: config.SkillPackage{Source: "o/r", Agents: []string{"claude-code"}},
	}}
	got := resolveRestoreTargets(pkgs, []string{}, []string{"codex"})
	if got[0].Agents == nil || len(got[0].Agents) != 0 {
		t.Fatalf("agents = %#v, want explicit empty target set", got[0].Agents)
	}
}

func TestImportPackagesUpsert(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{
		{Source: "o/keep", Ref: "main"},
		{Source: "o/changed", Ref: "old"},
	}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"a": {Source: "o/keep", Ref: "main"},
		"b": {Source: "o/changed", Ref: "new"},
		"c": {Source: "o/added", Ref: "main"},
	}}
	merged, diff := importPackages(existing, lock)
	if len(merged) != 3 {
		t.Fatalf("want 3 merged, got %d: %+v", len(merged), merged)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "o/added" {
		t.Fatalf("added = %v, want [o/added]", diff.Added)
	}
	if len(diff.Updated) != 1 || diff.Updated[0] != "o/changed" {
		t.Fatalf("updated = %v, want [o/changed]", diff.Updated)
	}
	if len(diff.Unchanged) != 1 || diff.Unchanged[0] != "o/keep" {
		t.Fatalf("unchanged = %v, want [o/keep]", diff.Unchanged)
	}
}

func TestImportPackagesCarriesPortableFieldsOnly(t *testing.T) {
	t.Parallel()
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"x": {
			Source:          "o/x",
			Ref:             "v2",
			SkillFolderHash: "abc123",
			InstalledAt:     "2026-06-01T00:00:00Z",
		},
	}}
	merged, diff := importPackages(nil, lock)
	if len(diff.Added) != 1 || diff.Added[0] != "o/x" {
		t.Fatalf("added = %v, want [o/x]", diff.Added)
	}
	p := merged[0]
	if p.Source != "o/x" || p.Ref != "v2" || !reflect.DeepEqual(p.Skills, []string{"x"}) {
		t.Fatalf("unexpected package: %+v", p)
	}
	if len(p.Agents) != 0 {
		t.Fatalf("agents should be empty, got %v", p.Agents)
	}
}

func TestUpdateSkillsNoPackagesReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	a := newSkillsTestApp(t, config.AgentsConfig{})

	output, dryRun, err := a.UpdateSkills(context.Background(), UpdateSkillsOptions{})
	if err != nil || output != "" || dryRun != "" {
		t.Fatalf("output=%q dryRun=%q err=%v", output, dryRun, err)
	}
}

func TestUpdateSkillsContinuesAfterPackageFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	source := filepath.Join(t.TempDir(), "valid")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")
	missing := filepath.Join(t.TempDir(), "missing")
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: missing}, {Source: source}},
	})
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Settings.AgentsUse = []string{"codex"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	output, _, err := a.UpdateSkills(context.Background(), UpdateSkillsOptions{})
	if err == nil {
		t.Fatal("update should report the missing package")
	}
	if output != "updated "+source {
		t.Fatalf("output = %q", output)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".codex", "skills", "demo", "SKILL.md")); statErr != nil {
		t.Fatalf("valid package was not processed after failure: %v", statErr)
	}
}

func TestUpdateSkillsSkipsPluginShadowedPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	source := filepath.Join(t.TempDir(), "shadowed-package")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")
	plugin := &shadowTestPluginAdapter{
		id:            "codex",
		listedPlugins: []InstalledPlugin{{Name: "shadowed-package"}},
	}
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: source}},
	}, WithPluginAdapters([]PluginAdapter{plugin}))
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Settings.AgentsUse = []string{"codex"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	output, _, err := a.UpdateSkills(context.Background(), UpdateSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if output != "skipped "+source+" (provided by plugin)" {
		t.Fatalf("output = %q", output)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "demo")); !os.IsNotExist(err) {
		t.Fatalf("shadowed package was installed: %v", err)
	}
}

func TestUnconfiguredHostSkillsWarning(t *testing.T) {
	t.Parallel()
	grouped := &config.RootConfig{Groups: []*config.GroupConfig{{Name: "dev", Skills: []string{"o/r"}}}}
	if w := unconfiguredHostSkillsWarning(grouped); w == "" {
		t.Fatal("want warning for unregistered host with grouped skills")
	}
	host := currentMachineGroupName()
	registered := &config.RootConfig{
		Hosts:  map[string][]string{host: {"dev"}},
		Groups: []*config.GroupConfig{{Name: "dev", Skills: []string{"o/r"}}},
	}
	if w := unconfiguredHostSkillsWarning(registered); w != "" {
		t.Fatalf("registered host: unexpected warning %q", w)
	}
	if w := unconfiguredHostSkillsWarning(&config.RootConfig{}); w != "" {
		t.Fatalf("no grouped skills: unexpected warning %q", w)
	}
}

func TestUpdateSkillsDryRunReportsPluginShadowedPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	source := filepath.Join(t.TempDir(), "shadowed-package")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")
	plugin := &shadowTestPluginAdapter{
		id:            "codex",
		listedPlugins: []InstalledPlugin{{Name: "shadowed-package"}},
	}
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: source}},
	}, WithPluginAdapters([]PluginAdapter{plugin}))
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Settings.AgentsUse = []string{"codex"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	_, dryRun, err := a.UpdateSkills(context.Background(), UpdateSkillsOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun != "skipped "+source+" (provided by plugin)" {
		t.Fatalf("dryRun = %q, want the same skip line the real run emits", dryRun)
	}
}
