package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestUpsertPackage(t *testing.T) {
	existing := []config.SkillPackage{{Source: "a/b", Ref: "main"}}
	got, added := upsertPackage(existing, "c/d", "v1")
	if !added || len(got) != 2 || got[1].Source != "c/d" || got[1].Ref != "v1" {
		t.Fatalf("add new: got=%+v added=%v", got, added)
	}
	got2, added2 := upsertPackage(existing, "a/b", "next")
	if added2 || len(got2) != 1 || got2[0].Ref != "next" {
		t.Fatalf("update existing: got=%+v added=%v", got2, added2)
	}
}

func TestRemoveSkillPackage_PersistsRemoval(t *testing.T) {
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

func TestRemoveSkillPackage_RejectsUnmanaged(t *testing.T) {
	a := newSkillsTestApp(t, config.AgentsConfig{})
	if err := a.RemoveSkillPackage("not-in-manifest"); err == nil {
		t.Fatal("expected error: omni must not remove packages it did not add")
	}
}

func TestRemoveSkillPackage_ClearsGroupAndIgnoreRefs(t *testing.T) {
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

type recordingSkillRemoveExecutor struct {
	runner string
	args   []string
}

func (e *recordingSkillRemoveExecutor) Run(_ context.Context, name string, args ...string) (string, string, error) {
	e.runner = name
	e.args = append([]string(nil), args...)
	return "", "", nil
}

func TestUninstallSkillPackage_RunsSkillsRemoveForLockfilePackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"taste-skill": {Source: "Leonxlnx/taste-skill"},
		},
	})
	claudeSkills := filepath.Join(home, ".claude", "skills", "taste-skill")
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	codexSkills := filepath.Join(home, ".codex", "skills", "taste-skill")
	if err := os.MkdirAll(codexSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	stubBinariesOnPath(t, "claude", "codex")

	a := newSkillsTestApp(t, config.AgentsConfig{
		Ignore: config.AgentsIgnore{Skills: []string{"Leonxlnx/taste-skill"}},
	})
	exec := &recordingSkillRemoveExecutor{}
	a.SetFallbackExecutor(exec)

	if err := a.UninstallSkillPackage(context.Background(), "Leonxlnx/taste-skill"); err != nil {
		t.Fatal(err)
	}
	want := []string{"skills", "remove", "-g", "-a", "claude-code", "codex", "-y", "taste-skill"}
	if exec.runner != "npx" || !reflect.DeepEqual(exec.args, want) {
		t.Fatalf("skills remove cmd = %s %v, want npx %v", exec.runner, exec.args, want)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Skills) != 0 {
		t.Fatalf("ignore list should drop uninstalled skill, got %v", cfg.Agents.Ignore.Skills)
	}
}

func TestUninstallSkillPackage_RejectsMissingLockfileEntries(t *testing.T) {
	a := newSkillsTestApp(t, config.AgentsConfig{})
	if err := a.UninstallSkillPackage(context.Background(), "ghost/pkg"); err == nil {
		t.Fatal("expected error when package has no lockfile entries")
	}
}

func TestUninstallSkillPackageExitZeroFailureMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	writeSkillLockFixture(t, home, config.SkillLockFile{
		Version: 3,
		Skills: map[string]config.SkillLockEntry{
			"taste-skill": {Source: "Leonxlnx/taste-skill"},
		},
	})
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "taste-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubBinariesOnPath(t, "claude")

	a := newSkillsTestApp(t, config.AgentsConfig{
		Ignore: config.AgentsIgnore{Skills: []string{"Leonxlnx/taste-skill"}},
	})
	a.SetFallbackExecutor(&fixedOutputExecutor{stdout: "✘ Failed to remove skill"})

	err := a.UninstallSkillPackage(context.Background(), "Leonxlnx/taste-skill")
	if err == nil || !strings.Contains(err.Error(), "exited 0 but reported failure") {
		t.Fatalf("UninstallSkillPackage err = %v, want exit-0 failure-marker error", err)
	}
	cfg, err := a.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Agents.Ignore.Skills) != 1 {
		t.Fatalf("ignore list must be untouched on failed uninstall, got %v", cfg.Agents.Ignore.Skills)
	}
}
