package provider

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registry holds all registered providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	metadata  map[string]Metadata
}

// ProviderKind classifies provider identity in config and resolver flows.
type ProviderKind string

const (
	// ProviderKindConcrete is a physical package manager or registry provider.
	ProviderKindConcrete ProviderKind = "concrete"
	// ProviderKindEcosystem is a portable provider such as system, node, or python.
	ProviderKindEcosystem ProviderKind = "ecosystem"
)

// Metadata describes a provider's role relative to ecosystem resolution.
type Metadata struct {
	Kind                           ProviderKind
	Ecosystem                      string
	ImportSkipsRegisteredDelegates bool
	DisplayOrder                   int
	DefaultInstallOrder            int
	ManagerOptions                 []ManagerOption
	SupportsTaps                   bool
	// RequiresPrivilege marks providers whose install/uninstall are sudo-backed
	// system package managers. It is a coarse display hint (drives the privilege
	// marker in tool views); the precise per-action decision lives in each
	// provider's PrivilegePlan.
	RequiresPrivilege bool
}

// ManagerOption describes one concrete manager usable through an ecosystem
// provider, such as node→pnpm or python→uv.
type ManagerOption struct {
	Name          string
	Provider      string
	SettingsValue string
	Order         int
	Alias         bool
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		metadata:  make(map[string]Metadata),
	}
}

// Register adds a provider. Overwrites if name already registered.
func (r *Registry) Register(p Provider) {
	r.RegisterWithMetadata(p, Metadata{Kind: ProviderKindConcrete})
}

// RegisterWithMetadata adds a provider with registry metadata. Overwrites if
// name already registered.
func (r *Registry) RegisterWithMetadata(p Provider, meta Metadata) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if meta.Kind == "" {
		meta.Kind = ProviderKindConcrete
	}
	r.providers[p.Name()] = p
	r.metadata[p.Name()] = meta
}

// Get returns a provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Metadata returns provider metadata by name.
func (r *Registry) Metadata(name string) (Metadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	meta, ok := r.metadata[name]
	return meta, ok
}

// IsEcosystemProvider reports whether name is registered as an ecosystem provider.
func (r *Registry) IsEcosystemProvider(name string) bool {
	meta, ok := r.Metadata(name)
	return ok && meta.Kind == ProviderKindEcosystem
}

// EcosystemProviders returns registered ecosystem providers in no particular order.
func (r *Registry) EcosystemProviders() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps := make([]Provider, 0)
	for name, p := range r.providers {
		if r.metadata[name].Kind == ProviderKindEcosystem {
			ps = append(ps, p)
		}
	}
	return ps
}

// ImportSkipsProvider reports whether full import/discovery should skip this
// provider because its registered concrete delegates are iterated separately.
func (r *Registry) ImportSkipsProvider(name string) bool {
	meta, ok := r.Metadata(name)
	return ok && meta.ImportSkipsRegisteredDelegates
}

// All returns all registered providers in no particular order.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		ps = append(ps, p)
	}
	return ps
}

// Names returns all registered provider names in no particular order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

// KnownNames returns all registered provider names in stable display order.
func (r *Registry) KnownNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedMetadataNames(r.metadata, func(_ string, _ Metadata) bool { return true })
}

// EcosystemNames returns registered ecosystem provider names in stable order.
func (r *Registry) EcosystemNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedMetadataNames(r.metadata, func(_ string, meta Metadata) bool {
		return meta.Kind == ProviderKindEcosystem
	})
}

// ConcreteEcosystems returns concrete provider/manager name → ecosystem name.
func (r *Registry) ConcreteEcosystems() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string)
	for name, meta := range r.metadata {
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

// EcosystemFor reports the ecosystem identity for an ecosystem provider,
// concrete provider, or concrete manager name.
func (r *Registry) EcosystemFor(name string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if meta, ok := r.metadata[name]; ok {
		if meta.Kind == ProviderKindEcosystem {
			return name, true
		}
		if meta.Ecosystem != "" {
			return meta.Ecosystem, true
		}
	}
	for ecoName, meta := range r.metadata {
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

// ManagerOptions returns concrete manager choices for an ecosystem in stable
// order.
func (r *Registry) ManagerOptions(ecosystem string) []ManagerOption {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var opts []ManagerOption
	for name, meta := range r.metadata {
		if name != ecosystem && meta.Ecosystem != ecosystem {
			continue
		}
		opts = append(opts, meta.ManagerOptions...)
	}
	sortManagerOptions(opts)
	return opts
}

// ManagerOption returns one concrete manager choice for an ecosystem.
func (r *Registry) ManagerOption(ecosystem, manager string) (ManagerOption, bool) {
	for _, opt := range r.ManagerOptions(ecosystem) {
		if opt.Name == manager {
			return opt, true
		}
	}
	return ManagerOption{}, false
}

// DefaultInstallProviderNames returns registered providers with a configured
// default install priority, in priority order.
func (r *Registry) DefaultInstallProviderNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedMetadataNamesByOrder(r.metadata, func(_ string, meta Metadata) bool {
		return meta.DefaultInstallOrder > 0
	}, func(meta Metadata) int {
		return meta.DefaultInstallOrder
	})
}

func sortedMetadataNames(metadata map[string]Metadata, include func(string, Metadata) bool) []string {
	names := make([]string, 0, len(metadata))
	for name, meta := range metadata {
		if include(name, meta) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := metadata[names[i]], metadata[names[j]]
		li, ri := displayOrder(left), displayOrder(right)
		if li != ri {
			return li < ri
		}
		return names[i] < names[j]
	})
	return names
}

func sortedMetadataNamesByOrder(metadata map[string]Metadata, include func(string, Metadata) bool, order func(Metadata) int) []string {
	names := make([]string, 0, len(metadata))
	for name, meta := range metadata {
		if include(name, meta) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := order(metadata[names[i]]), order(metadata[names[j]])
		if left == 0 {
			left = 1_000_000
		}
		if right == 0 {
			right = 1_000_000
		}
		if left != right {
			return left < right
		}
		return names[i] < names[j]
	})
	return names
}

func displayOrder(meta Metadata) int {
	if meta.DisplayOrder == 0 {
		return 1_000_000
	}
	return meta.DisplayOrder
}

func sortManagerOptions(opts []ManagerOption) {
	sort.Slice(opts, func(i, j int) bool {
		li, ri := opts[i].Order, opts[j].Order
		if li == 0 {
			li = 1_000_000
		}
		if ri == 0 {
			ri = 1_000_000
		}
		if li != ri {
			return li < ri
		}
		return opts[i].Name < opts[j].Name
	})
}

// Available returns the subset of registered providers whose binary is on PATH.
func (r *Registry) Available(ctx context.Context) ([]Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var available []Provider
	for _, p := range r.providers {
		ok, err := p.Available(ctx)
		if err != nil {
			return nil, fmt.Errorf("checking provider %s: %w", p.Name(), err)
		}
		if ok {
			available = append(available, p)
		}
	}
	return available, nil
}
