package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// driftStubProvider is a provider whose availability can be set per-test.
type driftStubProvider struct {
	stubProvider
	// installedMap reports what is installed (name → installedWith).
	installedMap map[string]string
}

func newDriftProvider(name string, available bool, installed map[string]string) *driftStubProvider {
	return &driftStubProvider{
		stubProvider: stubProvider{name: name, available: available},
		installedMap: installed,
	}
}

func (p *driftStubProvider) IsInstalled(_ context.Context, t provider.Tool) (bool, string, error) {
	ver, ok := p.installedMap[t.Name]
	return ok, ver, nil
}

func (p *driftStubProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	tools := make([]provider.InstalledTool, 0, len(p.installedMap))
	for name, ver := range p.installedMap {
		tools = append(tools, provider.InstalledTool{
			Tool:    provider.Tool{Name: name, Provider: p.name},
			Version: ver,
		})
	}
	return tools, nil
}

// seedDB writes ToolCache rows directly so drift tests can control InstalledWith
// without running a full RefreshInstalled cycle.
func seedDB(t *testing.T, a *app.App, rows []*database.ToolCache) {
	t.Helper()
	ctx := context.Background()
	if err := a.DB().UpsertBatch(ctx, rows); err != nil {
		t.Fatalf("seedDB: %v", err)
	}
}

// buildDriftConfig returns a RootConfig with the given tool→provider mapping
// and a host group that contains all tools.
func buildDriftConfig(toolProviders map[string]string) *config.RootConfig {
	tools := make(map[string]config.ToolSpec, len(toolProviders))
	entries := make([]config.ToolEntry, 0, len(toolProviders))
	for name, prov := range toolProviders {
		tools[name] = config.ToolSpec{
			Providers: []config.ToolInstallSpec{{Provider: prov}},
		}
		entries = append(entries, config.ToolEntry{Name: name})
	}
	return &config.RootConfig{
		Tools: tools,
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: entries},
		},
	}
}

// ─── Class 1: provider unusable ──────────────────────────────────────────────

func TestDoctorDrift_ProviderUnusable_DetectedWhenProviderUnavailable(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	unavail := newDriftProvider("brew", false, nil)
	a, cfgPath := newImportApp(t, unavail)
	if err := saveAppConfig(t, cfgPath, buildDriftConfig(map[string]string{
		"git": "brew",
	})); err != nil {
		t.Fatalf("save config: %v", err)
	}
	// No DB row → tool is not installed anywhere.

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil {
		t.Fatalf("missing drift check in %+v", result.Checks)
	}
	if check.Status != app.DoctorStatusWarn {
		t.Fatalf("drift status = %q, want warn: %+v", check.Status, check)
	}
	if !strings.Contains(check.Message, "1 drift finding") {
		t.Fatalf("drift message = %q, want 1 drift finding", check.Message)
	}
	if len(check.Groups) == 0 {
		t.Fatalf("drift groups empty, want at least one class group")
	}
	found := driftGroupContains(check.Groups, "git")
	if !found {
		t.Fatalf("drift groups do not mention 'git': %+v", check.Groups)
	}
	if !driftGroupContains(check.Groups, "fallback") && !driftGroupContains(check.Groups, "provider") {
		t.Fatalf("drift suggestion missing provider guidance: %+v", check.Groups)
	}
}

func TestDoctorDrift_ProviderUnusable_UnregisteredProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	// Register "brew" only — "npm" is not registered at all.
	a, cfgPath := newImportApp(t, newDriftProvider("brew", true, nil))
	if err := saveAppConfig(t, cfgPath, buildDriftConfig(map[string]string{
		"typescript": "npm", // npm not registered → unregistered provider
	})); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil || check.Status != app.DoctorStatusWarn {
		t.Fatalf("drift check = %+v, want warn", check)
	}
	if !driftGroupContains(check.Groups, "typescript") {
		t.Fatalf("drift groups do not mention 'typescript': %+v", check.Groups)
	}
}

// ─── Class 2: wrong-provider install ─────────────────────────────────────────

func TestDoctorDrift_WrongProvider_DetectedWhenInstalledWithDiffers(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	brew := newDriftProvider("brew", true, map[string]string{"pnpm": "0.1.0"})
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, buildDriftConfig(map[string]string{
		"pnpm": "brew",
	})); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Seed the DB with InstalledWith="corepack" to simulate wrong-provider install.
	seedDB(t, a, []*database.ToolCache{{
		Name:          "pnpm",
		Provider:      "brew",
		Package:       "pnpm",
		Installed:     true,
		InstalledWith: "corepack",
	}})

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil || check.Status != app.DoctorStatusWarn {
		t.Fatalf("drift check = %+v, want warn", check)
	}
	if !driftGroupContains(check.Groups, "pnpm") {
		t.Fatalf("drift groups do not mention 'pnpm': %+v", check.Groups)
	}
	if !driftGroupContains(check.Groups, "corepack") {
		t.Fatalf("drift groups do not mention actual provider 'corepack': %+v", check.Groups)
	}
	if !driftGroupContains(check.Groups, "reconcile") && !driftGroupContains(check.Groups, "sync") {
		t.Fatalf("drift suggestion missing reconcile guidance: %+v", check.Groups)
	}
}

