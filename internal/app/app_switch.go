package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Switch ───────────────────────────────────────────────────────────────────

// SwitchResult describes the outcome of a provider switch for a single tool.
type SwitchResult struct {
	Name             string
	FromProvider     string
	ToProvider       string
	Package          string
	UninstallWarning error
}

type providerRepairTarget struct {
	name          string
	configProv    string
	installedWith string
}

// Switch moves a single tool from one provider to another:
//  1. installs via toProvider
//  2. uninstalls from fromProvider (best-effort → warning)
//  3. rewrites the config entry
//  4. updates the DB
func (a *App) Switch(ctx context.Context, name, fromProvider, toProvider string) (*SwitchResult, error) {
	if !a.knownProvider(fromProvider) {
		return nil, fmt.Errorf("unknown provider %q", fromProvider)
	}
	targetProvider, targetInstallWith, opProvider, toProv, err := a.switchTarget(toProvider)
	if err != nil {
		return nil, err
	}

	var (
		result  *SwitchResult
		install config.ToolInstallSpec
		pkg     string
		tgtTool provider.Tool
		ver     string
	)
	err = a.withConfig(func(cfg *config.RootConfig) error {
		spec, ok := cfg.Tools[name]
		if !ok {
			return fmt.Errorf("tool %q with provider %q not found in config", name, fromProvider)
		}
		install = a.resolveInstallSpec(ctx, name, spec)
		if !installSpecMatchesProvider(install, fromProvider) {
			return fmt.Errorf("tool %q with provider %q not found in config", name, fromProvider)
		}
		pkg = install.EffectivePackage(name)

		tgtTool = provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: install.Options}
		if err := installWithProvider(ctx, toProv, tgtTool, targetInstallWith); err != nil {
			return fmt.Errorf("installing via %s: %w", toProvider, err)
		}
		var verifyErr error
		ver, verifyErr = verifyInstalledAfterInstall(ctx, toProv, tgtTool, targetInstallWith, opProvider)
		if verifyErr != nil {
			return verifyErr
		}

		result = &SwitchResult{
			Name:         name,
			FromProvider: fromProvider,
			ToProvider:   toProvider,
			Package:      pkg,
		}

		// Skip uninstall when from == to: installing and immediately uninstalling
		// the same provider would undo the install (e.g. cross-backend migration
		// within the python provider where the registered name stays "python").
		if fromProvider != toProvider {
			if uninstallErr := a.uninstallInstallSpec(ctx, install, name, opProvider, targetInstallWith); uninstallErr != nil {
				result.UninstallWarning = uninstallErr
			}
		}

		spec.Provider = targetProvider
		spec.Package = pkg
		spec.InstallWith = targetInstallWith
		spec.Options = install.Options
		cfg.Tools[name] = spec
		return nil
	})
	if err != nil {
		if result == nil {
			return nil, err
		}
		return result, err
	}

	installedOwner := installedWithForOperation(ctx, toProv, opProvider, targetInstallWith)
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      targetProvider,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return result, fmt.Errorf("upserting switch cache for %s/%s: %w", targetProvider, name, err)
	}
	if install.Provider != targetProvider {
		if err := a.readDB().Delete(ctx, name, install.Provider, pkg); err != nil {
			return result, fmt.Errorf("deleting old switch cache for %s/%s: %w", fromProvider, name, err)
		}
	}
	if fromProvider != install.Provider && fromProvider != targetProvider {
		if err := a.readDB().Delete(ctx, name, fromProvider, pkg); err != nil {
			return result, fmt.Errorf("deleting old switch cache for %s/%s: %w", fromProvider, name, err)
		}
	}

	return result, nil
}

