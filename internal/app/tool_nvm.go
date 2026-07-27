package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

type NvmManagedMigrationItem struct {
	Name         string
	FromProvider string
	ToProvider   string
	Removed      bool
}

type NvmManagedMigrationFailure struct {
	Name string
	Err  error
}

type NvmManagedMigrationBatchResult struct {
	Items    []NvmManagedMigrationItem
	Failures []NvmManagedMigrationFailure
}

func (r *NvmManagedMigrationBatchResult) HasFailures() bool {
	return r != nil && len(r.Failures) > 0
}

type NvmManagedMigrationStateResult struct {
	Batch      *NvmManagedMigrationBatchResult
	Tools      []*ToolView
	NvmManaged map[string]bool
}

// ToolResolvesViaNvm — Honours fallback binary overrides.
func ToolResolvesViaNvm(toolName string, spec config.ToolSpec) bool {
	bin := toolBinaryNameForNvm(toolName, spec)
	if bin == "" || !executor.CommandAvailable(bin) {
		return false
	}
	resolved, _ := executor.ResolveCommand(bin)
	return resolved != bin && executor.ResolvedUnderNvm(resolved)
}

func toolBinaryNameForNvm(name string, spec config.ToolSpec) string {
	if spec.Fallback != nil && strings.TrimSpace(spec.Fallback.Binary) != "" {
		return strings.TrimSpace(spec.Fallback.Binary)
	}
	return name
}

// RemoveNvmRuntimeFromConfigWithState — Provider-tool delete guards are intentionally bypassed for this nvm handoff path.
func (a *App) RemoveNvmRuntimeFromConfigWithState(ctx context.Context, name, providerName string) (*ToolGroupMutationState, error) {
	if name != "node" {
		return nil, fmt.Errorf("remove nvm runtime from config: tool %q is not the Node runtime", name)
	}
	return a.toolGroupMutationStateAfter(ctx, func() error {
		return a.removeNvmRuntimeFromConfig(ctx, name, providerName)
	})
}

func (a *App) removeNvmRuntimeFromConfig(ctx context.Context, name, providerName string) error {
	cacheProvider, pkg, err := a.configuredCacheIdentityForTool(ctx, name, providerName)
	if err != nil {
		return err
	}
	if err := a.removeToolFromConfig(name, providerName); err != nil {
		return err
	}
	if err := a.readDB().Delete(ctx, name, cacheProvider, pkg); err != nil {
		return err
	}
	if providerName != "" && providerName != cacheProvider {
		return a.readDB().Delete(ctx, name, providerName, pkg)
	}
	return nil
}

func (a *App) NvmManagedSystemToolNames(ctx context.Context) (map[string]bool, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	resolved, _ := a.currentResolvedTools(ctx, cfg)
	out := make(map[string]bool)
	for _, rt := range resolved {
		t := rt.entry
		if !IsSystemProvider(t.Provider) {
			continue
		}
		spec, ok := cfg.Tools[t.Name]
		if !ok {
			continue
		}
		if ToolResolvesViaNvm(t.Name, spec) {
			out[t.Name] = true
		}
	}
	return out, nil
}

// MigrateNvmManagedTool — The Node runtime is removed from config; other tools switch to the effective node package manager.
func (a *App) MigrateNvmManagedTool(ctx context.Context, name string) (*SwitchResult, error) {
	result, err := a.MigrateNvmManagedToolWithState(ctx, name)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result.Result, nil
	}
	return nil, nil
}

func (a *App) MigrateNvmManagedToolWithState(ctx context.Context, name string) (*ProviderRepairStateResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	spec, ok := cfg.Tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not in config", name)
	}
	fromProvider := spec.DefaultInstallSpec().Provider
	if !IsSystemProvider(fromProvider) {
		return nil, fmt.Errorf("tool %q is not configured for a system provider", name)
	}

	if name == "node" {
		state, err := a.RemoveNvmRuntimeFromConfigWithState(ctx, name, fromProvider)
		if err != nil {
			return nil, err
		}
		return &ProviderRepairStateResult{Tools: state.Tools}, nil
	}
	settings, _ := a.LoadSettings()
	_, nodeManager := a.effectiveManagersFromSettings(settings)
	target := defaultNodeManagerLabel(nodeManager)
	return a.SwitchWithState(ctx, name, fromProvider, target)
}

