package provider_test

import (
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

var factoryTestID atomic.Uint64

func TestRegisterConcrete_BuildAndNames(t *testing.T) {
	suffix := strconv.FormatUint(factoryTestID.Add(1), 10)
	zetaName := "factorytest-zeta-" + suffix
	alphaName := "factorytest-alpha-" + suffix
	var gotExec executor.Executor
	provider.RegisterConcrete(zetaName, func(exec executor.Executor) provider.Provider {
		gotExec = exec
		return &fakeProvider{name: zetaName}
	})
	provider.RegisterConcrete(alphaName, func(executor.Executor) provider.Provider {
		return &fakeProvider{name: alphaName}
	})

	names := provider.RegisteredConcreteNames()
	if !slices.Contains(names, alphaName) || !slices.Contains(names, zetaName) {
		t.Fatalf("RegisteredConcreteNames missing registrations: %v", names)
	}
	if !slices.IsSorted(names) {
		t.Fatalf("RegisteredConcreteNames must be sorted: %v", names)
	}

	exec := &executor.MockExecutor{}
	built := provider.BuildConcreteProviders(exec)
	zeta, ok := built[zetaName]
	if !ok || zeta.Name() != zetaName {
		t.Fatalf("BuildConcreteProviders missing %s: %+v", zetaName, built)
	}
	if _, ok := built[alphaName]; !ok {
		t.Fatalf("BuildConcreteProviders missing %s: %+v", alphaName, built)
	}
	if gotExec != exec {
		t.Fatal("factory should receive the executor passed to BuildConcreteProviders")
	}
}

func TestRegisterConcrete_PanicsOnEmptyName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterConcrete with empty name should panic")
		}
	}()
	provider.RegisterConcrete("", func(executor.Executor) provider.Provider { return nil })
}

func TestRegisterConcrete_PanicsOnNilFactory(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterConcrete with nil factory should panic")
		}
	}()
	provider.RegisterConcrete("factorytest-nilfac", nil)
}

func TestRegisterConcrete_PanicsOnDuplicate(t *testing.T) {
	name := "factorytest-dup-" + strconv.FormatUint(factoryTestID.Add(1), 10)
	provider.RegisterConcrete(name, func(executor.Executor) provider.Provider {
		return &fakeProvider{name: name}
	})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate RegisterConcrete should panic")
		}
	}()
	provider.RegisterConcrete(name, func(executor.Executor) provider.Provider {
		return &fakeProvider{name: name}
	})
}
