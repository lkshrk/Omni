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

type managerRoutingProvider struct {
	availabilityCountingProvider
	installCalls        []provider.Tool
	managerInstallCalls map[string][]provider.Tool
	checkCalls          []provider.Tool
	managerCheckCalls   map[string][]provider.Tool
}

func newManagerRoutingProvider(name string) *managerRoutingProvider {
	return &managerRoutingProvider{
		availabilityCountingProvider: availabilityCountingProvider{name: name, available: true},
		managerInstallCalls:          make(map[string][]provider.Tool),
		managerCheckCalls:            make(map[string][]provider.Tool),
	}
}

func (p *managerRoutingProvider) Install(_ context.Context, tool provider.Tool) error {
	p.installCalls = append(p.installCalls, tool)
	return nil
}

func (p *managerRoutingProvider) InstallWithManager(_ context.Context, tool provider.Tool, manager string) error {
	p.managerInstallCalls[manager] = append(p.managerInstallCalls[manager], tool)
	return nil
}

func (p *managerRoutingProvider) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	p.checkCalls = append(p.checkCalls, tool)
	return true, "1.0.0", nil
}

func (p *managerRoutingProvider) IsInstalledWithManager(_ context.Context, tool provider.Tool, manager string) (bool, string, error) {
	p.managerCheckCalls[manager] = append(p.managerCheckCalls[manager], tool)
	return true, "2.0.0", nil
}

func TestToolKeyDefaultsPackageAndString(t *testing.T) {
	key := NewToolKey("rg", "brew", "")
	if key != (ToolKey{Name: "rg", Provider: "brew", Package: "rg"}) {
		t.Fatalf("key = %+v, want package defaulted to name", key)
	}
	if got, want := key.String(), "rg\x00brew\x00rg"; got != want {
		t.Fatalf("key string = %q, want %q", got, want)
	}

	entryKey := ToolKeyFromEntry(config.ToolEntry{Name: "rg", Provider: "brew", Package: "ripgrep"})
	if entryKey != (ToolKey{Name: "rg", Provider: "brew", Package: "ripgrep"}) {
		t.Fatalf("entry key = %+v, want effective package identity", entryKey)
	}
}

func TestResolverProviderIdentityHelpers(t *testing.T) {
	ctx := context.Background()
	node := &availabilityCountingProvider{name: "node", available: true}
	bun := &availabilityCountingProvider{name: "bun", available: true}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, node, bun); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	nodeEntry := config.ToolEntry{Name: "prettier", Provider: "node", InstallWith: "bun"}
	if got := a.operationProviderName(nodeEntry); got != "bun" {
		t.Fatalf("operation provider = %q, want bun", got)
	}
	unknownManagerEntry := config.ToolEntry{Name: "prettier", Provider: "node", InstallWith: "missing"}
	if got := a.operationProviderName(unknownManagerEntry); got != "node" {
		t.Fatalf("operation provider with unknown manager = %q, want node", got)
	}

	resolved := map[string]string{"system": "apt", "node": "bun"}
	for _, tc := range []struct {
		name             string
		configProvider   string
		concreteProvider string
		want             string
	}{
		{name: "empty config provider", configProvider: "", concreteProvider: "brew"},
		{name: "empty concrete provider", configProvider: "system", concreteProvider: ""},
		{name: "same provider", configProvider: "brew", concreteProvider: "brew"},
		{name: "resolved ecosystem", configProvider: "system", concreteProvider: "apt"},
		{name: "concrete override", configProvider: "system", concreteProvider: "brew", want: "brew"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := configInstallWithForConcreteProvider(tc.configProvider, tc.concreteProvider, resolved); got != tc.want {
				t.Fatalf("install_with = %q, want %q", got, tc.want)
			}
		})
	}

	install := config.ToolInstallSpec{Provider: "node", InstallWith: "bun"}
	for _, providerName := range []string{"", "node", "bun"} {
		if !installSpecMatchesProvider(install, providerName) {
			t.Fatalf("install spec should match provider %q", providerName)
		}
	}
	if installSpecMatchesProvider(install, "npm") {
		t.Fatalf("install spec unexpectedly matched npm")
	}
}

