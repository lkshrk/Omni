package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func aptInstalled(names ...string) []provider.InstalledTool {
	out := make([]provider.InstalledTool, 0, len(names))
	for _, name := range names {
		out = append(out, provider.InstalledTool{Tool: provider.Tool{Name: name, Provider: "apt"}})
	}
	return out
}

// Reproduces the reported state: the baseline was recorded before another installer pulled in runtime packages.
func newBaselineApp(t *testing.T) (*app.App, *stubProvider) {
	t.Helper()
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: true, installed: aptInstalled("coreutils")}
	a, cfgPath := newImportApp(t, apt)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  map[string]config.ToolSpec{"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}}},
		Ignore: config.GlobalIgnore{Tools: []string{"htop"}},
		Groups: []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshDiscovered(ctx); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	apt.installed = aptInstalled("coreutils", "libnss3", "xvfb", "ripgrep", "htop")
	return a, apt
}

func discoveredNames(t *testing.T, ctx context.Context, a *app.App) map[string]bool {
	t.Helper()
	if err := a.RefreshDiscovered(ctx); err != nil {
		t.Fatalf("RefreshDiscovered: %v", err)
	}
	discovered, err := a.ListDiscovered(ctx)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	names := make(map[string]bool, len(discovered))
	for _, tool := range discovered {
		names[tool.Name] = true
	}
	return names
}

func TestRebaselineSystemInventory_AbsorbsPackagesAddedSinceTheBaseline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, _ := newBaselineApp(t)

	before := discoveredNames(t, ctx, a)
	if !before["libnss3"] || !before["xvfb"] {
		t.Fatalf("discovered = %v; runtime packages added after the baseline must surface first", before)
	}

	preview, err := a.RebaselineSystemInventory(ctx, true)
	if err != nil {
		t.Fatalf("RebaselineSystemInventory dry run: %v", err)
	}
	if len(preview.Providers) != 1 || preview.Providers[0].Provider != "apt" {
		t.Fatalf("providers = %+v; want apt only", preview.Providers)
	}
	absorbed := preview.Providers[0].Absorbed
	if len(absorbed) != 2 || absorbed[0] != "libnss3" || absorbed[1] != "xvfb" {
		t.Fatalf("absorbed = %v; want libnss3 and xvfb, leaving the tracked and ignored tools alone", absorbed)
	}
	if got := discoveredNames(t, ctx, a); !got["libnss3"] || !got["xvfb"] {
		t.Fatalf("discovered = %v after dry run; a dry run must not write the baseline", got)
	}

	if _, err := a.RebaselineSystemInventory(ctx, false); err != nil {
		t.Fatalf("RebaselineSystemInventory: %v", err)
	}
	after := discoveredNames(t, ctx, a)
	if after["libnss3"] || after["xvfb"] {
		t.Fatalf("discovered = %v; absorbed packages must stop being reported", after)
	}

	again, err := a.RebaselineSystemInventory(ctx, true)
	if err != nil {
		t.Fatalf("RebaselineSystemInventory second dry run: %v", err)
	}
	if again.AbsorbedCount() != 0 {
		t.Fatalf("second dry run absorbed %d packages; the baseline already covers them", again.AbsorbedCount())
	}
}

func TestRebaselineSystemInventory_LaterInstallsStillSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, apt := newBaselineApp(t)

	if _, err := a.RebaselineSystemInventory(ctx, false); err != nil {
		t.Fatalf("RebaselineSystemInventory: %v", err)
	}
	apt.installed = append(apt.installed, provider.InstalledTool{Tool: provider.Tool{Name: "jq", Provider: "apt"}})

	if got := discoveredNames(t, ctx, a); !got["jq"] {
		t.Fatalf("discovered = %v; a package installed after the re-baseline must still surface", got)
	}
}

// A declared tool whose apt package name differs from its key ("fd" -> "fd-find") is still tracked, so absorbing it would retire that declaration.
func TestRebaselineSystemInventory_ProtectsPerProviderPackageNames(t *testing.T) {
	ctx := context.Background()
	apt := &stubProvider{name: "apt", available: true, installed: aptInstalled("coreutils")}
	a, cfgPath := newImportApp(t, apt)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd": {Providers: []config.ToolInstallSpec{
				{Provider: "brew", Package: "fd"},
				{Provider: "apt", Package: "fd-find"},
			}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("fd")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshDiscovered(ctx); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
	apt.installed = aptInstalled("coreutils", "fd-find", "xvfb")

	result, err := a.RebaselineSystemInventory(ctx, true)
	if err != nil {
		t.Fatalf("RebaselineSystemInventory: %v", err)
	}
	var absorbed []string
	for _, p := range result.Providers {
		absorbed = append(absorbed, p.Absorbed...)
	}
	for _, name := range absorbed {
		if name == "fd-find" {
			t.Fatalf("absorbed the apt package of a declared tool: %v", absorbed)
		}
	}
	if len(absorbed) != 1 || absorbed[0] != "xvfb" {
		t.Fatalf("absorbed = %v, want [xvfb]", absorbed)
	}
}
