package app_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type cancelingExecutor struct {
	cancel context.CancelFunc
}

func (e cancelingExecutor) Run(context.Context, string, ...string) (string, string, error) {
	e.cancel()
	return "", "", context.Canceled
}

func TestRefreshRediscoverNativeProviderAfterFallbackRemoval(t *testing.T) {
	for _, tt := range []struct {
		name    string
		refresh func(*app.App, context.Context) error
	}{
		{name: "all", refresh: func(a *app.App, ctx context.Context) error {
			return a.RefreshInstalled(ctx, nil)
		}},
		{name: "provider", refresh: func(a *app.App, ctx context.Context) error {
			return a.RefreshProviderInstalled(ctx, "brew")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			brew := &stubProvider{
				name:      "brew",
				available: true,
				installed: []provider.InstalledTool{{
					Tool:    provider.Tool{Name: "rg", Provider: "brew"},
					Version: "14.2.0",
				}},
			}
			a, cfgPath := newImportApp(t, brew)
			ctx := context.Background()
			if err := saveAppConfig(t, cfgPath, &config.RootConfig{
				Tools:  logicalToolSpecs(logicalTool("rg", "brew")),
				Groups: []*config.GroupConfig{testHostToolGroup("rg")},
			}); err != nil {
				t.Fatalf("saving config: %v", err)
			}
			seedFallbackOwner(t, a, ctx)

			if err := tt.refresh(a, ctx); err != nil {
				t.Fatalf("refresh: %v", err)
			}

			got, err := a.DB().Get(ctx, "rg", "brew", "rg")
			if err != nil {
				t.Fatalf("Get rg: %v", err)
			}
			if !got.Installed || got.InstalledWith != "brew" || got.Version.String != "14.2.0" {
				t.Fatalf("cache = installed:%v owner:%q version:%q, want true/brew/14.2.0", got.Installed, got.InstalledWith, got.Version.String)
			}
		})
	}
}

func TestCachedFallbackRefreshPropagatesCancellation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		refresh func(*app.App, context.Context) error
	}{
		{name: "all", refresh: func(a *app.App, ctx context.Context) error {
			return a.RefreshInstalled(ctx, nil)
		}},
		{name: "provider", refresh: func(a *app.App, ctx context.Context) error {
			return a.RefreshProviderInstalled(ctx, "brew")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			brew := &stubProvider{
				name:      "brew",
				available: true,
				installed: []provider.InstalledTool{{
					Tool:    provider.Tool{Name: "rg", Provider: "brew"},
					Version: "14.2.0",
				}},
			}
			a, cfgPath := newImportApp(t, brew)
			if err := saveAppConfig(t, cfgPath, &config.RootConfig{
				Tools: map[string]config.ToolSpec{
					"rg": {
						Providers: []config.ToolInstallSpec{{Provider: "brew"}},
						Fallback: &config.FallbackSpec{
							Commands: config.FallbackCommands{Check: "check rg"},
						},
					},
				},
				Groups: []*config.GroupConfig{testHostToolGroup("rg")},
			}); err != nil {
				t.Fatalf("saving config: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			a.SetFallbackExecutor(cancelingExecutor{cancel: cancel})
			seedFallbackOwner(t, a, ctx)

			if err := tt.refresh(a, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("refresh error = %v, want context.Canceled", err)
			}

			got, err := a.DB().Get(context.Background(), "rg", "brew", "rg")
			if err != nil {
				t.Fatalf("Get rg: %v", err)
			}
			if !got.Installed || got.InstalledWith != "gh" || got.Version.String != "14.1.0" {
				t.Fatalf("cache = installed:%v owner:%q version:%q, want preserved fallback row", got.Installed, got.InstalledWith, got.Version.String)
			}
		})
	}
}

func seedFallbackOwner(t *testing.T, a *app.App, ctx context.Context) {
	t.Helper()
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:          "rg",
		Provider:      "brew",
		Package:       "rg",
		Installed:     true,
		InstalledWith: "gh",
		Version:       sql.NullString{String: "14.1.0", Valid: true},
	}); err != nil {
		t.Fatalf("seed fallback owner: %v", err)
	}
}
