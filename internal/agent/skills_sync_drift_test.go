package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func twoTargetSkills(t *testing.T) (*Skills, string, string) {
	t.Helper()
	home := t.TempDir()
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "one"), "one", "first")

	registry, err := newRegistry([]Target{
		{ID: "alpha", configDir: ".alpha"},
		{ID: "beta", configDir: ".beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewSkills(registry, home, filepath.Join(home, "data"), nil, nil, ""), source, home
}

// Sync skips a contested entry so the package still lands everywhere else, while Install keeps refusing outright.
func TestSyncSkipsDriftedTargetAndLinksHealthyOnes(t *testing.T) {
	svc, source, home := twoTargetSkills(t)
	foreign := filepath.Join(home, ".alpha", "skills", "one")
	writeTestSkill(t, foreign, "one", "written by another tool")

	pkg := config.SkillPackage{Source: source}
	targets := []string{"alpha", "beta"}
	if _, err := svc.Install(context.Background(), pkg, targets); err == nil {
		t.Fatal("Install over a foreign entry should fail")
	} else if !strings.Contains(err.Error(), SkillEntryConflict) {
		t.Fatalf("Install error = %v, want the entry-conflict phrase", err)
	}

	if _, err := svc.Sync(context.Background(), pkg, targets); err != nil {
		t.Fatalf("Sync over a foreign entry should degrade, got %v", err)
	}

	betaLink := filepath.Join(home, ".beta", "skills", "one")
	if !ownedLink(betaLink, svc.packageDir(source)) {
		t.Errorf("%s is not an owned link; the contested target blocked a healthy one", betaLink)
	}
	assertFileContains(t, filepath.Join(foreign, "SKILL.md"), "written by another tool")
	if info, err := os.Lstat(foreign); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("foreign entry = %v (%v), want the directory left in place", info, err)
	}

	inv, err := svc.Inventory(context.Background(), source, targets)
	if err != nil {
		t.Fatal(err)
	}
	if !inv.PerTargetDrifted["alpha"] || !inv.PerTarget["beta"] {
		t.Fatalf("inventory = drifted %v installed %v, want alpha drifted and beta installed",
			inv.PerTargetDrifted, inv.PerTarget)
	}
}

// A foreign symlink must be left pointing where it does rather than silently repointed at the canonical store.
func TestSyncLeavesForeignSymlinkInPlace(t *testing.T) {
	svc, source, home := twoTargetSkills(t)
	other := t.TempDir()
	writeTestSkill(t, filepath.Join(other, "one"), "one", "another tool's tree")
	foreign := filepath.Join(home, ".alpha", "skills", "one")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "one"), foreign); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Sync(context.Background(), config.SkillPackage{Source: source}, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Sync = %v, want the foreign link skipped", err)
	}
	link, err := os.Readlink(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if link != filepath.Join(other, "one") {
		t.Fatalf("foreign link now points at %q, want it untouched", link)
	}
}

// The reuse path runs its own link validation, so it needs the same tolerance.
func TestSyncToleratesDriftOnTheReusePath(t *testing.T) {
	svc, source, home := twoTargetSkills(t)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"beta"}); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(home, ".alpha", "skills", "one")
	writeTestSkill(t, foreign, "one", "written by another tool")

	if _, err := svc.Sync(context.Background(), pkg, []string{"alpha", "beta"}); err != nil {
		t.Fatalf("Sync = %v, want the contested target skipped on the reuse path", err)
	}
	if !ownedLink(filepath.Join(home, ".beta", "skills", "one"), svc.packageDir(source)) {
		t.Error("beta lost its managed link")
	}
	assertFileContains(t, filepath.Join(foreign, "SKILL.md"), "written by another tool")
}

// Upgrade degrades like Sync, while Refresh, serving explicit installs, keeps refusing.
func TestUpgradeDegradesWhileRefreshRefuses(t *testing.T) {
	svc, source, home := twoTargetSkills(t)
	writeTestSkill(t, filepath.Join(home, ".alpha", "skills", "one"), "one", "written by another tool")

	pkg := config.SkillPackage{Source: source}
	targets := []string{"alpha", "beta"}
	if _, err := svc.Refresh(context.Background(), pkg, targets); err == nil {
		t.Fatal("Refresh over a foreign entry should fail")
	}
	if _, err := svc.Upgrade(context.Background(), pkg, targets); err != nil {
		t.Fatalf("Upgrade = %v, want the contested target skipped", err)
	}
	if !ownedLink(filepath.Join(home, ".beta", "skills", "one"), svc.packageDir(source)) {
		t.Error("beta did not get the refreshed content")
	}
}
