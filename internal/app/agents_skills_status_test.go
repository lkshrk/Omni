package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillPackageStatusReportsStoreTargetsAndEntryStates(t *testing.T) {
	a, source, home := outdatedTestApp(t)
	ctx := context.Background()

	status, err := a.SkillPackageStatus(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Managed {
		t.Error("package is in the manifest but reported unmanaged")
	}
	if status.PackageDir == "" || status.ContentHash == "" {
		t.Errorf("store = %q %q, want a dir and a hash", status.PackageDir, status.ContentHash)
	}
	if len(status.Targets) != 1 || status.Targets[0] != "claude-code" {
		t.Errorf("targets = %v, want [claude-code]", status.Targets)
	}
	if len(status.Skills) != 1 || status.Skills[0] != "demo" {
		t.Errorf("skills = %v, want [demo]", status.Skills)
	}
	if len(status.Entries) != 1 {
		t.Fatalf("entries = %+v, want 1", status.Entries)
	}
	if status.Entries[0].State != "owned-link" || !strings.Contains(status.Entries[0].Detail, "omni's link") {
		t.Errorf("entry = %+v, want an owned link with its target", status.Entries[0])
	}
	if status.Entries[0].Hint != "" {
		t.Errorf("a healthy entry needs no next step, got %q", status.Entries[0].Hint)
	}
	if status.Outdated != SkillOutdatedCurrent {
		t.Errorf("Outdated = %q, want %q", status.Outdated, SkillOutdatedCurrent)
	}

	link := filepath.Join(home, ".claude", "skills", "demo")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	writeAppSkillBody(t, link, "demo", "another tool's copy")

	status, err = a.SkillPackageStatus(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if status.Entries[0].State != "drifted" {
		t.Fatalf("entry state = %q, want drifted", status.Entries[0].State)
	}
	if !strings.Contains(status.Entries[0].Hint, "skills resolve") {
		t.Errorf("drifted entry hint = %q, want the resolve remedy", status.Entries[0].Hint)
	}
}

func TestSkillPackageStatusNarrowsToOneSkillAndRejectsUnknownOnes(t *testing.T) {
	a, source, _ := outdatedTestApp(t)
	ctx := context.Background()

	status, err := a.SkillPackageStatus(ctx, source+"@demo")
	if err != nil {
		t.Fatal(err)
	}
	if status.Skill != "demo" || len(status.Entries) != 1 {
		t.Fatalf("status = %+v, want the demo skill only", status)
	}

	if _, err := a.SkillPackageStatus(ctx, source+"@absent"); err == nil {
		t.Fatal("a skill the package does not provide must be rejected")
	}
}

func TestSkillPackageStatusForUnmanagedPackagePointsAtImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"one": {Source: "o/unmanaged", Ref: "v1", UpdatedAt: "2026-07-05T00:00:00Z"},
		},
	})
	a := newSkillsTestApp(t, config.AgentsConfig{})

	status, err := a.SkillPackageStatus(context.Background(), "o/unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if status.Managed {
		t.Error("package is not in the manifest but reported managed")
	}
	if len(status.Lockfile) != 1 || status.Lockfile[0].Skill != "one" || status.Lockfile[0].Ref != "v1" {
		t.Fatalf("lockfile attribution = %+v, want the one entry", status.Lockfile)
	}
	if !hasHintContaining(status.Hints, "skills import o/unmanaged") {
		t.Fatalf("hints = %v, want the import next step", status.Hints)
	}
}

func hasHintContaining(hints []string, want string) bool {
	for _, hint := range hints {
		if strings.Contains(hint, want) {
			return true
		}
	}
	return false
}
