package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigrateLegacyDarwinAgentsTemplate(t *testing.T) {
	t.Run("copies legacy when canonical missing", func(t *testing.T) {
		dir := t.TempDir()
		canonical, legacy := filepath.Join(dir, "new", "apm.yml"), filepath.Join(dir, "old", "apm.yml")
		writeFile(t, legacy, "name: real\ndependencies:\n  apm:\n    - git: https://example.test/plugin.git\n")
		if err := migrateLegacyDarwinAgentsTemplate(canonical, legacy); err != nil {
			t.Fatal(err)
		}
		if raw, err := os.ReadFile(canonical); err != nil || !strings.Contains(string(raw), "plugin.git") {
			t.Fatalf("canonical = %q, %v", raw, err)
		}
	})

	t.Run("real legacy replaces empty migration stub", func(t *testing.T) {
		dir := t.TempDir()
		canonical, legacy := filepath.Join(dir, "new", "apm.yml"), filepath.Join(dir, "old", "apm.yml")
		writeFile(t, canonical, agentsMigrationMarker+"\nname: empty\ndependencies: {}\n")
		writeFile(t, legacy, "name: real\ndependencies:\n  apm:\n    - git: https://example.test/plugin.git\n")
		if err := migrateLegacyDarwinAgentsTemplate(canonical, legacy); err != nil {
			t.Fatal(err)
		}
		if raw, _ := os.ReadFile(canonical); !strings.Contains(string(raw), "plugin.git") {
			t.Fatalf("canonical = %q", raw)
		}
	})

	t.Run("symlinked canonical wins over legacy", func(t *testing.T) {
		dir := t.TempDir()
		canonical, legacy := filepath.Join(dir, "new", "apm.yml"), filepath.Join(dir, "old", "apm.yml")
		target := filepath.Join(dir, "dots", "apm.yml")
		const targetBody = "name: dots\ndependencies:\n  apm:\n    - git: https://example.test/dots.git\n"
		const legacyBody = "name: legacy\ndependencies: {}\n"
		writeFile(t, target, targetBody)
		writeFile(t, legacy, legacyBody)
		if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, canonical); err != nil {
			t.Fatal(err)
		}
		if err := migrateLegacyDarwinAgentsTemplate(canonical, legacy); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(canonical)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("canonical is no longer a symlink: %v, %v", info, err)
		}
		if raw, err := os.ReadFile(target); err != nil || string(raw) != targetBody {
			t.Fatalf("symlink target = %q, %v", raw, err)
		}
		if raw, err := os.ReadFile(legacy); err != nil || string(raw) != legacyBody {
			t.Fatalf("legacy = %q, %v", raw, err)
		}
	})

	t.Run("divergent real canonical wins", func(t *testing.T) {
		dir := t.TempDir()
		canonical, legacy := filepath.Join(dir, "new", "apm.yml"), filepath.Join(dir, "old", "apm.yml")
		writeFile(t, canonical, "name: canonical\ndependencies: {}\n")
		writeFile(t, legacy, "name: legacy\ndependencies: {}\n")
		if err := migrateLegacyDarwinAgentsTemplate(canonical, legacy); err != nil {
			t.Fatal(err)
		}
		if raw, _ := os.ReadFile(canonical); !strings.Contains(string(raw), "canonical") {
			t.Fatalf("canonical overwritten: %q", raw)
		}
	})
}

func TestAgentsTemplatePathUsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := AgentsTemplatePath()
	if err != nil || got != filepath.Join(dir, "omni", "apm.yml") {
		t.Fatalf("AgentsTemplatePath = %q, %v", got, err)
	}
}

func TestAgentsTemplatePathAdoptsLegacyDarwinTemplate(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the legacy template path exists only on darwin")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	legacy := filepath.Join(home, "Library", "Application Support", "omni", "apm.yml")
	writeFile(t, legacy, agentsMigrationMarker+"\nname: legacy\nversion: 1.0.0\ndependencies: {}\n")

	canonical, err := AgentsTemplatePath()
	if err != nil {
		t.Fatalf("AgentsTemplatePath: %v", err)
	}
	raw, err := os.ReadFile(canonical)
	if err != nil || !strings.Contains(string(raw), "name: legacy") {
		t.Fatalf("canonical = %q, %v; want the legacy template adopted", raw, err)
	}
}

func TestAgentsTemplatePathLeavesSymlinkedCanonicalAlone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the legacy template path exists only on darwin")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, "Library", "Application Support", "omni", "apm.yml"),
		agentsMigrationMarker+"\nname: legacy\nversion: 1.0.0\ndependencies: {}\n")
	repo := filepath.Join(home, "dotfiles", "apm.yml")
	writeFile(t, repo, agentsMigrationMarker+"\nname: dotfiles\nversion: 1.0.0\ndependencies: {}\n")
	canonical := filepath.Join(home, ".config", "omni", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, canonical); err != nil {
		t.Fatal(err)
	}

	if _, err := AgentsTemplatePath(); err != nil {
		t.Fatalf("AgentsTemplatePath: %v", err)
	}
	raw, err := os.ReadFile(repo)
	if err != nil || !strings.Contains(string(raw), "name: dotfiles") {
		t.Fatalf("repo template = %q, %v; want it untouched through the symlink", raw, err)
	}
	info, err := os.Lstat(canonical)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("canonical is no longer a symlink: %v, %v", info, err)
	}
}
