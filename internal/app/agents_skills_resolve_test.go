package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func driftedSkillsApp(t *testing.T, hostname string, skills []string, agents []string, selectors []string) (*App, string, config.SkillPackage) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", hostname)
	stubBinariesOnPath(t, "claude", "cursor")
	for _, dir := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(t.TempDir(), "skills-src")
	for _, name := range skills {
		writeAppSkill(t, filepath.Join(source, "skills", name), name)
	}

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	pkg := config.SkillPackage{Source: source, Agents: agents, Skills: selectors}
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), pkg, agents); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a, home, pkg
}

func agentSkillDir(home, agentID string) string {
	switch agentID {
	case "cursor":
		return filepath.Join(home, ".cursor", "skills")
	default:
		return filepath.Join(home, ".claude", "skills")
	}
}

func makeForeign(t *testing.T, home, agentID, name string) string {
	t.Helper()
	path := filepath.Join(agentSkillDir(home, agentID), name)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeAppSkill(t, path, name)
	if err := os.WriteFile(filepath.Join(path, "EXTRA.md"), []byte("another tool's copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func skillsBackupCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".omni-adopt-") {
			n++
		}
	}
	return n
}

func TestResolveSkillDriftUseManagedReplacesForeignEntry(t *testing.T) {
	a, home, pkg := driftedSkillsApp(t, "resolve-managed-host", []string{"demo"}, []string{"claude-code"}, nil)
	path := makeForeign(t, home, "claude-code", "demo")

	res, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Strategy: SkillDriftUseManaged,
	})
	if err != nil {
		t.Fatalf("ResolveSkillDrift: %v", err)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], path) {
		t.Fatalf("actions = %v, want the replaced entry path", res.Actions)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is still a real directory; the managed link did not take over", path)
	}
	if n := skillsBackupCount(t, agentSkillDir(home, "claude-code")); n != 0 {
		t.Fatalf("%d displaced backup(s) remain after a successful resolve", n)
	}

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rows[0].PerAgentStatus["claude-code"]; got != SkillStatusInstalled {
		t.Fatalf("PerAgentStatus[claude-code] = %q, want %q", got, SkillStatusInstalled)
	}
}

func TestResolveSkillDriftUseManagedTransfersManagedPackageOwnership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "resolve-managed-collision-host")
	stubBinariesOnPath(t, "claude", "cursor")
	for _, dir := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	writeAppSkill(t, filepath.Join(first, "skills", "same"), "same")
	writeAppSkill(t, filepath.Join(second, "skills", "same"), "same")

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: first}, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: second}, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{
			{Source: first, Agents: []string{"claude-code"}},
			{Source: second, Agents: []string{"claude-code"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source: second, Strategy: SkillDriftUseManaged,
	}); err != nil {
		t.Fatalf("ResolveSkillDrift: %v", err)
	}
	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status := make(map[string]SkillStatus, len(rows))
	for _, row := range rows {
		status[row.Source] = row.PerAgentStatus["claude-code"]
	}
	if status[first] != SkillStatusShadowed || status[second] != SkillStatusInstalled {
		t.Fatalf("statuses = %v, want first shadowed and second installed", status)
	}
}

func TestResolveSkillDriftUseManagedRestoresForeignEntryOnFailure(t *testing.T) {
	a, home, pkg := driftedSkillsApp(t, "resolve-rollback-host", []string{"demo"}, []string{"claude-code", "cursor"}, nil)
	claudePath := makeForeign(t, home, "claude-code", "demo")
	makeForeign(t, home, "cursor", "demo")

	cursorDir := agentSkillDir(home, "cursor")
	if err := os.Chmod(cursorDir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Strategy: SkillDriftUseManaged,
	})
	if chmodErr := os.Chmod(cursorDir, 0o755); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if err == nil || !strings.Contains(err.Error(), "staging backup in "+cursorDir) {
		t.Fatalf("err = %v, want the second target's staging failure", err)
	}

	info, lstatErr := os.Lstat(claudePath)
	if lstatErr != nil {
		t.Fatalf("the displaced entry was not restored: %v", lstatErr)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("%s = %v, want the foreign directory back", claudePath, info.Mode())
	}
	if _, err := os.Stat(filepath.Join(claudePath, "EXTRA.md")); err != nil {
		t.Fatalf("restored entry lost the foreign content: %v", err)
	}
	if n := skillsBackupCount(t, agentSkillDir(home, "claude-code")); n != 0 {
		t.Fatalf("%d staged backup(s) remain after the rollback", n)
	}
}

