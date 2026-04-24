package app_test

import (
	"context"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
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

func TestRenameGroup_UpdatesProfileReferences(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("go", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("go")},
		},
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{"base", "dev"}},
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

	p, ok := updated.Profiles["work"]
	if !ok {
		t.Fatal("profile 'work' not found")
	}
	for _, g := range p.Groups {
		if g == "dev" {
			t.Error("old group name 'dev' still referenced in profile 'work'")
		}
	}
	found := false
	for _, g := range p.Groups {
		if g == "development" {
			found = true
		}
	}
	if !found {
		t.Errorf("profile 'work' groups = %v, want to include 'development'", p.Groups)
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

func TestRenameGroup_BaseGroupReturnsError(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// The base group has Name == "" internally.
	err := a.RenameGroup("", "newbase")
	if err == nil {
		t.Error("expected error when renaming base group, got nil")
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

func TestDeleteGroup_HappyPath_MovesToolsToBase(t *testing.T) {
	a, cfgPath := newImportApp(t)

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

	if err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{MoveTo: "base"}); err != nil {
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

	// Its tools must have been moved to the base group.
	tools := toolsFromConfig(updated)
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		names[t.Name] = true
	}
	if !names["ripgrep"] {
		t.Error("base group missing original tool 'ripgrep'")
	}
	if !names["go"] {
		t.Error("base group missing moved tool 'go'")
	}
	if !names["rust"] {
		t.Error("base group missing moved tool 'rust'")
	}
}

func TestDeleteGroup_RemovesFromProfileReferences(t *testing.T) {
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
		Profiles: map[string]config.Profile{
			"laptop": {Groups: []string{"base", "dev", "work"}},
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

	p, ok := updated.Profiles["laptop"]
	if !ok {
		t.Fatal("profile 'laptop' not found")
	}
	for _, g := range p.Groups {
		if g == "dev" {
			t.Error("deleted group 'dev' still referenced in profile 'laptop'")
		}
	}
	// The remaining groups should be "base" and "work".
	if len(p.Groups) != 2 {
		t.Errorf("profile groups after delete = %v, want [base work]", p.Groups)
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

func TestDeleteGroup_SharedToolOnlyLosesDeletedMembership(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
			{Name: "dev", Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.DeleteGroup(context.Background(), "dev", app.DeleteGroupOptions{}); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := updated.Tools["ripgrep"]; !ok {
		t.Fatal("shared tool spec should remain")
	}
	tools := toolsFromConfig(updated)
	if len(tools) != 1 || tools[0].Name != "ripgrep" {
		t.Fatalf("tools = %+v, want only base ripgrep membership", tools)
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

func TestDeleteGroup_BaseGroupReturnsError(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// The base group is identified by empty name "".
	err := a.DeleteGroup(context.Background(), "", app.DeleteGroupOptions{MoveTo: "base"})
	if err == nil {
		t.Error("expected error when deleting base group, got nil")
	}
}

func TestDeleteGroup_NonExistentGroupReturnsNoError(t *testing.T) {
	// DeleteGroup is idempotent for non-existent groups — the implementation
	// performs a filter that naturally produces a no-op when the name is absent.
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Tools: groupTools("ripgrep")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Should not error for a group that doesn't exist.
	if err := a.DeleteGroup(context.Background(), "nonexistent", app.DeleteGroupOptions{MoveTo: "base"}); err != nil {
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
