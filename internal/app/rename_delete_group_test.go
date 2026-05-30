package app_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── RenameGroup ──────────────────────────────────────────────────────────────

func TestRenameGroup_HappyPath(t *testing.T) {
	a, cfgPath := newImportApp(t)

	// Seed config with a "dev" group containing two tools.
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
			logicalTool("rust", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go", "rust")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.RenameGroup("dev", "development"); err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Old name must be gone, new name must exist.
	var newGroup *config.GroupConfig
	for _, g := range updated.Groups {
		if g.Name == "dev" {
			t.Error("old group name 'dev' still present after rename")
		}
		if g.Name == "development" {
			newGroup = g
		}
	}
	if newGroup == nil {
		t.Fatal("renamed group 'development' not found in config")
	}
	// Tools must be preserved.
	if len(newGroup.Tools) != 2 {
		t.Errorf("renamed group tools = %d, want 2", len(newGroup.Tools))
	}
}

func TestRenameGroup_UpdatesHostReferences(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go")},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.RenameGroup("dev", "development"); err != nil {
		t.Fatalf("RenameGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	groups, ok := updated.Hosts["testhost"]
	if !ok {
		t.Fatal("host 'testhost' not found")
	}
	for _, g := range groups {
		if g == "dev" {
			t.Error("old group name 'dev' still referenced by host 'testhost'")
		}
	}
	found := false
	for _, g := range groups {
		if g == "development" {
			found = true
		}
	}
	if !found {
		t.Errorf("host groups = %v, want to include 'development'", groups)
	}
}

func TestRenameGroupWithStateReturnsUpdatedGroupState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t, &stubProvider{name: "brew", available: true})

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go")},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
	}); err != nil {
		t.Fatal(err)
	}

	state, err := a.RenameGroupWithState(context.Background(), "dev", "development")
	if err != nil {
		t.Fatalf("RenameGroupWithState: %v", err)
	}
	if slices.Contains(state.GroupNames, "dev") || !slices.Contains(state.GroupNames, "development") {
		t.Fatalf("GroupNames = %v, want development and no dev", state.GroupNames)
	}
	toolKey := "go\x00system"
	if got := state.ToolGroups[toolKey]; got != "development" {
		t.Fatalf("ToolGroups[%q] = %q, want development", toolKey, got)
	}
	if got := state.ToolMemberships[toolKey]; !slices.Equal(got, []string{"development"}) {
		t.Fatalf("ToolMemberships[%q] = %v, want [development]", toolKey, got)
	}
	if state.HostInfo == nil || state.HostInfo.Active != "testhost" {
		t.Fatalf("HostInfo = %#v, want active testhost", state.HostInfo)
	}
	if got := state.HostInfo.Hosts["testhost"].Groups; !slices.Equal(got, []string{"development"}) {
		t.Fatalf("host groups = %v, want [development]", got)
	}
}

func TestRenameGroup_DuplicateNameReturnsError(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go")},
			{Name: "work", Tools: groupTools("slack")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	err := a.RenameGroup("dev", "work")
	if err == nil {
		t.Error("expected error when renaming to an existing group name, got nil")
	}
}

func TestRenameGroup_EmptyNameReturnsError(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	err := a.RenameGroup("", "newbase")
	if err == nil {
		t.Error("expected error when renaming empty group name, got nil")
	}
}

func TestRenameGroup_NotFoundReturnsError(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	err := a.RenameGroup("nonexistent", "something")
	if err == nil {
		t.Error("expected error for non-existent group, got nil")
	}
}

// ─── DeleteGroup ──────────────────────────────────────────────────────────────

func TestDeleteGroup_HappyPath_MovesToolsToHost(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
			logicalTool("rust", "brew"),
		),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
			{Name: "dev", Tools: groupTools("go", "rust")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	host := testShortHostname()
	if err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{MoveTo: host}); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// The "dev" group must be gone.
	for _, g := range updated.Groups {
		if g.Name == "dev" {
			t.Error("deleted group 'dev' still present in config")
		}
	}

	// Its tools must have been moved to the host group.
	hostGroup := findTestGroup(updated, host)
	tools := materializeTestTools(updated, hostGroup.Tools)
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		names[t.Name] = true
	}
	if !names["ripgrep"] {
		t.Error("host group missing original tool 'ripgrep'")
	}
	if !names["go"] {
		t.Error("host group missing moved tool 'go'")
	}
	if !names["rust"] {
		t.Error("host group missing moved tool 'rust'")
	}
}

func TestDeleteGroupWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t, &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("go", "1.0.0", "brew"),
			installedTool("ripgrep", "1.0.0", "brew"),
		},
	})

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("go", "brew"),
			logicalTool("ripgrep", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go")},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	result, err := a.DeleteGroupWithState(context.Background(), "dev", app.DeleteGroupOptions{MoveTo: "testhost"})
	if err != nil {
		t.Fatalf("DeleteGroupWithState: %v", err)
	}
	if result.State == nil {
		t.Fatal("State is nil")
	}
	if slices.Contains(result.State.GroupNames, "dev") {
		t.Fatalf("GroupNames = %v, want no dev", result.State.GroupNames)
	}
	toolKey := "go\x00system"
	if got := result.State.ToolMemberships[toolKey]; !slices.Equal(got, []string{"testhost"}) {
		t.Fatalf("ToolMemberships[%q] = %v, want [testhost]", toolKey, got)
	}
	foundGo := false
	for _, tool := range result.Tools {
		if tool.Name == "go" && tool.Provider == "system" {
			foundGo = true
			break
		}
	}
	if !foundGo {
		t.Fatalf("Tools = %#v, want logical go tool", result.Tools)
	}
}

func TestDeleteGroupWithDefaultMoveTargetUsesCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("go", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "dev", Tools: groupTools("go")},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := a.DeleteGroupWithDefaultMoveTarget(context.Background(), "dev", false)
	if err != nil {
		t.Fatalf("DeleteGroupWithDefaultMoveTarget: %v", err)
	}
	if got := result.State.ToolMemberships["go\x00system"]; !slices.Equal(got, []string{"testhost"}) {
		t.Fatalf("ToolMemberships go = %v, want [testhost]", got)
	}
}

func TestDeleteGroup_RemovesFromHostReferences(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
			logicalTool("slack", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go")},
			{Name: "work", Tools: groupTools("slack")},
		},
		Hosts: map[string][]string{"testhost": {"dev", "work"}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{DeleteTools: true}); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	groups, ok := updated.Hosts["testhost"]
	if !ok {
		t.Fatal("host 'testhost' not found")
	}
	for _, g := range groups {
		if g == "dev" {
			t.Error("deleted group 'dev' still referenced by host 'testhost'")
		}
	}
	if !slices.Equal(groups, []string{"work"}) {
		t.Errorf("host groups after delete = %v, want [work]", groups)
	}
}

func TestDeleteGroup_RequiresHandlingForLastMembershipTools(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("go", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "dev", Tools: groupTools("go")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{})
	if err == nil {
		t.Fatal("DeleteGroup without MoveTo/DeleteTools returned nil")
	}
}

func TestDeleteGroup_MoveTargetMustDiffer(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{MoveTo: "dev"})
	if err == nil {
		t.Fatal("DeleteGroup with matching MoveTo returned nil")
	}
}

func TestDeleteGroup_MoveToHostPreservesToolSpec(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: testShortHostname(), Special: "host"},
			{Name: "dev", Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{MoveTo: testShortHostname()}); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["ripgrep"]; !ok {
		t.Fatal("moved tool spec should remain")
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Fatalf("tools = %+v, want only host ripgrep membership", tools)
	}
}

func TestDeleteGroup_DeleteToolsRemovesLastMembershipSpecs(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("go", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "dev", Tools: groupTools("go")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{DeleteTools: true}); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["go"]; ok {
		t.Fatal("last-membership logical tool spec still present")
	}
}

func TestDeleteGroup_DeleteToolsRejectsProviderTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalFixtureTool{Name: "npm", Provider: "node", InstallWith: "npm"}),
		Groups: []*config.GroupConfig{
			testHostGroup(),
			{Name: "dev", Tools: groupTools("npm")},
		},
		Hosts: map[string][]string{"testhost": {"dev"}},
	}); err != nil {
		t.Fatal(err)
	}

	err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{DeleteTools: true})
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("DeleteGroup err = %v, want protected provider tool error", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["npm"]; !ok {
		t.Fatal("npm logical spec was removed despite protected provider guard")
	}
	if group := findTestGroup(updated, "dev"); group == nil || !testGroupHasTool(group, "npm") {
		t.Fatalf("dev group was removed or lost npm despite protected provider guard: %+v", updated.Groups)
	}
	if !slices.Contains(updated.Hosts["testhost"], "dev") {
		t.Fatalf("host reference to dev was removed despite protected provider guard: %+v", updated.Hosts["testhost"])
	}
}

func TestDeleteGroup_EmptyNameReturnsError(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	err := a.DeleteGroup(context.Background(), "", app.DeleteGroupOptions{MoveTo: testShortHostname()})
	if err == nil {
		t.Error("expected error when deleting empty group name, got nil")
	}
}

func TestDeleteGroup_NonExistentGroupReturnsNoError(t *testing.T) {
	// DeleteGroup is idempotent for non-existent groups — the implementation
	// performs a filter that naturally produces a no-op when the name is absent.
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			testHostToolGroup("ripgrep"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Should not error for a group that doesn't exist.
	if err := a.DeleteGroup(context.Background(), "nonexistent", app.DeleteGroupOptions{MoveTo: testShortHostname()}); err != nil {
		t.Errorf("DeleteGroup on non-existent group: %v", err)
	}

	// Config should be unchanged.
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Errorf("config tools = %v after no-op delete, want [ripgrep]", tools)
	}
}