func (a *App) switchTarget(toProvider string) (targetProvider, targetInstallWith, opProvider string, prov provider.Provider, err error) {
	targetProvider, targetInstallWith = a.logicalInstallTarget(toProvider)
	opProvider = targetProvider
	if targetInstallWith != "" {
		if manager, ok := a.managerOption(targetProvider, targetInstallWith); ok {
			var resolved provider.Provider
			resolved, opProvider, err = a.managerOperationProvider(targetProvider, manager)
			if err != nil {
				return "", "", "", nil, err
			}
			return targetProvider, targetInstallWith, opProvider, resolved, nil
		}
		opProvider = toProvider
	}
	prov, ok := a.registry.Get(opProvider)
	if !ok {
		return "", "", "", nil, fmt.Errorf("unknown provider %q", toProvider)
	}
	return targetProvider, targetInstallWith, opProvider, prov, nil
}

// oldEnvCleaner is an optional interface that providers can implement to
// explicitly remove a package from a specific concrete backend's environment.
// This is used during cross-backend migration within the same logical provider
// (e.g. uv→pip3 both map to "python" but live in different envs).
type oldEnvCleaner interface {
	UninstallFrom(ctx context.Context, tool provider.Tool, binary string) error
}

// MigrateInstallation reinstalls a tool via its config-declared provider.
// installedWith is the concrete binary stored in InstalledWith (may be a raw
// binary like "uv" or "pip3" that isn't a registered provider, or it may be a
// fully registered provider name like "brew"). configProv is always a registered
// provider name.
//
// Three cases:
//  1. installedWith is a different registered provider (e.g. brew vs pip):
//     config already has the entry under configProv; install via configProv,
//     uninstall from installedWith, no config rewrite needed.
//  2. installedWith is an unregistered binary (e.g. "uv", "pip3"):
//     from=configProv; Switch runs same-provider install; old env is cleaned up
//     via UninstallFrom when the provider supports it.
//  3. installedWith == configProv: already consistent; Switch is a no-op install.
func (a *App) MigrateInstallation(ctx context.Context, name, installedWith, configProv string) (*SwitchResult, error) {
	_, installedIsRegistered := a.registry.Get(installedWith)

	// Case 1: installedWith is a different registered provider.
	// The config already records configProv as the intended provider.
	// Reinstall via configProv, remove from installedWith — no config rewrite.
	if installedIsRegistered && installedWith != configProv {
		return a.migrateWrongProvider(ctx, name, installedWith, configProv)
	}

	if !installedIsRegistered {
		return a.migrateSameProviderBackend(ctx, name, installedWith, configProv)
	}
	from := installedWith
	result, err := a.Switch(ctx, name, from, configProv)
	if err != nil {
		return nil, err
	}

	// When from == configProv the tool's registered provider didn't change
	// (only the concrete backend did, e.g. uv→pip3 within "python").
	// Switch skipped the uninstall in that case, so we clean up the old env
	// explicitly here.
	if from == configProv && installedWith != "" {
		if prov, ok := a.registry.Get(configProv); ok {
			if cleaner, ok := prov.(oldEnvCleaner); ok {
				tgt := provider.Tool{Name: name, Provider: configProv, Package: result.Package}
				// Best-effort: if the tool was already absent from the old env
				// (e.g. already migrated) the error is silently ignored.
				if cleanErr := cleaner.UninstallFrom(ctx, tgt, installedWith); cleanErr != nil {
					result.UninstallWarning = cleanErr
				}
			}
		}
	}

	return result, nil
}

