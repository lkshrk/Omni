package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SystemInventoryAbsorption — Absorbed lists the packages a re-baseline stops reporting as discovered on this provider.
type SystemInventoryAbsorption struct {
	Provider string
	Absorbed []string
	Total    int
}

// SystemInventoryRebaseline — Providers is sorted by name and omits providers with nothing to absorb.
type SystemInventoryRebaseline struct {
	Providers []SystemInventoryAbsorption
}

func (r SystemInventoryRebaseline) AbsorbedCount() int {
	total := 0
	for _, p := range r.Providers {
		total += len(p.Absorbed)
	}
	return total
}

// RebaselineSystemInventory — Re-snapshots system package managers so everything installed now stops surfacing as discovered; the baseline is per-host state, and later installs still surface.
func (a *App) RebaselineSystemInventory(ctx context.Context, dryRun bool) (SystemInventoryRebaseline, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return SystemInventoryRebaseline{}, err
	}
	protected := make(map[string]bool, len(cfg.Tools))
	// Providers report package names, which a declared tool may override per provider (fd -> apt fd-find).
	for name, spec := range cfg.Tools {
		protected[name] = true
		if spec.Package != "" {
			protected[spec.Package] = true
		}
		for _, install := range spec.Providers {
			if install.Package != "" {
				protected[install.Package] = true
			}
		}
	}
	for name := range ignoredToolSet(cfg) {
		protected[name] = true
	}

	var result SystemInventoryRebaseline
	for _, p := range a.registry.All() {
		if !isSystemInventoryProvider(p.Name()) {
			continue
		}
		available, err := p.Available(ctx)
		if err != nil || !available {
			continue
		}
		installed, err := p.ListInstalled(ctx)
		if err != nil {
			return SystemInventoryRebaseline{}, fmt.Errorf("listing installed %s packages: %w", p.Name(), err)
		}
		key := systemInventoryBaselineStateKey(p.Name())
		current, err := a.readDB().GetState(ctx, key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SystemInventoryRebaseline{}, fmt.Errorf("reading system inventory baseline for %s: %w", p.Name(), err)
		}
		baseline := systemInventoryBaselineSet(current)
		var absorbed []string
		for _, t := range installed {
			// A tracked or explicitly ignored tool keeps its own state; absorbing it would silently retire that decision.
			if baseline[t.Name] || protected[t.Name] {
				continue
			}
			baseline[t.Name] = true
			absorbed = append(absorbed, t.Name)
		}
		if len(absorbed) == 0 {
			continue
		}
		sort.Strings(absorbed)
		names := make([]string, 0, len(baseline))
		for name := range baseline {
			names = append(names, name)
		}
		sort.Strings(names)
		if !dryRun {
			if err := a.readDB().SetState(ctx, key, strings.Join(names, "\n")); err != nil {
				return SystemInventoryRebaseline{}, fmt.Errorf("recording system inventory baseline for %s: %w", p.Name(), err)
			}
		}
		result.Providers = append(result.Providers, SystemInventoryAbsorption{
			Provider: p.Name(),
			Absorbed: absorbed,
			Total:    len(names),
		})
	}
	sort.Slice(result.Providers, func(i, j int) bool {
		return result.Providers[i].Provider < result.Providers[j].Provider
	})
	return result, nil
}
