package app

import (
	"context"
	"strings"

	"github.com/lkshrk/omni/internal/database"
)

type RefreshProviderScanStep struct {
	Provider string
	Label    string
	Count    int
}

type RefreshProviderScanPlan struct {
	Steps []RefreshProviderScanStep
}

func (a *App) CurrentRefreshProviderScanPlan(ctx context.Context) (RefreshProviderScanPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return RefreshProviderScanPlan{}, err
	}
	resolvedEcosystems := map[string]string(nil)
	if a != nil && a.registry != nil {
		resolvedEcosystems = a.ResolvedEcosystemProviders(ctx)
	}
	tools, err := a.listToolsFromConfig(ctx, cfg, "", resolvedEcosystems)
	if err != nil {
		return RefreshProviderScanPlan{}, err
	}
	resolved, _ := a.resolveTools(ctx, cfg, cfg.Groups)
	providerToolCounts := a.configuredProviderToolCountsFromResolved(resolved)
	return newRefreshProviderScanPlan(tools, configuredProvidersFromCounts(providerToolCounts), providerToolCounts, resolvedEcosystems), nil
}

func (p RefreshProviderScanPlan) ProviderNames() []string {
	names := make([]string, 0, len(p.Steps))
	for _, step := range p.Steps {
		if step.Provider != "" {
			names = append(names, step.Provider)
		}
	}
	return names
}

func (p RefreshProviderScanPlan) ProviderSet() map[string]bool {
	providers := make(map[string]bool, len(p.Steps))
	for _, step := range p.Steps {
		if step.Provider != "" {
			providers[step.Provider] = true
		}
	}
	return providers
}

func (p RefreshProviderScanPlan) CountsByProvider() map[string]int {
	counts := make(map[string]int, len(p.Steps))
	for _, step := range p.Steps {
		if step.Provider != "" {
			counts[step.Provider] = step.Count
		}
	}
	return counts
}

func (p RefreshProviderScanPlan) LabelsByProvider() map[string]string {
	labels := make(map[string]string, len(p.Steps))
	for _, step := range p.Steps {
		if step.Provider != "" && step.Label != "" {
			labels[step.Provider] = step.Label
		}
	}
	return labels
}

func (p RefreshProviderScanPlan) Total() int {
	return RefreshProviderScanCountTotal(p.CountsByProvider())
}

func RefreshProviderScanCountTotal(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		if count > 0 {
			total += count
		}
	}
	return total
}

func RefreshProviderScanProviderNames(counts map[string]int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		if strings.TrimSpace(name) != "" {
			names = append(names, name)
		}
	}
	return names
}

func newRefreshProviderScanPlan(tools []*database.ToolCache, configuredProviders []string, providerToolCounts map[string]int, resolvedEcosystems map[string]string) RefreshProviderScanPlan {
	configured := make(map[string]bool, len(configuredProviders))
	for _, name := range configuredProviders {
		if name = strings.TrimSpace(name); name != "" {
			configured[name] = true
		}
	}

	coveredConcrete := make(map[string]bool, len(resolvedEcosystems))
	for ecosystem, concrete := range resolvedEcosystems {
		ecosystem = strings.TrimSpace(ecosystem)
		concrete = strings.TrimSpace(concrete)
		if ecosystem != "" && concrete != "" && configured[ecosystem] {
			coveredConcrete[concrete] = true
		}
	}

	var plan RefreshProviderScanPlan
	planned := make(map[string]bool, len(configuredProviders)+len(tools))
	addProvider := func(providerName string) {
		providerName = strings.TrimSpace(providerName)
		if providerName == "" || planned[providerName] || coveredConcrete[providerName] {
			return
		}
		planned[providerName] = true
		plan.Steps = append(plan.Steps, RefreshProviderScanStep{
			Provider: providerName,
			Label:    ProviderScanDisplayLabel(providerName, resolvedEcosystems[providerName]),
			Count:    refreshProviderScanCount(providerName, tools, providerToolCounts),
		})
	}

	for _, providerName := range configuredProviders {
		addProvider(providerName)
	}
	for _, tool := range tools {
		if tool != nil {
			addProvider(tool.Provider)
		}
	}
	return plan
}

func refreshProviderScanCount(providerName string, tools []*database.ToolCache, providerToolCounts map[string]int) int {
	count := providerToolCounts[providerName]
	if count == 0 {
		for _, tool := range tools {
			if tool == nil || !tool.Tracked || tool.Provider != providerName {
				continue
			}
			count++
		}
	}
	if count <= 0 {
		count = 1
	}
	return count
}
