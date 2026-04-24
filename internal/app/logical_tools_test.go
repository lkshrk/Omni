package app_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	gosync "github.com/lkshrk/omni/internal/sync"
)

func TestSetTool_UpsertsLogicalSpecWithoutMembership(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SetTool("ripgrep", "system", "rg", "brew"); err != nil {
		t.Fatalf("SetTool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec, ok := cfg.Tools["ripgrep"]
	if !ok {
		t.Fatal("logical tool ripgrep not found")
	}
	if spec.Provider != "system" || spec.Package != "rg" || spec.InstallWith != "brew" {
		t.Fatalf("spec = %+v, want provider system package rg install_with brew", spec)
	}
	if len(cfg.Groups) != 0 {
		t.Fatalf("SetTool should not add memberships, got groups %+v", cfg.Groups)
	}
}

func TestSetTool_RejectsConcreteProvider(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.SetTool("ripgrep", "brew", "ripgrep", "")
	if err == nil {
		t.Fatal("SetTool accepted concrete provider")
	}
	if got := err.Error(); got != `provider "brew" is not an ecosystem provider` {
		t.Fatalf("error = %q", got)
	}
}

func TestSetTool_RejectsMismatchedInstallWith(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.SetTool("ripgrep", "node", "ripgrep", "brew")
	if err == nil {
		t.Fatal("SetTool accepted mismatched install_with")
	}
	if got := err.Error(); got != `install_with "brew" belongs to ecosystem "system", not "node"` {
		t.Fatalf("error = %q", got)
	}
}

func TestAddToolToGroup_RequiresLogicalSpec(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.AddToolToGroup("ripgrep", "base"); err == nil {
		t.Fatal("AddToolToGroup without logical spec returned nil")
	}
}

func TestAddAndRemoveToolToGroup_UpdatesMembershipOnly(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{{Name: "work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.AddToolToGroup("ripgrep", "work"); err != nil {
		t.Fatalf("AddToolToGroup: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after add: %v", err)
	}
	work := logicalTestGroupByName(cfg, "work")
	if work == nil || !logicalTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("work group missing ripgrep after add: %+v", cfg.Groups)
	}

	if err := a.RemoveToolFromGroup("ripgrep", "work"); err != nil {
		t.Fatalf("RemoveToolFromGroup: %v", err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after remove: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; !ok {
		t.Fatal("RemoveToolFromGroup deleted logical spec")
	}
	work = logicalTestGroupByName(cfg, "work")
	if work != nil && logicalTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("work group still has ripgrep after remove: %+v", work.Tools)
	}
}

func TestSync_MachineGroupIgnoreSuppressesSharedLogicalTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "testhost", Ignore: []string{"ripgrep"}},
		},
		Profiles:  map[string]config.Profile{"default": {Groups: []string{"base"}}},
		Hostnames: map[string]string{"testhost": "default"},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	ignored := result.Ignored()
	if len(ignored) != 1 || ignored[0].Tool.Name != "ripgrep" {
		t.Fatalf("Ignored() = %+v, want ripgrep suppressed by machine group", ignored)
	}
	for _, op := range result.Ops {
		if op.Tool.Name == "ripgrep" && op.Kind != gosync.OpIgnored {
			t.Fatalf("ripgrep op = %v, want only ignored", op.Kind)
		}
	}
}

func TestRemoveLogicalTool_RemovesMembershipsIgnoresAndCache(t *testing.T) {
	a, cfgPath := newImportApp(t)
	ctx := context.Background()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
			"fd":      {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Tools: []config.ToolEntry{{Name: "ripgrep"}, {Name: "fd"}}},
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}, Ignore: []string{"ripgrep"}},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:        "ripgrep",
		Provider:    "system",
		Package:     "ripgrep",
		Installed:   true,
		Version:     sql.NullString{String: "1.0.0", Valid: true},
		LastChecked: time.Now(),
		Tracked:     true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := a.RemoveLogicalTool(ctx, "ripgrep"); err != nil {
		t.Fatalf("RemoveLogicalTool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; ok {
		t.Fatal("logical spec ripgrep still present")
	}
	for _, group := range cfg.Groups {
		if logicalTestGroupHasTool(group, "ripgrep") {
			t.Fatalf("group %q still has ripgrep membership", group.BaseName())
		}
		for _, ignored := range group.Ignore {
			if ignored == "ripgrep" {
				t.Fatalf("group %q still ignores ripgrep", group.BaseName())
			}
		}
	}
	cached, err := a.DB().List(ctx)
	if err != nil {
		t.Fatalf("cache list: %v", err)
	}
	for _, tool := range cached {
		if tool.Name == "ripgrep" {
			t.Fatalf("cache still has ripgrep: %+v", tool)
		}
	}
}

func logicalTestGroupByName(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, group := range cfg.Groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

func logicalTestGroupHasTool(group *config.GroupConfig, name string) bool {
	for _, tool := range group.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}
