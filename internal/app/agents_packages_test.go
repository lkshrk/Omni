package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSetSkillAgents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(ctx, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: "o/r"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetSkillAgents("o/r", []string{"codex", "claude-code"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents.Packages[0].Agents; len(got) != 2 || got[0] != "codex" || got[1] != "claude-code" {
		t.Fatalf("Agents = %v, want [codex claude-code]", got)
	}
	if err := a.SetSkillAgents("o/r", nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = a.loadConfig()
	if cfg.Agents.Packages[0].Agents != nil {
		t.Errorf("empty set should clear Agents, got %v", cfg.Agents.Packages[0].Agents)
	}
	if err := a.SetSkillAgents("absent/pkg", []string{"codex"}); err == nil {
		t.Error("expected error for unknown package")
	}
}

func TestWithConfigPersistsPackagesAndGroupRefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	brew := &availabilityCountingProvider{name: "brew", available: true}
	path := filepath.Join(t.TempDir(), "settings.json")
	a := New(path)
	if err := a.InitTestMode(ctx, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{{Source: "o/r", Ref: "main"}}
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: "g", Skills: []string{"o/r"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "o/r" {
		t.Fatalf("packages not persisted: %+v", cfg.Agents.Packages)
	}
	var found bool
	for _, g := range cfg.Groups {
		if g.Name == "g" && len(g.Skills) == 1 && g.Skills[0] == "o/r" {
			found = true
		}
	}
	if !found {
		t.Fatalf("group skill ref not persisted: %+v", cfg.Groups)
	}
}

func TestSkillLockSourceIndex(t *testing.T) {
	t.Parallel()
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"foo": {Source: "o/r", UpdatedAt: "2026-06-10T00:00:00Z"},
		"bar": {Source: "o/r", UpdatedAt: "2026-06-12T00:00:00Z"},
		"baz": {Source: "x/y", UpdatedAt: "2026-05-01T00:00:00Z"},
	}}
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
	installed, updated := a.packageLockStatus(lock, "o/r")
	if !installed {
		t.Fatal("o/r should be installed")
	}
	if updated != "2026-06-12" {
		t.Errorf("updated = %q, want latest 2026-06-12", updated)
	}
	if in, _ := a.packageLockStatus(lock, "absent/pkg"); in {
		t.Error("absent package should not be installed")
	}
}

func TestPackageSkills(t *testing.T) {
	t.Parallel()
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"beta":  {Source: "o/r"},
		"alpha": {Source: "o/r"},
		"other": {Source: "x/y"},
	}}
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
	got := a.packageSkills(lock, "o/r")
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("packageSkills = %v, want [alpha beta] (sorted)", got)
	}
	if len(a.packageSkills(lock, "absent/pkg")) != 0 {
		t.Error("absent package should have no skills")
	}
}

func TestImportPackages(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{{Source: "o/r", Ref: "main"}}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"a": {Source: "o/r", Ref: "main"},
		"b": {Source: "o/r", Ref: "main"},
		"c": {Source: "new/pkg", Ref: "v1"},
	}}
	merged, diff := importPackages(existing, lock)
	if len(merged) != 2 {
		t.Fatalf("merged = %+v, want 2 packages", merged)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "new/pkg" {
		t.Errorf("added = %v, want [new/pkg]", diff.Added)
	}
	if len(diff.Unchanged) != 1 || diff.Unchanged[0] != "o/r" {
		t.Errorf("unchanged = %v, want [o/r]", diff.Unchanged)
	}
}

func TestSetSkillGroupsInConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{{Source: "o/r"}}},
		Groups: []*config.GroupConfig{
			{Name: "work", Skills: []string{"o/r"}},
			{Name: "home"},
		},
	}
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
	a.setSkillGroupsInConfig(cfg, "o/r", map[string]struct{}{"home": {}})
	work := findGroupInConfig(cfg, "work")
	home := findGroupInConfig(cfg, "home")
	if len(work.Skills) != 0 {
		t.Errorf("work should no longer reference o/r: %v", work.Skills)
	}
	if len(home.Skills) != 1 || home.Skills[0] != "o/r" {
		t.Errorf("home should reference o/r: %v", home.Skills)
	}
}

func TestResolveSkillPackages(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "glob/al"},
			{Source: "work/only"},
			{Source: "home/only"},
		}},
		Groups: []*config.GroupConfig{
			{Name: "box"},
			{Name: "work", Skills: []string{"work/only"}},
			{Name: "home", Skills: []string{"home/only"}},
		},
		Hosts: map[string][]string{"box": {"work"}},
	}
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
	got := a.resolveSkillPackages(cfg, "box")
	srcs := make([]string, len(got))
	for i, r := range got {
		srcs[i] = r.Source
	}
	want := map[string]bool{"glob/al": true, "work/only": true}
	if len(srcs) != 2 {
		t.Fatalf("resolved = %v, want glob/al + work/only", srcs)
	}
	for _, s := range srcs {
		if !want[s] {
			t.Errorf("unexpected resolved package %q", s)
		}
	}
}

func TestResolveSkillPackagesNormalizesAndMergesDuplicateSources(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "https://github.com/acme/skills.git@alpha"},
			{Source: "acme/skills@beta"},
			{Source: "acme/skills", Ref: "main"},
		}},
	}

	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
	got := a.resolveSkillPackages(cfg, "box")
	if len(got) != 1 {
		t.Fatalf("resolved = %+v, want one normalized package", got)
	}
	if got[0].Source != "acme/skills" || got[0].Ref != "main" {
		t.Fatalf("package = %+v, want normalized source and latest ref", got[0])
	}
	if got[0].Skills != nil {
		t.Fatalf("selectorless duplicate should reset to all skills, got %v", got[0].Skills)
	}
}
