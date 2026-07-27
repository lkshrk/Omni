package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillPackageDriftIsDistinctFromMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "skills-drift-host")
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "drift-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code"}}
	if _, err := service.Install(context.Background(), pkg, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		return nil
	}); err != nil {
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

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	if got := rows[0].PerAgentStatus["claude-code"]; got != SkillStatusDrifted {
		t.Fatalf("PerAgentStatus[claude-code] = %q, want %q", got, SkillStatusDrifted)
	}
	if rows[0].Installed {
		t.Error("a drifted package must not report as installed")
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := a.DashboardAgentsSummary(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkillsDrifted != 1 || len(summary.SkillsDriftedNames) != 1 {
		t.Fatalf("summary drift = %d %v, want 1 name", summary.SkillsDrifted, summary.SkillsDriftedNames)
	}
	if summary.SkillsMissing != 0 {
		t.Errorf("drifted package counted as missing: %+v", summary)
	}
	if summary.OutOfSync() < 1 {
		t.Errorf("drift must count toward attention, got %d", summary.OutOfSync())
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, check := range result.Checks {
		if check.ID != "agents" {
			continue
		}
		for _, group := range check.Groups {
			if group.Header == "skills" {
				items = group.Items
			}
		}
	}
	found := false
	for _, item := range items {
		if strings.Contains(item, "drifted on claude-code") && strings.Contains(item, "agents skills import") {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor skills items = %v, want a drifted item naming the agent and a hint", items)
	}
}

func TestSkillPackageDriftFromForeignSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("OMNI_HOSTNAME", "skills-drift-link-host")
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "drift-link-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "demo"), "demo")

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(t.Context(), &availabilityCountingProvider{name: "brew", available: true}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	pkg := config.SkillPackage{Source: source, Agents: []string{"claude-code"}}
	if _, err := service.Install(context.Background(), pkg, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "skills", "demo"), link); err != nil {
		t.Fatal(err)
	}

	rows, err := a.SkillPackageRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	if got := rows[0].PerAgentStatus["claude-code"]; got != SkillStatusDrifted {
		t.Fatalf("PerAgentStatus[claude-code] = %q, want %q", got, SkillStatusDrifted)
	}
}