func (a *App) migrateSameProviderBackend(ctx context.Context, name, installedWith, configProv string) (*SwitchResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	install := a.resolveInstallSpec(ctx, name, spec)
	if !installSpecMatchesProvider(install, configProv) {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	pkg := install.EffectivePackage(name)
	opProvider := a.installOperationProviderName(install)
	toProv, ok := a.registry.Get(opProvider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", opProvider)
	}

	tgtTool := provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: install.Options}
	if err := installWithProvider(ctx, toProv, tgtTool, install.InstallWith); err != nil {
		return nil, fmt.Errorf("installing via %s: %w", opProvider, err)
	}
	installedOwner := installedWithForOperation(ctx, toProv, opProvider, install.InstallWith)
	result := &SwitchResult{
		Name:         name,
		FromProvider: configProv,
		ToProvider:   opProvider,
		Package:      pkg,
	}
	ver, err := verifyInstalledAfterInstall(ctx, toProv, tgtTool, install.InstallWith, opProvider)
	if err != nil {
		return result, err
	}

	if installedWith != "" && installedWith != installedOwner {
		if prov, ok := a.registry.Get(configProv); ok {
			if cleaner, ok := prov.(oldEnvCleaner); ok {
				tgt := provider.Tool{Name: name, Provider: configProv, Package: pkg}
				if cleanErr := cleaner.UninstallFrom(ctx, tgt, installedWith); cleanErr != nil {
					result.UninstallWarning = cleanErr
				}
			}
		}
	}

	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      install.Provider,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return result, fmt.Errorf("upserting migration cache for %s/%s: %w", install.Provider, name, err)
	}
	return result, nil
}

// ReinstallWithDefault reinstalls the tool with its configured provider and
// then refreshes installed state so DB ownership reflects the repaired state.
// configProv is optional and only needed to disambiguate duplicate tool names.
func (a *App) ReinstallWithDefault(ctx context.Context, name, configProv string) (*SwitchResult, error) {
	target, err := a.providerRepairTarget(ctx, name, configProv)
	if err != nil {
		return nil, err
	}
	result, err := a.MigrateInstallation(ctx, target.name, target.installedWith, target.configProv)
	if err != nil {
		return result, err
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		return result, fmt.Errorf("refreshing installed state after reinstall: %w", err)
	}
	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		return result, fmt.Errorf("refreshing descriptions after reinstall: %w", err)
	}
	return result, nil
}

// ReinstallWithDefaultAfterClearingInstallOverride removes the effective
// install_with override, then reinstalls the tool with its ecosystem default.
func (a *App) ReinstallWithDefaultAfterClearingInstallOverride(ctx context.Context, name, configProv string) (*SwitchResult, ClearInstallOverrideResult, error) {
	target, err := a.providerRepairTarget(ctx, name, configProv)
	if err != nil {
		return nil, ClearInstallOverrideResult{}, err
	}
	originalSpec, found, err := a.toolSpecSnapshot(name)
	if err != nil {
		return nil, ClearInstallOverrideResult{}, err
	}
	cleared, err := a.ClearToolInstallOverride(ctx, name, target.configProv)
	if err != nil {
		return nil, ClearInstallOverrideResult{}, err
	}
	result, err := a.MigrateInstallation(ctx, target.name, target.installedWith, target.configProv)
	if err != nil {
		if restoreErr := a.restoreToolSpecSnapshot(name, originalSpec, found); restoreErr != nil {
			return result, cleared, fmt.Errorf("%w (restore provider override failed: %v)", err, restoreErr)
		}
		return result, cleared, err
	}
	if err := a.RefreshInstalled(ctx, nil); err != nil {
		return result, cleared, fmt.Errorf("refreshing installed state after reinstall: %w", err)
	}
	if err := a.RefreshDescriptions(ctx, 0); err != nil {
		return result, cleared, fmt.Errorf("refreshing descriptions after reinstall: %w", err)
	}
	return result, cleared, nil
}

func (a *App) toolSpecSnapshot(name string) (config.ToolSpec, bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return config.ToolSpec{}, false, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	return spec, ok, nil
}

func (a *App) restoreToolSpecSnapshot(name string, spec config.ToolSpec, found bool) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		if found {
			if cfg.Tools == nil {
				cfg.Tools = make(map[string]config.ToolSpec)
			}
			cfg.Tools[name] = spec
		} else if cfg.Tools != nil {
			delete(cfg.Tools, name)
		}
		return nil
	})
}

