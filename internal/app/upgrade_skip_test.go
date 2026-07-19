package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type externallyManagedPipStub struct {
	stubProvider
	upgraded []string
}

func (s *externallyManagedPipStub) Upgrade(_ context.Context, tool provider.Tool) error {
	s.upgraded = append(s.upgraded, tool.EffectivePackage())
	return provider.NewExternallyManagedPythonError(
		"pip3", "upgrade", tool, errors.New("error: externally-managed-environment"),
		"error: externally-managed-environment", nil,
	)
}

type manualInstallerBrewStub struct {
	stubProvider
	upgraded []string
}

func (s *manualInstallerBrewStub) Upgrade(_ context.Context, tool provider.Tool) error {
	s.upgraded = append(s.upgraded, tool.EffectivePackage())
	return errors.New("brew upgrade --cask battle-net: exit status 1 (stderr: Error: Not upgrading 1 `installer manual` cask.)")
}

// Homebrew refuses to automate upgrades for casks that declare `installer manual`.
// Those require user intervention and should not fail an otherwise successful
// upgrade-all/reconcile run.
func TestUpgradeAll_SkipsBrewManualInstallerCaskWithoutError(t *testing.T) {
	t.Parallel()
	stub := &manualInstallerBrewStub{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, stub)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"battle-net": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "battle-net", Options: map[string]string{"brew_kind": "cask"}}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("battle-net")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:      "battle-net",
		Provider:  "brew",
		Package:   "battle-net",
		Installed: true,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed battle-net: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "battle-net", "brew", "battle-net", true, "1.19.3.3219"); err != nil {
		t.Fatalf("outdated battle-net: %v", err)
	}
	if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "brew",
		Package:     "battle-net",
		Version:     "1.19.3.3219",
		AvailableAt: time.Now().Add(-1 * time.Hour),
		DateSource:  "brew",
		CheckedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata: %v", err)
	}

	result, err := a.UpgradeAllDetailedWithOptions(ctx, nil, nil, app.UpgradeAllOptions{})
	if err != nil {
		t.Fatalf("UpgradeAllDetailedWithOptions returned error, want graceful skip: %v", err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("Failures = %+v, want none (manual cask upgrade skipped)", result.Failures)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Name != "battle-net" {
		t.Fatalf("Skipped = %+v, want battle-net", result.Skipped)
	}
}

// Upgrading the pip package itself via pip in an externally managed Python is
// impossible (PEP 668) and not fixable by switching to uv, so upgrade-all must
// skip it gracefully rather than failing the whole run.
func TestUpgradeAll_SkipsExternallyManagedPipSelfWithoutError(t *testing.T) {
	t.Parallel()
	stub := &externallyManagedPipStub{stubProvider: stubProvider{name: "pip", available: true}}
	a, cfgPath := newImportApp(t, stub)
	ctx := context.Background()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"pip": {Providers: []config.ToolInstallSpec{{Provider: "pip", Package: "pip"}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("pip")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "pip",
		Provider:      "pip",
		Package:       "pip",
		Installed:     true,
		InstalledWith: "pip",
		Tracked:       true,
	}); err != nil {
		t.Fatalf("seed pip: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "pip", "pip", "pip", true, "26.2.0"); err != nil {
		t.Fatalf("outdated pip: %v", err)
	}
	if err := a.DB().UpsertUpdateMetadata(ctx, database.UpdateMetadata{
		Provider:    "pip",
		Package:     "pip",
		Version:     "26.2.0",
		AvailableAt: time.Now().Add(-1 * time.Hour),
		DateSource:  "pypi",
		CheckedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("UpsertUpdateMetadata: %v", err)
	}

	result, err := a.UpgradeAllDetailedWithOptions(ctx, nil, nil, app.UpgradeAllOptions{})
	if err != nil {
		t.Fatalf("UpgradeAllDetailedWithOptions returned error, want graceful skip: %v", err)
	}
	if len(result.Failures) != 0 {
		t.Fatalf("Failures = %+v, want none (pip self-upgrade skipped)", result.Failures)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Name != "pip" {
		t.Fatalf("Skipped = %+v, want pip", result.Skipped)
	}
}
