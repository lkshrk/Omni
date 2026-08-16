package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/executor"
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
	// InstalledPackages counts the config-declared packages handed to APM by the last sync.
	InstalledPackages int
}

func (a *App) APMClient(scope apm.Scope) *apm.Client {
	return apm.New(a.fallbackExecutor(), scope)
}

func (a *App) commandAvailable(name string) bool {
	if checker, ok := a.fallbackExecutor().(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable(name)
	}
	return executor.CommandAvailable(name)
}

// APMAvailable gates legacy migration: config must not move into a manifest no installed tool can act on.
func (a *App) APMAvailable() bool {
	return a.commandAvailable("apm")
}

func errAPMNotInstalled() error {
	return fmt.Errorf("%w: %s", apm.ErrNotInstalled, apm.InstallHint)
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

// AgentsSyncAll installs this host's config-declared packages directly through APM; the Omni config is
// never mutated, so a fleet sharing it via dotfiles keeps its declarations and per-host group scoping.
func (a *App) AgentsSyncAll(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	manifestExists := false
	if _, statErr := os.Stat(manifestPath); statErr == nil {
		manifestExists = true
	} else if !os.IsNotExist(statErr) {
		return AgentsSyncAllResult{}, statErr
	}
	if opts.Frozen {
		if !manifestExists {
			return AgentsSyncAllResult{}, fmt.Errorf("frozen APM install requires %s and its lockfile", manifestPath)
		}
		if opts.Progress != nil {
			opts.Progress("replaying frozen APM manifest…")
		}
		result, err := a.APMClient(apm.Global).Install(ctx, true, opts.DryRun)
		if opts.Output != nil {
			opts.Output(result.Stdout, result.Stderr)
		}
		return AgentsSyncAllResult{Output: result.Stdout, Stderr: result.Stderr}, err
	}

	desired, warnings := a.resolveDesiredAgentPackages(cfg)
	res := AgentsSyncAllResult{Warnings: warnings}
	if len(desired) == 0 && !manifestExists {
		res.Warnings = append(res.Warnings, "no agent packages configured for this host; nothing to install")
		return res, nil
	}
	if !a.APMAvailable() {
		return res, errAPMNotInstalled()
	}
	if opts.Progress != nil {
		if opts.DryRun {
			opts.Progress("previewing APM install…")
		} else {
			opts.Progress("installing agent packages through APM…")
		}
	}
	client := a.APMClient(apm.Global)
	var outputs, errOutputs []string
	appendResult := func(result apm.Result) {
		if result.Stdout != "" {
			outputs = append(outputs, result.Stdout)
		}
		if result.Stderr != "" {
			errOutputs = append(errOutputs, result.Stderr)
		}
		if opts.Output != nil {
			opts.Output(result.Stdout, result.Stderr)
		}
	}
	finish := func(err error) (AgentsSyncAllResult, error) {
		res.Output, res.Stderr = strings.Join(outputs, ""), strings.Join(errOutputs, "")
		if err == nil {
			res.InstalledPackages = len(desired)
		}
		return res, err
	}
	for _, batch := range groupDesiredByTargets(desired) {
		specs := make([]string, 0, len(batch))
		for _, pkg := range batch {
			specs = append(specs, pkg.installSpec())
		}
		result, err := client.InstallPackages(ctx, opts.DryRun, batch[0].Targets, specs...)
		appendResult(result)
		if err != nil {
			return finish(err)
		}
	}
	// A manifest can hold user-added packages beyond the Omni config; replay it so those deploy too.
	if manifestExists && len(desired) == 0 {
		result, err := client.Install(ctx, false, opts.DryRun)
		appendResult(result)
		if err != nil {
			return finish(err)
		}
	}
	return finish(nil)
}

// AgentsUpdateAll refreshes locked APM dependencies; sync owns creating the manifest from config.
func (a *App) AgentsUpdateAll(ctx context.Context, dryRun bool, packages ...string) (apm.Result, error) {
	path, err := a.globalAPMManifestPath()
	if err != nil {
		return apm.Result{}, err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return apm.Result{}, fmt.Errorf("global APM manifest %s not found: run 'omni agents sync' to install this host's configured packages first", path)
		}
		return apm.Result{}, statErr
	}
	return a.APMClient(apm.Global).Update(ctx, dryRun, packages...)
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
