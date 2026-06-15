package app

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillAddArgs(t *testing.T) {
	skill := config.ManifestSkill{Name: "frontend-design", Source: "vercel-labs/agent-skills", Ref: "main"}
	got := skillAddArgs(skill, []string{"claude-code", "codex"})
	want := []string{"skills", "add", "vercel-labs/agent-skills#main", "-s", "frontend-design", "-g", "-a", "claude-code", "codex", "-y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestSkillAddArgsNoRef(t *testing.T) {
	skill := config.ManifestSkill{Name: "x", Source: "owner/repo"}
	got := skillAddArgs(skill, []string{"claude-code"})
	want := []string{"skills", "add", "owner/repo", "-s", "x", "-g", "-a", "claude-code", "-y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
}

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
	skills := []config.ManifestSkill{
		{Name: "good", Source: "o/r", Agents: []string{"claude-code"}},
		{Name: "bad", Source: "o/r2", Agents: []string{"claude-code"}},
	}
	inst := stubInstaller{fail: map[string]bool{"bad": true}}
	res := restoreSkills(context.Background(), skills, nil, inst)
	if len(res.Installed) != 1 || res.Installed[0] != "good" {
		t.Fatalf("installed = %v", res.Installed)
	}
	if len(res.Failed) != 1 || res.Failed[0].Name != "bad" {
		t.Fatalf("failed = %v", res.Failed)
	}
}

type stubInstaller struct{ fail map[string]bool }

func (s stubInstaller) Install(_ context.Context, skill config.ManifestSkill, _ []string) error {
	if s.fail[skill.Name] {
		return fmt.Errorf("boom")
	}
	return nil
}

func TestRestoreSkillsOptionsDryRun(t *testing.T) {
	skills := []config.ManifestSkill{{Name: "a", Source: "o/r", Ref: "main", Agents: []string{"claude-code"}}}
	lines := dryRunLines("npx", skills, nil)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(lines))
	}
	want := "npx skills add o/r#main -s a -g -a claude-code -y"
	if lines[0] != want {
		t.Fatalf("line = %q want %q", lines[0], want)
	}
}

func TestImportSkillsUpsert(t *testing.T) {
	existing := []config.ManifestSkill{
		{Name: "keep", Source: "o/keep", Ref: "main"},
		{Name: "changed", Source: "o/changed", Ref: "old"},
	}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"keep":    {Source: "o/keep", Ref: "main"},
		"changed": {Source: "o/changed", Ref: "new"},
		"added":   {Source: "o/added", Ref: "main", SkillPath: "skills/added"},
	}}
	merged, diff := importSkills(existing, lock)
	if len(merged) != 3 {
		t.Fatalf("want 3 merged, got %d", len(merged))
	}
	if len(diff.Added) != 1 || diff.Added[0] != "added" {
		t.Fatalf("added = %v", diff.Added)
	}
	if len(diff.Updated) != 1 || diff.Updated[0] != "changed" {
		t.Fatalf("updated = %v", diff.Updated)
	}
	if len(diff.Unchanged) != 1 || diff.Unchanged[0] != "keep" {
		t.Fatalf("unchanged = %v", diff.Unchanged)
	}
}

func TestImportSkillsDropsRuntimeFields(t *testing.T) {
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"x": {Source: "o/x", SkillFolderHash: "abc123", InstalledAt: "2026-06-01T00:00:00Z"},
	}}
	merged, diff := importSkills(nil, lock)
	if len(diff.Added) != 1 || diff.Added[0] != "x" {
		t.Fatalf("added = %v", diff.Added)
	}
	s := merged[0]
	if s.Ref != "" {
		t.Fatalf("expected Ref dropped, got %q", s.Ref)
	}
	if s.Source != "o/x" || s.Name != "x" {
		t.Fatalf("unexpected manifest skill: %+v", s)
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
	skills := []config.ManifestSkill{{Name: "frontend-design"}, {Name: "tdd"}}
	got := skillUpdateArgs(skills)
	want := []string{"skills", "update", "-g", "-y", "frontend-design", "tdd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got %v\nwant %v", got, want)
	}
	if got := skillUpdateArgs(nil); !reflect.DeepEqual(got, []string{"skills", "update", "-g", "-y"}) {
		t.Fatalf("empty manifest args = %v, want all-skills update", got)
	}
}
