package app

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type BrewTapBackfill struct {
	Name       string
	OldPackage string
	NewPackage string
	Tap        string
}

// Declared here to avoid importing the brew package from the app layer.
type brewTapResolver interface {
	Available(ctx context.Context) (bool, error)
	ResolveTap(ctx context.Context, name string) (fullName string, tap string, ok bool)
}

type brewTapResolution struct {
	fullName string
	tap      string
	ok       bool
}

type brewTapResolveFunc func(name string) brewTapResolution

// BackfillBrewTaps — A bare name loses the tap origin, so Homebrew 5.2+ hides the formula from the scan.
func (a *App) BackfillBrewTaps(ctx context.Context, dryRun bool) ([]BrewTapBackfill, error) {
	brewProv, ok := a.registry.Get("brew")
	if !ok {
		return nil, nil
	}
	resolver, ok := brewProv.(brewTapResolver)
	if !ok {
		return nil, nil
	}
	if available, err := resolver.Available(ctx); err != nil || !available {
		return nil, nil
	}

	// Cache resolutions so a package shared across tools is queried once.
	cache := map[string]brewTapResolution{}
	resolve := func(name string) brewTapResolution {
		if r, seen := cache[name]; seen {
			return r
		}
		full, tap, ok := resolver.ResolveTap(ctx, name)
		r := brewTapResolution{fullName: full, tap: tap, ok: ok}
		cache[name] = r
		return r
	}

	var backfilled []BrewTapBackfill

	if dryRun {
		cfg, err := a.loadConfig()
		if err != nil {
			return nil, err
		}
		backfilled = backfillBrewTapsInConfig(cfg, resolve)
		sortBrewTapBackfills(backfilled)
		return backfilled, nil
	}

	err := a.withConfig(func(cfg *config.RootConfig) error {
		backfilled = backfillBrewTapsInConfig(cfg, resolve)
		if len(backfilled) == 0 {
			return errSkipSave
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortBrewTapBackfills(backfilled)
	return backfilled, nil
}

// Mutates cfg in place; harmless in dry-run because the config is not persisted.
func backfillBrewTapsInConfig(cfg *config.RootConfig, resolve brewTapResolveFunc) []BrewTapBackfill {
	if cfg == nil {
		return nil
	}
	var out []BrewTapBackfill
	for name, spec := range cfg.Tools {
		changed := false
		for i := range spec.Providers {
			if rewriteBrewInstallSpec(&spec.Providers[i], &spec, name, resolve, &out) {
				changed = true
			}
		}
		// Legacy single-provider form stored on the spec itself.
		if spec.Provider == "brew" && isBareBrewPackage(spec.Package) {
			r := resolve(spec.Package)
			if r.ok && r.fullName != spec.Package {
				out = append(out, BrewTapBackfill{Name: name, OldPackage: spec.Package, NewPackage: r.fullName, Tap: r.tap})
				spec.Package = r.fullName
				addTap(&spec, r.tap)
				changed = true
			}
		}
		if changed {
			cfg.Tools[name] = spec
		}
	}
	return out
}

func rewriteBrewInstallSpec(install *config.ToolInstallSpec, spec *config.ToolSpec, name string, resolve brewTapResolveFunc, out *[]BrewTapBackfill) bool {
	if install.Provider != "brew" || !isBareBrewPackage(install.Package) {
		return false
	}
	r := resolve(install.Package)
	if !r.ok || r.fullName == install.Package {
		return false
	}
	*out = append(*out, BrewTapBackfill{Name: name, OldPackage: install.Package, NewPackage: r.fullName, Tap: r.tap})
	install.Package = r.fullName
	addTap(spec, r.tap)
	return true
}

func isBareBrewPackage(pkg string) bool {
	return pkg != "" && !strings.Contains(pkg, "/")
}

func addTap(spec *config.ToolSpec, tap string) {
	if tap == "" || slices.Contains(spec.Taps, tap) {
		return
	}
	spec.Taps = append(spec.Taps, tap)
}

func sortBrewTapBackfills(b []BrewTapBackfill) {
	sort.Slice(b, func(i, j int) bool { return b[i].Name < b[j].Name })
}
