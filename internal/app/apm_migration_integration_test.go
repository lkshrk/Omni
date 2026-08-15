//go:build integration

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	commandexec "github.com/lkshrk/omni/internal/executor"
)

func TestLegacyMigrationInstallsThroughRealAPM(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	pkg := filepath.Join(root, "migrated-skill")
	for _, dir := range []string{home, filepath.Join(home, ".codex"), pkg, filepath.Join(pkg, ".apm", "agents")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeMigrationFixture(t, filepath.Join(pkg, "apm.yml"), "name: migrated-skill\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeMigrationFixture(t, filepath.Join(pkg, "SKILL.md"), "---\nname: migrated-skill\ndescription: Migrated integration fixture\n---\n")
	writeMigrationFixture(t, filepath.Join(pkg, ".apm", "agents", "migrated.agent.md"), "---\nname: migrated\ndescription: Migrated agent fixture.\n---\n\nValidate migration.\n")
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: pkg, Agents: []string{"codex"}}},
			McpServers: []config.McpServer{{Name: "migration-http", Transport: "http", URL: "https://mcp.example.test"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath, WithEnvLookup(os.Getenv))
	a.SetFallbackExecutor(commandexec.New())
	if result, err := a.AgentsSyncAll(ctx, AgentsSyncAllOptions{}); err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, result.Output, result.Stderr)
	}
	for _, path := range []string{
		filepath.Join(home, ".apm", "apm.yml"),
		filepath.Join(home, ".apm", "apm.lock.yaml"),
		filepath.Join(home, ".agents", "skills", "migrated-skill", "SKILL.md"),
		filepath.Join(home, ".codex", "agents", "migrated.toml"),
		filepath.Join(home, ".codex", "config.toml"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected migrated output %s: %v", path, err)
		}
	}
	codexConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexConfig), "migration-http") || !strings.Contains(string(codexConfig), "https://mcp.example.test") {
		t.Fatalf("migrated MCP missing from Codex config:\n%s", codexConfig)
	}
	got, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Agents.Packages) != 0 || len(got.Agents.McpServers) != 0 {
		t.Fatalf("legacy agent entries remain after migration: %+v", got.Agents)
	}
}

func writeMigrationFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
