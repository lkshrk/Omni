package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func driftedPluginApp(t *testing.T, stubs ...*stubPluginAdapter) *app.App {
	t.Helper()
	adapters := make([]app.PluginAdapter, 0, len(stubs))
	for _, s := range stubs {
		adapters = append(adapters, s)
	}
	agents := config.AgentsConfig{
		Plugins: []config.Plugin{{Name: "helper", Marketplace: "declared"}},
		Marketplaces: []config.Marketplace{
			{Name: "declared", Source: "o/declared"},
			{Name: "other", Source: "o/other"},
		},
	}
	return newPluginTestApp(t, agents, app.WithPluginAdapters(adapters))
}

func TestPluginRows_MarketplaceMismatchIsDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		live       app.InstalledPlugin
		wantStatus app.PluginStatus
	}{
		{
			name:       "declared marketplace",
			live:       app.InstalledPlugin{Name: "helper", Marketplace: "declared"},
			wantStatus: app.PluginStatusInstalled,
		},
		{
			name:       "other marketplace",
			live:       app.InstalledPlugin{Name: "helper", Marketplace: "other"},
			wantStatus: app.PluginStatusDrifted,
		},
		{
			name:       "marketplace unknown to the agent",
			live:       app.InstalledPlugin{Name: "helper"},
			wantStatus: app.PluginStatusInstalled,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{tc.live}}
			a := driftedPluginApp(t, stub)
			rows, unmanaged, err := a.PluginRows(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := rows[0].PerAgentStatus["claude-code"]; got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if len(unmanaged["claude-code"]) != 0 {
				t.Fatalf("unmanaged = %v, want none: the manifest owns the name", unmanaged)
			}
			if tc.wantStatus == app.PluginStatusDrifted && rows[0].DriftMarketplaces["claude-code"] != "other" {
				t.Fatalf("DriftMarketplaces = %v, want the installed marketplace named", rows[0].DriftMarketplaces)
			}
		})
	}
}

func TestImportPlugins_MarketplaceMismatchIsNotClaimable(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "helper", Marketplace: "other"},
		{Name: "stray", Marketplace: "other"},
	}}
	a := driftedPluginApp(t, stub)

	diff, err := a.ImportPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Unmanaged["claude-code"]) != 1 || diff.Unmanaged["claude-code"][0].Name != "stray" {
		t.Fatalf("Unmanaged = %v, want only the undeclared name", diff.Unmanaged)
	}
	if len(diff.Drifted["claude-code"]) != 1 || diff.Drifted["claude-code"][0].Name != "helper" {
		t.Fatalf("Drifted = %v, want the marketplace mismatch", diff.Drifted)
	}
}

func TestPluginRows_OutdatedSurfacesWithoutDrift(t *testing.T) {
	t.Parallel()
	outdated := true
	stub := &stubPluginAdapter{id: "claude-code", available: true, listedPlugins: []app.InstalledPlugin{
		{Name: "helper", Marketplace: "declared", PathOutdated: &outdated},
	}}
	a := driftedPluginApp(t, stub)
	rows, _, err := a.PluginRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Outdated() || rows[0].Drifted {
		t.Fatalf("row = %+v, want outdated and not drifted", rows[0])
	}

	stub.listedPlugins[0].Marketplace = "other"
	rows, _, err = a.PluginRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].Drifted {
		t.Fatal("want the marketplace mismatch to drift even while outdated")
	}
}

func TestRestorePlugins_SkipsDriftedMarketplace(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}},
	}
	a := driftedPluginApp(t, stub)

	res, err := a.RestorePlugins(context.Background(), app.RestorePluginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stub.installedPlugin) != 0 {
		t.Fatalf("installed = %v, want the drifted plugin left alone", stub.installedPlugin)
	}
	if len(res.Drift) != 1 || !strings.Contains(res.Drift[0], "installed from other") {
		t.Fatalf("Drift = %v, want one line naming the installed marketplace", res.Drift)
	}
	if !strings.Contains(res.Drift[0], "plugins resolve") {
		t.Fatalf("Drift line = %q, want the resolve remedy", res.Drift[0])
	}
	if len(res.Installed)+len(res.AlreadyInstalled) != 0 {
		t.Fatal("a drifted plugin must not be counted as converged")
	}
}

func TestResolvePluginDrift_UseManagedReinstallsFromDeclaredMarketplace(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}},
	}
	a := driftedPluginApp(t, stub)

	res, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || res.Agents[0] != "claude-code" {
		t.Fatalf("Agents = %v, want [claude-code]", res.Agents)
	}
	if len(stub.removedNames) != 1 || len(stub.installedPlugin) != 1 {
		t.Fatalf("remove/install = %v/%v, want one reinstall", stub.removedNames, stub.installedPlugin)
	}
	if stub.installedPlugin[0].Marketplace != "declared" {
		t.Fatalf("installed from %q, want the declared marketplace", stub.installedPlugin[0].Marketplace)
	}
	if got := loadPluginTestConfig(t, a).Agents.Plugins[0].Marketplace; got != "declared" {
		t.Fatalf("manifest marketplace = %q, want it untouched", got)
	}
}

