package app

import (
	"context"
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

type DashboardAgentsSummary struct {
	SkillPackages        int
	SkillsInstalled      int
	SkillsMissing        int
	SkillsMissingNames   []string
	SkillsDrifted        int
	SkillsDriftedNames   []string
	SkillsUnmanaged      int
	SkillsUnmanagedNames []string
	SkillsOutdated       int
	SkillsOutdatedNames  []string
	McpServers           int
	McpDrifted           int
	McpDriftedNames      []string
	Plugins              int
	PluginsDrifted       int
	PluginsDriftedNames  []string
	PluginsOutdated      int
	PluginsOutdatedNames []string
	Marketplaces         int
	AgentsEnabled        bool
}

func (s DashboardAgentsSummary) Managed() int {
	return s.SkillPackages + s.McpServers + s.Plugins
}

// OutOfSync — Disjoint buckets: outdated packages are installed and consistent, so they stay in their own count.
func (s DashboardAgentsSummary) OutOfSync() int {
	return s.SkillsMissing + s.SkillsDrifted + s.SkillsUnmanaged +
		s.McpDrifted + s.PluginsDrifted
}

// DashboardAgentsSummary — Returns a zero-value summary when agent skills are disabled, without touching inventory or the lockfile.
func (a *App) DashboardAgentsSummary(ctx context.Context, cfg *config.RootConfig) (DashboardAgentsSummary, error) {
	if !a.AgentsEnabled(cfg) {
		return DashboardAgentsSummary{AgentsEnabled: false}, nil
	}

	rows, err := a.SkillPackageRows(ctx)
	if err != nil {
		return DashboardAgentsSummary{}, err
	}
	// A drifted package is installed, so its pending upgrade is real; a missing one counts only as missing.
	counts := classifySkillRows(rows)
	// An unreadable source is a gap the user must close, so it joins the missing bucket rather than vanishing.
	missingNames := append(counts.Missing, counts.Errored...)
	sort.Strings(missingNames)

	unmanaged, err := a.UnmanagedSkillPackages(ctx)
	if err != nil {
		return DashboardAgentsSummary{}, err
	}
	unmanagedNames := make([]string, 0, len(unmanaged))
	for _, row := range unmanaged {
		unmanagedNames = append(unmanagedNames, packageDisplayName(row.Source))
	}
	sort.Strings(unmanagedNames)

	mcpDrifted, pluginsDrifted, pluginsOutdated := a.dashboardAdapterState(ctx)

	return DashboardAgentsSummary{
		SkillPackages:        len(rows),
		SkillsInstalled:      counts.Installed,
		SkillsMissing:        len(missingNames),
		SkillsMissingNames:   missingNames,
		SkillsDrifted:        len(counts.Drifted),
		SkillsDriftedNames:   counts.Drifted,
		SkillsUnmanaged:      len(unmanaged),
		SkillsUnmanagedNames: unmanagedNames,
		SkillsOutdated:       len(counts.Outdated),
		SkillsOutdatedNames:  counts.Outdated,
		McpServers:           len(cfg.Agents.McpServers),
		McpDrifted:           len(mcpDrifted),
		McpDriftedNames:      mcpDrifted,
		Plugins:              len(cfg.Agents.Plugins),
		PluginsDrifted:       len(pluginsDrifted),
		PluginsDriftedNames:  pluginsDrifted,
		PluginsOutdated:      len(pluginsOutdated),
		PluginsOutdatedNames: pluginsOutdated,
		Marketplaces:         len(cfg.Agents.Marketplaces),
		AgentsEnabled:        true,
	}, nil
}

// A listing failure yields no names rather than an error: one unreachable agent CLI must not blank the section.
func (a *App) dashboardAdapterState(ctx context.Context) (mcpDrifted, pluginsDrifted, pluginsOutdated []string) {
	if mcpRows, _, err := a.McpServerRows(ctx); err == nil {
		for _, row := range mcpRows {
			if row.Drifted {
				mcpDrifted = append(mcpDrifted, row.Name)
			}
		}
	}
	if pluginRows, _, err := a.PluginRows(ctx); err == nil {
		for _, row := range pluginRows {
			if row.Drifted {
				pluginsDrifted = append(pluginsDrifted, row.Name)
			}
			// Drift and an available upgrade are independent facts.
			if row.Outdated() {
				pluginsOutdated = append(pluginsOutdated, row.Name)
			}
		}
	}
	sort.Strings(mcpDrifted)
	sort.Strings(pluginsDrifted)
	sort.Strings(pluginsOutdated)
	return mcpDrifted, pluginsDrifted, pluginsOutdated
}
