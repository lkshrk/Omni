//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryToolSpecLifecyclePersistsProviderGroupsAndIgnore(t *testing.T) {
	root, _, cache, env, configPath := toolMaintenanceFixture(t, &config.RootConfig{
		Hosts: map[string][]string{"testhost": {"dev", "ops"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev"},
			{Name: "ops"},
		},
	})
	bin := buildOmniBinary(t)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "set", "fixture", "--provider", "node", "--package", "fixture-cli", "--install-with", "pnpm")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "groups", "move-tool", "dev", "fixture")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "ignore", "fixture")
	cfg := loadToolMaintenanceConfig(t, configPath)
	spec := cfg.Tools["fixture"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "pnpm" || spec.Providers[0].Package != "fixture-cli" || !spec.Ignore {
		t.Fatalf("tool spec = %#v", spec)
	}
	if !toolMaintenanceGroupHas(cfg, "dev", "fixture") {
		t.Fatalf("ignored/group state = spec %#v, groups %#v", spec, cfg.Groups)
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "unignore", "fixture")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "groups", "move-tool", "ops", "fixture")
	cfg = loadToolMaintenanceConfig(t, configPath)
	if cfg.Tools["fixture"].Ignore || toolMaintenanceGroupHas(cfg, "dev", "fixture") || !toolMaintenanceGroupHas(cfg, "ops", "fixture") {
		t.Fatalf("updated ignore/group state = spec %#v, groups %#v", cfg.Tools["fixture"], cfg.Groups)
	}

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "delete-spec", "fixture")
	cfg = loadToolMaintenanceConfig(t, configPath)
	if _, ok := cfg.Tools["fixture"]; ok || toolMaintenanceGroupHas(cfg, "ops", "fixture") {
		t.Fatalf("deleted tool remained = tools %#v, groups %#v", cfg.Tools, cfg.Groups)
	}
}

func TestCLIBinaryHealBrewTapsPersistsQualifiedPackage(t *testing.T) {
	root, _, cache, env, configPath := toolMaintenanceFixture(t, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"quarkdown": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "quarkdown"}}},
		},
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "quarkdown"}}}},
	})
	binDir := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(binDir, "brew"), `#!/bin/sh
set -eu
case "${1:-}" in
  --version) echo 'Homebrew 6.0.0' ;;
  info)
    printf '{"formulae":[{"name":"quarkdown","full_name":"quarkdown-labs/quarkdown/quarkdown","desc":"documents as code","installed":[]}],"casks":[]}\n'
    ;;
  *) exit 64 ;;
esac
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))

	runOmniCommand(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "tools", "heal-taps")
	spec := loadToolMaintenanceConfig(t, configPath).Tools["quarkdown"]
	if len(spec.Providers) != 1 || spec.Providers[0].Package != "quarkdown-labs/quarkdown/quarkdown" || !slices.Contains(spec.Taps, "quarkdown-labs/quarkdown") {
		t.Fatalf("healed brew spec = %#v", spec)
	}
}

func toolMaintenanceFixture(t *testing.T, cfg *config.RootConfig) (root, home, cache string, env []string, configPath string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	configPath = filepath.Join(root, "settings.json")
	cfg.Version = config.CurrentVersion
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return root, home, cache, env, configPath
}

func loadToolMaintenanceConfig(t *testing.T, path string) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func toolMaintenanceGroupHas(cfg *config.RootConfig, groupName, toolName string) bool {
	for _, group := range cfg.Groups {
		if group == nil || group.BaseName() != groupName {
			continue
		}
		for _, tool := range group.Tools {
			if tool.Name == toolName {
				return true
			}
		}
	}
	return false
}
