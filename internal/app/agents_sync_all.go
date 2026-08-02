package app

import (
	"context"
	"fmt"
)

const (
	AgentsFeatureImport  = "skills import"
	AgentsFeatureSkills  = "skills"
	AgentsFeatureMcp     = "mcp"
	AgentsFeaturePlugins = "plugins"
)

type AgentsSyncAllOptions struct {
	// Plain restore stays converge-only, matching the per-feature restores; only sync-all surfaces set it.
	ImportUnmanaged bool
	DryRun          bool
	Progress        func(string)
}

type AgentsFeatureError struct {
	Feature string
	Message string
}

func (e AgentsFeatureError) Error() string {
	return e.Feature + ": " + e.Message
}

// AgentsSyncAllResult — Warnings, Drift and Errors are the flattened report, so a caller printing both does not repeat itself.
type AgentsSyncAllResult struct {
	Imported       ImportDiff
	McpAdopted     McpAdoptResult
	PluginsAdopted PluginAdoptResult
	Skills         RestoreSkillsResult
	SkillsDryRun   []string
	Mcp            RestoreMcpResult
	Plugins        RestorePluginResult
	Warnings       []string
	Drift          []string
	Errors         []AgentsFeatureError
}

func (r *AgentsSyncAllResult) addWarnings(feature string, warnings []string) {
	for _, w := range warnings {
		r.Warnings = append(r.Warnings, feature+": "+w)
	}
}

func (r *AgentsSyncAllResult) addError(feature, message string) {
	r.Errors = append(r.Errors, AgentsFeatureError{Feature: feature, Message: message})
}

func (r *AgentsSyncAllResult) AddSkillsImport(diff ImportDiff, err error) {
	r.Imported = diff
	r.addWarnings(AgentsFeatureImport, diff.Warnings)
	if err != nil {
		r.addError(AgentsFeatureImport, err.Error())
	}
}

func (r *AgentsSyncAllResult) AddSkills(res RestoreSkillsResult, dryRun []string, err error) {
	r.Skills, r.SkillsDryRun = res, dryRun
	r.addWarnings(AgentsFeatureSkills, res.Warnings)
	r.Drift = append(r.Drift, res.Drift...)
	if err != nil {
		r.addError(AgentsFeatureSkills, err.Error())
	}
	for _, f := range res.Failed {
		r.addError(AgentsFeatureSkills, fmt.Sprintf("%s: %s", f.Name, f.Message))
	}
}

func (r *AgentsSyncAllResult) AddMcp(res RestoreMcpResult, err error) {
	r.Mcp = res
	r.addWarnings(AgentsFeatureMcp, res.Warnings)
	r.Drift = append(r.Drift, res.Drift...)
	if err != nil {
		r.addError(AgentsFeatureMcp, err.Error())
	}
	for _, e := range res.Errors {
		r.addError(AgentsFeatureMcp, fmt.Sprintf("%s/%s: %v", e.AgentID, e.ServerName, e.Err))
	}
}

func (r *AgentsSyncAllResult) AddPlugins(res RestorePluginResult, err error) {
	r.Plugins = res
	r.addWarnings(AgentsFeaturePlugins, res.Warnings)
	r.Drift = append(r.Drift, res.Drift...)
	if err != nil {
		r.addError(AgentsFeaturePlugins, err.Error())
	}
	for _, e := range res.Errors {
		r.addError(AgentsFeaturePlugins, fmt.Sprintf("%s/%s: %v", e.AgentID, e.Name, e.Err))
	}
}

// AgentsSyncAll — Each feature keeps its own enablement gate, and a failing feature never stops the ones after it.
func (a *App) AgentsSyncAll(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return AgentsSyncAllResult{}, err
	}

	var res AgentsSyncAllResult
	progress := func(doing, would string) {
		if opts.Progress == nil {
			return
		}
		if opts.DryRun {
			opts.Progress(would)
			return
		}
		opts.Progress(doing)
	}

	if opts.ImportUnmanaged && a.PluginsEnabled(cfg) {
		progress("importing unmanaged plugins…", "would import unmanaged plugins…")
		adopted, err := a.adoptUnmanagedPlugins(ctx, opts.DryRun)
		res.PluginsAdopted = adopted
		res.addWarnings(AgentsFeaturePlugins, adopted.Skipped)
		if err != nil {
			res.addError(AgentsFeaturePlugins, err.Error())
		}
	}

	pluginNames, pluginWarnings := installedPluginNames(ctx, a)
	progress("restoring plugins…", "would restore plugins…")
	plugins, err := a.RestorePlugins(ctx, RestorePluginOptions{DryRun: opts.DryRun})
	res.AddPlugins(plugins, err)
	ctx = projectPluginInstalls(ctx, pluginNames, pluginWarnings, plugins)

	if opts.ImportUnmanaged && a.SkillsEnabled(cfg) {
		progress("importing unmanaged skills…", "would import unmanaged skills…")
		diff, err := a.ImportSkills(ctx, ImportSkillsOptions{DryRun: opts.DryRun})
		res.AddSkillsImport(diff, err)
	}

	progress("restoring skills…", "would restore skills…")
	skills, lines, err := a.RestoreSkills(ctx, RestoreSkillsOptions{DryRun: opts.DryRun})
	res.AddSkills(skills, lines, err)

	if opts.ImportUnmanaged && a.McpEnabled(cfg) {
		progress("importing unmanaged mcp servers…", "would import unmanaged mcp servers…")
		adopted, err := a.adoptUnmanagedMcpServers(ctx, mcpAdoptOptions{DryRun: opts.DryRun})
		// Conflicts, Skipped and Warnings travel on McpAdopted alone; mirroring two of the three into
		// res.Warnings printed them twice and left the third looking like a different kind of line.
		res.McpAdopted = adopted
		if err != nil {
			res.addError(AgentsFeatureMcp, err.Error())
		}
	}

	progress("restoring mcp servers…", "would restore mcp servers…")
	mcp, err := a.RestoreMcpServers(ctx, RestoreMcpOptions{DryRun: opts.DryRun})
	res.AddMcp(mcp, err)

	return res, nil
}

// AgentsSyncAllSummaryText — The imported count spans every claimed capability, not skills alone.
// Deliberately untouched packages are counted too: a caller that shows only this string would otherwise
// read a sync that skipped work as a clean one.
func AgentsSyncAllSummaryText(res AgentsSyncAllResult) string {
	imported := len(res.Imported.Added) +
		res.McpAdopted.Adopted + len(res.McpAdopted.WouldAdopt) +
		res.PluginsAdopted.Adopted + len(res.PluginsAdopted.WouldAdopt)
	summary := fmt.Sprintf("%d imported, %d skills installed, %d mcp servers installed, %d plugins installed, %d failed",
		imported, len(res.Skills.Installed), len(res.Mcp.Installed), len(res.Plugins.Installed), len(res.Errors))
	for _, extra := range []struct {
		n     int
		label string
	}{
		{len(res.Drift), "drifted"},
		{len(res.Skills.ShadowedByPlugin), "skipped (provided by plugin)"},
		{len(res.Warnings), "warnings"},
	} {
		if extra.n > 0 {
			summary += fmt.Sprintf(", %d %s", extra.n, extra.label)
		}
	}
	return summary
}