// ─── Class 3: provider unavailable but tool present another way ───────────────

func TestDoctorDrift_UnavailableButPresent_DetectedViaInstalledWith(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	// brew is unavailable but pnpm is present via corepack (HCL-26 PATH detection
	// would have written the InstalledWith row).
	unavailBrew := newDriftProvider("brew", false, nil)
	a, cfgPath := newImportApp(t, unavailBrew)
	if err := saveAppConfig(t, cfgPath, buildDriftConfig(map[string]string{
		"pnpm": "brew",
	})); err != nil {
		t.Fatalf("save config: %v", err)
	}

	seedDB(t, a, []*database.ToolCache{{
		Name:          "pnpm",
		Provider:      "brew",
		Package:       "pnpm",
		Installed:     true,
		InstalledWith: "corepack",
	}})

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil || check.Status != app.DoctorStatusWarn {
		t.Fatalf("drift check = %+v, want warn", check)
	}
	if !driftGroupContains(check.Groups, "pnpm") {
		t.Fatalf("drift groups do not mention 'pnpm': %+v", check.Groups)
	}
	if !driftGroupContains(check.Groups, "corepack") {
		t.Fatalf("drift groups do not mention actual provider 'corepack': %+v", check.Groups)
	}
	// Suggestion must guide user to reconfigure provider.
	if !driftGroupContains(check.Groups, "reconfigure") {
		t.Fatalf("drift suggestion missing 'reconfigure' guidance: %+v", check.Groups)
	}
}

// ─── Class 1 false-positive guard: working fallback ──────────────────────────

func TestDoctorDrift_NoDrift_WhenPrimaryProviderUnavailableButFallbackWorks(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	// Primary provider "pip" is unavailable, but "brew" is available and listed
	// as a secondary Providers[] entry. The resolver picks "brew" as the usable
	// route — this must NOT produce a Class-1 finding.
	unavailPip := newDriftProvider("pip", false, nil)
	availBrew := newDriftProvider("brew", true, map[string]string{"git": "2.43.0"})
	a, cfgPath := newImportApp(t, unavailPip, availBrew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {
				Providers: []config.ToolInstallSpec{
					{Provider: "pip"},
					{Provider: "brew"},
				},
			},
		},
		Hosts: map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "git"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil {
		t.Fatalf("missing drift check in %+v", result.Checks)
	}
	if check.Status != app.DoctorStatusOK {
		t.Fatalf("drift status = %q, want ok (fallback provider available, no drift): %+v", check.Status, check)
	}
}

// ─── Class 2: PATH-detected tools (InstalledWith="") do not trigger wrong-provider ──

func TestDoctorDrift_NoDrift_WhenInstalledWithEmptyPathDetected(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	// HCL-26 PATH-detection writes Installed:true with InstalledWith:"" to avoid
	// wrong-provider classification. This must not produce a Class-2 finding.
	brew := newDriftProvider("brew", true, nil)
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, buildDriftConfig(map[string]string{
		"git": "brew",
	})); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Seed with InstalledWith="" — the HCL-26 PATH-detected shape.
	seedDB(t, a, []*database.ToolCache{{
		Name:          "git",
		Provider:      "brew",
		Package:       "git",
		Installed:     true,
		InstalledWith: "",
	}})

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil {
		t.Fatalf("missing drift check in %+v", result.Checks)
	}
	if check.Status != app.DoctorStatusOK {
		t.Fatalf("drift status = %q, want ok (InstalledWith empty → no wrong-provider finding): %+v", check.Status, check)
	}
}

// ─── Clean (no-drift) case ────────────────────────────────────────────────────

func TestDoctorDrift_NoDrift_WhenAllProvidersAvailableAndInstalledWithMatches(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	brew := newDriftProvider("brew", true, map[string]string{"git": "2.43.0"})
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, buildDriftConfig(map[string]string{
		"git": "brew",
	})); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// Seed with InstalledWith matching the configured provider.
	seedDB(t, a, []*database.ToolCache{{
		Name:          "git",
		Provider:      "brew",
		Package:       "git",
		Installed:     true,
		InstalledWith: "brew",
	}})

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil {
		t.Fatalf("missing drift check in %+v", result.Checks)
	}
	if check.Status != app.DoctorStatusOK {
		t.Fatalf("drift status = %q, want ok (no drift): %+v", check.Status, check)
	}
}

func TestDoctorDrift_NoDrift_WhenNoToolsConfigured(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")

	a, cfgPath := newImportApp(t, newDriftProvider("brew", true, nil))
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Hosts:  map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	result, err := a.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	check := doctorCheck(result, "drift")
	if check == nil || check.Status != app.DoctorStatusOK {
		t.Fatalf("drift check = %+v, want ok for empty config", check)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// driftGroupContains reports whether any group header or item contains substr.
func driftGroupContains(groups []app.DoctorDetailGroup, substr string) bool {
	for _, g := range groups {
		if strings.Contains(g.Header, substr) {
			return true
		}
		for _, item := range g.Items {
			if strings.Contains(item, substr) {
				return true
			}
		}
	}
	return false
}
