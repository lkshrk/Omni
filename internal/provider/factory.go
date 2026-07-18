package provider

import (
	"fmt"
	"sort"

	"github.com/lkshrk/omni/internal/executor"
)

// ConcreteFactory builds one concrete provider (a single package manager such as
// brew or apt) from the shared executor.
//
// This is the registration seam for concrete providers. A concrete provider
// self-registers its factory from its own package init(); internal/provider/all
// blank-imports every built-in so those init()s run. Adding a concrete provider
// therefore needs no change to internal/app: implement Provider, call
// RegisterConcrete in the package init(), add a blank import to
// internal/provider/all, and add a Metadata entry to the catalog.
//
// The parameterized/coordinated providers — the node and python ecosystem
// families and their named managers (bun/pnpm/npm/uv), and the system family
// that delegates to the concrete package managers — take a manager hint or a
// delegate set and are still wired explicitly in internal/app; they are not
// built through this seam.
type ConcreteFactory func(executor.Executor) Provider

var concreteFactories = map[string]ConcreteFactory{}

// RegisterConcrete records a concrete provider factory under name. It panics on
// an empty name, a nil factory, or a duplicate: registration runs once per name
// at init() time, so any of these is a build-time wiring bug, not a runtime
// condition.
func RegisterConcrete(name string, factory ConcreteFactory) {
	switch {
	case name == "":
		panic("provider: RegisterConcrete with empty name")
	case factory == nil:
		panic("provider: RegisterConcrete nil factory for " + name)
	}
	if _, dup := concreteFactories[name]; dup {
		panic(fmt.Sprintf("provider: concrete provider %q already registered", name))
	}
	concreteFactories[name] = factory
}

// BuildConcreteProviders constructs every registered concrete provider with the
// given executor, keyed by provider name.
func BuildConcreteProviders(exec executor.Executor) map[string]Provider {
	out := make(map[string]Provider, len(concreteFactories))
	for name, factory := range concreteFactories {
		out[name] = factory(exec)
	}
	return out
}

// RegisteredConcreteNames returns the registered concrete provider names, sorted.
func RegisteredConcreteNames() []string {
	names := make([]string, 0, len(concreteFactories))
	for name := range concreteFactories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
