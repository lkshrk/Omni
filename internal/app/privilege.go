package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

func (a *App) ToolPrivilegePlan(ctx context.Context, t *database.ToolCache, action provider.PrivilegeAction) (provider.PrivilegePlan, error) {
	if t == nil {
		return provider.PrivilegePlan{}, nil
	}
	cached := provider.PrivilegePlan{}
	if t.Privilege != "" {
		cached = provider.PrivilegePlan{
			Requirement: provider.PrivilegeRequirement(t.Privilege),
			Reason:      t.PrivilegeReason.String,
		}
	}
	providerName := t.Provider
	manager := ""
	if (action == provider.PrivilegeActionUninstall || action == provider.PrivilegeActionUpgrade) && t.InstalledWith != "" {
		providerName = t.InstalledWith
	} else if t.InstalledWith != "" && t.InstalledWith != t.Provider {
		manager = t.InstalledWith
	}
	if cached.RequiresPrivilege() && providerName != "brew" {
		return cached, nil
	}
	prov, opProvider, _, ok := a.lifecycleProvider(providerName, manager)
	if !ok {
		return cached, nil
	}
	tool := provider.Tool{Name: t.Name, Provider: opProvider, Package: t.Package}
	if tool.Package == "" {
		tool.Package = t.Name
	}
	planner, ok := prov.(provider.PrivilegePlanner)
	if !ok {
		return cached, nil
	}
	plan, err := planner.PrivilegePlan(ctx, action, tool)
	if err != nil {
		if cached.RequiresPrivilege() {
			return cached, nil
		}
		return provider.PrivilegePlan{}, err
	}
	if plan.RequiresPrivilege() {
		return plan, nil
	}
	return cached, nil
}

func (a *App) recordPrivilegeError(ctx context.Context, name, prov, pkg string, err error) {
	plan, ok := provider.ClassifyPrivilegeError(err)
	if !ok || !plan.RequiresPrivilege() {
		return
	}
	if pkg == "" {
		pkg = name
	}
	db := a.readDB()
	if db == nil {
		return // DB not initialised; skip best-effort privilege recording
	}
	if err := db.MarkPrivilegeRequired(context.WithoutCancel(ctx), name, prov, pkg, string(plan.Requirement), plan.Reason); err != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: record privilege for %s: %v\n", name, err)
	}
}

func (a *App) MarkToolPrivilegeRequired(ctx context.Context, name, prov, pkg, requirement, reason string) error {
	if pkg == "" {
		pkg = name
	}
	return a.readDB().MarkPrivilegeRequired(ctx, name, prov, pkg, requirement, reason)
}

// CompleteExternalToolAction reconciles app state after a lifecycle action was
// run outside the normal provider executor, for example by a TUI-owned terminal
// session that can answer sudo prompts.
func (a *App) CompleteExternalToolAction(ctx context.Context, action provider.PrivilegeAction, name, providerName, pkg, installedWith string) error {
	if pkg == "" {
		pkg = name
	}
	switch action {
	case provider.PrivilegeActionInstall, provider.PrivilegeActionUpgrade:
		return a.completeExternalInstalledToolAction(ctx, action, name, providerName, pkg, installedWith)
	case provider.PrivilegeActionUninstall:
		if err := a.rejectProviderToolDelete(name); err != nil {
			return err
		}
		if err := a.removeToolFromConfig(name, providerName); err != nil {
			return err
		}
		if err := a.readDB().Delete(ctx, name, providerName, pkg); err != nil {
			return fmt.Errorf("delete cache after external uninstall for %s/%s: %w", providerName, name, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported external tool action %q", action)
	}
}

func (a *App) completeExternalInstalledToolAction(ctx context.Context, action provider.PrivilegeAction, name, providerName, pkg, installedWith string) error {
	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	tool := provider.Tool{Name: name, Provider: opProvider, Package: pkg}
	installed, ver, err := isInstalledTool(ctx, prov, tool, manager)
	if err != nil {
		return fmt.Errorf("verify %s after external %s: %w", name, externalToolActionVerb(action), err)
	}
	if !installed {
		return fmt.Errorf("verify %s after external %s: not installed", name, externalToolActionVerb(action))
	}
	cacheInstalledWith := installedWithForOperation(ctx, prov, opProvider, installedWith)
	if cacheInstalledWith == "" {
		cacheInstalledWith = installedWithForLifecycle(opProvider, manager)
	}
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		Installed:     true,
		InstalledWith: cacheInstalledWith,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return fmt.Errorf("update cache after external %s for %s/%s: %w", externalToolActionVerb(action), providerName, name, err)
	}
	if action == provider.PrivilegeActionUpgrade {
		if err := a.readDB().UpdateOutdated(ctx, name, providerName, pkg, false, ""); err != nil {
			return fmt.Errorf("clear outdated after external upgrade for %s/%s: %w", providerName, name, err)
		}
	}
	return nil
}

func externalToolActionVerb(action provider.PrivilegeAction) string {
	switch action {
	case provider.PrivilegeActionInstall:
		return "install"
	case provider.PrivilegeActionUninstall:
		return "uninstall"
	case provider.PrivilegeActionUpgrade:
		return "upgrade"
	default:
		return string(action)
	}
}

func privilegeReason(plan provider.PrivilegePlan) string {
	if plan.Reason != "" {
		return plan.Reason
	}
	return "package manager needs privileged access"
}
