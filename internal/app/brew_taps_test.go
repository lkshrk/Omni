package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

// tapResolverStub is a brew provider stub that also resolves tap-qualified
// names for the BackfillBrewTaps heal.
type tapResolverStub struct {
	stubProvider
	resolutions map[string][2]string // bare name -> [fullName, tap]
}

func (s *tapResolverStub) ResolveTap(_ context.Context, name string) (string, string, bool) {
	if r, ok := s.resolutions[name]; ok {
		return r[0], r[1], true
	}
	return "", "", false
}

func newTapResolverStub(resolutions map[string][2]string) *tapResolverStub {
	return &tapResolverStub{
		stubProvider: stubProvider{name: "brew", available: true},
		resolutions:  resolutions,
	}
}

func loadRootConfig(t *testing.T, path string) *config.RootConfig {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestBackfillBrewTaps_RewritesBarePackageAndAddsTap(t *testing.T) {
	t.Parallel()
	stub := newTapResolverStub(map[string][2]string{
		"quarkdown": {"quarkdown-labs/quarkdown/quarkdown", "quarkdown-labs/quarkdown"},
	})
	a, cfgPath := newImportApp(t, stub)
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"quarkdown": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "quarkdown"}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("quarkdown")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	backfilled, err := a.BackfillBrewTaps(context.Background(), false)
	if err != nil {
		t.Fatalf("BackfillBrewTaps: %v", err)
	}
	if len(backfilled) != 1 {
		t.Fatalf("backfilled = %d, want 1: %+v", len(backfilled), backfilled)
	}
	got := backfilled[0]
	if got.Name != "quarkdown" || got.NewPackage != "quarkdown-labs/quarkdown/quarkdown" || got.Tap != "quarkdown-labs/quarkdown" {
		t.Fatalf("backfill = %+v, want quarkdown tap-qualified", got)
	}

	saved := loadRootConfig(t, cfgPath)
	spec := saved.Tools["quarkdown"]
	if len(spec.Providers) != 1 || spec.Providers[0].Package != "quarkdown-labs/quarkdown/quarkdown" {
		t.Fatalf("saved package = %+v, want tap-qualified", spec.Providers)
	}
	if len(spec.Taps) != 1 || spec.Taps[0] != "quarkdown-labs/quarkdown" {
		t.Fatalf("saved taps = %v, want [quarkdown-labs/quarkdown]", spec.Taps)
	}
}

func TestBackfillBrewTaps_DryRunDoesNotWrite(t *testing.T) {
	t.Parallel()
	stub := newTapResolverStub(map[string][2]string{
		"yabai": {"asmvik/formulae/yabai", "asmvik/formulae"},
	})
	a, cfgPath := newImportApp(t, stub)
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"yabai": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "yabai"}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("yabai")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	backfilled, err := a.BackfillBrewTaps(context.Background(), true)
	if err != nil {
		t.Fatalf("BackfillBrewTaps: %v", err)
	}
	if len(backfilled) != 1 {
		t.Fatalf("dry-run backfilled = %d, want 1", len(backfilled))
	}
	saved := loadRootConfig(t, cfgPath)
	if pkg := saved.Tools["yabai"].Providers[0].Package; pkg != "yabai" {
		t.Fatalf("dry-run must not write; package = %q, want yabai", pkg)
	}
}

func TestBackfillBrewTaps_LeavesCoreFormulaeAndQualifiedAlone(t *testing.T) {
	t.Parallel(
	// wget is a core formula (ResolveTap returns false); terraform is already
	// tap-qualified. Neither should be touched.
	)

	stub := newTapResolverStub(map[string][2]string{})
	a, cfgPath := newImportApp(t, stub)
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"wget":      {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "wget"}}},
			"terraform": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "hashicorp/tap/terraform"}}},
		},
		Groups: []*config.GroupConfig{{Tools: groupTools("wget", "terraform")}},
	}
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	backfilled, err := a.BackfillBrewTaps(context.Background(), false)
	if err != nil {
		t.Fatalf("BackfillBrewTaps: %v", err)
	}
	if len(backfilled) != 0 {
		t.Fatalf("backfilled = %+v, want none", backfilled)
	}
}