func TestResolveSkillDriftUseLocalNarrowsSelectors(t *testing.T) {
	a, home, pkg := driftedSkillsApp(t,
		"resolve-selector-host", []string{"demo", "other"}, []string{"claude-code"}, []string{"demo", "other"})
	path := makeForeign(t, home, "claude-code", "demo")

	res, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source + "@demo",
		Strategy: SkillDriftUseLocal,
	})
	if err != nil {
		t.Fatalf("ResolveSkillDrift: %v", err)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], `"demo"`) {
		t.Fatalf("actions = %v, want a selector narrowing line", res.Actions)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents.Packages[0].Skills; len(got) != 1 || got[0] != "other" {
		t.Fatalf("skills = %v, want [other]", got)
	}
	if _, err := os.Stat(filepath.Join(path, "EXTRA.md")); err != nil {
		t.Fatalf("use-local touched the foreign content: %v", err)
	}
}

func TestResolveSkillDriftUseLocalNarrowsAgents(t *testing.T) {
	a, home, pkg := driftedSkillsApp(t, "resolve-agents-host", []string{"demo"}, []string{"claude-code", "cursor"}, nil)
	makeForeign(t, home, "claude-code", "demo")

	res, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Agents:   []string{"claude-code"},
		Strategy: SkillDriftUseLocal,
	})
	if err != nil {
		t.Fatalf("ResolveSkillDrift: %v", err)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], "claude-code") {
		t.Fatalf("actions = %v, want an agent narrowing line", res.Actions)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents.Packages[0].Agents; len(got) != 1 || got[0] != "cursor" {
		t.Fatalf("agents = %v, want [cursor]", got)
	}
}

func TestResolveSkillDriftUseLocalRefusesEmptyManifestState(t *testing.T) {
	a, home, pkg := driftedSkillsApp(t, "resolve-refuse-host", []string{"demo"}, []string{"claude-code"}, []string{"demo"})
	makeForeign(t, home, "claude-code", "demo")

	_, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source + "@demo",
		Strategy: SkillDriftUseLocal,
	})
	if err == nil || !strings.Contains(err.Error(), "agents skills remove") {
		t.Fatalf("err = %v, want a refusal pointing at remove", err)
	}

	_, err = a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Strategy: SkillDriftUseLocal,
	})
	if err == nil || !strings.Contains(err.Error(), "agents skills remove") {
		t.Fatalf("err = %v, want a refusal pointing at remove for the last agent", err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents.Packages[0]; len(got.Skills) != 1 || len(got.Agents) != 1 {
		t.Fatalf("manifest changed on a refused narrowing: %+v", got)
	}
}

func TestResolveSkillDriftDryRunChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		strategy SkillDriftStrategy
		want     string
	}{
		{name: "use-managed", strategy: SkillDriftUseManaged, want: "managed link"},
		{name: "use-local", strategy: SkillDriftUseLocal, want: "stop managing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, home, pkg := driftedSkillsApp(t,
				"resolve-dryrun-"+tc.name, []string{"demo"}, []string{"claude-code", "cursor"}, nil)
			path := makeForeign(t, home, "claude-code", "demo")

			res, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
				Source:   pkg.Source,
				Agents:   []string{"claude-code"},
				Strategy: tc.strategy,
				DryRun:   true,
			})
			if err != nil {
				t.Fatalf("ResolveSkillDrift: %v", err)
			}
			if len(res.Actions) == 0 || !strings.Contains(strings.Join(res.Actions, "\n"), tc.want) {
				t.Fatalf("actions = %v, want a %s line", res.Actions, tc.want)
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				t.Fatalf("dry run touched %s: %v %v", path, info, err)
			}
			cfg, err := a.loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Agents.Packages[0]; len(got.Agents) != 2 || len(got.Skills) != 0 {
				t.Fatalf("dry run changed the manifest: %+v", got)
			}
		})
	}
}

func TestResolveSkillDriftRejectsUnknownAndCleanTargets(t *testing.T) {
	a, home, pkg := driftedSkillsApp(t, "resolve-reject-host", []string{"demo"}, []string{"claude-code", "cursor"}, nil)

	if _, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Strategy: SkillDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "not drifted") {
		t.Fatalf("err = %v, want a not-drifted refusal", err)
	}

	makeForeign(t, home, "claude-code", "demo")
	if _, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Agents:   []string{"cursor"},
		Strategy: SkillDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "not drifted on cursor") {
		t.Fatalf("err = %v, want a per-agent not-drifted refusal", err)
	}
	if _, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   pkg.Source,
		Agents:   []string{"nonexistent"},
		Strategy: SkillDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "does not target agent") {
		t.Fatalf("err = %v, want an unknown-agent refusal", err)
	}
	if _, err := a.ResolveSkillDrift(context.Background(), ResolveSkillDriftOptions{
		Source:   filepath.Join(t.TempDir(), "absent"),
		Strategy: SkillDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "not in this host's manifest") {
		t.Fatalf("err = %v, want an unknown-package refusal", err)
	}
}
