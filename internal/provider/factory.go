package provider

import (
	"fmt"
	"sort"

	"github.com/lkshrk/omni/internal/executor"
)

// ConcreteFactory — The registration seam: providers self-register from their own init(), and provider/all blank-imports them.
type ConcreteFactory func(executor.Executor) Provider

var concreteFactories = map[string]ConcreteFactory{}

// RegisterConcrete — Panics on an empty name, nil factory, or duplicate: those are init-time wiring bugs.
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
