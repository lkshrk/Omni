package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func mixedDirSkills(t *testing.T) (*Skills, string, string) {
	t.Helper()
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")

	registry, err := newRegistry([]Target{{
		ID:              "test",
		configDir:       ".codex",
		extraSkillsDirs: []string{".agents/skills"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, "")
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	return svc, source, home
}

func TestInventoryReportsInstalledWhenEveryDirIsOwned(t *testing.T) {
	svc, source, home := mixedDirSkills(t)
	for _, dir := range []string{filepath.Join(home, ".codex", "skills", "one"), filepath.Join(home, ".agents", "skills", "one")} {
		if !ownedLink(dir, svc.packageDir(source)) {
			t.Fatalf("%s is not an owned link after install", dir)
		}
	}

	inv, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if !inv.PerTarget["test"] || inv.PerTargetDrifted["test"] {
		t.Fatalf("all-owned target = installed %v drifted %v, want installed", inv.PerTarget["test"], inv.PerTargetDrifted["test"])
	}
}

// An owned link in one dir must not short-circuit the scan past a foreign real dir in the other.
func TestInventoryReportsDriftWhenAnotherDirHoldsAForeignEntry(t *testing.T) {
	svc, source, home := mixedDirSkills(t)

	foreign := filepath.Join(home, ".agents", "skills", "one")
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, foreign, "one", "written by another tool")

	inv, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.PerTarget["test"] {
		t.Error("target with a foreign entry in one of its dirs should not read as installed")
	}
	if !inv.PerTargetDrifted["test"] {
		t.Error("target with a foreign entry in one of its dirs should read as drifted")
	}
	if inv.Installed {
		t.Error("package should not read as installed while a target is drifted")
	}

	// The drift marker has to fire exactly where reconcile fails.
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: source}, []string{"test"}); err == nil {
		t.Fatal("restore over a foreign entry should fail")
	} else if !strings.Contains(err.Error(), SkillEntryConflict) {
		t.Fatalf("restore error = %v, want the entry-conflict phrase", err)
	}
}

func TestInventoryReportsMissingWhenEveryDirIsAbsent(t *testing.T) {
	svc, source, home := mixedDirSkills(t)
	for _, dir := range []string{filepath.Join(home, ".codex", "skills", "one"), filepath.Join(home, ".agents", "skills", "one")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}

	inv, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.PerTarget["test"] || inv.PerTargetDrifted["test"] {
		t.Fatalf("absent target = installed %v drifted %v, want neither", inv.PerTarget["test"], inv.PerTargetDrifted["test"])
	}
}
