package app

import (
	"context"
	"errors"
	"slices"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

// AddAgentPackages declares packages in the fleet config first: APM's own positional install edits the manifest, which the next sync regenerates from config.
func (a *App) AddAgentPackages(ctx context.Context, sources []string) (apm.Result, []string, error) {
	if len(sources) == 0 {
		return apm.Result{}, nil, errors.New("add requires at least one package")
	}
	pkgs := make([]config.SkillPackage, 0, len(sources))
	for _, source := range sources {
		pkg, err := a.parseSkillPackage(source)
		if err != nil {
			return apm.Result{}, nil, err
		}
		if err := refuseCredentialSource(source); err != nil {
			return apm.Result{}, nil, err
		}
		pkgs = append(pkgs, pkg)
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		for _, pkg := range pkgs {
			merged, _ := upsertNormalizedPackages(c.Agents.Packages, pkg)
			c.Agents.Packages = merged
		}
		return nil
	}); err != nil {
		return apm.Result{}, nil, err
	}
	return a.convergeAPMPackageSurface(ctx)
}

// RemoveAgentPackages undeclares and uninstalls together: undeclaring alone leaves the files deployed, uninstalling alone lets the next sync reinstall them.
func (a *App) RemoveAgentPackages(ctx context.Context, sources []string) (apm.Result, error) {
	if len(sources) == 0 {
		return apm.Result{}, errors.New("remove requires at least one package")
	}
	retired := make(map[string]bool, len(sources))
	for _, source := range sources {
		retired[apmPackageIdentityOf(source)] = true
	}
	if err := a.withConfig(func(c *config.RootConfig) error {
		c.Agents.Packages = slices.DeleteFunc(c.Agents.Packages, func(p config.SkillPackage) bool {
			return retired[apmPackageIdentityOf(p.Source)]
		})
		return nil
	}); err != nil {
		return apm.Result{}, err
	}
	if !a.APMAvailable() {
		return apm.Result{}, errAPMNotInstalled()
	}
	// The ownership sidecar deliberately keeps the retired identity: the next sync sees an entry omni owns and no longer declares, and retires whatever APM's uninstall left behind.
	return a.APMClient(apm.Global).Uninstall(ctx, sources...)
}

// convergeAPMPackageSurface regenerates the manifest's package surface from the config an authoring verb just wrote and installs it. The mcp surface is left verbatim because this path reconciles none of it.
func (a *App) convergeAPMPackageSurface(ctx context.Context) (apm.Result, []string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return apm.Result{}, nil, err
	}
	desired, warnings := a.resolveDesiredAgentPackages(cfg)
	manifestPath, err := a.globalAPMManifestPath()
	if err != nil {
		return apm.Result{}, warnings, err
	}
	preState, err := a.snapshotAPMState(a.apmSelectableTargets(cfg))
	if err != nil {
		return apm.Result{}, warnings, err
	}
	converged, err := a.convergeAPMManifest(cfg, manifestPath, apmConvergePlan{
		Packages:     desiredPackageEntries(desired),
		SyncPackages: true,
	})
	if err != nil {
		return apm.Result{}, warnings, err
	}
	targets := unionAPMTargets(desired)
	if converged.PackageCount == 0 || len(targets) == 0 {
		return apm.Result{}, append(warnings, "no APM target for this host's declared packages; the manifest was updated without installing"), nil
	}
	if !a.APMAvailable() {
		return apm.Result{}, warnings, errAPMNotInstalled()
	}
	result, reverted, err := a.installAPMSurface(ctx, a.APMClient(apm.Global), apm.SurfacePackages, targets, &preState, apm.InstallOptions{})
	warnings = append(warnings, reverted...)
	if err != nil {
		return result, warnings, err
	}
	return result, warnings, advanceAPMApplied(manifestPath, apm.SurfacePackages, converged.PendingOwned.Packages)
}
