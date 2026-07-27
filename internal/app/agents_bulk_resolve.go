package app

import "context"

// ResolveAllDriftOptions selects the side every drifted resource is settled with.
type ResolveAllDriftOptions struct {
	UseManaged bool
	DryRun     bool
}

// BulkDriftResolution — Per-item failures are collected, not fatal: one contested entry never blocks the rest.
type BulkDriftResolution struct {
	SkillsResolved  int
	McpResolved     int
	PluginsResolved int
	Actions         []string
	Warnings        []string
	Errors          []string
}

func (r BulkDriftResolution) Resolved() int {
	return r.SkillsResolved + r.McpResolved + r.PluginsResolved
}

func (a *App) ResolveAllDrift(ctx context.Context, opts ResolveAllDriftOptions) (BulkDriftResolution, error) {
	var result BulkDriftResolution
	cfg, err := a.loadConfig()
	if err != nil {
		return result, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return result, err
	}

	skillStrategy := SkillDriftUseLocal
	mcpStrategy := McpDriftUseLocal
	pluginStrategy := PluginDriftUseLocal
	if opts.UseManaged {
		skillStrategy = SkillDriftUseManaged
		mcpStrategy = McpDriftUseManaged
		pluginStrategy = PluginDriftUseManaged
	}

	rows, err := a.SkillPackageRows(ctx)
	if err != nil {
		result.Errors = append(result.Errors, "skills: "+err.Error())
	} else {
		for _, source := range driftedSkillSources(rows) {
			res, err := a.ResolveSkillDrift(ctx, ResolveSkillDriftOptions{
				Source: source, Strategy: skillStrategy, DryRun: opts.DryRun,
			})
			result.Actions = append(result.Actions, res.Actions...)
			result.Warnings = append(result.Warnings, res.Warnings...)
			if err != nil {
				result.Errors = append(result.Errors, source+": "+err.Error())
				continue
			}
			result.SkillsResolved++
		}
	}

	mcpRows, _, err := a.McpServerRows(ctx)
	if err != nil {
		result.Errors = append(result.Errors, "mcp: "+err.Error())
	} else {
		for _, row := range mcpRows {
			if !row.Drifted {
				continue
			}
			res, err := a.ResolveMcpDrift(ctx, ResolveMcpDriftOptions{
				Name: row.Name, Strategy: mcpStrategy, DryRun: opts.DryRun,
			})
			result.Actions = append(result.Actions, res.Actions...)
			result.Warnings = append(result.Warnings, res.Warnings...)
			if err != nil {
				result.Errors = append(result.Errors, row.Name+": "+err.Error())
				continue
			}
			result.McpResolved++
		}
	}

	pluginRows, _, err := a.PluginRows(ctx)
	if err != nil {
		result.Errors = append(result.Errors, "plugins: "+err.Error())
	} else {
		for _, row := range pluginRows {
			if !row.Drifted {
				continue
			}
			res, err := a.ResolvePluginDrift(ctx, ResolvePluginDriftOptions{
				Name: row.Name, Strategy: pluginStrategy, DryRun: opts.DryRun,
			})
			result.Actions = append(result.Actions, res.Actions...)
			result.Warnings = append(result.Warnings, res.Warnings...)
			if err != nil {
				result.Errors = append(result.Errors, row.Name+": "+err.Error())
				continue
			}
			result.PluginsResolved++
		}
	}

	return result, nil
}

func driftedSkillSources(rows []SkillPackageRow) []string {
	var sources []string
	for _, row := range rows {
		for _, status := range row.PerAgentStatus {
			if status == SkillStatusDrifted {
				sources = append(sources, row.Source)
				break
			}
		}
	}
	return sources
}
