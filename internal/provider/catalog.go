package provider

import "sort"

const (
	EcosystemSystem = "system"
	EcosystemNode   = "node"
	EcosystemPython = "python"
)

var builtinMetadata = map[string]Metadata{
	EcosystemSystem: {
		Kind:                           ProviderKindEcosystem,
		Ecosystem:                      EcosystemSystem,
		ImportSkipsRegisteredDelegates: true,
		DisplayOrder:                   10,
		SupportsTaps:                   true,
	},
	EcosystemNode: {
		Kind:         ProviderKindEcosystem,
		Ecosystem:    EcosystemNode,
		DisplayOrder: 20,
		ManagerOptions: []ManagerOption{
			{Name: "bun", Provider: EcosystemNode, SettingsValue: "bun", Order: 10},
			{Name: "pnpm", Provider: EcosystemNode, SettingsValue: "pnpm", Order: 20},
			{Name: "npm", Provider: EcosystemNode, SettingsValue: "npm", Order: 30},
		},
		DefaultInstallOrder: 20,
	},
	EcosystemPython: {
		Kind:         ProviderKindEcosystem,
		Ecosystem:    EcosystemPython,
		DisplayOrder: 30,
		ManagerOptions: []ManagerOption{
			{Name: "uv", Provider: EcosystemPython, SettingsValue: "uv", Order: 10},
			{Name: "pip3", Provider: "pip", SettingsValue: "pip3", Order: 20},
			{Name: "pip", Provider: "pip", SettingsValue: "pip3", Order: 30, Alias: true},
		},
		DefaultInstallOrder: 30,
	},
	"brew": {
		Kind:                ProviderKindConcrete,
		Ecosystem:           EcosystemSystem,
		DisplayOrder:        100,
		DefaultInstallOrder: 10,
		SupportsTaps:        true,
	},
	"apt":    {Kind: ProviderKindConcrete, Ecosystem: EcosystemSystem, DisplayOrder: 110, RequiresPrivilege: true},
	"apk":    {Kind: ProviderKindConcrete, Ecosystem: EcosystemSystem, DisplayOrder: 120, RequiresPrivilege: true},
	"dnf":    {Kind: ProviderKindConcrete, Ecosystem: EcosystemSystem, DisplayOrder: 130, RequiresPrivilege: true},
	"pacman": {Kind: ProviderKindConcrete, Ecosystem: EcosystemSystem, DisplayOrder: 140, RequiresPrivilege: true},
	"zypper": {Kind: ProviderKindConcrete, Ecosystem: EcosystemSystem, DisplayOrder: 150, RequiresPrivilege: true},
	"bun":    {Kind: ProviderKindConcrete, Ecosystem: EcosystemNode, DisplayOrder: 200},
	"pnpm":   {Kind: ProviderKindConcrete, Ecosystem: EcosystemNode, DisplayOrder: 210},
	"npm":    {Kind: ProviderKindConcrete, Ecosystem: EcosystemNode, DisplayOrder: 220},
	"uv":     {Kind: ProviderKindConcrete, Ecosystem: EcosystemPython, DisplayOrder: 300},
	"pip3":   {Kind: ProviderKindConcrete, Ecosystem: EcosystemPython, DisplayOrder: 310},
	"pip": {
		Kind:                ProviderKindConcrete,
		Ecosystem:           EcosystemPython,
		DisplayOrder:        320,
		DefaultInstallOrder: 40,
	},
	"cargo": {
		Kind:                ProviderKindConcrete,
		DisplayOrder:        330,
		DefaultInstallOrder: 50,
	},
	"script":   {Kind: ProviderKindConcrete, DisplayOrder: 400},
	"apt_repo": {Kind: ProviderKindConcrete, Ecosystem: EcosystemSystem, DisplayOrder: 105},
}

var systemProviderPriority = []string{"apt", "apk", "dnf", "zypper", "pacman", "brew"}

func BuiltinMetadata(name string) Metadata {
	meta, ok := builtinMetadata[name]
	if !ok {
		return Metadata{Kind: ProviderKindConcrete}
	}
	meta.ManagerOptions = cloneManagerOptions(meta.ManagerOptions)
	return meta
}

func BuiltinKnownNames() []string {
	return sortedMetadataNames(builtinMetadata, func(_ string, _ Metadata) bool { return true })
}

func BuiltinEcosystemNames() []string {
	return sortedMetadataNames(builtinMetadata, func(_ string, meta Metadata) bool {
		return meta.Kind == ProviderKindEcosystem
	})
}

func BuiltinConcreteEcosystems() map[string]string {
	out := make(map[string]string)
	for name, meta := range builtinMetadata {
		if meta.Kind == ProviderKindConcrete && meta.Ecosystem != "" {
			out[name] = meta.Ecosystem
		}
		for _, opt := range meta.ManagerOptions {
			if opt.Name != "" && meta.Ecosystem != "" {
				out[opt.Name] = meta.Ecosystem
			}
		}
	}
	return out
}

