package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/executor"
)

const (
	AgentsFeatureSkills = "skills"
	AgentsFeatureMcp    = "mcp"
)

type AgentsSyncAllOptions struct {
	DryRun   bool
	Frozen   bool
	Progress func(string)
	Output   func(stdout, stderr string)
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
	Output string
	Stderr string
	Mcp    RestoreMcpResult
	// Plan describes what the sync would change when a dry run could not hand the generated manifest to APM.
	Plan     []string
	Warnings []string
	Drift    []string
	Errors   []AgentsFeatureError
	// InstalledPackages counts the declared packages the last sync actually handed to APM, and stays zero when the install never ran.
	InstalledPackages int
}

// AgentsSyncAllFailure — without it a partial failure exits 0 and scripted callers chain onto work that did not happen.
func AgentsSyncAllFailure(res AgentsSyncAllResult) error {
	if len(res.Errors) == 0 {
		return nil
	}
	return fmt.Errorf("%d agent operation(s) failed", len(res.Errors))
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

// AgentsSyncAll reconciles each manifest surface with one scoped install; the Omni config is never mutated, so a fleet sharing it via dotfiles keeps its per-host scoping.
func (a *App) AgentsSyncAll(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	existing, err := os.ReadFile(manifestPath)
	if err != nil && !os.IsNotExist(err) {
		return AgentsSyncAllResult{}, err
	}
	manifestExists := err == nil
	if opts.Frozen && !manifestExists {
		return AgentsSyncAllResult{}, fmt.Errorf("frozen APM install requires %s and its lockfile", manifestPath)
	}

	desired, warnings := a.resolveDesiredAgentPackages(cfg)
	res := AgentsSyncAllResult{Warnings: warnings}

	mcpPlan := a.resolveAPMMcpPlan(cfg)
	res.addWarnings(AgentsFeatureMcp, mcpPlan.Warnings)

	// Nothing declared and no manifest to prune from: converging would create ~/.apm as this sync's only effect, on hosts that never asked for it.
	owned := readAPMOwnedIdentities(manifestPath)
	if !manifestExists && len(desired) == 0 && len(mcpPlan.Servers) == 0 && owned.empty() {
		res.Warnings = append(res.Warnings, "no agent packages configured for this host; nothing to install")
		a.syncNativeMcp(ctx, &res, mcpPlan, opts.DryRun)
		return res, nil
	}
	// Checked before anything is written: a manifest and ledger APM cannot read are state this host never asked for.
	if !a.APMAvailable() {
		return res, errAPMNotInstalled()
	}

	// Captured before convergence: a ledger taken after it would restore the pruned manifest a failed batch must undo.
	var preState apmStateSnapshot
	if !opts.DryRun {
		if preState, err = a.snapshotAPMState(a.apmSelectableTargets(cfg)); err != nil {
			return res, err
		}
	}

	// A frozen replay installs what the manifest and lockfile already agree on; targets are still resolved, because an unscoped install is never safe.
	var surfaces apmManagedSurfaces
	var pending apmSurfaceIdentities
	if opts.Frozen {
		if surfaces, err = managedAPMSurfaces(cfg, manifestPath, existing); err != nil {
			return res, err
		}
	} else {
		converged, convergeErr := a.convergeAPMManifest(cfg, manifestPath, apmConvergePlan{
			Packages:     desiredPackageEntries(desired),
			Mcp:          apmMcpDependencies(mcpPlan.Servers),
			SyncPackages: true,
			SyncMcp:      !mcpPlan.Skip,
			DryRun:       opts.DryRun,
		})
		if convergeErr != nil {
			return res, convergeErr
		}
		surfaces = apmManagedSurfaces{
			declaredPackages: converged.PackageCount,
			packages:         converged.ManagedPackages, priorPackages: converged.PriorManagedPackages,
			mcp: converged.ManagedMcp, priorMcp: converged.PriorManagedMcp,
		}
		pending = converged.PendingOwned
		// APM reads the manifest from disk, so previewing an install it has not been given would report on the stale file.
		if opts.DryRun && !apmManifestEquivalent(converged.Existing, converged.Content) {
			return a.previewAPMPlan(ctx, res, manifestPath, converged.Existing, converged.Content, desired, mcpPlan)
		}
	}

	// Targets come from what this host declares, never from the manifest: installing a preserved foreign entry unscoped would let APM pick the targets.
	packageTargets := unionAPMTargets(desired)
	// Only omni's own entries justify an install: a manifest holding nothing but foreign ones has nothing for this sync to deploy or retire.
	installPackages := surfaces.packages > 0 || surfaces.priorPackages > 0
	if installPackages && surfaces.packages == 0 && len(packageTargets) == 0 {
		// A pure prune has no declared package left to scope from, and nothing to deploy either — only the retired files, which sit under whatever this host selects.
		packageTargets = a.apmSelectableTargets(cfg)
	}
	if installPackages && len(packageTargets) == 0 {
		res.addWarnings(AgentsFeatureSkills, []string{"no APM target for this host's declared packages; skipping the package install"})
		installPackages = false
	}
	installMcp := !mcpPlan.Skip && (surfaces.mcp > 0 || surfaces.priorMcp > 0)
	if installMcp && len(mcpPlan.Targets) == 0 {
		res.addWarnings(AgentsFeatureMcp, []string{"no MCP-capable APM target on this host; skipping the MCP install"})
		installMcp = false
	}
	if !installPackages && !installMcp && !mcpPlan.NativeWork {
		if len(desired) == 0 {
			res.Warnings = append(res.Warnings, "no agent packages configured for this host; nothing to install")
		}
		return res, nil
	}
	if opts.Progress != nil {
		switch {
		case opts.Frozen:
			opts.Progress("replaying frozen APM manifest…")
		case opts.DryRun:
			opts.Progress("previewing APM install…")
		default:
			opts.Progress("installing agent packages through APM…")
		}
	}

	client := a.APMClient(apm.Global)
	installOpts := apm.InstallOptions{Frozen: opts.Frozen, DryRun: opts.DryRun}
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
		return res, err
	}

	// Each surface's reversal must undo its own work only.
	refreshState := func() error {
		if opts.DryRun {
			return nil
		}
		refreshed, err := a.snapshotAPMState(a.apmSelectableTargets(cfg))
		if err != nil {
			return err
		}
		preState = refreshed
		return nil
	}

	if installPackages {
		result, reverted, err := a.installAPMSurface(ctx, client, apm.SurfacePackages, packageTargets, &preState, installOpts)
		appendResult(result)
		res.addWarnings(AgentsFeatureSkills, reverted)
		if err != nil {
			// The MCP surface holds its own manifest entries and reverts its own batch; one bad package must not cost this host every server too.
			res.addError(AgentsFeatureSkills, err.Error())
		} else {
			// A frozen replay installs what the manifest holds, which is not what this host's config declares.
			res.InstalledPackages = len(desired)
			if opts.Frozen {
				res.InstalledPackages = surfaces.declaredPackages
			} else if err := advanceAPMApplied(manifestPath, apm.SurfacePackages, pending.Packages); err != nil {
				return finish(err)
			}
		}
		// Refreshed after the reversal too, so the MCP surface reverts to the state the failed batch left behind.
		if err := refreshState(); err != nil {
			return finish(err)
		}
	}
	if installMcp {
		if err := a.installAPMMcpSurface(ctx, &res, client, mcpPlan, &preState, installOpts, manifestPath, pending.Mcp, appendResult); err != nil {
			return finish(err)
		}
	}
	a.syncNativeMcp(ctx, &res, mcpPlan, opts.DryRun)
	return finish(nil)
}

