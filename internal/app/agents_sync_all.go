package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
)

const (
	AgentsFeatureImport  = "skills import"
	AgentsFeatureSkills  = "skills"
	AgentsFeatureMcp     = "mcp"
	AgentsFeaturePlugins = "plugins"
)

type AgentsSyncAllOptions struct {
	// Kept for source compatibility while APM owns adoption and deployed-file state.
	ImportUnmanaged bool
	DryRun          bool
	Frozen          bool
	Progress        func(string)
	Output          func(stdout, stderr string)
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
	Output         string
	Stderr         string
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

func (a *App) APMClient(scope apm.Scope) *apm.Client {
	return apm.New(a.fallbackExecutor(), scope)
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

// AgentsSyncAll delegates desired state, resolution, and deployment to APM.
func (a *App) AgentsSyncAll(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	_ = cfg // Loading still validates the config before migration.
	var migration APMMigrationResult
	if !opts.DryRun && !opts.Frozen {
		migration, err = a.MigrateAgentsToAPM()
		if err != nil {
			return AgentsSyncAllResult{}, err
		}
	} else {
		migration.Path, err = a.globalAPMManifestPath()
		if err != nil {
			return AgentsSyncAllResult{}, err
		}
	}
	res := AgentsSyncAllResult{Warnings: migration.Warnings}
	if _, err := os.Stat(migration.Path); err != nil {
		if os.IsNotExist(err) {
			if opts.Frozen {
				return res, fmt.Errorf("frozen APM install requires %s and its lockfile", migration.Path)
			}
			res.Warnings = append(res.Warnings, "global APM manifest not found; the next non-dry-run sync will migrate compatible legacy declarations")
			return res, nil
		}
		return res, err
	}

	if opts.Progress != nil {
		if opts.DryRun {
			opts.Progress("previewing APM install…")
		} else {
			opts.Progress("installing APM manifest…")
		}
	}
	result, err := a.APMClient(apm.Global).Install(ctx, opts.Frozen, opts.DryRun)
	if opts.Output != nil {
		opts.Output(result.Stdout, result.Stderr)
	}
	res.Output, res.Stderr = result.Stdout, result.Stderr
	return res, err
}

// AgentsSyncAllSummaryText — The imported count spans every claimed capability, not skills alone.
// Deliberately untouched packages are counted too: a caller that shows only this string would otherwise
// read a sync that skipped work as a clean one.
func AgentsSyncAllSummaryText(res AgentsSyncAllResult) string {
	if strings.TrimSpace(res.Output) != "" || strings.TrimSpace(res.Stderr) != "" {
		return "APM install complete"
	}
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
