package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// files maps relative path to content and must include "settings.json".
func writeOptimizeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "settings.json")
}

func TestPlanOptimizeParentDotDuplicates(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/dots.json"],
  "groups": [{"name": "dev", "description": "dev tools",
    "dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "vim", "path": "~/.vimrc"}]}]
}`,
		"settings.d/dots.json": `{
  "groups": [{"name": "dev", "dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "zsh", "path": "~/.zshrc"}]}]
}`,
	})
	chain, err := loadIncludeChain(main)
	if err != nil {
		t.Fatal(err)
	}
	files, report, err := planOptimize(chain)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalRemoved() != 1 {
		t.Fatalf("TotalRemoved = %d, want 1: %+v", report.TotalRemoved(), report.Removals)
	}
	r := report.Removals[0]
	if r.Group != "dev" || r.Key != "dots" || len(r.Names) != 1 || r.Names[0] != "git" {
		t.Fatalf("removal = %+v", r)
	}
	if r.File != main {
		t.Fatalf("removal file = %q, want main %q (later include wins, parent copy dead)", r.File, main)
	}
	if !files[0].changed || files[1].changed {
		t.Fatalf("changed flags: main=%v fragment=%v, want true/false", files[0].changed, files[1].changed)
	}
}

func TestPlanOptimizeStringListLaterCopyRemoved(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/tools.json"],
  "groups": [{"name": "dev", "tools": ["ripgrep", "fd"]}]
}`,
		"settings.d/tools.json": `{
  "groups": [{"name": "dev", "tools": ["ripgrep", "jq"]}]
}`,
	})
	chain, err := loadIncludeChain(main)
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := planOptimize(chain)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalRemoved() != 1 {
		t.Fatalf("TotalRemoved = %d, want 1: %+v", report.TotalRemoved(), report.Removals)
	}
	r := report.Removals[0]
	// Earlier value wins for string lists; the FRAGMENT copy is the dead one.
	if r.Key != "tools" || r.Names[0] != "ripgrep" || r.File == main {
		t.Fatalf("removal = %+v (want tools/ripgrep removed from fragment)", r)
	}
}

func TestPlanOptimizeFragmentPairDots(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/a.json", "settings.d/b.json"]
}`,
		"settings.d/a.json": `{"groups": [{"name": "dev", "dots": [{"name": "git"}]}]}`,
		"settings.d/b.json": `{"groups": [{"name": "dev", "dots": [{"name": "git"}]}]}`,
	})
	chain, err := loadIncludeChain(main)
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := planOptimize(chain)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalRemoved() != 1 {
		t.Fatalf("TotalRemoved = %d: %+v", report.TotalRemoved(), report.Removals)
	}
	if base := filepath.Base(report.Removals[0].File); base != "a.json" {
		t.Fatalf("removed from %q, want a.json (earlier fragment loses for dots)", base)
	}
}

func TestPlanOptimizeGroupEmptiedByDedupeIsDropped(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/dots.json"],
  "groups": [{"name": "dev", "dots": [{"name": "git"}]}]
}`,
		"settings.d/dots.json": `{"groups": [{"name": "dev", "dots": [{"name": "git"}]}]}`,
	})
	chain, err := loadIncludeChain(main)
	if err != nil {
		t.Fatal(err)
	}
	files, report, err := planOptimize(chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(files[0].groups) != 0 {
		t.Fatalf("main groups = %d, want 0 (name-only group we emptied is dropped)", len(files[0].groups))
	}
	// One removal for the dot + one for the group shell.
	foundGroup := false
	for _, r := range report.Removals {
		if r.Key == "group" {
			foundGroup = true
		}
	}
	if !foundGroup {
		t.Fatalf("no group-shell removal recorded: %+v", report.Removals)
	}
}

func TestPlanOptimizePreexistingNameOnlyGroupKept(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/x.json"],
  "groups": [{"name": "placeholder"}]
}`,
		"settings.d/x.json": `{"groups": [{"name": "placeholder", "dots": [{"name": "git"}]}]}`,
	})
	chain, err := loadIncludeChain(main)
	if err != nil {
		t.Fatal(err)
	}
	files, report, err := planOptimize(chain)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Empty() {
		t.Fatalf("report not empty: %+v", report.Removals)
	}
	if len(files[0].groups) != 1 {
		t.Fatal("pre-existing name-only group must be kept")
	}
}

func TestPlanOptimizeNoDuplicatesEmptyReport(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json":     `{"$include": ["settings.d/a.json"], "groups": [{"name": "dev", "dots": [{"name": "vim"}]}]}`,
		"settings.d/a.json": `{"groups": [{"name": "other", "dots": [{"name": "git"}]}]}`,
	})
	chain, err := loadIncludeChain(main)
	if err != nil {
		t.Fatal(err)
	}
	files, report, err := planOptimize(chain)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Empty() || files[0].changed || files[1].changed {
		t.Fatalf("expected no-op, report=%+v", report.Removals)
	}
}

func TestOptimizeIncludeChainAppliesAndPreservesMergedConfig(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/dots.json"],
  "custom_top_level_key": {"keep": true},
  "groups": [{"name": "dev", "description": "d",
    "dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "vim", "path": "~/.vimrc"}]}]
}`,
		"settings.d/dots.json": `{
  "groups": [{"name": "dev", "dots": [{"name": "git", "path": "~/.gitconfig"}]}]
}`,
	})
	before, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.MergeNotices) == 0 {
		t.Fatal("fixture must produce a merge notice")
	}
	beforeNorm, err := normalizeForCompare(before)
	if err != nil {
		t.Fatal(err)
	}

	report, err := OptimizeIncludeChain(main, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalRemoved() != 1 {
		t.Fatalf("TotalRemoved = %d: %+v", report.TotalRemoved(), report.Removals)
	}

	after, err := Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.MergeNotices) != 0 {
		t.Fatalf("merge notices survived: %v", after.MergeNotices)
	}
	afterNorm, err := normalizeForCompare(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeNorm) != string(afterNorm) {
		t.Fatalf("merged config changed:\nbefore: %s\nafter:  %s", beforeNorm, afterNorm)
	}
	data, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "custom_top_level_key") {
		t.Fatal("foreign top-level key lost")
	}
}

