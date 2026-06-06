package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type quarantineUpgradeStub struct {
	stubProvider
	upgraded []string
}

func (s *quarantineUpgradeStub) Upgrade(_ context.Context, tool provider.Tool) error {
	s.upgraded = append(s.upgraded, tool.EffectivePackage())
	return nil
}

func TestQueryTools_ClassifiesQuarantinedAndMetadataBlockedUpdates(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	stub := &quarantineUpgradeStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, stub)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{UpdateQuarantine: "2d"},
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: groupTools("ripgrep", "fd")}},
		Hosts:  map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	for _, row := range []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep", InstalledWith: "brew", Installed: true, Tracked: true},
		{Name: "fd", Provider: "brew", Package: "fd", InstalledWith: "brew", Installed: true, Tracked: true},
	} {
		if err := a.DB().Upsert(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.Name, err)
		}
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("outdated ripgrep: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "fd", "brew", "fd", true, "10.0.0"); err != nil {
		t.Fatalf("outdated fd: %v", err)
	}
	if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "brew",
		Package:     "ripgrep",
		Version:     "15.0.0",
		AvailableAt: time.Now().Add(-12 * time.Hour),
		DateSource:  "brew_api",
		CheckedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata: %v", err)
	}

	quarantined, err := a.QueryTools(ctx, app.ToolListOptions{State: "quarantined"})
	if err != nil {
		t.Fatalf("QueryTools quarantined: %v", err)
	}
	if len(quarantined) != 1 || quarantined[0].Tool.Name != "ripgrep" || quarantined[0].State != app.ToolStateQuarantined {
		t.Fatalf("quarantined = %#v, want ripgrep quarantined", quarantined)
	}

	blocked, err := a.QueryTools(ctx, app.ToolListOptions{State: "blocked-metadata"})
	if err != nil {
		t.Fatalf("QueryTools blocked-metadata: %v", err)
	}
	if len(blocked) != 1 || blocked[0].Tool.Name != "fd" || blocked[0].State != app.ToolStateBlockedMetadata {
		t.Fatalf("blocked = %#v, want fd blocked-metadata", blocked)
	}
}

func TestQueryTools_UpdateQuarantinePolicyPrecedence(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	stub := &quarantineUpgradeStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, stub)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{
			UpdateQuarantine: "2d",
			ProviderUpdateQuarantine: map[string]string{
				"brew": "1d",
				"npm":  "5d",
			},
		},
		Tools: logicalToolSpecs(
			logicalTool("concrete-wins", "brew"),
			logicalTool("tool-duration-wins", "brew"),
			logicalTool("tool-exempt-wins", "brew"),
			logicalTool("logical-provider-wins", "npm"),
			logicalTool("global-applies", "pip"),
		),
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: groupTools(
			"concrete-wins",
			"tool-duration-wins",
			"tool-exempt-wins",
			"logical-provider-wins",
			"global-applies",
		)}},
		Hosts: map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.SetToolQuarantine("tool-duration-wins", "5d"); err != nil {
		t.Fatalf("SetToolQuarantine duration: %v", err)
	}
	if err := a.SetToolQuarantine("tool-exempt-wins", "exempt"); err != nil {
		t.Fatalf("SetToolQuarantine exempt: %v", err)
	}

	for _, row := range []*database.ToolCache{
		{Name: "concrete-wins", Provider: "brew", Package: "concrete-wins", InstalledWith: "brew", Installed: true, Tracked: true},
		{Name: "tool-duration-wins", Provider: "brew", Package: "tool-duration-wins", InstalledWith: "brew", Installed: true, Tracked: true},
		{Name: "tool-exempt-wins", Provider: "brew", Package: "tool-exempt-wins", InstalledWith: "brew", Installed: true, Tracked: true},
		{Name: "logical-provider-wins", Provider: "npm", Package: "logical-provider-wins", Installed: true, Tracked: true},
		{Name: "global-applies", Provider: "pip", Package: "global-applies", Installed: true, Tracked: true},
	} {
		if err := a.DB().Upsert(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.Name, err)
		}
		latest := "2.0.0"
		if err := a.DB().UpdateOutdated(ctx, row.Name, row.Provider, row.Package, true, latest); err != nil {
			t.Fatalf("outdated %s: %v", row.Name, err)
		}
		metadataProvider := row.Provider
		if row.InstalledWith != "" {
			metadataProvider = row.InstalledWith
		}
		if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
			Provider:    metadataProvider,
			Package:     row.Package,
			Version:     latest,
			AvailableAt: time.Now().Add(-3 * 24 * time.Hour),
			DateSource:  "test",
			CheckedAt:   time.Now(),
		}); err != nil {
			t.Fatalf("metadata %s: %v", row.Name, err)
		}
	}
	if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "pip",
		Package:     "global-applies",
		Version:     "2.0.0",
		AvailableAt: time.Now().Add(-1 * time.Hour),
		DateSource:  "test",
		CheckedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("metadata global-applies override: %v", err)
	}

	quarantined, err := a.QueryTools(ctx, app.ToolListOptions{State: "quarantined"})
	if err != nil {
		t.Fatalf("QueryTools quarantined: %v", err)
	}
	got := make(map[string]bool, len(quarantined))
	for _, item := range quarantined {
		got[item.Tool.Name] = true
	}
	want := map[string]bool{
		"tool-duration-wins":    true,
		"logical-provider-wins": true,
		"global-applies":        true,
	}
	if len(got) != len(want) {
		t.Fatalf("quarantined names = %v, want %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("quarantined names = %v, want %s included", got, name)
		}
	}
	for _, name := range []string{"concrete-wins", "tool-exempt-wins"} {
		if got[name] {
			t.Fatalf("%s was quarantined; precedence should leave it upgradable", name)
		}
	}
}

