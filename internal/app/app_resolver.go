package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ToolKey is the canonical identity for a resolved physical tool.
type ToolKey struct {
	Name     string
	Provider string
	Package  string
}

func NewToolKey(name, providerName, packageName string) ToolKey {
	if packageName == "" {
		packageName = name
	}
	return ToolKey{Name: name, Provider: providerName, Package: packageName}
}

func (k ToolKey) String() string {
	return k.Name + "\x00" + k.Provider + "\x00" + k.Package
}

func ToolKeyFromEntry(t config.ToolEntry) ToolKey {
	return NewToolKey(t.Name, t.Provider, t.EffectivePackage())
}

type resolvedTool struct {
	entry       config.ToolEntry
	memberships []string
	taps        []string
}

func (a *App) resolveTools(ctx context.Context, cfg *config.RootConfig, groups []*config.GroupConfig) ([]resolvedTool, []string) {
	if cfg == nil {
		return nil, nil
	}
	ignored := ignoredToolSet(cfg)
	memberships := make(map[string][]string)
	order := make([]string, 0)
	seen := make(map[string]struct{})
	for _, g := range groups {
		if g == nil {
			continue
		}
		groupName := g.BaseName()
		for _, membership := range g.Tools {
			if membership.Name == "" {
				continue
			}
			if _, ok := seen[membership.Name]; !ok {
				seen[membership.Name] = struct{}{}
				order = append(order, membership.Name)
			}
			memberships[membership.Name] = append(memberships[membership.Name], groupName)
		}
	}

	var warnings []string
	resolved := make([]resolvedTool, 0, len(order))
	for _, name := range order {
		spec, ok := cfg.Tools[name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("tool %q is referenced by a group but has no logical spec", name))
			continue
		}
		install := a.resolveInstallSpec(ctx, name, spec)
		entry := spec.ToToolEntry(name, install)
		if toolNameIgnored(ignored, name) || entry.Ignore {
			continue
		}
		resolved = append(resolved, resolvedTool{
			entry:       entry,
			memberships: memberships[name],
			taps:        append([]string(nil), spec.Taps...),
		})
	}
	return resolved, warnings
}

func (a *App) resolvedToolEntries(ctx context.Context, cfg *config.RootConfig, groups []*config.GroupConfig) ([]config.ToolEntry, []string) {
	resolved, warnings := a.resolveTools(ctx, cfg, groups)
	tools := make([]config.ToolEntry, 0, len(resolved))
	for _, t := range resolved {
		tools = append(tools, t.entry)
	}
	return tools, warnings
}

func (a *App) resolveInstallSpec(ctx context.Context, logicalName string, spec config.ToolSpec) config.ToolInstallSpec {
	hostname := currentHostname()
	if install, ok := spec.Hosts[hostname]; ok {
		return install
	}
	if short := shortHostname(hostname); short != hostname {
		if install, ok := spec.Hosts[short]; ok {
			return install
		}
	}

	defaultSpec := spec.DefaultInstallSpec()
	candidates := append([]config.ToolInstallSpec{defaultSpec}, spec.Variants...)
	for _, candidate := range candidates {
		if a.providerUsable(ctx, candidate.Provider) {
			return candidate
		}
	}
	return defaultSpec
}

func (a *App) providerUsable(ctx context.Context, providerName string) bool {
	prov, ok := a.registry.Get(providerName)
	if !ok {
		return false
	}
	available, err := prov.Available(ctx)
	return err == nil && available
}

func resolvedToolKey(t config.ToolEntry) string {
	return ToolKeyFromEntry(t).String()
}

func collectResolvedTaps(resolved []resolvedTool) []string {
	seen := make(map[string]struct{})
	var taps []string
	for _, tool := range resolved {
		for _, tap := range tool.taps {
			if _, ok := seen[tap]; ok {
				continue
			}
			seen[tap] = struct{}{}
			taps = append(taps, tap)
		}
	}
	return taps
}

func resolvedToolByName(tools []config.ToolEntry, name, providerName string) (config.ToolEntry, bool) {
	for _, t := range tools {
		if t.Name == name && t.Provider == providerName {
			return t, true
		}
	}
	return config.ToolEntry{}, false
}

func (a *App) operationProviderName(t config.ToolEntry) string {
	if t.InstallWith != "" {
		if _, ok := a.registry.Get(t.InstallWith); ok {
			return t.InstallWith
		}
	}
	return t.Provider
}

func (a *App) operationTool(t config.ToolEntry, opProvider string) provider.Tool {
	return provider.Tool{
		Name:     t.Name,
		Provider: opProvider,
		Package:  t.EffectivePackage(),
		Options:  t.Options,
	}
}

func (a *App) isInstalledWithEntry(ctx context.Context, prov provider.Provider, opProvider string, t config.ToolEntry) (bool, string, error) {
	tool := a.operationTool(t, opProvider)
	if t.InstallWith != "" && opProvider == t.Provider {
		if checker, ok := prov.(provider.ManagerInstalledChecker); ok {
			return checker.IsInstalledWithManager(ctx, tool, t.InstallWith)
		}
	}
	return prov.IsInstalled(ctx, tool)
}

func (a *App) logicalInstallTarget(providerName string) (configProvider, installWith string) {
	if a.knownEcosystemProvider(providerName) {
		return providerName, ""
	}
	if ecosystem, ok := a.providerEcosystem(providerName); ok && ecosystem != providerName {
		return ecosystem, providerName
	}
	return providerName, ""
}

func configInstallWithForConcreteProvider(configProvider, concreteProvider string, resolvedEcosystems map[string]string) string {
	if configProvider == "" || concreteProvider == "" || configProvider == concreteProvider {
		return ""
	}
	if resolvedEcosystems[configProvider] == concreteProvider {
		return ""
	}
	return concreteProvider
}

func installSpecMatchesProvider(install config.ToolInstallSpec, providerName string) bool {
	if providerName == "" {
		return true
	}
	return install.Provider == providerName || install.InstallWith == providerName
}

func (a *App) installSpecMatchesProvider(ctx context.Context, install config.ToolInstallSpec, providerName string) bool {
	if installSpecMatchesProvider(install, providerName) {
		return true
	}
	if prov, ok := a.registry.Get(install.Provider); ok {
		if cr, ok := prov.(provider.ConcreteResolver); ok {
			if concrete, err := cr.ResolvedName(ctx); err == nil && concrete == providerName {
				return true
			}
		}
	}
	return false
}

func installWithProvider(ctx context.Context, prov provider.Provider, tool provider.Tool, manager string) error {
	if manager != "" && manager != tool.Provider {
		if installer, ok := prov.(provider.ManagerInstaller); ok {
			return installer.InstallWithManager(ctx, tool, manager)
		}
	}
	return prov.Install(ctx, tool)
}

func installedWithProvider(ctx context.Context, prov provider.Provider, tool provider.Tool, manager string) (bool, string, error) {
	if manager != "" && manager != tool.Provider {
		if checker, ok := prov.(provider.ManagerInstalledChecker); ok {
			return checker.IsInstalledWithManager(ctx, tool, manager)
		}
	}
	return prov.IsInstalled(ctx, tool)
}

func uninstallWithProvider(ctx context.Context, prov provider.Provider, tool provider.Tool, manager string) error {
	if manager != "" && manager != tool.Provider {
		if uninstaller, ok := prov.(provider.ManagerUninstaller); ok {
			return uninstaller.UninstallFrom(ctx, tool, manager)
		}
	}
	return prov.Uninstall(ctx, tool)
}
