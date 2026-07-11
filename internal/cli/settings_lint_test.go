package cli_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/cli"
	"github.com/lkshrk/omni/internal/config"
)

func TestSettingsLint_ReportsWarningsAndExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{
			"rg": {
				Hosts: map[string]config.ToolInstallSpec{
					"box": {Provider: "apt", Package: "ripgrep"},
				},
				Providers: []config.ToolInstallSpec{
					{
						Provider: "script",
						Options: map[string]string{
							"install": "curl -fsSL https://github.com/BurntSushi/ripgrep/releases/latest/download/rg.tar.gz",
							"check":   "true",
						},
					},
				},
			},
		},
		Groups: []*config.GroupConfig{
			{Name: "a", Tools: []config.ToolEntry{{Name: "rg"}}},
			{Name: "b", Tools: []config.ToolEntry{{Name: "rg"}}},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := cli.NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "settings", "lint"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected lint to exit with warnings, got output=%q", out.String())
	}
	if !errors.Is(err, cli.ErrLintWarnings) {
		t.Fatalf("err = %v, want ErrLintWarnings; output=%q", err, out.String())
	}
	text := out.String()
	for _, want := range []string{
		"multiple groups",
		"hosts provider overrides are deprecated",
		"github_release_asset recipe",
	} {
		if !bytes.Contains([]byte(text), []byte(want)) {
			t.Fatalf("output = %q, want substring %q", text, want)
		}
	}
}

func TestSettingsMigrateHostOverrides_FoldsHostsIntoProviders(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := config.Save(cfgPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{
			"rg": {
				Hosts: map[string]config.ToolInstallSpec{
					"box": {Provider: "apt", Package: "ripgrep"},
				},
				Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "ripgrep"}},
			},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "settings", "migrate-host-overrides"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	spec := cfg.Tools["rg"]
	if len(spec.Hosts) != 0 {
		t.Fatalf("hosts = %+v, want removed", spec.Hosts)
	}
	if len(spec.Providers) != 2 {
		t.Fatalf("providers = %+v, want brew and migrated apt", spec.Providers)
	}
}