func TestUpgrade_BlocksQuarantinedToolUnlessForced(t *testing.T) {
	stub := &quarantineUpgradeStub{
		stubProvider: stubProvider{
			name:      "brew",
			available: true,
			installed: []provider.InstalledTool{{
				Tool:    provider.Tool{Name: "ripgrep", Provider: "brew"},
				Version: "14.0.0",
			}},
		},
	}
	a, cfgPath := newImportApp(t, stub)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{UpdateQuarantine: "2d"},
		Tools:    logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups:   []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("outdated ripgrep: %v", err)
	}
	if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "brew",
		Package:     "ripgrep",
		Version:     "15.0.0",
		AvailableAt: time.Now().Add(-1 * time.Hour),
		DateSource:  "brew_api",
		CheckedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata: %v", err)
	}

	err := a.Upgrade(ctx, "ripgrep", "brew")
	if err == nil || !strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("Upgrade error = %v, want quarantined error", err)
	}
	if len(stub.upgraded) != 0 {
		t.Fatalf("upgraded = %v, want blocked before provider call", stub.upgraded)
	}

	if err := a.UpgradeWithOptions(ctx, "ripgrep", "brew", app.UpgradeOptions{Force: true}); err != nil {
		t.Fatalf("forced UpgradeWithOptions: %v", err)
	}
	if len(stub.upgraded) != 1 || stub.upgraded[0] != "ripgrep" {
		t.Fatalf("upgraded = %v, want forced ripgrep upgrade", stub.upgraded)
	}
}

func TestUpgradeAll_SkipsQuarantinedUpdatesWithoutError(t *testing.T) {
	stub := &quarantineUpgradeStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, stub)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{UpdateQuarantine: "2d"},
		Tools:    logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups:   []*config.GroupConfig{{Tools: groupTools("ripgrep")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "ripgrep",
		Provider:      "brew",
		Package:       "ripgrep",
		Installed:     true,
		InstalledWith: "brew",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "15.0.0"); err != nil {
		t.Fatalf("outdated ripgrep: %v", err)
	}
	if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "brew",
		Package:     "ripgrep",
		Version:     "15.0.0",
		AvailableAt: time.Now().Add(-1 * time.Hour),
		DateSource:  "brew_api",
		CheckedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata: %v", err)
	}

	result, err := a.UpgradeAllDetailedWithOptions(ctx, nil, nil, app.UpgradeAllOptions{})
	if err != nil {
		t.Fatalf("UpgradeAllDetailedWithOptions: %v", err)
	}
	if len(result.Upgraded) != 0 || len(stub.upgraded) != 0 {
		t.Fatalf("upgraded result/provider = %v/%v, want none", result.Upgraded, stub.upgraded)
	}
	if len(result.Quarantined) != 1 || result.Quarantined[0].Name != "ripgrep" {
		t.Fatalf("Quarantined = %+v, want ripgrep", result.Quarantined)
	}
}
