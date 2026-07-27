package app

import (
	"context"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

// EffectiveManagers — Honours settings hints, falling back to PATH probing in preference order; empty when none is found.
func (a *App) EffectiveManagers() (pythonBin, nodeBin string) {
	s, _ := a.LoadSettings()
	return a.effectiveManagersFromSettings(s)
}

func (a *App) effectiveManagersFromSettings(s config.Settings) (pythonBin, nodeBin string) {
	pythonBin = probeFirst(EffectiveEcosystemManager(s, provider.EcosystemPython), a.managerNames(provider.EcosystemPython))
	nodeBin = probeFirst(EffectiveEcosystemManager(s, provider.EcosystemNode), a.managerNames(provider.EcosystemNode))
	return pythonBin, nodeBin
}

// ResolvedEcosystemProviders — Only families implementing provider.ConcreteResolver and currently available.
func (a *App) ResolvedEcosystemProviders(ctx context.Context) map[string]string {
	ecos := a.registry.EcosystemProviders()
	type resolved struct {
		name     string
		concrete string
	}
	out := make([]resolved, len(ecos))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range ecos {
		g.Go(func() error {
			cr, ok := p.(provider.ConcreteResolver)
			if !ok {
				return nil
			}
			concrete, err := cr.ResolvedName(gctx)
			if err == nil && concrete != "" {
				out[i] = resolved{name: p.Name(), concrete: concrete}
			}
			return nil
		})
	}
	// Goroutines return nil unconditionally; Wait only fails on panic recovery.
	if err := g.Wait(); err != nil {
		return nil
	}
	result := make(map[string]string, len(out))
	for _, r := range out {
		if r.name != "" {
			result[r.name] = r.concrete
		}
	}
	return result
}

func (a *App) EffectiveSystemManager(ctx context.Context) string {
	return a.ResolvedEcosystemProviders(ctx)[provider.EcosystemSystem]
}

// AllAvailableManagers — Every available manager, unlike EffectiveManagers' single preferred binary.
func (a *App) AllAvailableManagers() (pythonBins, nodeBins []string) {
	s, _ := a.LoadSettings()
	return a.allAvailableManagersFromSettings(s)
}

func (a *App) allAvailableManagersFromSettings(s config.Settings) (pythonBins, nodeBins []string) {
	pythonBins = probeAll(EffectiveEcosystemManager(s, provider.EcosystemPython), a.managerNames(provider.EcosystemPython))
	nodeBins = probeAll(EffectiveEcosystemManager(s, provider.EcosystemNode), a.managerNames(provider.EcosystemNode))
	return pythonBins, nodeBins
}

type SetupProviderOption struct {
	Name    string
	Label   string
	Enabled bool
}

func (a *App) SetupProviderOptions(ctx context.Context, settings config.Settings) []SetupProviderOption {
	pythonBins, nodeBins := a.allAvailableManagersFromSettings(settings)
	return SetupProviderOptionsFromManagers(a.ResolvedEcosystemProviders(ctx), pythonBins, nodeBins, settings)
}

func SetupProviderOptionsFromManagers(metaMap map[string]string, allPyBins, allNodeBins []string, settings config.Settings) []SetupProviderOption {
	managerLabel := func(meta string, bins []string) string {
		if len(bins) == 0 {
			return meta
		}
		return meta + "(" + strings.Join(bins, " • ") + ")"
	}

	type entry struct {
		name  string
		label string
	}
	entries := []entry{
		{provider.EcosystemSystem, provider.EcosystemSystem},
		{provider.EcosystemNode, managerLabel(provider.EcosystemNode, allNodeBins)},
		{provider.EcosystemPython, managerLabel(provider.EcosystemPython, allPyBins)},
	}
	if concrete := metaMap[provider.EcosystemSystem]; concrete != "" {
		entries[0].label = provider.EcosystemSystem + "(" + concrete + ")"
	}

	rows := make([]SetupProviderOption, 0, len(entries))
	for _, e := range entries {
		isEnabled := !slices.Contains(settings.DisabledProviders, e.name)
		rows = append(rows, SetupProviderOption{Name: e.name, Label: e.label, Enabled: isEnabled})
	}
	return rows
}

func SetupDisabledProviders(options []SetupProviderOption) []string {
	var disabled []string
	for _, option := range options {
		if !option.Enabled {
			disabled = append(disabled, option.Name)
		}
	}
	return disabled
}

// Falls back to the first candidate from priority found on PATH.
func probeFirst(hint string, priority []string) string {
	if hint != "" {
		if managerAvailable(hint) {
			return hint
		}
	}
	for _, bin := range priority {
		if managerAvailable(bin) {
			return bin
		}
	}
	return ""
}

// A non-empty hint on PATH is included first, deduplicated.
func probeAll(hint string, priority []string) []string {
	seen := make(map[string]bool)
	var found []string
	add := func(bin string) {
		if bin != "" && !seen[bin] {
			if managerAvailable(bin) {
				seen[bin] = true
				found = append(found, bin)
			}
		}
	}
	add(hint)
	for _, bin := range priority {
		add(bin)
	}
	return found
}

func managerAvailable(bin string) bool {
	resolved, _ := executor.ResolveCommand(bin)
	return resolved != bin
}