func TestResolvePluginDrift_UseManagedRestoresThePluginWhenTheInstallFails(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}},
		installFunc: func(p config.Plugin) error {
			if p.Marketplace == "declared" {
				return errors.New("marketplace unreachable")
			}
			return nil
		},
	}
	a := driftedPluginApp(t, stub)

	_, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseManaged,
	})
	if err == nil {
		t.Fatal("a failed reinstall must be reported")
	}
	if !strings.Contains(err.Error(), "was restored") {
		t.Fatalf("err = %v, want it to report the previous copy restored", err)
	}
	if len(stub.installedPlugin) != 2 || stub.installedPlugin[1].Marketplace != "other" {
		t.Fatalf("installs = %+v, want the removed copy from other reinstalled", stub.installedPlugin)
	}
}

func TestResolvePluginDrift_UseManagedReportsAFailedRestore(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}},
		installErr:    errors.New("marketplace unreachable"),
	}
	a := driftedPluginApp(t, stub)

	_, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseManaged,
	})
	if err == nil || !strings.Contains(err.Error(), "restore the copy from other") {
		t.Fatalf("err = %v, want the failed restore surfaced", err)
	}
}

func TestResolvePluginDrift_UseManagedKeepsThePluginWhenTheMarketplaceIsUnreachable(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}},
		addMarketErr:  errors.New("marketplace unreachable"),
	}
	a := driftedPluginApp(t, stub)

	_, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseManaged,
	})
	if err == nil {
		t.Fatal("an unreachable marketplace must be reported")
	}
	if len(stub.removedNames) != 0 {
		t.Fatalf("removed = %v, want the installed copy left in place", stub.removedNames)
	}
}

func TestResolvePluginDrift_UseLocalRepointsManifest(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}},
	}
	a := driftedPluginApp(t, stub)

	if _, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseLocal,
	}); err != nil {
		t.Fatal(err)
	}
	if len(stub.removedNames)+len(stub.installedPlugin) != 0 {
		t.Fatal("use-local must not touch the agent")
	}
	if got := loadPluginTestConfig(t, a).Agents.Plugins[0].Marketplace; got != "other" {
		t.Fatalf("manifest marketplace = %q, want it repointed to other", got)
	}
}

func TestResolvePluginDrift_UseLocalRefusesUndeclaredMarketplace(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{
		id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "nowhere"}},
	}
	a := driftedPluginApp(t, stub)

	_, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseLocal,
	})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error = %v, want an undeclared-marketplace refusal", err)
	}
	if got := loadPluginTestConfig(t, a).Agents.Plugins[0].Marketplace; got != "declared" {
		t.Fatalf("manifest marketplace = %q, want it unchanged by the refusal", got)
	}
}

func TestResolvePluginDrift_UseLocalRefusesCrossAgentConflict(t *testing.T) {
	t.Parallel()
	claude := &stubPluginAdapter{id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}}}
	codex := &stubPluginAdapter{id: "codex", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "nowhere"}}}
	a := driftedPluginApp(t, claude, codex)

	_, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseLocal,
	})
	if err == nil || !strings.Contains(err.Error(), "different marketplaces") {
		t.Fatalf("error = %v, want a cross-agent conflict refusal", err)
	}
}

func TestResolvePluginDrift_DryRunAndNotDrifted(t *testing.T) {
	t.Parallel()
	stub := &stubPluginAdapter{id: "claude-code", available: true,
		listedPlugins: []app.InstalledPlugin{{Name: "helper", Marketplace: "other"}}}
	a := driftedPluginApp(t, stub)

	res, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseManaged, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Actions) != 1 || !strings.Contains(res.Actions[0], "reinstall helper on claude-code") {
		t.Fatalf("Actions = %v, want a reinstall preview", res.Actions)
	}
	if len(stub.removedNames)+len(stub.installedPlugin) != 0 {
		t.Fatal("dry run mutated the agent")
	}

	stub.listedPlugins[0].Marketplace = "declared"
	if _, err := a.ResolvePluginDrift(context.Background(), app.ResolvePluginDriftOptions{
		Name: "helper", Strategy: app.PluginDriftUseManaged,
	}); err == nil || !strings.Contains(err.Error(), "not drifted") {
		t.Fatalf("error = %v, want a not-drifted refusal", err)
	}
}
