package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestForeignSkillEntries_ReportsOnlyUnaccountedEntries(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-foreign-host")

	drifted := filepath.Join(t.TempDir(), "drifted-skills")
	writeAppSkill(t, filepath.Join(drifted, "skills", "shared"), "shared")
	driftPkg := config.SkillPackage{Source: drifted, Agents: []string{"claude-code"}}
	service, err := f.app.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), driftPkg, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := f.app.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = append(cfg.Agents.Packages, driftPkg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	driftEntry := filepath.Join(f.skillsDir, "shared")
	if err := os.Remove(driftEntry); err != nil {
		t.Fatal(err)
	}
	writeAppSkill(t, driftEntry, "shared")
	if err := os.WriteFile(filepath.Join(driftEntry, "EXTRA.md"), []byte("another tool's copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agentsDir := filepath.Join(f.home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{"skills":{"legacy":{"source":"owner/legacy-repo","updatedAt":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(agentsDir, ".skill-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	writeAppSkill(t, filepath.Join(f.skillsDir, "legacy"), "legacy")

	if err := os.MkdirAll(filepath.Join(f.skillsDir, ".demo.omni-legacy-987"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.packageDir, "skills", "demo"), filepath.Join(f.skillsDir, "demo.omni-tmp")); err != nil {
		t.Fatal(err)
	}

	handmade := filepath.Join(f.skillsDir, "handmade")
	writeAppSkill(t, handmade, "handmade")
	elsewhere := filepath.Join(f.skillsDir, "elsewhere")
	otherTool := filepath.Join(t.TempDir(), "other-tool", "elsewhere")
	writeAppSkill(t, otherTool, "elsewhere")
	if err := os.Symlink(otherTool, elsewhere); err != nil {
		t.Fatal(err)
	}

	got, err := f.app.ForeignSkillEntries()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{elsewhere, handmade}
	if len(got) != len(want) {
		t.Fatalf("foreign entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("foreign entries = %v, want %v", got, want)
		}
	}

	items := f.doctorSkillsItems(t)
	for _, item := range items {
		if strings.Contains(item, "foreign skill") {
			t.Fatalf("doctor skills items = %v, want foreign content omitted", items)
		}
	}
	if _, err := os.Lstat(handmade); err != nil {
		t.Fatalf("doctor must not touch foreign content: %v", err)
	}
}

func TestForeignSkillEntries_DoctorStaysOK(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-foreign-ok-host")
	handmade := filepath.Join(f.skillsDir, "handmade")
	writeAppSkill(t, handmade, "handmade")

	result, err := f.app.Doctor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var check *DoctorCheck
	for i := range result.Checks {
		if result.Checks[i].ID == "agents" {
			check = &result.Checks[i]
		}
	}
	if check == nil {
		t.Fatal("agents check missing")
	}
	if !strings.Contains(check.Message, "skills ok") {
		t.Errorf("message = %q, want the skills group ok: a hand-managed skill is not breakage", check.Message)
	}
	report := f.fix(t, false)
	if !report.Empty() {
		t.Fatalf("report = %+v, want empty: foreign content is never fixed", report)
	}
	mustExist(t, handmade)
}

func TestForeignSkillEntries_ExcludesDeclaredButUninstalledPackage(t *testing.T) {
	f := newSkillStoreFixture(t, "skills-foreign-declared-host")
	if err := f.app.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = append(cfg.Agents.Packages, config.SkillPackage{
			Source: "owner/never-installed",
			Skills: []string{"planned"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeAppSkill(t, filepath.Join(f.skillsDir, "planned"), "planned")

	got, err := f.app.ForeignSkillEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("foreign entries = %v, want none: the manifest declares that skill", got)
	}
}
