package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func newPersistTestApp(t *testing.T) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgPath
}

func TestSaveAgentFeatureFlags_PersistToHostSettings(t *testing.T) {
	t.Parallel()
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	for name, save := range map[string]func(context.Context, bool) error{
		"agents_disabled":  a.SaveAgentsDisabled,
		"skills_disabled":  a.SaveSkillsDisabled,
		"mcp_disabled":     a.SaveMcpDisabled,
		"plugins_disabled": a.SavePluginsDisabled,
	} {
		if err := save(ctx, true); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), name) {
			t.Errorf("%s not persisted", name)
		}
	}
}

func TestSaveSettings_PreservesAgentFeatureFlags(t *testing.T) {
	t.Parallel()
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	if err := a.SaveAgentsDisabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSettings(ctx, config.Settings{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "agents_disabled") {
		t.Error("SaveSettings erased agents_disabled from host_settings")
	}
}

func TestSaveAgentsUse_PersistsToHostSettings(t *testing.T) {
	t.Parallel()
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	if err := a.SaveAgentsUse(ctx, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"agents_use"`) || !strings.Contains(string(data), `"claude-code"`) {
		t.Errorf("agents_use not persisted with expected value, got: %s", data)
	}
}

func TestSaveAgentsUse_PersistsExplicitEmptyList(t *testing.T) {
	t.Parallel()
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	if err := a.SaveAgentsUse(ctx, []string{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"agents_use": []`) {
		t.Errorf("explicit empty agents_use not preserved, got: %s", data)
	}
}

func TestSaveSettings_PreservesAgentsUse(t *testing.T) {
	t.Parallel()
	a, cfgPath := newPersistTestApp(t)
	ctx := context.Background()
	if err := a.SaveAgentsUse(ctx, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSettings(ctx, config.Settings{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "agents_use") {
		t.Error("SaveSettings erased agents_use from host_settings")
	}
}

func TestPatchCurrentHostSettings_PreservesProviders(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	rawHostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	hostname := shortHostnameForTest(rawHostname)
	root := config.RootConfig{
		Version: config.CurrentVersion,
		HostSettings: map[string]config.Settings{
			hostname: {
				Providers: []config.ProviderEntry{{Name: "custom", Provider: "brew"}},
			},
		},
	}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ctx := context.Background()
	if err := a.SaveAgentsDisabled(ctx, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"providers"`) {
		t.Errorf("providers erased from host_settings after unrelated patch, got: %s", data)
	}
}
