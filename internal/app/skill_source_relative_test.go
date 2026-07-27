package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func newRelativeSourceApp(t *testing.T, pkg config.SkillPackage, groups []*config.GroupConfig) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	stubBinariesOnPath(t, "claude")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeAppSkill(t, filepath.Join(dir, "skills", "skills", "demo"), "demo")
	path := filepath.Join(dir, "settings.json")
	a := New(path)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		cfg.Agents.Packages = []config.SkillPackage{pkg}
		cfg.Groups = groups
		cfg.Settings.AgentsUse = []string{"claude-code"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestRelativeSkillSourceResolvesForEveryFlow(t *testing.T) {
	t.Run("restore then uninstall", func(t *testing.T) {
		a := newRelativeSourceApp(t, config.SkillPackage{Source: "./skills"}, nil)
		res, _, err := a.RestoreSkills(context.Background(), RestoreSkillsOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Failed) != 0 || len(res.Installed) != 1 {
			t.Fatalf("restore = %+v, want one installed package", res)
		}
		if err := a.UninstallSkillPackage(context.Background(), "./skills"); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
	})

	t.Run("remove from manifest", func(t *testing.T) {
		a := newRelativeSourceApp(t, config.SkillPackage{Source: "./skills"}, nil)
		if err := a.RemoveSkillPackage("./skills"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		cfg, err := a.loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Agents.Packages) != 0 {
			t.Fatalf("packages = %+v, want empty", cfg.Agents.Packages)
		}
	})

	t.Run("set groups and agents", func(t *testing.T) {
		a := newRelativeSourceApp(t, config.SkillPackage{Source: "./skills"},
			[]*config.GroupConfig{{Name: "dev"}})
		if err := a.SetSkillGroups("./skills", []string{"dev"}, nil, ""); err != nil {
			t.Fatalf("set groups: %v", err)
		}
		if err := a.SetSkillAgents("./skills", []string{"claude-code"}); err != nil {
			t.Fatalf("set agents: %v", err)
		}
		cfg, err := a.loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		dev := findGroupInConfig(cfg, "dev")
		if dev == nil || len(dev.Skills) != 1 || dev.Skills[0] != "./skills" {
			t.Fatalf("group skills = %+v, want the manifest spelling", dev)
		}
		if got := cfg.Agents.Packages[0].Agents; len(got) != 1 || got[0] != "claude-code" {
			t.Fatalf("agents = %v, want [claude-code]", got)
		}
	})

	t.Run("absolute argument matches relative manifest entry", func(t *testing.T) {
		a := newRelativeSourceApp(t, config.SkillPackage{Source: "./skills"}, nil)
		absolute := filepath.Join(filepath.Dir(a.ConfigPath), "skills")
		if err := a.SetSkillAgents(absolute, []string{"claude-code"}); err != nil {
			t.Fatalf("set agents by absolute path: %v", err)
		}
	})
}
