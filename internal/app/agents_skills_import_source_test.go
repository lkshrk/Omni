package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func importFixtureApp(t *testing.T, packages ...config.SkillPackage) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"one": {Source: "o/first", Ref: "v1", UpdatedAt: "2026-07-05T00:00:00Z"},
			"two": {Source: "o/second", UpdatedAt: "2026-07-06T00:00:00Z"},
		},
	})
	return newSkillsTestApp(t, config.AgentsConfig{Packages: packages})
}

func TestImportSkillsWithSourceClaimsOnlyThatPackage(t *testing.T) {
	a := importFixtureApp(t)

	diff, err := a.ImportSkills(context.Background(), ImportSkillsOptions{Source: "o/second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "o/second" {
		t.Fatalf("added = %v, want only o/second", diff.Added)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "o/second" {
		t.Fatalf("manifest = %+v, want only o/second", cfg.Agents.Packages)
	}
}

func TestImportSkillsWithoutSourceStillClaimsEverything(t *testing.T) {
	a := importFixtureApp(t)

	diff, err := a.ImportSkills(context.Background(), ImportSkillsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Added) != 2 {
		t.Fatalf("added = %v, want both packages", diff.Added)
	}
}

func TestImportSkillsWithSourceNamesWhyItIsNotACandidate(t *testing.T) {
	t.Run("not in the lockfile", func(t *testing.T) {
		a := importFixtureApp(t)
		_, err := a.ImportSkills(context.Background(), ImportSkillsOptions{Source: "o/absent"})
		if err == nil || !strings.Contains(err.Error(), "not in the legacy lockfile") {
			t.Fatalf("err = %v, want a lockfile-absence reason", err)
		}
	})

	t.Run("already managed", func(t *testing.T) {
		a := importFixtureApp(t, config.SkillPackage{Source: "o/first"})
		_, err := a.ImportSkills(context.Background(), ImportSkillsOptions{Source: "o/first"})
		if err == nil || !strings.Contains(err.Error(), "already in the manifest") {
			t.Fatalf("err = %v, want an already-managed reason", err)
		}
	})

	t.Run("provided by a plugin", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "")
		stubBinariesOnPath(t, "claude")
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "some-skill"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeSkillLockFixture(t, home, config.SkillLockFile{
			Version: 3,
			Skills: map[string]config.SkillLockEntry{
				"some-skill": {Source: "owner/academic-research-skills"},
			},
		})
		pluginStub := &shadowTestPluginAdapter{
			id:            "claude-code",
			listedPlugins: []InstalledPlugin{{Name: "academic-research-skills", Marketplace: "some-marketplace"}},
		}
		a := newSkillsTestApp(t, config.AgentsConfig{}, WithPluginAdapters([]PluginAdapter{pluginStub}))

		_, err := a.ImportSkills(context.Background(), ImportSkillsOptions{Source: "owner/academic-research-skills"})
		if err == nil || !strings.Contains(err.Error(), "provided by an installed plugin") {
			t.Fatalf("err = %v, want a plugin-shadow reason", err)
		}
	})
}

func TestImportSkillsWithSourceDryRunDoesNotWrite(t *testing.T) {
	a := importFixtureApp(t)

	diff, err := a.ImportSkills(context.Background(), ImportSkillsOptions{Source: "o/first", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Added) != 1 || diff.Added[0] != "o/first" {
		t.Fatalf("added = %v, want only o/first", diff.Added)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 0 {
		t.Fatalf("dry run wrote %+v", cfg.Agents.Packages)
	}
}
