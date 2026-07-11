package app

import (
	"context"
	"os"
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

// DashboardAgentsSummary is the manifest+lock rollup for the TUI dashboard's
// agents/skills/mcp/plugins section.
type DashboardAgentsSummary struct {
	SkillPackages        int
	SkillsInstalled      int
	SkillsMissing        int
	SkillsMissingNames   []string
	SkillsUnmanaged      int
	SkillsUnmanagedNames []string
	McpServers           int
	Plugins              int
	Marketplaces         int
	AgentsEnabled        bool
}

// Managed is the count of manifest-tracked resources.
func (s DashboardAgentsSummary) Managed() int {
	return s.SkillPackages + s.McpServers + s.Plugins
}

// OutOfSync is the count of skills needing attention: not yet installed, or
// installed but no longer tracked in the manifest. Deliberately narrower than
// the Agents tab's Out of Sync section: mcp/plugin state needs adapter exec
// calls, which the dashboard must never make.
func (s DashboardAgentsSummary) OutOfSync() int {
	return s.SkillsMissing + s.SkillsUnmanaged
}

// DashboardAgentsSummary builds the agents dashboard rollup. When agent
// skills are disabled for this host, it returns a zero-value summary without
// touching the skill lockfile.
func (a *App) DashboardAgentsSummary(ctx context.Context, cfg *config.RootConfig) (DashboardAgentsSummary, error) {
	if !a.AgentsEnabled(cfg) {
		return DashboardAgentsSummary{AgentsEnabled: false}, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return DashboardAgentsSummary{}, err
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return DashboardAgentsSummary{}, err
	}

	resolved := resolveSkillPackages(cfg, currentMachineGroupName())
	pluginNames := installedPluginNames(ctx, a)
	installed := 0
	var missingNames []string
	for _, p := range resolved {
		if ok, _ := packageLockStatus(lock, p.Source); ok {
			installed++
			continue
		}
		targets := p.Agents
		if len(targets) == 0 {
			targets = a.EnabledAgentIDs(cfg)
		}
		repoName := skillPackageRepoName(p.Source)
		shadowed := false
		for _, id := range targets {
			if pluginShadowsName(pluginNames, id, repoName) {
				shadowed = true
				break
			}
		}
		if shadowed {
			installed++
			continue
		}
		missingNames = append(missingNames, packageDisplayName(p.Source))
	}
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

	return DashboardAgentsSummary{
		SkillPackages:        len(resolved),
		SkillsInstalled:      installed,
		SkillsMissing:        len(missingNames),
		SkillsMissingNames:   missingNames,
		SkillsUnmanaged:      len(unmanaged),
		SkillsUnmanagedNames: unmanagedNames,
		McpServers:           len(cfg.Agents.McpServers),
		Plugins:              len(cfg.Agents.Plugins),
		Marketplaces:         len(cfg.Agents.Marketplaces),
		AgentsEnabled:        true,
	}, nil
}