func TestOptimizeIncludeChainDryRunWritesNothing(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json":     `{"$include": ["settings.d/a.json"], "groups": [{"name": "dev", "dots": [{"name": "git"}]}]}`,
		"settings.d/a.json": `{"groups": [{"name": "dev", "dots": [{"name": "git"}]}]}`,
	})
	origMain, _ := os.ReadFile(main)
	origFrag, _ := os.ReadFile(filepath.Join(filepath.Dir(main), "settings.d", "a.json"))

	report, err := OptimizeIncludeChain(main, true)
	if err != nil {
		t.Fatal(err)
	}
	if report.Empty() {
		t.Fatal("dry-run report empty, want planned removals")
	}
	nowMain, _ := os.ReadFile(main)
	nowFrag, _ := os.ReadFile(filepath.Join(filepath.Dir(main), "settings.d", "a.json"))
	if string(origMain) != string(nowMain) || string(origFrag) != string(nowFrag) {
		t.Fatal("dry-run modified files")
	}
}

func TestOptimizeIncludeChainNoopLeavesFilesUntouched(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json":     `{"$include": ["settings.d/a.json"], "groups": [{"name": "dev", "dots": [{"name": "vim"}]}]}`,
		"settings.d/a.json": `{"groups": [{"name": "other", "dots": [{"name": "git"}]}]}`,
	})
	orig, _ := os.ReadFile(main)
	report, err := OptimizeIncludeChain(main, false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Empty() {
		t.Fatalf("report = %+v, want empty", report.Removals)
	}
	now, _ := os.ReadFile(main)
	if string(orig) != string(now) {
		t.Fatal("no-op run rewrote main file")
	}
}

// Two empty-base groups match for dedupe but mergeGroups drops empty-base src groups, so only the equivalence check prevents silently losing an entry.
func TestOptimizeIncludeChainEmptyBaseGroupIsSafe(t *testing.T) {
	main := writeOptimizeFixture(t, map[string]string{
		"settings.json": `{
  "$include": ["settings.d/dots.json"],
  "groups": [{"dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "vim", "path": "~/.vimrc"}]}]
}`,
		"settings.d/dots.json": `{
  "groups": [{"dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "zsh", "path": "~/.zshrc"}]}]
}`,
	})
	origMain, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	fragPath := filepath.Join(filepath.Dir(main), "settings.d", "dots.json")
	origFrag, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}

	report, err := OptimizeIncludeChain(main, false)

	// The equivalence check catches the would-be change and aborts, restoring files untouched.
	if err == nil {
		t.Fatalf("expected an abort error for empty-base group mismatch, got report=%+v", report)
	}
	if !strings.Contains(err.Error(), "merged config would change") {
		t.Fatalf("unexpected error: %v", err)
	}

	nowMain, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	nowFrag, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(origMain) != string(nowMain) {
		t.Fatalf("main file changed:\nbefore: %s\nafter:  %s", origMain, nowMain)
	}
	if string(origFrag) != string(nowFrag) {
		t.Fatalf("fragment file changed:\nbefore: %s\nafter:  %s", origFrag, nowFrag)
	}
}

func TestNormalizeForCompareOrderInsensitive(t *testing.T) {
	a := &RootConfig{Groups: []*GroupConfig{
		{Name: "b", Dots: []DotEntry{{Name: "y"}, {Name: "x"}}},
		{Name: "a"},
	}}
	b := &RootConfig{Groups: []*GroupConfig{
		{Name: "a"},
		{Name: "b", Dots: []DotEntry{{Name: "x"}, {Name: "y"}}},
	}}
	na, err := normalizeForCompare(a)
	if err != nil {
		t.Fatal(err)
	}
	nb, err := normalizeForCompare(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(na) != string(nb) {
		t.Fatalf("normalization not order-insensitive:\n%s\n%s", na, nb)
	}
}
