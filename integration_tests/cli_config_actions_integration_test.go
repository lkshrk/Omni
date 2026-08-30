//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIBinaryGroupLifecyclePersistsHostReferences(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	})
	bin := buildOmniBinary(t)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "groups", "create", "work")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "add-group", "testhost", "work")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "groups", "rename", "work", "dev")
	cfg := loadConfigActions(t, configPath)
	if configActionGroup(cfg, "work") != nil || configActionGroup(cfg, "dev") == nil || !slices.Equal(cfg.Hosts["testhost"], []string{"dev"}) {
		t.Fatalf("renamed group state = groups %#v, hosts %#v", configActionGroupNames(cfg), cfg.Hosts)
	}

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "groups", "delete", "dev")
	cfg = loadConfigActions(t, configPath)
	if configActionGroup(cfg, "dev") != nil || len(cfg.Hosts["testhost"]) != 0 {
		t.Fatalf("deleted group remained = groups %#v, hosts %#v", configActionGroupNames(cfg), cfg.Hosts)
	}
}

func TestCLIBinaryHostLifecycleCopiesAndEditsGroups(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev"},
			{Name: "ops"},
		},
	})
	bin := buildOmniBinary(t)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "source")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "set-groups", "source", "dev")
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "copy", "source", "target")
	cfg := loadConfigActions(t, configPath)
	if !slices.Equal(cfg.Hosts["target"], []string{"dev"}) || configActionGroup(cfg, "target") == nil {
		t.Fatalf("copied host state = hosts %#v, groups %#v", cfg.Hosts, configActionGroupNames(cfg))
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "set-groups", "target", "ops")
	cfg = loadConfigActions(t, configPath)
	if !slices.Equal(cfg.Hosts["target"], []string{"ops"}) {
		t.Fatalf("edited target groups = %#v", cfg.Hosts["target"])
	}

	runOmniCommand(t, bin, root, env, "--yes", "--config", configPath, "--cache-dir", cache, "hosts", "remove", "target")
	cfg = loadConfigActions(t, configPath)
	if _, ok := cfg.Hosts["target"]; ok || configActionGroup(cfg, "target") != nil {
		t.Fatalf("removed host remained = hosts %#v, groups %#v", cfg.Hosts, configActionGroupNames(cfg))
	}
	if !slices.Equal(cfg.Hosts["source"], []string{"dev"}) {
		t.Fatalf("source host changed = %#v", cfg.Hosts["source"])
	}
}

func TestCLIBinaryProviderTogglePersistsPerHost(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	})
	bin := buildOmniBinary(t)

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "disable-provider", "brew")
	cfg := loadConfigActions(t, configPath)
	if !slices.Contains(cfg.HostSettings["testhost"].DisabledProviders, "brew") {
		t.Fatalf("disabled providers = %#v", cfg.HostSettings)
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "settings", "enable-provider", "brew")
	cfg = loadConfigActions(t, configPath)
	if slices.Contains(cfg.HostSettings["testhost"].DisabledProviders, "brew") {
		t.Fatalf("brew remained disabled = %#v", cfg.HostSettings)
	}
}

func TestCLIBinarySettingsResetPreservesInventory(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		Tools: map[string]config.ToolSpec{
			"fixture": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "fixture"}}},
		},
		HostSettings: map[string]config.Settings{"testhost": {DisabledProviders: []string{"brew"}}},
	})

	runOmniCommand(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "settings", "reset")
	cfg := loadConfigActions(t, configPath)
	if cfg.Settings.AutoImport || len(cfg.HostSettings["testhost"].DisabledProviders) != 0 {
		t.Fatalf("settings were not reset = global %#v, host %#v", cfg.Settings, cfg.HostSettings["testhost"])
	}
	if _, ok := cfg.Tools["fixture"]; !ok || !slices.Equal(cfg.Hosts["testhost"], []string{"dev"}) || configActionGroup(cfg, "dev") == nil {
		t.Fatalf("reset lost inventory = tools %#v, hosts %#v, groups %#v", cfg.Tools, cfg.Hosts, configActionGroupNames(cfg))
	}
}

func TestCLIBinarySettingsResetCacheRemovesCachedTools(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	})
	seedTUIToolCache(t, cache, &database.ToolCache{Name: "fixture", Provider: "brew", Package: "fixture", Installed: true})

	runOmniCommand(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "settings", "reset-cache")
	db, err := database.Open(filepath.Join(cache, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tools, err := db.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 0 {
		t.Fatalf("cache reset retained tools = %#v", tools)
	}
}

func TestCLIBinaryMigrateHostOverridesPromotesProviderCandidates(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fixture": {Hosts: map[string]config.ToolInstallSpec{"testhost": {Provider: "brew", Package: "fixture-cli"}}},
		},
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}}},
	})

	runOmniCommand(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "settings", "migrate-host-overrides")
	spec := loadConfigActions(t, configPath).Tools["fixture"]
	if len(spec.Hosts) != 0 || len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Package != "fixture-cli" {
		t.Fatalf("migrated tool spec = %#v", spec)
	}
}

func TestCLIBinarySettingsExtractPreservesEffectiveConfig(t *testing.T) {
	root, _, cache, env, configPath := configActionsFixture(t, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		Tools: map[string]config.ToolSpec{
			"fixture": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "fixture"}}, Dots: []config.DotEntry{{Name: "git", Path: "~/.gitconfig"}}},
		},
	})

	runOmniCommand(t, buildOmniBinary(t), root, env, "--yes", "--config", configPath, "--cache-dir", cache, "settings", "extract")
	cfg := loadConfigActions(t, configPath)
	dev := configActionGroup(cfg, "dev")
	_, toolOK := cfg.Tools["fixture"]
	if !cfg.Settings.AutoImport || !toolOK || !slices.Equal(cfg.Hosts["testhost"], []string{"dev"}) || dev == nil || len(dev.Tools) != 1 || len(dev.Dots) != 1 {
		t.Fatalf("effective config changed after extract = %#v", cfg)
	}
	mainKeys := configActionJSONKeys(t, configPath)
	if _, ok := mainKeys["tools"]; ok {
		t.Fatal("settings.json still contains tools after extract")
	}
	if _, ok := mainKeys["groups"]; ok {
		t.Fatal("settings.json still contains groups after extract")
	}
	for _, name := range []string{"tools.json", "groups.json", "dots.json"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "settings.d", name)); err != nil {
			t.Fatalf("missing extracted fragment %s: %v", name, err)
		}
	}
}

func configActionsFixture(t *testing.T, cfg *config.RootConfig) (root, home, cache string, env []string, configPath string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	configPath = filepath.Join(root, "settings.json")
	cfg.Version = config.CurrentVersion
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return root, home, cache, env, configPath
}

func loadConfigActions(t *testing.T, path string) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func configActionGroup(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, group := range cfg.Groups {
		if group != nil && group.BaseName() == name {
			return group
		}
	}
	return nil
}

func configActionGroupNames(cfg *config.RootConfig) []string {
	names := make([]string, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group != nil {
			names = append(names, group.BaseName())
		}
	}
	return names
}

func configActionJSONKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}
