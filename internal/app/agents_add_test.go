package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestUpsertPackage(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{{Source: "a/b", Ref: "main", Skills: []string{"one"}}}
	got, added := upsertPackage(existing, config.SkillPackage{Source: "c/d", Ref: "v1", Skills: []string{"new"}})
	if !added || len(got) != 2 || got[1].Source != "c/d" || got[1].Ref != "v1" || !reflect.DeepEqual(got[1].Skills, []string{"new"}) {
		t.Fatalf("add new: got=%+v added=%v", got, added)
	}
	got2, added2 := upsertPackage(existing, config.SkillPackage{Source: "a/b", Ref: "next", Skills: []string{"two", "one"}})
	if added2 || len(got2) != 1 || got2[0].Ref != "next" || !reflect.DeepEqual(got2[0].Skills, []string{"one", "two"}) {
		t.Fatalf("update existing: got=%+v added=%v", got2, added2)
	}
	got3, _ := upsertPackage(got2, config.SkillPackage{Source: "a/b"})
	if got3[0].Skills != nil {
		t.Fatalf("selectorless re-add skills = %v, want nil (all)", got3[0].Skills)
	}
}

func TestUpsertNormalizedPackagesDeduplicatesEquivalentSources(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{{
		Source: "https://github.com/owner/repo.git",
		Skills: []string{"one"},
	}}
	got, added := upsertNormalizedPackages(existing, config.SkillPackage{
		Source: "owner/repo",
		Skills: []string{"two"},
	})
	if added || len(got) != 1 {
		t.Fatalf("got=%+v added=%v, want one merged package", got, added)
	}
	if got[0].Source != "owner/repo" || !reflect.DeepEqual(got[0].Skills, []string{"one", "two"}) {
		t.Fatalf("merged package = %+v", got[0])
	}
}

func TestRemoveSkillPackage_PersistsRemoval(t *testing.T) {
	t.Parallel()
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "o/keep"}, {Source: "o/del"}},
	})
	if err := a.RemoveSkillPackage("o/del"); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Packages) != 1 || cfg.Agents.Packages[0].Source != "o/keep" {
		t.Fatalf("expected only o/keep to remain, got %+v", cfg.Agents.Packages)
	}
}

func TestAddSkillPackageDetectsTargetsWhenAgentsUseUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	stubBinariesOnPath(t, "codex")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "skills-source")
	writeAppSkill(t, filepath.Join(source, "skills", "one"), "one")
	a := newSkillsTestApp(t, config.AgentsConfig{})

	if _, _, err := a.AddSkillPackage(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "one", "SKILL.md")); err != nil {
		t.Fatalf("detected target was not installed: %v", err)
	}
}

func TestRemoveSkillPackage_RejectsUnmanaged(t *testing.T) {
	t.Parallel()
	a := newSkillsTestApp(t, config.AgentsConfig{})
	if err := a.RemoveSkillPackage("not-in-manifest"); err == nil {
		t.Fatal("expected error: omni must not remove packages it did not add")
	}
}

func TestRemoveSkillPackage_ClearsGroupAndIgnoreRefs(t *testing.T) {
	t.Parallel()
	a := newSkillsTestApp(t, config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "alirezarezvani/claude-skills"}, {Source: "o/keep"}},
		Ignore: config.AgentsIgnore{
			Skills: []string{"alirezarezvani/claude-skills"},
		},
	})
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Groups = []*config.GroupConfig{
			{Name: "work", Skills: []string{"alirezarezvani/claude-skills"}},
			{Name: "home", Skills: []string{"alirezarezvani/claude-skills", "o/keep"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveSkillPackage("alirezarezvani/claude-skills"); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if errs := config.ValidateRoot(cfg, config.ProviderValidation{}); len(errs) > 0 {
		t.Fatalf("config should validate after delete: %v", errs)
	}
	work := findGroupInConfig(cfg, "work")
	home := findGroupInConfig(cfg, "home")
	if len(work.Skills) != 0 {
		t.Fatalf("work group should drop deleted skill, got %v", work.Skills)
	}
	if len(home.Skills) != 1 || home.Skills[0] != "o/keep" {
		t.Fatalf("home group should keep o/keep only, got %v", home.Skills)
	}
	if len(cfg.Agents.Ignore.Skills) != 0 {
		t.Fatalf("ignore list should drop deleted skill, got %v", cfg.Agents.Ignore.Skills)
	}
}

func TestUninstallSkillPackageRemovesNativeLinksAndContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	source := filepath.Join(t.TempDir(), "taste-skills")
	writeAppSkill(t, filepath.Join(source, "skills", "taste-skill"), "taste-skill")
	a := newSkillsTestApp(t, config.AgentsConfig{
		Ignore: config.AgentsIgnore{Skills: []string{source}},
	})
	service, err := a.skillService()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), config.SkillPackage{Source: source}, []string{"claude-code", "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := a.UninstallSkillPackage(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(home, ".claude", "skills", "taste-skill"),
		filepath.Join(home, ".codex", "skills", "taste-skill"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s remains after uninstall: %v", path, err)
		}
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Skills) != 0 {
		t.Fatalf("ignore list should drop uninstalled skill, got %v", cfg.Agents.Ignore.Skills)
	}
}

func TestUninstallSkillPackageRejectsMissingNativeInstall(t *testing.T) {
	t.Parallel()
	a := newSkillsTestApp(t, config.AgentsConfig{})
	if err := a.UninstallSkillPackage(context.Background(), "ghost/pkg"); err == nil {
		t.Fatal("expected error when package has no native installation")
	}
}

func writeAppSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