func (a *App) providerRepairTarget(ctx context.Context, name, configProv string) (providerRepairTarget, error) {
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		return providerRepairTarget{}, fmt.Errorf("listing tools: %w", err)
	}
	var matches []*database.ToolCache
	for _, t := range tools {
		if t.Name != name {
			continue
		}
		if configProv != "" && t.Provider != configProv {
			continue
		}
		matches = append(matches, t)
	}
	if len(matches) == 0 {
		if configProv != "" {
			return providerRepairTarget{}, fmt.Errorf("tool %q with provider %q not found in cache", name, configProv)
		}
		return providerRepairTarget{}, fmt.Errorf("tool %q not found in cache", name)
	}
	if len(matches) > 1 {
		return providerRepairTarget{}, fmt.Errorf("tool %q has multiple cached providers; pass --provider to choose one", name)
	}
	t := matches[0]
	if t.InstalledWith == "" {
		return providerRepairTarget{}, fmt.Errorf("installed provider for %q is unknown; refresh installed state first", name)
	}
	return providerRepairTarget{name: t.Name, configProv: t.Provider, installedWith: t.InstalledWith}, nil
}

// migrateWrongProvider handles the syncWrongProv scenario: the tool is in config
// under configProv but was physically installed via a different registered provider
// (installedWith). We install via configProv, remove from installedWith, and update
// the DB. The config entry is already correct — no rewrite required.
func (a *App) migrateWrongProvider(ctx context.Context, name, installedWith, configProv string) (*SwitchResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	install := a.resolveInstallSpec(ctx, name, spec)
	if !installSpecMatchesProvider(install, configProv) {
		return nil, fmt.Errorf("tool %q with provider %q not found in config", name, configProv)
	}
	pkg := install.EffectivePackage(name)

	opProvider := a.installOperationProviderName(install)
	toProv, ok := a.registry.Get(opProvider)
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", opProvider)
	}

	tgtTool := provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: install.Options}
	if err := installWithProvider(ctx, toProv, tgtTool, install.InstallWith); err != nil {
		return nil, fmt.Errorf("installing via %s: %w", opProvider, err)
	}

	result := &SwitchResult{
		Name:         name,
		FromProvider: installedWith,
		ToProvider:   opProvider,
		Package:      pkg,
	}
	ver, err := verifyInstalledAfterInstall(ctx, toProv, tgtTool, install.InstallWith, opProvider)
	if err != nil {
		return result, err
	}

	// Remove from the old (wrong) provider — best-effort. Skip when clearing an
	// override revealed the same concrete owner as the ecosystem default.
	installedOwner := installedWithForOperation(ctx, toProv, opProvider, install.InstallWith)
	if installedWith == installedOwner {
		result.FromProvider = ""
	} else {
		if fromProv, ok := a.registry.Get(installedWith); ok {
			srcTool := provider.Tool{Name: name, Provider: installedWith, Package: pkg}
			if uninstallErr := fromProv.Uninstall(ctx, srcTool); uninstallErr != nil {
				result.UninstallWarning = uninstallErr
			}
		}
	}

	// Update DB: mark installed under configProv, remove stale installedWith entry.
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      install.Provider,
		Package:       pkg,
		Installed:     true,
		InstalledWith: installedOwner,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return result, fmt.Errorf("upserting migration cache for %s/%s: %w", install.Provider, name, err)
	}
	if installedWith != install.Provider && installedWith != installedOwner {
		if err := a.readDB().Delete(ctx, name, installedWith, pkg); err != nil {
			return result, fmt.Errorf("deleting old migration cache for %s/%s: %w", installedWith, name, err)
		}
	}
	if configProv != install.Provider && configProv != installedWith {
		if err := a.readDB().Delete(ctx, name, configProv, pkg); err != nil {
			return result, fmt.Errorf("deleting old migration cache for %s/%s: %w", configProv, name, err)
		}
	}
	if install.InstallWith != "" && install.InstallWith != install.Provider && install.InstallWith != installedWith {
		if err := a.readDB().Delete(ctx, name, install.InstallWith, pkg); err != nil {
			return result, fmt.Errorf("deleting old migration cache for %s/%s: %w", install.InstallWith, name, err)
		}
	}
	return result, nil
}
