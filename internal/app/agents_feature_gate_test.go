package app_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func newFeatureGateApp(t *testing.T, s config.Settings, opts ...func(*app.App)) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	root := config.RootConfig{Version: config.CurrentVersion, Settings: s}
	if err := config.Save(cfgPath, &root); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func wantDisabledErr(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Errorf("err = %v, want containing %q", err, substr)
	}
}

func wantWarning(t *testing.T, warnings []string, substr string) {
	t.Helper()
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("warnings %v missing %q", warnings, substr)
}

func TestOpsGatedByFeatureFlags(t *testing.T) {
	ctx := context.Background()

	t.Run("skills add", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
		_, err := a.AddSkillPackage(ctx, "owner/repo")
		wantDisabledErr(t, err, "skills are disabled")
	})
	t.Run("skills find", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
		_, err := a.FindSkillPackages(ctx, "q")
		wantDisabledErr(t, err, "skills are disabled")
	})
	t.Run("mcp add", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{McpDisabled: config.BoolPtr(true)})
		_, err := a.AddMcpServer(ctx, config.McpServer{Name: "x", Command: "y"})
		wantDisabledErr(t, err, "mcp servers are disabled")
	})
	t.Run("plugins add", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{PluginsDisabled: config.BoolPtr(true)})
		_, err := a.AddPlugin(ctx, config.Plugin{Name: "x"})
		wantDisabledErr(t, err, "plugins are disabled")
	})
}

func TestRestoreSkipsDisabledFeatureWithWarning(t *testing.T) {
	ctx := context.Background()

	t.Run("skills", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{SkillsDisabled: config.BoolPtr(true)})
		res, _, err := a.RestoreSkills(ctx, app.RestoreSkillsOptions{})
		if err != nil {
			t.Fatalf("want skip not error, got %v", err)
		}
		wantWarning(t, res.Warnings, "skills are disabled")
		if len(res.Installed) != 0 {
			t.Errorf("Installed = %v, want empty when skills disabled", res.Installed)
		}
		if len(res.Failed) != 0 {
			t.Errorf("Failed = %v, want empty when skills disabled", res.Failed)
		}
		if len(res.Drift) != 0 {
			t.Errorf("Drift = %v, want empty when skills disabled", res.Drift)
		}
	})
	t.Run("mcp", func(t *testing.T) {
		stub := &stubMcpAdapter{id: "claude-code", available: true}
		a := newFeatureGateApp(t, config.Settings{McpDisabled: config.BoolPtr(true)}, app.WithMcpAdapters([]app.McpAdapter{stub}))
		res, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
		if err != nil {
			t.Fatalf("want skip not error, got %v", err)
		}
		wantWarning(t, res.Warnings, "mcp servers are disabled")
		if stub.listCalls != 0 {
			t.Errorf("listCalls = %d, want 0 when mcp disabled", stub.listCalls)
		}
		if len(stub.addedServers) != 0 {
			t.Errorf("addedServers = %v, want empty when mcp disabled", stub.addedServers)
		}
	})
	t.Run("plugins", func(t *testing.T) {
		stub := &stubPluginAdapter{id: "claude-code", available: true}
		a := newFeatureGateApp(t, config.Settings{PluginsDisabled: config.BoolPtr(true)}, app.WithPluginAdapters([]app.PluginAdapter{stub}))
		res, err := a.RestorePlugins(ctx, app.RestorePluginOptions{})
		if err != nil {
			t.Fatalf("want skip not error, got %v", err)
		}
		wantWarning(t, res.Warnings, "plugins are disabled")
		if stub.listCalls != 0 {
			t.Errorf("listCalls = %d, want 0 when plugins disabled", stub.listCalls)
		}
		if len(stub.installedPlugin) != 0 {
			t.Errorf("installedPlugin = %v, want empty when plugins disabled", stub.installedPlugin)
		}
	})
	t.Run("master still hard error", func(t *testing.T) {
		a := newFeatureGateApp(t, config.Settings{AgentsDisabled: config.BoolPtr(true)})
		_, err := a.RestoreMcpServers(ctx, app.RestoreMcpOptions{})
		wantDisabledErr(t, err, "agent skills are disabled")
	})
}
