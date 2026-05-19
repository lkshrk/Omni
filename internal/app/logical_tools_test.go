package app_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
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

func TestMoveToolToGroup_RequiresLogicalSpec(t *testing.T) {
	a, _ := newImportApp(t)

	if err := a.MoveToolToGroup("ripgrep", "base"); err == nil {
		t.Fatal("MoveToolToGroup without logical spec returned nil")
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

	if err := a.MoveToolToGroup("ripgrep", "work"); err != nil {
		t.Fatalf("MoveToolToGroup: %v", err)
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

func TestMoveToolToGroupMovesSingleOwnerMembership(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			{Name: "work"},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.MoveToolToGroup("ripgrep", "work"); err != nil {
		t.Fatalf("MoveToolToGroup: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	host := logicalTestGroupByName(cfg, "testhost")
	if host != nil && logicalTestGroupHasTool(host, "ripgrep") {
		t.Fatalf("ripgrep should move out of host group, host tools=%+v", host.Tools)
	}
	work := logicalTestGroupByName(cfg, "work")
	if work == nil || !logicalTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("ripgrep should move into work group, groups=%+v", cfg.Groups)
	}
	memberships, err := a.ToolMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("ToolMembershipMap: %v", err)
	}
	if got := memberships["ripgrep\x00system"]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("memberships = %v, want [work]", got)
	}
}

func TestSync_MachineGroupIgnoreSuppressesSharedLogicalTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	brew := &installTracker{stubProvider: stubProvider{name: "brew", available: true}}
	a, cfgPath := newImportApp(t, brew)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
		},
		Hosts:  map[string][]string{"testhost": {}},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	result, err := a.Sync(context.Background(), gosync.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(result.Ops) != 0 {
		t.Fatalf("Sync ops = %+v, want globally ignored logical tool excluded", result.Ops)
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
			{Name: "tools", Tools: []config.ToolEntry{{Name: "ripgrep"}, {Name: "fd"}}},
			{Name: "work"},
		},
		Ignore: config.GlobalIgnore{Tools: []string{"ripgrep"}},
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
	if err := a.DB().Upsert(ctx, &database.ToolCache{
		Name:        "fd",
		Provider:    "system",
		Package:     "fd",
		Installed:   true,
		Version:     sql.NullString{String: "9.0.0", Valid: true},
		LastChecked: time.Now(),
		Tracked:     true,
	}); err != nil {
		t.Fatalf("seed fd cache: %v", err)
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
	}
	for _, ignored := range cfg.Ignore.Tools {
		if ignored == "ripgrep" {
			t.Fatalf("global ignore still contains ripgrep")
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
	if _, err := a.DB().Get(ctx, "fd", "system", "fd"); err != nil {
		t.Fatalf("unrelated fd cache row was removed: %v", err)
	}
}

func TestRemoveLogicalTool_RejectsProviderTool(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "pnpm", Provider: "node", InstallWith: "pnpm"}),
		Groups: []*config.GroupConfig{{
			Name:  "tools",
			Tools: groupTools("pnpm"),
		}},
		Ignore: config.GlobalIgnore{Tools: []string{"pnpm"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	err := a.RemoveLogicalTool(context.Background(), "pnpm")
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("RemoveLogicalTool err = %v, want protected provider tool error", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["pnpm"]; !ok {
		t.Fatal("pnpm logical spec was removed despite protected provider guard")
	}
	if group := logicalTestGroupByName(cfg, "tools"); group == nil || !logicalTestGroupHasTool(group, "pnpm") {
		t.Fatalf("pnpm membership was removed despite protected provider guard: %+v", cfg.Groups)
	}
	if !slices.Contains(cfg.Ignore.Tools, "pnpm") {
		t.Fatalf("pnpm ignore entry was removed despite protected provider guard: %+v", cfg.Ignore.Tools)
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
