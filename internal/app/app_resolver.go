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
	route       installRoute
}

type installRouteKind string

const (
	installRouteNative           installRouteKind = "native"
	installRouteFallbackEligible installRouteKind = "fallback"
	installRouteUnavailable      installRouteKind = "unavailable"
)

type installRouteSkipReason string

const (
	installRouteSkipProviderUnavailable installRouteSkipReason = "provider-unavailable"
	installRouteSkipPackageUnavailable  installRouteSkipReason = "package-unavailable"
)

type installRouteSkip struct {
	Install config.ToolInstallSpec
	Reason  installRouteSkipReason
	Detail  string
}

type installRoute struct {
	Kind               installRouteKind
	Install            config.ToolInstallSpec
	Skipped            []installRouteSkip
	FallbackConfigured bool
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
	availability := make(map[string]bool)
	for _, name := range order {
		spec, ok := cfg.Tools[name]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("tool %q is referenced by a group but has no logical spec", name))
			continue
		}
		route := a.planInstallRoute(ctx, name, spec, availability)
		install := route.Install
		entry := spec.ToToolEntry(name, install)
		if toolNameIgnored(ignored, name) || entry.Ignore {
			continue
		}
		resolved = append(resolved, resolvedTool{
			entry:       entry,
			memberships: memberships[name],
			taps:        append([]string(nil), spec.Taps...),
			route:       route,
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

func (a *App) currentResolvedTools(ctx context.Context, cfg *config.RootConfig) ([]resolvedTool, []string) {
	return a.resolveTools(ctx, cfg, a.currentToolGroups(cfg))
}

func (a *App) currentResolvedToolEntries(ctx context.Context, cfg *config.RootConfig) ([]config.ToolEntry, []string) {
	resolved, warnings := a.currentResolvedTools(ctx, cfg)
	tools := make([]config.ToolEntry, 0, len(resolved))
	for _, t := range resolved {
		tools = append(tools, t.entry)
	}
	return tools, warnings
}

func (a *App) currentToolGroups(cfg *config.RootConfig) []*config.GroupConfig {
	groups, _ := a.currentToolGroupsWithAuthority(cfg)
	return groups
}

func (a *App) currentToolGroupsWithAuthority(cfg *config.RootConfig) ([]*config.GroupConfig, bool) {
	if cfg == nil {
		return nil, false
	}
	if effective, _, ok := effectiveHostGroups(cfg, cfg.Groups, currentMachineGroupName()); ok {
		return effective, true
	}
	return cfg.Groups, false
}

func (a *App) resolveInstallSpec(ctx context.Context, logicalName string, spec config.ToolSpec) config.ToolInstallSpec {
	return a.resolveInstallSpecWithAvailability(ctx, logicalName, spec, nil)
}

func (a *App) resolveInstallSpecWithAvailability(ctx context.Context, logicalName string, spec config.ToolSpec, availability map[string]bool) config.ToolInstallSpec {
	return a.planInstallRoute(ctx, logicalName, spec, availability).Install
}

func (a *App) planInstallRoute(ctx context.Context, logicalName string, spec config.ToolSpec, availability map[string]bool) installRoute {
	hostname := currentHostname()
	if install, ok := spec.Hosts[hostname]; ok {
		return installRoute{Kind: installRouteNative, Install: normalizeInstallRouteCandidate(logicalName, spec, install), FallbackConfigured: spec.Fallback != nil}
	}
	if short := shortHostname(hostname); short != hostname {
		if install, ok := spec.Hosts[short]; ok {
			return installRoute{Kind: installRouteNative, Install: normalizeInstallRouteCandidate(logicalName, spec, install), FallbackConfigured: spec.Fallback != nil}
		}
	}

	defaultSpec := spec.DefaultInstallSpec()
	candidates := append([]config.ToolInstallSpec(nil), spec.Providers...)
	if len(candidates) == 0 {
		candidates = append([]config.ToolInstallSpec{defaultSpec}, spec.Variants...)
	}
	skipped := make([]installRouteSkip, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = normalizeInstallRouteCandidate(logicalName, spec, candidate)
		if usable, skip := a.installCandidateUsableCached(ctx, logicalName, candidate, availability); usable {
			return installRoute{Kind: installRouteNative, Install: candidate, Skipped: skipped, FallbackConfigured: spec.Fallback != nil}
		} else {
			skipped = append(skipped, skip)
		}
	}
	if len(candidates) > 0 {
		kind := installRouteUnavailable
		if spec.Fallback != nil && allInstallRouteSkipsArePackageUnavailable(skipped, len(candidates)) {
			kind = installRouteFallbackEligible
		}
		return installRoute{Kind: kind, Install: normalizeInstallRouteCandidate(logicalName, spec, candidates[0]), Skipped: skipped, FallbackConfigured: spec.Fallback != nil}
	}
	return installRoute{Kind: installRouteUnavailable, Install: normalizeInstallRouteCandidate(logicalName, spec, defaultSpec), FallbackConfigured: spec.Fallback != nil}
}

func normalizeInstallRouteCandidate(logicalName string, spec config.ToolSpec, install config.ToolInstallSpec) config.ToolInstallSpec {
	if install.Package == "" && spec.Package != "" {
		install.Package = spec.Package
	}
	return install
}

func allInstallRouteSkipsArePackageUnavailable(skipped []installRouteSkip, candidates int) bool {
	if len(skipped) != candidates || candidates == 0 {
		return false
	}
	for _, skip := range skipped {
		if skip.Reason != installRouteSkipPackageUnavailable {
			return false
		}
	}
	return true
}

// allInstallRouteSkipsAreProviderUnavailable reports whether every skip in the
// list is due to the provider itself being absent on the current system. A
// non-empty list is required (zero skips are not "all provider-unavailable").
func allInstallRouteSkipsAreProviderUnavailable(skipped []installRouteSkip, candidates int) bool {
	if len(skipped) != candidates || candidates == 0 {
		return false
	}
	for _, skip := range skipped {
		if skip.Reason != installRouteSkipProviderUnavailable {
			return false
		}
	}
	return true
}

func (a *App) installCandidateUsableCached(ctx context.Context, logicalName string, candidate config.ToolInstallSpec, availability map[string]bool) (bool, installRouteSkip) {
	if !a.providerUsableCached(ctx, candidate.Provider, availability) {
		return false, installRouteSkip{Install: candidate, Reason: installRouteSkipProviderUnavailable}
	}
	pkg := candidate.EffectivePackage(logicalName)
	cached, err := a.readDB().GetPackageAvailability(ctx, logicalName, candidate.Provider, pkg)
	if err == nil {
		if cached.Available {
			return true, installRouteSkip{}
		}
		return false, installRouteSkip{Install: candidate, Reason: installRouteSkipPackageUnavailable, Detail: cached.Reason}
	}
	// Only an explicit unavailable package probe blocks a candidate. Missing or
	// unreadable availability should still try native before considering fallback.
	return true, installRouteSkip{}
}

func (a *App) providerUsableCached(ctx context.Context, providerName string, availability map[string]bool) bool {
	if availability == nil {
		return a.providerUsable(ctx, providerName)
	}
	if usable, ok := availability[providerName]; ok {
		return usable
	}
	usable := a.providerUsable(ctx, providerName)
	availability[providerName] = usable
	return usable
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