// BuiltinConcreteConfigNames returns concrete names accepted in tool config.
// Script is appended separately by the schema because it has extra validation.
func BuiltinConcreteConfigNames() []string {
	return sortedMetadataNames(builtinMetadata, func(name string, meta Metadata) bool {
		return meta.Kind == ProviderKindConcrete && name != "script"
	})
}

func BuiltinEcosystemFor(name string) (string, bool) {
	if meta, ok := builtinMetadata[name]; ok {
		if meta.Kind == ProviderKindEcosystem {
			return name, true
		}
		if meta.Ecosystem != "" {
			return meta.Ecosystem, true
		}
	}
	for ecoName, meta := range builtinMetadata {
		ecosystem := meta.Ecosystem
		if ecosystem == "" && meta.Kind == ProviderKindEcosystem {
			ecosystem = ecoName
		}
		if ecosystem == "" {
			continue
		}
		for _, opt := range meta.ManagerOptions {
			if opt.Name == name {
				return ecosystem, true
			}
		}
	}
	return "", false
}

func BuiltinIsEcosystem(name string) bool {
	meta, ok := builtinMetadata[name]
	return ok && meta.Kind == ProviderKindEcosystem
}

func BuiltinManagerOptions(ecosystem string) []ManagerOption {
	var opts []ManagerOption
	for name, meta := range builtinMetadata {
		if name != ecosystem && meta.Ecosystem != ecosystem {
			continue
		}
		opts = append(opts, meta.ManagerOptions...)
	}
	sortManagerOptions(opts)
	return opts
}

func BuiltinManagerOption(ecosystem, manager string) (ManagerOption, bool) {
	for _, opt := range BuiltinManagerOptions(ecosystem) {
		if opt.Name == manager {
			return opt, true
		}
	}
	return ManagerOption{}, false
}

func BuiltinManagerNames(ecosystem string) []string {
	opts := BuiltinManagerOptions(ecosystem)
	return managerNames(opts, false)
}

func BuiltinSettingsManagerNames(ecosystem string) []string {
	opts := BuiltinManagerOptions(ecosystem)
	return managerNames(opts, true)
}

func managerNames(opts []ManagerOption, excludeAliases bool) []string {
	names := make([]string, 0, len(opts))
	seen := make(map[string]struct{}, len(opts))
	for _, opt := range opts {
		if excludeAliases && opt.Alias {
			continue
		}
		if opt.Name == "" {
			continue
		}
		if _, ok := seen[opt.Name]; ok {
			continue
		}
		seen[opt.Name] = struct{}{}
		names = append(names, opt.Name)
	}
	return names
}

func BuiltinDefaultInstallProviderNames() []string {
	return sortedMetadataNamesByOrder(builtinMetadata, func(_ string, meta Metadata) bool {
		return meta.DefaultInstallOrder > 0
	}, func(meta Metadata) int {
		return meta.DefaultInstallOrder
	})
}

func BuiltinSystemProviderPriorityNames() []string {
	return append([]string(nil), systemProviderPriority...)
}

// BuiltinConcreteProviderPriorityNames returns every concrete package-manager
// provider, ordered by DisplayOrder, suitable as the default host
// provider-priority list. The special "script" provider and the "pip3" alias of
// "pip" are excluded — they are not user-prioritizable package managers.
func BuiltinConcreteProviderPriorityNames() []string {
	return sortedMetadataNames(builtinMetadata, func(name string, meta Metadata) bool {
		if meta.Kind != ProviderKindConcrete {
			return false
		}
		if name == "script" || name == "apt_repo" || name == "pip3" {
			return false
		}
		return true
	})
}

// BuiltinConcreteProvidersForEcosystem returns the concrete providers belonging
// to the given ecosystem family ("system"/"node"/"python"), ordered by
// DisplayOrder. Used to expand a family-level disable into concrete providers.
// The "pip3" alias is excluded in favour of "pip".
func BuiltinConcreteProvidersForEcosystem(ecosystem string) []string {
	if ecosystem == "" {
		return nil
	}
	return sortedMetadataNames(builtinMetadata, func(name string, meta Metadata) bool {
		if meta.Kind != ProviderKindConcrete || meta.Ecosystem != ecosystem {
			return false
		}
		if name == "script" || name == "apt_repo" || name == "pip3" {
			return false
		}
		return true
	})
}

func MergeKnownNames(registered []string) []string {
	seen := make(map[string]struct{})
	merged := append([]string(nil), BuiltinKnownNames()...)
	for _, name := range merged {
		seen[name] = struct{}{}
	}
	for _, name := range registered {
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		left, right := BuiltinMetadata(merged[i]), BuiltinMetadata(merged[j])
		li, ri := displayOrder(left), displayOrder(right)
		if li != ri {
			return li < ri
		}
		return merged[i] < merged[j]
	})
	return merged
}

func cloneManagerOptions(opts []ManagerOption) []ManagerOption {
	return append([]ManagerOption(nil), opts...)
}