func (a *App) MigrateAllNvmManagedTools(ctx context.Context) (*NvmManagedMigrationBatchResult, error) {
	names, err := a.NvmManagedSystemToolNames(ctx)
	if err != nil {
		return nil, err
	}
	return a.MigrateNvmManagedTools(ctx, sortedNvmManagedNames(names, nil))
}

func (a *App) MigrateNvmManagedTools(ctx context.Context, names []string) (*NvmManagedMigrationBatchResult, error) {
	if len(names) == 0 {
		return &NvmManagedMigrationBatchResult{}, nil
	}
	managed, err := a.NvmManagedSystemToolNames(ctx)
	if err != nil {
		return nil, err
	}
	result := &NvmManagedMigrationBatchResult{}
	var errs []error
	for _, name := range names {
		if !managed[name] {
			result.Failures = append(result.Failures, NvmManagedMigrationFailure{
				Name: name,
				Err:  fmt.Errorf("tool %q is not nvm-managed", name),
			})
			errs = append(errs, fmt.Errorf("%q: not nvm-managed", name))
			continue
		}
		fromProvider := ""
		if cfg, cfgErr := a.loadConfig(); cfgErr == nil {
			if spec, ok := cfg.Tools[name]; ok {
				fromProvider = spec.DefaultInstallSpec().Provider
			}
		}
		switchResult, migrateErr := a.MigrateNvmManagedTool(ctx, name)
		if migrateErr != nil {
			result.Failures = append(result.Failures, NvmManagedMigrationFailure{Name: name, Err: migrateErr})
			errs = append(errs, fmt.Errorf("%q: %w", name, migrateErr))
			continue
		}
		item := NvmManagedMigrationItem{Name: name, Removed: name == "node", FromProvider: fromProvider}
		if switchResult != nil {
			if switchResult.FromProvider != "" {
				item.FromProvider = switchResult.FromProvider
			}
			item.ToProvider = switchResult.ToProvider
		}
		result.Items = append(result.Items, item)
	}
	return result, errors.Join(errs...)
}

func (a *App) MigrateNvmManagedToolsWithState(ctx context.Context, names []string) (*NvmManagedMigrationStateResult, error) {
	batch, err := a.MigrateNvmManagedTools(ctx, names)
	state, stateErr := a.nvmManagedMigrationState(ctx)
	if stateErr != nil {
		return nil, stateErr
	}
	state.Batch = batch
	return state, err
}

func (a *App) MigrateAllNvmManagedToolsWithState(ctx context.Context) (*NvmManagedMigrationStateResult, error) {
	names, err := a.NvmManagedSystemToolNames(ctx)
	if err != nil {
		return nil, err
	}
	return a.MigrateNvmManagedToolsWithState(ctx, sortedNvmManagedNames(names, nil))
}

func (a *App) nvmManagedMigrationState(ctx context.Context) (*NvmManagedMigrationStateResult, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	ecosystemProviders := a.ResolvedEcosystemProviders(ctx)
	tools, err := a.listToolsFromConfig(ctx, cfg, "", ecosystemProviders, true)
	if err != nil {
		return nil, err
	}
	nvmManaged, err := a.NvmManagedSystemToolNames(ctx)
	if err != nil {
		return nil, err
	}
	return &NvmManagedMigrationStateResult{Tools: toolViewsFromCache(tools), NvmManaged: nvmManaged}, nil
}

func sortedNvmManagedNames(managed map[string]bool, ignored map[string]bool) []string {
	if len(managed) == 0 {
		return nil
	}
	names := make([]string, 0, len(managed))
	for name := range managed {
		if ignored != nil && ignored[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func NvmManagedMigrationSummaryText(result *NvmManagedMigrationBatchResult) string {
	if result == nil {
		return "no nvm-managed tools to migrate"
	}
	if len(result.Items) == 0 && len(result.Failures) == 0 {
		return "no nvm-managed tools to migrate"
	}
	parts := make([]string, 0, 2)
	if len(result.Items) > 0 {
		parts = append(parts, fmt.Sprintf("migrated %d tool(s)", len(result.Items)))
	}
	if len(result.Failures) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", len(result.Failures)))
	}
	return strings.Join(parts, ", ")
}
