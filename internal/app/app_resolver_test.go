package app

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type availabilityCountingProvider struct {
	name      string
	available bool
	calls     int
}

func (p *availabilityCountingProvider) Name() string        { return p.name }
func (p *availabilityCountingProvider) Description() string { return p.name + " stub" }
func (p *availabilityCountingProvider) Available(context.Context) (bool, error) {
	p.calls++
	return p.available, nil
}
func (p *availabilityCountingProvider) Install(context.Context, provider.Tool) error   { return nil }
func (p *availabilityCountingProvider) Uninstall(context.Context, provider.Tool) error { return nil }
func (p *availabilityCountingProvider) Upgrade(context.Context, provider.Tool) error   { return nil }
func (p *availabilityCountingProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *availabilityCountingProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func TestResolveToolsCachesProviderAvailabilityPerPass(t *testing.T) {
	ctx := context.Background()
	unavailable := &availabilityCountingProvider{name: "missing", available: false}
	available := &availabilityCountingProvider{name: "available", available: true}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, unavailable, available); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	group := &config.GroupConfig{Name: "base"}
	cfg := &config.RootConfig{
		Tools:  make(map[string]config.ToolSpec),
		Groups: []*config.GroupConfig{group},
	}
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("tool%d", i)
		cfg.Tools[name] = config.ToolSpec{
			Provider: "missing",
			Variants: []config.ToolInstallSpec{
				{Provider: "available"},
			},
		}
		group.Tools = append(group.Tools, config.ToolEntry{Name: name})
	}

	resolved, warnings := a.resolveTools(ctx, cfg, cfg.Groups)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(resolved) != 8 {
		t.Fatalf("resolved tools = %d, want 8", len(resolved))
	}
	for _, tool := range resolved {
		if tool.entry.Provider != "available" {
			t.Fatalf("resolved provider = %q, want available", tool.entry.Provider)
		}
	}
	if unavailable.calls != 1 {
		t.Fatalf("unavailable Available calls = %d, want 1", unavailable.calls)
	}
	if available.calls != 1 {
		t.Fatalf("available Available calls = %d, want 1", available.calls)
	}

	_, _ = a.resolveTools(ctx, cfg, cfg.Groups)
	if unavailable.calls != 2 || available.calls != 2 {
		t.Fatalf("availability cache escaped pass: missing=%d available=%d, want 2/2", unavailable.calls, available.calls)
	}
}

func TestPlanInstallRoute_SelectsLaterCandidateWhenPackageUnavailable(t *testing.T) {
	ctx := context.Background()
	apt := &availabilityCountingProvider{name: "apt", available: true}
	brew := &availabilityCountingProvider{name: "brew", available: true}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, apt, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "apt",
		Package:   "ripgrep",
		Available: false,
		Reason:    "no apt candidate",
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed apt availability: %v", err)
	}
	if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
		Name:      "rg",
		Provider:  "brew",
		Package:   "ripgrep",
		Available: true,
		CheckedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed brew availability: %v", err)
	}

	route := a.planInstallRoute(ctx, "rg", config.ToolSpec{
		Providers: []config.ToolInstallSpec{
			{Provider: "apt", Package: "ripgrep"},
			{Provider: "brew", Package: "ripgrep"},
		},
	}, make(map[string]bool))

	if route.Kind != installRouteNative {
		t.Fatalf("route kind = %q, want native", route.Kind)
	}
	if route.Install.Provider != "brew" {
		t.Fatalf("route install = %+v, want brew candidate", route.Install)
	}
	if len(route.Skipped) != 1 || route.Skipped[0].Reason != installRouteSkipPackageUnavailable || route.Skipped[0].Install.Provider != "apt" {
		t.Fatalf("skipped = %+v, want apt package-unavailable skip", route.Skipped)
	}
}

func TestPlanInstallRoute_MarksFallbackEligibleWhenAllNativeCandidatesUnavailable(t *testing.T) {
	ctx := context.Background()
	apt := &availabilityCountingProvider{name: "apt", available: true}
	brew := &availabilityCountingProvider{name: "brew", available: true}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, apt, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck
	for _, providerName := range []string{"apt", "brew"} {
		if err := a.DB().UpsertPackageAvailability(ctx, database.PackageAvailability{
			Name:      "rg",
			Provider:  providerName,
			Package:   "ripgrep",
			Available: false,
			Reason:    "no " + providerName + " candidate",
			CheckedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed %s availability: %v", providerName, err)
		}
	}

	route := a.planInstallRoute(ctx, "rg", config.ToolSpec{
		Providers: []config.ToolInstallSpec{
			{Provider: "apt", Package: "ripgrep"},
			{Provider: "brew", Package: "ripgrep"},
		},
		Fallback: &config.FallbackSpec{
			Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
			Status: config.FallbackStatusUnverified,
			Commands: config.FallbackCommands{
				Install: "install rg",
				Check:   "command -v rg",
			},
		},
	}, make(map[string]bool))

	if route.Kind != installRouteFallbackEligible {
		t.Fatalf("route kind = %q, want fallback eligible", route.Kind)
	}
	if route.Install.Provider != "apt" {
		t.Fatalf("route install = %+v, want first candidate for fallback cache identity", route.Install)
	}
	if len(route.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want both native candidates skipped", route.Skipped)
	}
}
