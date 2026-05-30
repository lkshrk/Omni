package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestCurrentRefreshProviderScanPlan_DedupesConcreteCoveredByEcosystem(t *testing.T) {
	system := &bulkConcreteStub{
		bulkCheckingStub: bulkCheckingStub{
			stubProvider: stubProvider{name: "system", available: true},
		},
		concreteName: "brew",
	}
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, system, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd":  {Provider: "system"},
			"git": {Provider: "system"},
		},
		Hosts: map[string][]string{host: {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: host, Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "fd"}, {Name: "git"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	plan, err := a.CurrentRefreshProviderScanPlan(context.Background())
	if err != nil {
		t.Fatalf("CurrentRefreshProviderScanPlan: %v", err)
	}

	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %#v, want one logical ecosystem scan", plan.Steps)
	}
	step := plan.Steps[0]
	if step.Provider != "system" || step.Label != "system/brew" || step.Count != 2 {
		t.Fatalf("step = %#v, want system/brew count 2", step)
	}
	if total := plan.Total(); total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
}

func TestRefreshProviderScanProviderNamesFromCountsDropsEmptyProviders(t *testing.T) {
	got := app.RefreshProviderScanProviderNames(map[string]int{
		"system": 2,
		"":       3,
		"node":   1,
	})
	want := map[string]bool{"system": true, "node": true}
	if len(got) != len(want) {
		t.Fatalf("provider names = %v, want %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("provider names = %v, unexpected %q", got, name)
		}
		delete(want, name)
	}
	if len(want) > 0 {
		t.Fatalf("provider names = %v, missing %v", got, want)
	}
}

func TestCurrentRefreshProviderScanPlan_KeepsConcreteWhenNoConfiguredEcosystemCoversIt(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Hosts: map[string][]string{host: {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: host, Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "git"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	plan, err := a.CurrentRefreshProviderScanPlan(context.Background())
	if err != nil {
		t.Fatalf("CurrentRefreshProviderScanPlan: %v", err)
	}

	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %#v, want one concrete scan", plan.Steps)
	}
	step := plan.Steps[0]
	if step.Provider != "brew" || step.Label != "brew" || step.Count != 1 {
		t.Fatalf("step = %#v, want brew count 1", step)
	}
}

func TestCurrentRefreshProviderScanPlanReadsCurrentAppState(t *testing.T) {
	system := &bulkConcreteStub{
		bulkCheckingStub: bulkCheckingStub{
			stubProvider: stubProvider{name: "system", available: true},
		},
		concreteName: "brew",
	}
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, system, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system"},
		},
		Hosts: map[string][]string{host: {"dev"}},
		Groups: []*config.GroupConfig{
			{Name: host, Special: "host"},
			{Name: "dev", Tools: []config.ToolEntry{{Name: "git"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	plan, err := a.CurrentRefreshProviderScanPlan(context.Background())
	if err != nil {
		t.Fatalf("CurrentRefreshProviderScanPlan: %v", err)
	}

	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %#v, want one logical ecosystem scan", plan.Steps)
	}
	step := plan.Steps[0]
	if step.Provider != "system" || step.Label != "system/brew" || step.Count != 1 {
		t.Fatalf("step = %#v, want system/brew count 1", step)
	}
}
