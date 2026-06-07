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

// Upgrading the pip package itself via pip in an externally managed Python is
// impossible (PEP 668) and not fixable by switching to uv, so upgrade-all must
// skip it gracefully rather than failing the whole run.
func TestUpgradeAll_SkipsExternallyManagedPipSelfWithoutError(t *testing.T) {
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