// installAPMMcpSurface deploys the MCP surface with the servers' variables scrubbed, then rewrites any value
// APM resolved anyway. Its returned error is a failure of the sync itself; a failed install is recorded
// against the surface, so the agents on the native path still get theirs.
func (a *App) installAPMMcpSurface(
	ctx context.Context,
	res *AgentsSyncAllResult,
	client *apm.Client,
	plan apmMcpPlan,
	pre *apmStateSnapshot,
	opts apm.InstallOptions,
	manifestPath string,
	pendingMcp []string,
	appendResult func(apm.Result),
) error {
	adopted, err := apmMcpLockHasState(manifestPath)
	if err != nil {
		return err
	}
	if !adopted {
		actions, err := a.normalizeMcpForAPM(ctx, plan, opts.DryRun)
		res.addWarnings(AgentsFeatureMcp, actions)
		if err != nil {
			res.addError(AgentsFeatureMcp, err.Error())
			return nil
		}
	}
	opts.ScrubEnv = plan.EnvRefs
	result, reverted, err := a.installAPMSurface(ctx, client, apm.SurfaceMcp, plan.Targets, pre, opts)
	appendResult(result)
	res.addWarnings(AgentsFeatureMcp, reverted)
	if err != nil {
		res.addError(AgentsFeatureMcp, err.Error())
		return nil
	}
	if opts.DryRun {
		return nil
	}
	if !opts.Frozen {
		if err := advanceAPMApplied(manifestPath, apm.SurfaceMcp, pendingMcp); err != nil {
			return err
		}
	}
	restored, err := a.restoreMcpPlaceholders(plan)
	res.addWarnings(AgentsFeatureMcp, restored)
	return err
}

func (a *App) syncNativeMcp(ctx context.Context, res *AgentsSyncAllResult, plan apmMcpPlan, dryRun bool) {
	if !plan.NativeWork {
		return
	}
	restored, err := a.RestoreMcpServers(ctx, RestoreMcpOptions{DryRun: dryRun, OnlyAgents: plan.NativeIDs})
	res.AddMcp(restored, err)
}

