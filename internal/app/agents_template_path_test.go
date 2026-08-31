package app

import (
	"os"
	"path/filepath"
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
