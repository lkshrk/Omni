package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestNpxInstallerVerifiesLockfile guards the "skills CLI exits 0 but the
// install did not happen" contract (the claude-plugin bug class): a zero
// exit with no lockfile entry for the package must be treated as a failure.
func TestNpxInstallerVerifiesLockfile(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	okExec := func(context.Context, string, ...string) (string, string, error) { return "", "", nil }
	inst := npxInstaller{runner: "npx", exec: okExec}
	pkg := config.SkillPackage{Source: "o/r"}

	err := inst.Install(context.Background(), pkg, nil)
	if err == nil || !strings.Contains(err.Error(), "wrote no lockfile entries") {
		t.Fatalf("Install with empty lockfile: err = %v, want lockfile-verification failure", err)
	}

	lockPath := filepath.Join(state, "skills", ".skill-lock.json")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"version":3,"skills":{"sk":{"source":"o/r"}}}`
	if err := os.WriteFile(lockPath, []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := inst.Install(context.Background(), pkg, nil); err != nil {
		t.Fatalf("Install with lockfile entry: err = %v, want nil", err)
	}
}

// TestShadowCheckAgents_NilUseFallsBackToDetectedAgents is the regression
// test for the default configuration (no agents_use, package without Agents):
// filterShadowedSkillPackages fed a nil use list iterates zero agents and
// shadows nothing, while the install auto-detects agents and would reinstall
// a plugin-provided skill as a user-scope duplicate. RestoreSkills must feed
// the filter the detected-agents fallback instead.
func TestShadowCheckAgents_NilUseFallsBackToDetectedAgents(t *testing.T) {
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

	keep, shadowed := filterShadowedSkillPackages(pkgs, a.shadowCheckAgents(cfg, nil), pluginNames)
	if len(shadowed) != 1 || shadowed[0] != "owner/academic-research-skills" {
		t.Fatalf("shadowed = %v, want [owner/academic-research-skills]", shadowed)
	}
	if len(keep) != 1 || keep[0].Source != "o/normal-package" {
		t.Fatalf("keep = %+v, want only o/normal-package", keep)
	}

	explicit := []string{"cursor"}
	if got := a.shadowCheckAgents(cfg, explicit); !reflect.DeepEqual(got, explicit) {
		t.Fatalf("shadowCheckAgents(explicit) = %v, want %v", got, explicit)
	}
	if got := a.shadowCheckAgents(cfg, []string{}); got == nil || len(got) != 0 {
		t.Fatalf("shadowCheckAgents(empty) = %#v, want non-nil empty", got)
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

func TestSkillsCLIFailureMarkers(t *testing.T) {
	cases := []struct {
		name           string
		stdout, stderr string
		wantErr        bool
	}{
		{"clean output", "Installed owner/repo (2 skills)", "", false},
		{"failed to install on stdout", "Failed to install 2", "", true},
		{"failed to remove on stderr", "", "Failed to remove 1 skill(s)", true},
		{"upstream ballot x per-item line", "  ✗ my-skill → claude-code: EACCES", "", true},
		{"legacy heavy ballot x", "✘ install error", "", true},
		{"ansi-wrapped failure", "\x1b[31mFailed to install 1\x1b[39m", "", true},
		{"empty output", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := skillsCLIFailure("skills add owner/repo", tc.stdout, tc.stderr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("skillsCLIFailure(%q, %q) err = %v, wantErr %v", tc.stdout, tc.stderr, err, tc.wantErr)
			}
		})
	}
}

func TestSkillsCLIFailureIncludesDetailLines(t *testing.T) {
	stdout := "cloning repo\nFailed to install 2 skill(s)\n  ✗ my-skill → claude-code: EACCES permission denied\ndone"
	err := skillsCLIFailure("skills add owner/repo", stdout, "")
	if err == nil {
		t.Fatal("expected failure error")
	}
	for _, want := range []string{"Failed to install 2 skill(s)", "EACCES permission denied"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must carry detail %q", err, want)
		}
	}
}

func TestSkillsFailureDetailCapsLines(t *testing.T) {
	stdout := strings.Repeat("✗ broken line\n", 6)
	detail := skillsFailureDetail(stdout, "")
	if got := strings.Count(detail, "✗"); got != 4 {
		t.Fatalf("detail should cap at 4 failure lines, got %d in %q", got, detail)
	}
	if !strings.HasSuffix(detail, "…") {
		t.Fatalf("capped detail must end with ellipsis, got %q", detail)
	}
}

type fixedOutputExecutor struct {
	stdout, stderr string
	called         bool
}

func (e *fixedOutputExecutor) Run(_ context.Context, _ string, _ ...string) (string, string, error) {
	e.called = true
	return e.stdout, e.stderr, nil
}

func TestUpdateSkillsExitZeroFailureMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	a := newSkillsTestApp(t, config.AgentsConfig{})
	exec := &fixedOutputExecutor{stdout: "Failed to update skill x"}
	a.SetFallbackExecutor(exec)

	_, _, err := a.UpdateSkills(context.Background(), UpdateSkillsOptions{})
	if !exec.called {
		t.Fatal("executor was not invoked")
	}
	if err == nil || !strings.Contains(err.Error(), "exited 0 but reported failure") {
		t.Fatalf("UpdateSkills err = %v, want exit-0 failure-marker error", err)
	}
}

func TestUnconfiguredHostSkillsWarning(t *testing.T) {
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
