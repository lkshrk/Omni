package app_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestQueryTools_FiltersByNameStateAndGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew"), logicalTool("bat", "brew")),
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: groupTools("ripgrep", "bat")}},
		Hosts:  map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep", InstalledWith: "brew", Installed: true}); err != nil {
		t.Fatalf("upsert ripgrep: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "14.0.0"); err != nil {
		t.Fatalf("outdated ripgrep: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "bat", Provider: "brew", Package: "bat", InstalledWith: "brew", Installed: false}); err != nil {
		t.Fatalf("upsert bat: %v", err)
	}

	items, err := a.QueryTools(ctx, app.ToolListOptions{Name: "ripgrep", State: "updates", Group: "testhost"})
	if err != nil {
		t.Fatalf("QueryTools: %v", err)
	}
	if len(items) != 1 || items[0].Tool.Name != "ripgrep" || items[0].State != app.ToolStateOutdated {
		t.Fatalf("items = %#v, want ripgrep outdated", items)
	}
}

func TestQueryTools_HostIncludesMachineGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("slack", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: groupTools("slack")},
			{Name: "testhost", Special: "host", Tools: groupTools("fd")},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	ctx := context.Background()
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "slack", Provider: "brew", Package: "slack", InstalledWith: "brew", Installed: true, Tracked: true}); err != nil {
		t.Fatalf("upsert slack: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "fd", Provider: "brew", Package: "fd", InstalledWith: "brew", Installed: true, Tracked: true}); err != nil {
		t.Fatalf("upsert fd: %v", err)
	}

	items, err := a.QueryTools(ctx, app.ToolListOptions{Host: "testhost"})
	if err != nil {
		t.Fatalf("QueryTools: %v", err)
	}
	names := make(map[string]string)
	for _, item := range items {
		names[item.Tool.Name] = item.Group
	}
	if names["slack"] != "work" || names["fd"] != "testhost" {
		t.Fatalf("items by group = %v, want slack/work and fd/testhost", names)
	}
}

func TestQueryDots_FiltersByNameAndState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", t.TempDir())
	a, cfgPath := newImportApp(t)
	repoDir := t.TempDir()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{Dots: []config.DotEntry{
			{Name: "nvim", Path: "~/.config/nvim"},
			{Name: "zsh", Path: "~/.zshrc"},
		}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	statuses, err := a.QueryDots(app.DotsQueryOptions{Name: "zsh", State: "source-missing"})
	if err != nil {
		t.Fatalf("QueryDots: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Name != "zsh" || statuses[0].Health != app.HealthNoSource {
		t.Fatalf("statuses = %#v, want zsh no-source", statuses)
	}
}

func TestQuerySettings_FiltersSingleKey(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: filepath.Join(t.TempDir(), "dots")},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	values, err := a.QuerySettings("dots-repo")
	if err != nil {
		t.Fatalf("QuerySettings: %v", err)
	}
	if len(values) != 1 || values["dots_repo"] == "" {
		t.Fatalf("values = %#v, want dots_repo only", values)
	}
}

func TestQuerySettings_IncludesGlobalSettingsParityKeys(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{
			AutoImport: true,
			DotsGit: config.DotsGitConfig{
				AutoCommit: true,
				AutoPush:   true,
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	values, err := a.QuerySettings("")
	if err != nil {
		t.Fatalf("QuerySettings: %v", err)
	}
	for _, key := range []string{"auto_import", "dots_git.auto_commit", "dots_git.auto_push"} {
		if values[key] != true {
			t.Fatalf("values[%q] = %#v, want true", key, values[key])
		}
	}
}
