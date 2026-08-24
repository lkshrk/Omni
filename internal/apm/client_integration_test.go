//go:build integration

package apm_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	commandexec "github.com/lkshrk/omni/internal/executor"
	"gopkg.in/yaml.v3"
)

func TestGlobalInstallReplaysManifestIntoIsolatedUserScope(t *testing.T) {
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	apmHome := filepath.Join(root, "apm-home")
	codexHome := filepath.Join(root, "codex-home")
	work := filepath.Join(root, "work")
	pkg := filepath.Join(root, "fixture-skill")
	for _, dir := range []string{filepath.Join(home, ".apm"), apmHome, codexHome, work, pkg} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("APM_HOME", apmHome)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	t.Chdir(work)

	writeFile(t, filepath.Join(pkg, "apm.yml"), `name: fixture-skill
version: 1.0.0
type: skill
dependencies:
  apm: []
  mcp: []
`)
	writeFile(t, filepath.Join(pkg, "SKILL.md"), `---
name: fixture-skill
description: Offline APM integration fixture
---

# Fixture skill
`)
	writeFile(t, filepath.Join(pkg, ".apm", "agents", "reviewer.agent.md"), "---\nname: reviewer\ndescription: Reviews integration fixtures.\n---\n\nReview the fixture.\n")
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: omni-integration\nversion: 1.0.0\ntargets:\n  - codex\n  - claude\ndependencies:\n  apm:\n    - "+pkg+"\n  mcp: []\n")

	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{DryRun: true}); err != nil {
		t.Fatalf("global dry-run: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.lock.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a lockfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "fixture-skill", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run deployed a skill: %v", err)
	}
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{}); err != nil {
		t.Fatalf("initial global install: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}

	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	if content, err := os.ReadFile(lock); err != nil || !strings.Contains(string(content), "fixture-skill") {
		t.Fatalf("lockfile missing installed package: error=%v content=%q", err, content)
	}
	deployed := filepath.Join(home, ".agents", "skills", "fixture-skill", "SKILL.md")
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("skill was not deployed: %v", err)
	}
	for _, path := range []string{
		filepath.Join(home, ".codex", "agents", "reviewer.toml"),
		filepath.Join(home, ".claude", "agents", "reviewer.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("target-specific agent was not deployed to %s: %v", path, err)
		}
	}

	if err := os.RemoveAll(filepath.Dir(deployed)); err != nil {
		t.Fatal(err)
	}
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Frozen: true}); err != nil {
		t.Fatalf("frozen global install: %v\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("frozen manifest install did not restore skill: %v", err)
	}
}

