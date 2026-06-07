package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestCurrentRefreshProviderScanPlan_GroupsConcreteProviderTools(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("fd", "brew"),
			logicalTool("git", "brew"),
		),
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
		t.Fatalf("steps = %#v, want one concrete provider scan", plan.Steps)
	}
	step := plan.Steps[0]
	if step.Provider != "brew" || step.Label != "brew" || step.Count != 2 {
		t.Fatalf("step = %#v, want brew count 2", step)
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

func TestRefreshProviderScanPlanAccessorsSkipEmptyProviders(t *testing.T) {
	plan := app.RefreshProviderScanPlan{Steps: []app.RefreshProviderScanStep{
		{Provider: "system", Label: "system (apt)", Count: 2},
		{Provider: "", Label: "ignored", Count: 9},
		{Provider: "node", Count: -3},
		{Provider: "brew", Label: "brew", Count: 1},
	}}

	names := plan.ProviderNames()
	if len(names) != 3 || names[0] != "system" || names[1] != "node" || names[2] != "brew" {
		t.Fatalf("provider names = %v, want system/node/brew", names)
	}

	set := plan.ProviderSet()
	if len(set) != 3 || !set["system"] || !set["node"] || !set["brew"] || set[""] {
		t.Fatalf("provider set = %#v, want non-empty providers only", set)
	}

	counts := plan.CountsByProvider()
	if len(counts) != 3 || counts["system"] != 2 || counts["node"] != -3 || counts["brew"] != 1 {
		t.Fatalf("counts = %#v, want counts for non-empty providers", counts)
	}
	if total := plan.Total(); total != 3 {
		t.Fatalf("total = %d, want positive provider counts only", total)
	}

	labels := plan.LabelsByProvider()
	if len(labels) != 2 || labels["system"] != "system (apt)" || labels["brew"] != "brew" || labels["node"] != "" {
		t.Fatalf("labels = %#v, want non-empty labels for non-empty providers", labels)
	}
}

func TestCurrentRefreshProviderScanPlan_KeepsConfiguredConcreteProvider(t *testing.T) {
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("git", "brew")),
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
	brew := &stubProvider{name: "brew", available: true}
	a, cfgPath := newImportApp(t, brew)
	host := testShortHostname()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("git", "brew")),
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
		t.Fatalf("steps = %#v, want one concrete provider scan", plan.Steps)
	}
	step := plan.Steps[0]
	if step.Provider != "brew" || step.Label != "brew" || step.Count != 1 {
		t.Fatalf("step = %#v, want brew count 1", step)
	}
}