func TestManagerSpecificInstallAndCheckRouting(t *testing.T) {
	ctx := context.Background()
	manager := newManagerRoutingProvider("node")
	tool := provider.Tool{Name: "prettier", Provider: "node", Package: "prettier"}

	if err := installWithProvider(ctx, manager, tool, "bun"); err != nil {
		t.Fatalf("install with manager: %v", err)
	}
	if len(manager.managerInstallCalls["bun"]) != 1 {
		t.Fatalf("manager install calls = %+v, want one bun call", manager.managerInstallCalls)
	}
	if len(manager.installCalls) != 0 {
		t.Fatalf("normal install calls = %+v, want none", manager.installCalls)
	}

	if err := installWithProvider(ctx, manager, tool, "node"); err != nil {
		t.Fatalf("install with own provider: %v", err)
	}
	if len(manager.installCalls) != 1 {
		t.Fatalf("normal install calls = %+v, want one call", manager.installCalls)
	}

	installed, version, err := installedWithProvider(ctx, manager, tool, "bun")
	if err != nil {
		t.Fatalf("check with manager: %v", err)
	}
	if !installed || version != "2.0.0" {
		t.Fatalf("manager check = %v %q, want true 2.0.0", installed, version)
	}
	if len(manager.managerCheckCalls["bun"]) != 1 {
		t.Fatalf("manager check calls = %+v, want one bun call", manager.managerCheckCalls)
	}
	if len(manager.checkCalls) != 0 {
		t.Fatalf("normal check calls = %+v, want none", manager.checkCalls)
	}

	installed, version, err = installedWithProvider(ctx, manager, tool, "node")
	if err != nil {
		t.Fatalf("check with own provider: %v", err)
	}
	if !installed || version != "1.0.0" {
		t.Fatalf("normal check = %v %q, want true 1.0.0", installed, version)
	}
	if len(manager.checkCalls) != 1 {
		t.Fatalf("normal check calls = %+v, want one call", manager.checkCalls)
	}

	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, manager); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	entry := config.ToolEntry{Name: "prettier", Provider: "node", Package: "prettier", InstallWith: "bun"}
	_, version, err = a.isInstalledWithEntry(ctx, manager, "node", entry)
	if err != nil {
		t.Fatalf("entry check with manager: %v", err)
	}
	if version != "2.0.0" || len(manager.managerCheckCalls["bun"]) != 2 {
		t.Fatalf("entry manager check version=%q calls=%+v, want second bun call", version, manager.managerCheckCalls)
	}

	_, version, err = a.isInstalledWithEntry(ctx, manager, "bun", entry)
	if err != nil {
		t.Fatalf("entry check concrete provider: %v", err)
	}
	if version != "1.0.0" || len(manager.checkCalls) != 2 {
		t.Fatalf("entry normal check version=%q calls=%+v, want second normal call", version, manager.checkCalls)
	}
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

func TestPlanInstallRoute_DoesNotUseFallbackWhenProviderUnavailable(t *testing.T) {
	ctx := context.Background()
	apt := &availabilityCountingProvider{name: "apt", available: false}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, apt); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	route := a.planInstallRoute(ctx, "rg", config.ToolSpec{
		Providers: []config.ToolInstallSpec{
			{Provider: "apt", Package: "ripgrep"},
		},
		Fallback: githubFallbackSpec(),
	}, make(map[string]bool))

	if route.Kind != installRouteUnavailable {
		t.Fatalf("route kind = %q, want unavailable", route.Kind)
	}
	if len(route.Skipped) != 1 || route.Skipped[0].Reason != installRouteSkipProviderUnavailable || route.Skipped[0].Install.Provider != "apt" {
		t.Fatalf("skipped = %+v, want apt provider-unavailable skip", route.Skipped)
	}
}

func TestPlanInstallRoute_DoesNotUseFallbackForMixedSkipReasons(t *testing.T) {
	ctx := context.Background()
	apt := &availabilityCountingProvider{name: "apt", available: true}
	brew := &availabilityCountingProvider{name: "brew", available: false}
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

	route := a.planInstallRoute(ctx, "rg", config.ToolSpec{
		Providers: []config.ToolInstallSpec{
			{Provider: "apt", Package: "ripgrep"},
			{Provider: "brew", Package: "ripgrep"},
		},
		Fallback: githubFallbackSpec(),
	}, make(map[string]bool))

	if route.Kind != installRouteUnavailable {
		t.Fatalf("route kind = %q, want unavailable", route.Kind)
	}
	if len(route.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want both native candidates skipped", route.Skipped)
	}
	if route.Skipped[0].Reason != installRouteSkipPackageUnavailable || route.Skipped[1].Reason != installRouteSkipProviderUnavailable {
		t.Fatalf("skip reasons = %+v, want package-unavailable then provider-unavailable", route.Skipped)
	}
}

func TestPlanInstallRoute_HostOverrideShortCircuitsCandidates(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "topaz.example")
	ctx := context.Background()
	apt := &availabilityCountingProvider{name: "apt", available: false}
	brew := &availabilityCountingProvider{name: "brew", available: false}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	if err := a.InitTestMode(ctx, apt, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close() //nolint:errcheck

	route := a.planInstallRoute(ctx, "rg", config.ToolSpec{
		Hosts: map[string]config.ToolInstallSpec{
			"topaz": {Provider: "brew", Package: "ripgrep"},
		},
		Providers: []config.ToolInstallSpec{
			{Provider: "apt", Package: "ripgrep"},
		},
		Fallback: githubFallbackSpec(),
	}, make(map[string]bool))

	if route.Kind != installRouteNative {
		t.Fatalf("route kind = %q, want native", route.Kind)
	}
	if route.Install.Provider != "brew" || route.Install.Package != "ripgrep" {
		t.Fatalf("route install = %+v, want host override", route.Install)
	}
	if len(route.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want no skipped candidates for host override", route.Skipped)
	}
	if apt.calls != 0 || brew.calls != 0 {
		t.Fatalf("provider availability calls = apt:%d brew:%d, want none", apt.calls, brew.calls)
	}
}

func githubFallbackSpec() *config.FallbackSpec {
	return &config.FallbackSpec{
		Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
		Status: config.FallbackStatusUnverified,
		Commands: config.FallbackCommands{
			Install: "install rg",
			Check:   "command -v rg",
		},
	}
}