func TestGlobalPackageLifecycleAndLocalMarketplaceSearch(t *testing.T) {
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)
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
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: lifecycle\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm:\n    - "+pkg+"\n  mcp: []\n")
	writeFile(t, filepath.Join(pkg, "apm.yml"), "name: searchable-skill\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeFile(t, filepath.Join(pkg, "SKILL.md"), "---\nname: searchable-skill\ndescription: Searchable offline fixture\n---\n")
	writeFile(t, filepath.Join(market, "apm.yml"), "name: local-marketplace\nversion: 0.1.0\nmarketplace:\n  owner:\n    name: omni\n    url: https://example.invalid/omni\n  outputs:\n    claude: {}\n  packages:\n    - name: searchable-skill\n      description: Searchable offline fixture\n      source: ./packages/searchable-skill\n      version: 1.0.0\n")

	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{}); err != nil {
		t.Fatalf("install: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	deployed := filepath.Join(home, ".agents", "skills", "searchable-skill", "SKILL.md")
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("install did not deploy skill: %v", err)
	}
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Update: true}); err != nil {
		t.Fatalf("update: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}

	pack := exec.CommandContext(ctx, "apm", "pack", "--marketplace=claude")
	pack.Dir = market
	pack.Env = os.Environ()
	if output, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("pack marketplace: %v\n%s", err, output)
	}
	register := exec.CommandContext(ctx, "apm", "marketplace", "add", market, "--name", "local")
	register.Env = os.Environ()
	if output, err := register.CombinedOutput(); err != nil {
		t.Fatalf("register marketplace: %v\n%s", err, output)
	}
	if result, err := apm.New(commandexec.New(), apm.Project).Search(ctx, "searchable@local"); err != nil || !strings.Contains(result.Stdout, "searchable-skill") {
		t.Fatalf("search: err=%v stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	if result, err := client.Uninstall(ctx, pkg); err != nil {
		t.Fatalf("remove: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	if _, err := os.Stat(deployed); !os.IsNotExist(err) {
		t.Fatalf("remove left deployed skill: %v", err)
	}
}

func TestGlobalPluginMarketplaceLifecycle(t *testing.T) {
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	market := filepath.Join(root, "marketplace")
	pkg := filepath.Join(market, "packages", "fixture-plugin")
	for _, dir := range []string{filepath.Join(home, ".apm"), filepath.Join(pkg, "agents"), filepath.Join(pkg, "skills", "fixture-plugin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(pkg, "plugin.json"), `{"name":"fixture-plugin","version":"1.0.0","description":"Offline plugin fixture"}`)
	writeFile(t, filepath.Join(pkg, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews offline plugin fixtures\n---\n\nReview the fixture.\n")
	writeFile(t, filepath.Join(pkg, "skills", "fixture-plugin", "SKILL.md"), "---\nname: fixture-plugin\ndescription: Offline plugin fixture\n---\n\nversion one\n")
	writeFile(t, filepath.Join(market, "apm.yml"), `name: local-marketplace
version: 1.0.0
license: MIT
marketplace:
  owner: {name: omni, url: https://example.invalid/omni}
  outputs: {claude: {}, codex: {}}
  packages:
    - name: fixture-plugin
      description: Offline plugin fixture
      category: testing
      source: ./packages/fixture-plugin
      version: 1.0.0
`)
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "apm", args...)
		cmd.Dir = dir
		cmd.Env = os.Environ()
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("apm %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return string(output)
	}

	run(market, "pack", "--marketplace=claude")
	run(root, "marketplace", "add", market, "--name", "local")
	registry := filepath.Join(home, ".apm", "marketplaces.json")
	if content, err := os.ReadFile(registry); err != nil || !strings.Contains(string(content), `"local"`) {
		t.Fatalf("marketplace add did not register exact name local: err=%v content=%q", err, content)
	}
	if output := run(root, "search", "fixture@local"); !strings.Contains(output, "fixture-plugin") {
		t.Fatalf("marketplace search omitted fixture-plugin: %s", output)
	}
	run(root, "install", "-g", "fixture-plugin@local", "--target", "claude,codex")
	deployed := []string{
		filepath.Join(home, ".claude", "agents", "reviewer.md"),
		filepath.Join(home, ".codex", "agents", "reviewer.toml"),
		filepath.Join(home, ".claude", "skills", "fixture-plugin", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "fixture-plugin", "SKILL.md"),
	}
	modulePlugin := filepath.Join(home, ".apm", "apm_modules", "_local", "fixture-plugin", "plugin.json")
	if content, err := os.ReadFile(modulePlugin); err != nil || !strings.Contains(string(content), `"name":"fixture-plugin"`) {
		t.Fatalf("APM did not register the plugin module: err=%v content=%q", err, content)
	}
	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	if content, err := os.ReadFile(lock); err != nil || !strings.Contains(string(content), "package_type: marketplace_plugin") {
		t.Fatalf("APM lock did not identify a marketplace plugin: err=%v content=%q", err, content)
	}
	for _, path := range deployed {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("plugin primitive was not materialized to %s: %v", path, err)
		}
	}

	writeFile(t, filepath.Join(pkg, "skills", "fixture-plugin", "SKILL.md"), "---\nname: fixture-plugin\ndescription: Offline plugin fixture\n---\n\nversion two\n")
	run(root, "marketplace", "update", "local")
	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Update: true}); err != nil {
		t.Fatalf("update plugin: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	for _, path := range deployed[2:] {
		if content, err := os.ReadFile(path); err != nil || !strings.Contains(string(content), "version two") {
			t.Fatalf("plugin update did not refresh %s: err=%v content=%q", path, err, content)
		}
	}

	if result, err := client.Uninstall(ctx, pkg); err != nil {
		t.Fatalf("uninstall plugin: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	for _, path := range deployed {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("plugin uninstall left %s: %v", path, err)
		}
	}
	if content, err := os.ReadFile(lock); err != nil && !os.IsNotExist(err) || strings.Contains(string(content), "fixture-plugin") {
		t.Fatalf("plugin uninstall left lock ownership: err=%v content=%q", err, content)
	}
	run(root, "marketplace", "remove", "local", "--yes")
	if content, err := os.ReadFile(registry); err != nil || strings.Contains(string(content), `"local"`) {
		t.Fatalf("marketplace removal left exact registration local: err=%v content=%q", err, content)
	}
}

var stableGlobalMCPTargets = []struct {
	name string
	path string
}{
	{"antigravity", filepath.Join(".gemini", "config", "mcp_config.json")},
	{"claude", ".claude.json"},
	{"codex", filepath.Join(".codex", "config.toml")},
	{"copilot", filepath.Join(".copilot", "mcp-config.json")},
	{"gemini", filepath.Join(".gemini", "settings.json")},
	{"hermes", filepath.Join(".hermes", "config.yaml")},
	{"kiro", filepath.Join(".kiro", "settings", "mcp.json")},
	{"windsurf", filepath.Join(".codeium", "windsurf", "mcp_config.json")},
}

func TestGlobalMCPLifecycleForEveryStableTarget(t *testing.T) {
	for _, target := range stableGlobalMCPTargets {
		t.Run(target.name, func(t *testing.T) {
			ctx, client, home := newGlobalMCPIntegrationClient(t, "["+target.name+"]", `
    - name: omni-test
      registry: false
      transport: stdio
      command: echo
      args: [hello]
`)
			if result, err := client.InstallOnly(ctx, apm.SurfaceMcp, nil, apm.InstallOptions{}); err != nil {
				t.Fatalf("install MCP: %v\n%s\n%s", err, result.Stdout, result.Stderr)
			}
			configPath := filepath.Join(home, target.path)
			if content, err := os.ReadFile(configPath); err != nil || !strings.Contains(string(content), "omni-test") {
				t.Fatalf("%s missing omni-test: err=%v content=%q", configPath, err, content)
			}
			writeFile(t, filepath.Join(home, ".apm", "apm.yml"), globalMCPManifest("["+target.name+"]", " []"))
			if result, err := client.InstallOnly(ctx, apm.SurfaceMcp, nil, apm.InstallOptions{}); err != nil {
				t.Fatalf("remove MCP: %v\n%s\n%s", err, result.Stdout, result.Stderr)
			}
			for _, path := range []string{configPath, filepath.Join(home, ".apm", "apm.lock.yaml")} {
				content, err := os.ReadFile(path)
				if err != nil || strings.Contains(string(content), "omni-test") {
					t.Fatalf("%s retained omni-test: err=%v content=%q", path, err, content)
				}
			}
		})
	}
}

func TestGlobalMCPInstallRejectsWorkspaceOnlyTargets(t *testing.T) {
	for _, target := range []string{"cursor", "opencode"} {
		t.Run(target, func(t *testing.T) {
			ctx, client, _ := newGlobalMCPIntegrationClient(t, "["+target+"]", `
    - name: omni-test
      registry: false
      transport: stdio
      command: echo
`)
			result, err := client.InstallOnly(ctx, apm.SurfaceMcp, nil, apm.InstallOptions{})
			if err == nil {
				t.Fatalf("expected user-scope %s install to fail", target)
			}
			if output := result.Stdout + result.Stderr + err.Error(); !strings.Contains(output, "workspace-only") {
				t.Fatalf("error lacks workspace-only detail: %v\n%s\n%s", err, result.Stdout, result.Stderr)
			}
		})
	}
}

func TestGlobalSkillSubsetPersistsAcrossRepeatedInstalls(t *testing.T) {
	ctx, client, home, bundle := newGlobalSkillBundleIntegrationClient(t)

	if result, err := client.Run(ctx, "install", "-g", bundle, "--target", "codex", "--skill", "alpha"); err != nil {
		t.Fatalf("install alpha: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	assertPathExists(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"))
	assertPathMissing(t, filepath.Join(home, ".agents", "skills", "beta", "SKILL.md"))
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.yml"), "skills:", "- alpha")
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "skill_subset:", "- alpha")

	if result, err := client.Run(ctx, "install", "-g", bundle, "--target", "codex", "--skill", "beta"); err != nil {
		t.Fatalf("add beta: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	assertPathExists(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"))
	assertPathExists(t, filepath.Join(home, ".agents", "skills", "beta", "SKILL.md"))
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.yml"), "- alpha", "- beta")
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "- alpha", "- beta")
}

func TestGlobalSkillSubsetRemovalPersistsOnReplay(t *testing.T) {
	ctx, client, home, bundle := newGlobalSkillBundleIntegrationClient(t)
	if result, err := client.Run(ctx, "install", "-g", bundle, "--target", "codex", "--skill", "alpha", "--skill", "beta"); err != nil {
		t.Fatalf("install subset: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}

	manifest := filepath.Join(home, ".apm", "apm.yml")
	removeSkillFromManifest(t, manifest, "beta")
	if result, err := client.Run(ctx, "install", "-g"); err != nil {
		t.Fatalf("replay reduced subset: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	assertPathExists(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"))
	assertPathMissing(t, filepath.Join(home, ".agents", "skills", "beta", "SKILL.md"))
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "- alpha")
	assertFileOmits(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "- beta")
}

func TestGlobalOutdatedRunsFromGlobalWorkspace(t *testing.T) {
	ctx, client, home, bundle := newGlobalSkillBundleIntegrationClient(t)
	if result, err := client.Run(ctx, "install", "-g", bundle, "--target", "codex", "--skill", "alpha"); err != nil {
		t.Fatalf("install fixture: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	outside := t.TempDir()
	t.Chdir(outside)

	if result, err := client.Run(ctx, "outdated", "-g"); err != nil {
		t.Fatalf("global outdated from %s: %v\n%s\n%s", outside, err, result.Stdout, result.Stderr)
	}
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "alpha")
}

func TestGlobalAuditSucceedsAndDetectsDeploymentTamper(t *testing.T) {
	ctx, client, home, bundle := newGlobalSkillBundleIntegrationClient(t)
	if result, err := client.Run(ctx, "install", "-g", bundle, "--target", "codex", "--skill", "alpha"); err != nil {
		t.Fatalf("install fixture: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	t.Chdir(t.TempDir())

	result, err := client.Run(ctx, "audit", "--ci", "--format", "json", "--no-policy")
	if err != nil || !strings.Contains(result.Stdout, `"passed": true`) {
		t.Fatalf("clean global audit: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	writeFile(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"), "tampered\n")
	result, err = client.Run(ctx, "audit", "--ci", "--format", "json", "--no-policy")
	if err == nil || !strings.Contains(result.Stdout+result.Stderr, `"passed": false`) {
		t.Fatalf("tampered global audit: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
}

func TestMarketplaceListBrowseAndValidateUseRegisteredFixture(t *testing.T) {
	ctx, client, home, market := newMarketplaceIntegrationClient(t)

	if result, err := client.Run(ctx, "marketplace", "add", market, "--name", "local"); err != nil {
		t.Fatalf("marketplace add: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	if result, err := client.Run(ctx, "marketplace", "list"); err != nil || !strings.Contains(result.Stdout, "local") {
		t.Fatalf("marketplace list: err=%v stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	if result, err := client.Run(ctx, "marketplace", "browse", "local"); err != nil || !strings.Contains(result.Stdout, "fixture-plugin") {
		t.Fatalf("marketplace browse: err=%v stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
	}
	if result, err := client.Run(ctx, "marketplace", "validate", "local"); err != nil {
		t.Fatalf("marketplace validate: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	assertFileContainsAll(t, filepath.Join(home, ".apm", "marketplaces.json"), `"local"`)
}

func TestRepeatedConcurrentFrozenGlobalInstallLeavesWorkspaceRecoverable(t *testing.T) {
	ctx, client, home, bundle := newGlobalSkillBundleIntegrationClient(t)
	if result, err := client.Run(ctx, "install", "-g", bundle, "--target", "codex"); err != nil {
		t.Fatalf("initial install: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}

	for round := 0; round < 3; round++ {
		errs := make(chan error, 4)
		for worker := 0; worker < cap(errs); worker++ {
			go func() {
				result, err := client.Run(ctx, "install", "-g", "--frozen")
				if err != nil {
					errs <- fmt.Errorf("%w\n%s\n%s", err, result.Stdout, result.Stderr)
					return
				}
				errs <- nil
			}()
		}
		for worker := 0; worker < cap(errs); worker++ {
			if err := <-errs; err != nil {
				t.Fatalf("round %d concurrent install: %v", round, err)
			}
		}
	}
	if result, err := client.Run(ctx, "install", "-g", "--frozen"); err != nil {
		t.Fatalf("workspace was not recoverable after concurrent installs: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}

	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.yml"), bundle)
	assertFileContainsAll(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "alpha", "beta")
	assertPathExists(t, filepath.Join(home, ".agents", "skills", "alpha", "SKILL.md"))
	assertPathExists(t, filepath.Join(home, ".agents", "skills", "beta", "SKILL.md"))
}

func newGlobalSkillBundleIntegrationClient(t *testing.T) (context.Context, *apm.Client, string, string) {
	t.Helper()
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bundle := filepath.Join(root, "bundle")
	for _, dir := range []string{filepath.Join(home, ".apm"), filepath.Join(bundle, "skills", "alpha"), filepath.Join(bundle, "skills", "beta")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(bundle, "apm.yml"), "name: fixture-bundle\nversion: 1.0.0\ndependencies:\n  apm: []\n  mcp: []\n")
	writeFile(t, filepath.Join(bundle, "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: Alpha fixture\n---\n")
	writeFile(t, filepath.Join(bundle, "skills", "beta", "SKILL.md"), "---\nname: beta\ndescription: Beta fixture\n---\n")
	return ctx, apm.New(commandexec.New(), apm.Global), home, bundle
}

func newMarketplaceIntegrationClient(t *testing.T) (context.Context, *apm.Client, string, string) {
	t.Helper()
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	market := filepath.Join(root, "marketplace")
	pkg := filepath.Join(market, "packages", "fixture-plugin")
	for _, dir := range []string{filepath.Join(home, ".apm"), filepath.Join(pkg, "skills", "fixture-plugin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(pkg, "plugin.json"), `{"name":"fixture-plugin","version":"1.0.0","description":"Offline plugin fixture"}`)
	writeFile(t, filepath.Join(pkg, "skills", "fixture-plugin", "SKILL.md"), "---\nname: fixture-plugin\ndescription: Offline plugin fixture\n---\n")
	writeFile(t, filepath.Join(market, "apm.yml"), "name: local-marketplace\nversion: 1.0.0\nlicense: MIT\nmarketplace:\n  owner: {name: omni, url: https://example.invalid/omni}\n  outputs: {claude: {}, codex: {}}\n  packages:\n    - name: fixture-plugin\n      description: Offline plugin fixture\n      category: testing\n      source: ./packages/fixture-plugin\n      version: 1.0.0\n")
	cmd := exec.CommandContext(ctx, "apm", "pack", "--marketplace=claude")
	cmd.Dir = market
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pack marketplace: %v\n%s", err, output)
	}
	return ctx, apm.New(commandexec.New(), apm.Global), home, market
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent: %v", path, err)
	}
}

func assertFileContainsAll(t *testing.T, path string, values ...string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !bytes.Contains(content, []byte(value)) {
			t.Errorf("%s does not contain %q:\n%s", path, value, content)
		}
	}
}

func assertFileOmits(t *testing.T, path, value string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte(value)) {
		t.Errorf("%s still contains %q:\n%s", path, value, content)
	}
}

func removeSkillFromManifest(t *testing.T, path, name string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	removed := false
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == "skills" && node.Content[i+1].Kind == yaml.SequenceNode {
					sequence := node.Content[i+1]
					for j, skill := range sequence.Content {
						if skill.Value == name {
							sequence.Content = append(sequence.Content[:j], sequence.Content[j+1:]...)
							removed = true
							break
						}
					}
				}
			}
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(&document)
	if !removed {
		t.Fatalf("skill %q not found in manifest:\n%s", name, content)
	}
	updated, err := yaml.Marshal(&document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newGlobalMCPIntegrationClient(t *testing.T, targets, mcp string) (context.Context, *apm.Client, string) {
	t.Helper()
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), globalMCPManifest(targets, mcp))
	return ctx, apm.New(commandexec.New(), apm.Global), home
}

func globalMCPManifest(targets, mcp string) string {
	return "name: mcp-integration\nversion: 1.0.0\ntargets: " + targets + "\ndependencies:\n  apm: []\n  mcp:" + mcp + "\n"
}

// APM reports this failure on stdout only; the wrapper must surface it as error detail.
func TestGlobalInstallFailureSurfacesStdoutDetail(t *testing.T) {
	requirePinnedAPM(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))

	_, err := apm.New(commandexec.New(), apm.Global).InstallOnly(ctx, apm.SurfacePackages, []string{"claude"}, apm.InstallOptions{Frozen: true})
	if err == nil {
		t.Fatal("expected the replay to fail without a global manifest")
	}
	if detail := strings.Join(strings.Fields(err.Error()), ""); !strings.Contains(detail, "apm.ymlfound") {
		t.Fatalf("error lacks apm stdout detail: %v", err)
	}
}

func TestGlobalUpdateRefreshesRemoteGitPackage(t *testing.T) {
	requirePinnedAPM(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("integration tests require git on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	blockExternalNetwork(t)

	root := t.TempDir()
	home := filepath.Join(root, "home")
	remote := filepath.Join(root, "owner", "fixture.git")
	work := filepath.Join(root, "work")
	for _, dir := range []string{filepath.Join(home, ".apm"), work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	runGit(t, root, "config", "--global", "http.sslVerify", "false")
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init", "--bare", remote)
	runGit(t, work, "init", "-b", "main")
	runGit(t, work, "config", "user.email", "integration@example.test")
	runGit(t, work, "config", "user.name", "APM Integration")
	writeFile(t, filepath.Join(work, "apm.yml"), "name: update-fixture\nversion: 1.0.0\ntype: skill\ndependencies:\n  apm: []\n  mcp: []\n")
	writeFile(t, filepath.Join(work, "SKILL.md"), "---\nname: update-fixture\ndescription: Version one\n---\n\nversion one\n")
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-m", "v1")
	runGit(t, work, "remote", "add", "origin", remote)
	runGit(t, work, "push", "-u", "origin", "main")

	server := httptest.NewTLSServer(gitHTTPHandler(root))
	t.Cleanup(server.Close)
	gitURL := server.URL + "/owner/fixture"
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: update-integration\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm:\n    - git: "+gitURL+"\n      ref: main\n  mcp: []\n")
	client := apm.New(commandexec.New(), apm.Global)
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{}); err != nil {
		t.Fatalf("install v1: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	deployed := filepath.Join(home, ".agents", "skills", "fixture", "SKILL.md")
	if content, err := os.ReadFile(deployed); err != nil || !strings.Contains(string(content), "version one") {
		t.Fatalf("v1 deployment: err=%v content=%q", err, content)
	}
	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	lockV1, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(work, "SKILL.md"), "---\nname: update-fixture\ndescription: Version two\n---\n\nversion two\n")
	runGit(t, work, "add", "SKILL.md")
	runGit(t, work, "commit", "-m", "v2")
	runGit(t, work, "push", "origin", "main")
	if result, err := client.InstallOnly(ctx, apm.SurfacePackages, nil, apm.InstallOptions{Update: true}); err != nil {
		t.Fatalf("update v2: %v\n%s\n%s", err, result.Stdout, result.Stderr)
	}
	if content, err := os.ReadFile(deployed); err != nil || !strings.Contains(string(content), "version two") {
		t.Fatalf("v2 deployment: err=%v content=%q", err, content)
	}
	if lockV2, err := os.ReadFile(lock); err != nil || bytes.Equal(lockV1, lockV2) {
		t.Fatalf("lockfile did not change: err=%v", err)
	}
}

func requirePinnedAPM(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("apm")
	if err != nil {
		t.Fatalf("integration tests require apm 0.28.0+omni.7 on PATH: %v", err)
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("read APM version: %v\n%s", err, output)
	}
	fields := strings.Fields(string(output))
	if !slices.Contains(fields, "0.28.0+omni.7") {
		t.Fatalf("integration tests require exactly apm 0.28.0+omni.7, got %q", strings.TrimSpace(string(output)))
	}
}

func blockExternalNetwork(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitHTTPHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := exec.Command("git", "http-backend")
		cmd.Env = append(os.Environ(),
			"GIT_PROJECT_ROOT="+root,
			"GIT_HTTP_EXPORT_ALL=1",
			"PATH_INFO="+r.URL.Path,
			"REQUEST_METHOD="+r.Method,
			"QUERY_STRING="+r.URL.RawQuery,
			"CONTENT_TYPE="+r.Header.Get("Content-Type"),
			fmt.Sprintf("CONTENT_LENGTH=%d", r.ContentLength),
		)
		cmd.Stdin = r.Body
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			http.Error(w, stderr.String(), http.StatusInternalServerError)
			return
		}
		parts := bytes.SplitN(output, []byte("\r\n\r\n"), 2)
		if len(parts) != 2 {
			http.Error(w, "invalid git http-backend response", http.StatusInternalServerError)
			return
		}
		status := http.StatusOK
		for _, line := range strings.Split(string(parts[0]), "\r\n") {
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			if name == "Status" {
				_, _ = fmt.Sscanf(value, "%d", &status)
			} else {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(parts[1])
	})
}
