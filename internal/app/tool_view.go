package app

import (
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type ToolViewSection string

const (
	ToolViewSectionUpdates     ToolViewSection = "updates"
	ToolViewSectionQuarantined ToolViewSection = "quarantined"
	ToolViewSectionOutOfSync   ToolViewSection = "out-of-sync"
	ToolViewSectionInstalled   ToolViewSection = "installed"
	ToolViewSectionIgnored     ToolViewSection = "ignored"
	ToolViewSectionAvailable   ToolViewSection = "available"
)

type ToolSyncStatus string

const (
	ToolSyncOK            ToolSyncStatus = "ok"
	ToolSyncMissing       ToolSyncStatus = "missing"
	ToolSyncUnclaimed     ToolSyncStatus = "unclaimed"
	ToolSyncWrongProvider ToolSyncStatus = "wrong-provider"
	ToolSyncNvmManaged    ToolSyncStatus = "nvm-managed"
)

type ToolClassificationContext struct {
	Ignored    bool
	Discovered bool

	ToolProviderPins map[string]string

	EffectiveSystemManager string
	EffectivePythonManager string
	EffectiveNodeManager   string

	// NvmManaged marks system-provider tools whose active binary resolves via nvm.
	NvmManaged map[string]bool
}

type ToolProviderDisplayInput struct {
	Provider         string
	InstalledWith    string
	ExplicitProvider string

	EffectiveSystemManager string
	EffectivePythonManager string
	EffectiveNodeManager   string
}

type ToolProviderDisplay struct {
	Meta     string
	Concrete string
	Override bool
}

// Label renders the provider for display: the concrete package manager when one
// is resolved (e.g. "bun"), falling back to the ecosystem name only when the
// tool is unresolved (e.g. "node"). The meta(concrete) wrapper and the override
// "!" marker are intentionally omitted — wrong-provider state is conveyed by the
// Out-of-sync section and the expanded candidate detail, not the column.
func (d ToolProviderDisplay) Label() string {
	if d.Concrete != "" {
		return d.Concrete
	}
	return d.Meta
}

type ToolViewClassification struct {
	Section    ToolViewSection
	SyncStatus ToolSyncStatus
}

type ToolViewListOptions struct {
	Tools           []*database.ToolCache
	DiscoveredTools []*database.ToolCache
	SearchTools     []*database.ToolCache

	Query          string
	ProviderFilter string
	GroupFilter    string

	ToolMemberships map[string][]string
	IgnoredTools    map[string]bool
	IgnoreLabels    map[string]string

	Classification ToolClassificationContext
}

type ToolViewList struct {
	Tools  []*database.ToolCache
	Counts map[ToolViewSection]int
}

func ClassifyToolView(tool *database.ToolCache, context ToolClassificationContext) ToolViewClassification {
	syncStatus := ToolSyncStatusForTool(tool, context)
	return ToolViewClassification{
		Section:    toolViewSection(tool, context, syncStatus),
		SyncStatus: syncStatus,
	}
}

func BuildToolViewList(options ToolViewListOptions) ToolViewList {
	q := strings.ToLower(options.Query)
	targetProvider := options.ProviderFilter
	normal := make([]*database.ToolCache, 0, len(options.Tools)+len(options.DiscoveredTools)+len(options.SearchTools))
	ignored := make([]*database.ToolCache, 0)
	discoveredKeys := toolViewKeySet(options.DiscoveredTools)

	for _, tool := range options.Tools {
		if tool == nil {
			continue
		}
		if !toolMatchesGroup(tool, options.GroupFilter, options.ToolMemberships) {
			continue
		}
		if targetProvider != "" && ToolProviderEcosystem(tool.Provider) != targetProvider {
			continue
		}
		if !toolMatchesQuery(tool, q) {
			continue
		}
		if toolListedAsIgnored(tool.Name, options) {
			ignored = append(ignored, tool)
		} else {
			normal = append(normal, tool)
		}
	}

	if len(options.DiscoveredTools) > 0 {
		configNames := make(map[string]bool, len(options.Tools))
		for _, tool := range options.Tools {
			if tool != nil {
				configNames[tool.Name] = true
			}
		}
		for _, tool := range options.DiscoveredTools {
			if tool == nil || configNames[tool.Name] {
				continue
			}
			if !toolMatchesQuery(tool, q) {
				continue
			}
			if options.GroupFilter == "" && targetProvider == "" {
				normal = append(normal, tool)
			}
		}
	}

	if len(options.SearchTools) > 0 {
		existing := make(map[string]bool, len(options.Tools)+len(options.DiscoveredTools))
		for _, tool := range options.Tools {
			existing[toolViewKey(tool)] = true
		}
		for _, tool := range options.DiscoveredTools {
			existing[toolViewKey(tool)] = true
		}
		for _, tool := range options.SearchTools {
			if tool == nil || existing[toolViewKey(tool)] {
				continue
			}
			if targetProvider == "" || ToolProviderEcosystem(tool.Provider) == targetProvider {
				normal = append(normal, tool)
			}
		}
	}

	visible := append(normal, ignored...)
	sort.SliceStable(visible, func(i, j int) bool {
		sectionI := classifyToolForList(visible[i], options, discoveredKeys).Section
		sectionJ := classifyToolForList(visible[j], options, discoveredKeys).Section
		if sectionI != sectionJ {
			return toolViewSectionOrder(sectionI) < toolViewSectionOrder(sectionJ)
		}
		nameI := strings.ToLower(visible[i].Name)
		nameJ := strings.ToLower(visible[j].Name)
		if nameI != nameJ {
			return nameI < nameJ
		}
		return strings.ToLower(visible[i].Provider) < strings.ToLower(visible[j].Provider)
	})

	counts := make(map[ToolViewSection]int, 5)
	for _, tool := range visible {
		counts[classifyToolForList(tool, options, discoveredKeys).Section]++
	}
	return ToolViewList{Tools: visible, Counts: counts}
}

func ToolSyncStatusForTool(tool *database.ToolCache, context ToolClassificationContext) ToolSyncStatus {
	if tool == nil {
		return ToolSyncOK
	}
	if !tool.Tracked {
		return ToolSyncUnclaimed
	}
	if !tool.Installed {
		return ToolSyncMissing
	}
	desired, _ := ExpectedConcreteProviderForTool(tool, context)
	if desired != "" && tool.InstalledWith != "" && tool.InstalledWith != desired {
		return ToolSyncWrongProvider
	}
	// Only apply nvm-managed detection for tools without an explicit pin.
	// If pinned (to bun etc), respect the pin even if the provider looks system.
	if context.ToolProviderPins[tool.Name] == "" &&
		IsSystemProvider(tool.Provider) && tool.Installed &&
		context.NvmManaged != nil && context.NvmManaged[tool.Name] {
		return ToolSyncNvmManaged
	}
	return ToolSyncOK
}

func DesiredConcreteProviderForTool(tool *database.ToolCache, context ToolClassificationContext) (string, string) {
	if tool == nil {
		return "", ""
	}
	if pin := context.ToolProviderPins[tool.Name]; pin != "" {
		return pin, "configured"
	}
	switch tool.Provider {
	case provider.EcosystemSystem:
		return context.EffectiveSystemManager, "default"
	case provider.EcosystemPython:
		return context.EffectivePythonManager, "default"
	case provider.EcosystemNode:
		return context.EffectiveNodeManager, "default"
	default:
		return "", ""
	}
}

// ExpectedConcreteProviderForTool returns the concrete manager omni expects for a
// tracked tool: ecosystem defaults/pins for node/system/python, otherwise the
// configured route provider for multi-concrete tools (e.g. brew vs bun).
func ExpectedConcreteProviderForTool(tool *database.ToolCache, context ToolClassificationContext) (string, string) {
	desired, source := DesiredConcreteProviderForTool(tool, context)
	if desired != "" || tool == nil {
		return desired, source
	}
	if tool.Provider != "" && !provider.BuiltinIsEcosystem(tool.Provider) {
		return tool.Provider, "configured"
	}
	return "", ""
}

func ToolProviderDisplayLabel(input ToolProviderDisplayInput) string {
	return ToolProviderDisplayParts(input).Label()
}

func ToolHasPrivilegeMarker(tool *database.ToolCache, context ToolClassificationContext) bool {
	if tool == nil {
		return false
	}
	if tool.Privilege != "" {
		return true
	}
	if sudoBackedSystemProvider(tool.Provider) || sudoBackedSystemProvider(tool.InstalledWith) {
		return true
	}
	return tool.Provider == provider.EcosystemSystem && sudoBackedSystemProvider(context.EffectiveSystemManager)
}

func ToolProviderDisplayParts(input ToolProviderDisplayInput) ToolProviderDisplay {
	raw := strings.TrimSpace(input.Provider)
	ecosystem, ok := provider.BuiltinEcosystemFor(raw)
	if !ok {
		return ToolProviderDisplay{Meta: raw}
	}
	if !provider.BuiltinIsEcosystem(raw) {
		return ToolProviderDisplay{
			Meta:     ecosystem,
			Concrete: concreteProviderDisplayLabel(ecosystem, raw),
			Override: true,
		}
	}
	concrete := strings.TrimSpace(input.InstalledWith)
	explicit := strings.TrimSpace(input.ExplicitProvider)
	if concrete == "" || concrete == raw {
		concrete = explicit
	}
	if concrete == "" || concrete == raw {
		switch ecosystem {
		case provider.EcosystemSystem:
			concrete = strings.TrimSpace(input.EffectiveSystemManager)
		case provider.EcosystemPython:
			concrete = strings.TrimSpace(input.EffectivePythonManager)
		case provider.EcosystemNode:
			concrete = strings.TrimSpace(input.EffectiveNodeManager)
		}
	}
	if concrete == raw {
		concrete = ""
	}
	return ToolProviderDisplay{
		Meta:     ecosystem,
		Concrete: concrete,
		Override: explicit != "" && concrete == explicit,
	}
}

func concreteProviderDisplayLabel(ecosystem, raw string) string {
	if opt, ok := provider.BuiltinManagerOption(ecosystem, raw); ok && opt.SettingsValue != "" {
		return opt.SettingsValue
	}
	return raw
}

func sudoBackedSystemProvider(name string) bool {
	return provider.BuiltinMetadata(name).RequiresPrivilege
}

func toolViewSection(tool *database.ToolCache, context ToolClassificationContext, syncStatus ToolSyncStatus) ToolViewSection {
	if tool == nil {
		return ToolViewSectionAvailable
	}
	if context.Ignored {
		return ToolViewSectionIgnored
	}
	if tool.Installed && tool.Outdated && tool.UpdateBlocked == UpdateBlockSelfUpdates {
		// self-updating casks read as a normal update (the version is behind),
		// just marked and non-actionable — not a deferred quarantine.
		return ToolViewSectionUpdates
	}
	if tool.Installed && tool.Outdated && tool.UpdateBlocked != "" {
		return ToolViewSectionQuarantined
	}
	if tool.Installed && tool.Outdated {
		return ToolViewSectionUpdates
	}
	if context.Discovered || (tool.Tracked && !tool.Installed) || syncStatus == ToolSyncWrongProvider || syncStatus == ToolSyncNvmManaged {
		return ToolViewSectionOutOfSync
	}
	if tool.Installed {
		return ToolViewSectionInstalled
	}
	return ToolViewSectionAvailable
}

func classifyToolForList(tool *database.ToolCache, options ToolViewListOptions, discoveredKeys map[string]bool) ToolViewClassification {
	context := options.Classification
	if tool != nil {
		context.Ignored = options.IgnoreLabels[tool.Name] != "" || options.IgnoredTools[tool.Name]
		context.Discovered = discoveredKeys[toolViewKey(tool)]
	}
	return ClassifyToolView(tool, context)
}

func toolMatchesGroup(tool *database.ToolCache, group string, memberships map[string][]string) bool {
	if group == "" {
		return true
	}
	for _, name := range memberships[toolViewKey(tool)] {
		if name == group {
			return true
		}
	}
	return false
}

func toolMatchesQuery(tool *database.ToolCache, q string) bool {
	if q == "" {
		return true
	}
	if tool == nil {
		return false
	}
	return strings.Contains(strings.ToLower(tool.Name), q) ||
		strings.Contains(strings.ToLower(tool.Provider), q)
}

func ToolProviderEcosystem(raw string) string {
	if ecosystem, ok := provider.BuiltinEcosystemFor(raw); ok {
		return ecosystem
	}
	return raw
}

// IsSystemProvider reports whether raw names a concrete system package manager
// (brew, apt, dnf, pacman, zypper, apk) or the system ecosystem alias.
func IsSystemProvider(raw string) bool {
	return ToolProviderEcosystem(raw) == provider.EcosystemSystem
}

type ToolProviderDisplayRole string

const (
	ToolProviderDisplayRoleOther  ToolProviderDisplayRole = "other"
	ToolProviderDisplayRoleSystem ToolProviderDisplayRole = provider.EcosystemSystem
	ToolProviderDisplayRoleNode   ToolProviderDisplayRole = provider.EcosystemNode
	ToolProviderDisplayRolePython ToolProviderDisplayRole = provider.EcosystemPython
)

func ToolProviderDisplayRoleFor(raw string) ToolProviderDisplayRole {
	switch ToolProviderEcosystem(raw) {
	case provider.EcosystemSystem:
		return ToolProviderDisplayRoleSystem
	case provider.EcosystemNode:
		return ToolProviderDisplayRoleNode
	case provider.EcosystemPython:
		return ToolProviderDisplayRolePython
	default:
		return ToolProviderDisplayRoleOther
	}
}

func toolViewKey(tool *database.ToolCache) string {
	if tool == nil {
		return ""
	}
	return tool.Name + "\x00" + tool.Provider
}

func toolViewKeySet(tools []*database.ToolCache) map[string]bool {
	keys := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		keys[toolViewKey(tool)] = true
	}
	return keys
}

func toolListedAsIgnored(name string, options ToolViewListOptions) bool {
	return options.IgnoredTools[name] || options.IgnoreLabels[name] != ""
}

func toolViewSectionOrder(section ToolViewSection) int {
	switch section {
	case ToolViewSectionUpdates:
		return 0
	case ToolViewSectionQuarantined:
		return 1
	case ToolViewSectionOutOfSync:
		return 2
	case ToolViewSectionInstalled:
		return 3
	case ToolViewSectionAvailable:
		return 4
	case ToolViewSectionIgnored:
		return 5
	default:
		return 4
	}
}
