package app

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillRunnerFromManager(t *testing.T) {
	if got := skillRunner("bun"); got != "bunx" {
		t.Fatalf("bun -> %s, want bunx", got)
	}
	for _, m := range []string{"npm", "pnpm", ""} {
		if got := skillRunner(m); got != "npx" {
			t.Fatalf("%q -> %s, want npx", m, got)
		}
	}
}

func TestRestoreSkillsAggregatesResults(t *testing.T) {
	pkgs := []resolvedPackage{
		{SkillPackage: config.SkillPackage{Source: "o/r", Agents: []string{"claude-code"}}},
		{SkillPackage: config.SkillPackage{Source: "o/r2", Agents: []string{"claude-code"}}},
	}
	inst := stubInstaller{fail: map[string]bool{"o/r2": true}}
	res := restoreSkills(context.Background(), pkgs, nil, inst)
	if len(res.Installed) != 1 || res.Installed[0] != "o/r" {
		t.Fatalf("installed = %v", res.Installed)
	}
	if len(res.Failed) != 1 || res.Failed[0].Name != "o/r2" {
		t.Fatalf("failed = %v", res.Failed)
	}
}

type stubInstaller struct{ fail map[string]bool }

func (s stubInstaller) Install(_ context.Context, pkg config.SkillPackage, _ []string) error {
	if s.fail[pkg.Source] {
		return fmt.Errorf("boom")
	}
	return nil
}

func TestRestoreSkillsOptionsDryRun(t *testing.T) {
	pkgs := []resolvedPackage{
		{SkillPackage: config.SkillPackage{Source: "o/r", Ref: "main", Agents: []string{"claude-code"}}},
	}
	lines := dryRunLines("npx", pkgs, nil)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	want := "npx skills add o/r#main -g -a claude-code -y"
	if lines[0] != want {
		t.Fatalf("line = %q want %q", lines[0], want)
	}
}

// TestFilterShadowedSkillPackages_SkipsPluginProvided is a root-cause
// regression test for the "restore reinstalls a plugin-provided skill as a
// duplicate" scenario: RestoreSkills must not run the skills-CLI install for
// a package a target agent's installed plugin already provides — that would
// create a user-scope duplicate of plugin-scoped content, which is harm, not
// repair (see RestoreSkillsResult.ShadowedByPlugin's doc comment).
func TestFilterShadowedSkillPackages_SkipsPluginProvided(t *testing.T) {
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

func TestImportPackagesUpsert(t *testing.T) {
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

func TestImportPackagesCarriesOnlySourceRef(t *testing.T) {
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"x": {Source: "o/x", Ref: "v2", SkillFolderHash: "abc123", InstalledAt: "2026-06-01T00:00:00Z"},
	}}
	merged, diff := importPackages(nil, lock)
	if len(diff.Added) != 1 || diff.Added[0] != "o/x" {
		t.Fatalf("added = %v, want [o/x]", diff.Added)
	}
	p := merged[0]
	if p.Source != "o/x" || p.Ref != "v2" {
		t.Fatalf("unexpected package: %+v", p)
	}
	if len(p.Agents) != 0 {
		t.Fatalf("agents should be empty, got %v", p.Agents)
	}
}

func TestSkillDriftDiff(t *testing.T) {
	before := map[string]string{"a": "h1", "b": "h2", "gone": "h9"}
	after := map[string]string{"a": "h1", "b": "h2new", "c": "h3"}
	drift := skillDrift(before, after)
	if len(drift) != 1 || drift[0] != "b" {
		t.Fatalf("want [b] only, got %v", drift)
	}
}

func TestSkillUpdateArgs(t *testing.T) {
	got := skillUpdateArgs([]string{"frontend-design", "tdd"})
	want := []string{"skills", "update", "-g", "-y", "frontend-design", "tdd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
	if got := skillUpdateArgs(nil); !reflect.DeepEqual(got, []string{"skills", "update", "-g", "-y"}) {
		t.Fatalf("empty names args = %v, want all-skills update", got)
	}
}

func TestSkillPackageRemoveArgs(t *testing.T) {
	got := skillPackageRemoveArgs([]string{"taste-skill"}, []string{"claude-code", "codex"})
	want := []string{"skills", "remove", "-g", "-a", "claude-code", "codex", "-y", "taste-skill"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
}
