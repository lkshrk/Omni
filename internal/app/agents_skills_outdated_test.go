package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func outdatedTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "skills-outdated-host")
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "outdated-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code"}}
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), pkg, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a, source, home
}

func TestSkillOutdatedSurfacesInRowsDashboardAndDoctor(t *testing.T) {
	a, source, _ := outdatedTestApp(t)
	ctx := context.Background()

	rows, err := a.SkillPackageRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Outdated != SkillOutdatedCurrent {
		t.Fatalf("rows = %+v, want one current package", rows)
	}

	writeAppSkillBody(t, filepath.Join(source, "skills", "demo"), "demo", "upstream moved")
	checks, err := a.RefreshSkillOutdated(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Outdated != SkillOutdatedBehind {
		t.Fatalf("checks = %+v, want one outdated package", checks)
	}

	rows, err = a.SkillPackageRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Outdated != SkillOutdatedBehind {
		t.Fatalf("row Outdated = %q, want %q", rows[0].Outdated, SkillOutdatedBehind)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := a.DashboardAgentsSummary(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkillsOutdated != 1 || len(summary.SkillsOutdatedNames) != 1 {
		t.Fatalf("summary outdated = %d %v, want 1", summary.SkillsOutdated, summary.SkillsOutdatedNames)
	}
	if summary.OutOfSync() != 0 {
		t.Errorf("outdated is installed and consistent, not out of sync, got %d", summary.OutOfSync())
	}

	group, _ := a.doctorAgentsSkills(ctx, cfg)
	if !groupHasItemContaining(group, "behind their source") {
		t.Fatalf("doctor skills group did not name the outdated package: %+v", group.Items)
	}
}

func TestSkillOutdatedIsNotCountedTwiceWhenAlsoMissing(t *testing.T) {
	a, source, home := outdatedTestApp(t)
	ctx := context.Background()

	if err := os.RemoveAll(filepath.Join(home, ".claude", "skills", "demo")); err != nil {
		t.Fatal(err)
	}
	writeAppSkillBody(t, filepath.Join(source, "skills", "demo"), "demo", "upstream moved")
	if _, err := a.RefreshSkillOutdated(ctx, true); err != nil {
		t.Fatal(err)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := a.DashboardAgentsSummary(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkillsMissing != 1 || summary.SkillsOutdated != 0 {
		t.Fatalf("missing=%d outdated=%d, want 1 and 0", summary.SkillsMissing, summary.SkillsOutdated)
	}
}

func TestSkillOutdatedStaysVisibleWhileDrifted(t *testing.T) {
	a, source, home := outdatedTestApp(t)
	ctx := context.Background()

	link := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	writeAppSkill(t, link, "demo")
	if err := os.WriteFile(filepath.Join(link, "EXTRA.md"), []byte("another tool's copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeAppSkillBody(t, filepath.Join(source, "skills", "demo"), "demo", "upstream moved")
	if _, err := a.RefreshSkillOutdated(ctx, true); err != nil {
		t.Fatal(err)
	}

	rows, err := a.SkillPackageRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Outdated != SkillOutdatedBehind {
		t.Fatalf("rows = %+v, want the drifted package still marked outdated", rows)
	}
	if rows[0].PerAgentStatus["claude-code"] != SkillStatusDrifted {
		t.Fatalf("PerAgentStatus = %v, want drifted", rows[0].PerAgentStatus)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := a.DashboardAgentsSummary(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkillsDrifted != 1 {
		t.Errorf("SkillsDrifted = %d, want 1", summary.SkillsDrifted)
	}
	if summary.SkillsOutdated != 1 || len(summary.SkillsOutdatedNames) != 1 {
		t.Fatalf("SkillsOutdated = %d %v, want the upgrade visible alongside the drift",
			summary.SkillsOutdated, summary.SkillsOutdatedNames)
	}

	if _, err := a.ResolveSkillDrift(ctx, ResolveSkillDriftOptions{
		Source:   source,
		Strategy: SkillDriftUseManaged,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RefreshSkillOutdated(ctx, true); err != nil {
		t.Fatal(err)
	}
	summary, err = a.DashboardAgentsSummary(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkillsDrifted != 0 {
		t.Errorf("SkillsDrifted = %d after resolving, want 0", summary.SkillsDrifted)
	}
	if summary.SkillsOutdated != 1 {
		t.Errorf("SkillsOutdated = %d after resolving, want the same 1", summary.SkillsOutdated)
	}
}

func TestUpdateSkillsCheckReportsWithoutRefreshing(t *testing.T) {
	a, source, _ := outdatedTestApp(t)
	ctx := context.Background()

	writeAppSkillBody(t, filepath.Join(source, "skills", "demo"), "demo", "upstream moved")
	out, dryRun, err := a.UpdateSkills(ctx, UpdateSkillsOptions{Check: true})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun != "" {
		t.Fatalf("dryRun = %q, want empty for --check", dryRun)
	}
	if !strings.HasPrefix(out, "outdated "+source) {
		t.Fatalf("check output = %q, want it to report %s outdated", out, source)
	}

	out, _, err = a.UpdateSkills(ctx, UpdateSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(outdated: source content changed)") {
		t.Fatalf("update output = %q, want it to name the outdated reason", out)
	}

	out, _, err = a.UpdateSkills(ctx, UpdateSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(forced: already up to date)") {
		t.Fatalf("second update output = %q, want it to say nothing changed", out)
	}
}

func writeAppSkillBody(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func groupHasItemContaining(g DoctorDetailGroup, want string) bool {
	for _, item := range g.Items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}
