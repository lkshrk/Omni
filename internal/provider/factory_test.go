package provider_test

import (
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
)

func TestRegisterConcrete_BuildAndNames(t *testing.T) {
	var gotExec executor.Executor
	provider.RegisterConcrete("factorytest-zeta", func(exec executor.Executor) provider.Provider {
		gotExec = exec
		return &fakeProvider{name: "factorytest-zeta"}
	})
	provider.RegisterConcrete("factorytest-alpha", func(executor.Executor) provider.Provider {
		return &fakeProvider{name: "factorytest-alpha"}
	})

	names := provider.RegisteredConcreteNames()
	if !slices.Contains(names, "factorytest-alpha") || !slices.Contains(names, "factorytest-zeta") {
		t.Fatalf("RegisteredConcreteNames missing registrations: %v", names)
	}
	if !slices.IsSorted(names) {
		t.Fatalf("RegisteredConcreteNames must be sorted: %v", names)
	}

	exec := &executor.MockExecutor{}
	built := provider.BuildConcreteProviders(exec)
	zeta, ok := built["factorytest-zeta"]
	if !ok || zeta.Name() != "factorytest-zeta" {
		t.Fatalf("BuildConcreteProviders missing factorytest-zeta: %+v", built)
	}
	if _, ok := built["factorytest-alpha"]; !ok {
		t.Fatalf("BuildConcreteProviders missing factorytest-alpha: %+v", built)
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
	provider.RegisterConcrete("factorytest-dup", func(executor.Executor) provider.Provider {
		return &fakeProvider{name: "factorytest-dup"}
	})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate RegisterConcrete should panic")
		}
	}()
	provider.RegisterConcrete("factorytest-dup", func(executor.Executor) provider.Provider {
		return &fakeProvider{name: "factorytest-dup"}
	})
}
