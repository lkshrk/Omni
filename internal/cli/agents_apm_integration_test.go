//go:build integration

package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	commandexec "github.com/lkshrk/omni/internal/executor"
)

func TestAgentsSyncRunsRealAPMFromGlobalManifest(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockAPMExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	codexHome := filepath.Join(root, "codex-home")
	pkg := filepath.Join(root, "fixture-skill")
	for _, dir := range []string{filepath.Join(home, ".apm"), codexHome, pkg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeIntegrationFile(t, filepath.Join(pkg, "apm.yml"), "name: fixture-skill\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeIntegrationFile(t, filepath.Join(pkg, "SKILL.md"), "---\nname: fixture-skill\ndescription: CLI integration fixture\n---\n")
	writeIntegrationFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: omni-integration\nversion: 1.0.0\ntargets:\n  - codex\ndependencies:\n  apm:\n    - "+pkg+"\n  mcp: []\n")
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath, app.WithEnvLookup(os.Getenv))
	a.SetFallbackExecutor(commandexec.New())
	cmd := newAgentsCmd(&rootState{app: a})
	cmd.SetContext(ctx)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"sync"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "fixture-skill", "SKILL.md")); err != nil {
		t.Fatalf("APM did not deploy the fixture: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.lock.yaml")); err != nil {
		t.Fatalf("APM did not create its lockfile: %v", err)
	}
}

func TestAgentsCommandsRunRealOfflineAPMLifecycle(t *testing.T) {
	if _, err := exec.LookPath("apm"); err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockAPMExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	market := filepath.Join(root, "marketplace")
	pkg := filepath.Join(market, "packages", "searchable-skill")
	for _, dir := range []string{filepath.Join(home, ".apm"), pkg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeIntegrationFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: lifecycle\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm: []\n  mcp: []\n")
	writeIntegrationFile(t, filepath.Join(pkg, "apm.yml"), "name: searchable-skill\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeIntegrationFile(t, filepath.Join(pkg, "SKILL.md"), "---\nname: searchable-skill\ndescription: Searchable offline fixture\n---\n")
	writeIntegrationFile(t, filepath.Join(market, "apm.yml"), "name: local-marketplace\nversion: 0.1.0\nmarketplace:\n  owner:\n    name: omni\n    url: https://example.invalid/omni\n  outputs:\n    claude: {}\n  packages:\n    - name: searchable-skill\n      description: Searchable offline fixture\n      source: ./packages/searchable-skill\n      version: 1.0.0\n")
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	a := app.New(configPath, app.WithEnvLookup(os.Getenv))
	a.SetFallbackExecutor(commandexec.New())
	run := func(args ...string) (string, string, error) {
		cmd := newAgentsCmd(&rootState{app: a})
		cmd.SetContext(ctx)
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return stdout.String(), stderr.String(), err
	}
	if out, errOut, err := run("add", pkg); err != nil {
		t.Fatalf("add: %v\n%s\n%s", err, out, errOut)
	}
	deployed := filepath.Join(home, ".agents", "skills", "searchable-skill", "SKILL.md")
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("add did not deploy skill: %v", err)
	}
	if out, errOut, err := run("update", pkg); err != nil {
		t.Fatalf("update: %v\n%s\n%s", err, out, errOut)
	}
	pack := exec.CommandContext(ctx, "apm", "pack", "--marketplace=claude")
	pack.Dir, pack.Env = market, os.Environ()
	if output, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("pack marketplace: %v\n%s", err, output)
	}
	register := exec.CommandContext(ctx, "apm", "marketplace", "add", market, "--name", "local")
	register.Env = os.Environ()
	if output, err := register.CombinedOutput(); err != nil {
		t.Fatalf("register marketplace: %v\n%s", err, output)
	}
	if out, errOut, err := run("search", "searchable@local"); err != nil || !strings.Contains(out, "searchable-skill") {
		t.Fatalf("search: err=%v stdout=%q stderr=%q", err, out, errOut)
	}
	if out, errOut, err := run("remove", pkg); err != nil {
		t.Fatalf("remove: %v\n%s\n%s", err, out, errOut)
	}
	if _, err := os.Stat(deployed); !os.IsNotExist(err) {
		t.Fatalf("remove left deployed skill: %v", err)
	}
}

func blockAPMExternalNetwork(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
}

func writeIntegrationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