// previewAPMPlan reports from the generated manifest; invoking APM would read the file still on disk and report success for work this sync has not done.
func (a *App) previewAPMPlan(
	ctx context.Context,
	res AgentsSyncAllResult,
	manifestPath string,
	existing, generated []byte,
	desired []desiredAgentPackage,
	mcpPlan apmMcpPlan,
) (AgentsSyncAllResult, error) {
	delta, err := apmManifestDiff(existing, generated)
	if err != nil {
		return res, err
	}
	res.Plan = delta.lines()
	if targets := unionAPMTargets(desired); len(targets) > 0 {
		res.Plan = append(res.Plan, "packages: targets "+strings.Join(targets, ","))
	}
	if !mcpPlan.Skip && len(mcpPlan.Targets) > 0 {
		res.Plan = append(res.Plan, "mcp: targets "+strings.Join(mcpPlan.Targets, ","))
		adopted, err := apmMcpLockHasState(manifestPath)
		if err != nil {
			return res, err
		}
		if !adopted {
			actions, err := a.normalizeMcpForAPM(ctx, mcpPlan, true)
			res.addWarnings(AgentsFeatureMcp, actions)
			if err != nil {
				return res, err
			}
		}
	}
	res.Warnings = append(res.Warnings,
		"dry run: APM was not consulted because the manifest on disk does not match this host's config")
	return res, nil
}

// AgentsUpdateAll refreshes the manifest's dependencies one surface at a time, scoped exactly as sync scopes
// its installs; sync owns creating the manifest from config. `apm update` is never invoked: it has no --only,
// so a single run deploys the MCP surface into every target the package surface selected.
func (a *App) AgentsUpdateAll(ctx context.Context, dryRun bool) (apm.Result, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return apm.Result{}, nil, err
	}
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return apm.Result{}, nil, err
	}
	if _, statErr := os.Stat(manifestPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return apm.Result{}, nil, fmt.Errorf("global APM manifest %s not found: run 'omni agents sync' to install this host's configured packages first", manifestPath)
		}
		return apm.Result{}, nil, statErr
	}
	if !a.APMAvailable() {
		return apm.Result{}, nil, errAPMNotInstalled()
	}

	desired, warnings := a.resolveDesiredAgentPackages(cfg)
	mcpPlan := a.resolveAPMMcpPlan(cfg)
	warnings = append(warnings, mcpPlan.Warnings...)
	// A manifest can declare packages this host's config no longer names, and their files still sit under whatever it selects.
	packageTargets := unionAPMTargets(desired)
	if len(packageTargets) == 0 {
		packageTargets = a.apmSelectableTargets(cfg)
	}

	client := a.APMClient(apm.Global)
	var result apm.Result
	var errs []error
	run := func(surface string, targets []string, opts apm.InstallOptions) bool {
		if len(targets) == 0 {
			warnings = append(warnings, fmt.Sprintf("no APM target on this host for the %s surface; skipping its update", surface))
			return false
		}
		preState, err := a.snapshotAPMState(a.apmSelectableTargets(cfg))
		if err != nil {
			errs = append(errs, err)
			return false
		}
		opts.DryRun, opts.Update = dryRun, true
		surfaceResult, reverted, err := a.installAPMSurface(ctx, client, surface, targets, &preState, opts)
		result.Stdout += surfaceResult.Stdout
		result.Stderr += surfaceResult.Stderr
		warnings = append(warnings, reverted...)
		if err != nil {
			errs = append(errs, err)
			return false
		}
		return true
	}

	run(apm.SurfacePackages, packageTargets, apm.InstallOptions{})
	if !mcpPlan.Skip && run(apm.SurfaceMcp, mcpPlan.Targets, apm.InstallOptions{ScrubEnv: mcpPlan.EnvRefs}) && !dryRun {
		restored, err := a.restoreMcpPlaceholders(mcpPlan)
		warnings = append(warnings, restored...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return result, warnings, errors.Join(errs...)
}

// AgentsSyncAllSummaryText — APM's own output replaces this whenever the install produced any, so the counts only describe a run APM stayed out of.
func AgentsSyncAllSummaryText(res AgentsSyncAllResult) string {
	if strings.TrimSpace(res.Output) != "" || strings.TrimSpace(res.Stderr) != "" {
		return "APM install complete"
	}
	summary := fmt.Sprintf("%d packages installed, %d mcp servers installed, %d failed",
		res.InstalledPackages, len(res.Mcp.Installed), len(res.Errors))
	for _, extra := range []struct {
		n     int
		label string
	}{
		{len(res.Plan), "planned changes"},
		{len(res.Drift), "drifted"},
		{len(res.Warnings), "warnings"},
	} {
		if extra.n > 0 {
			summary += fmt.Sprintf(", %d %s", extra.n, extra.label)
		}
	}
	return summary
}
