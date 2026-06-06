package app_test

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

func TestSetTool_UpsertsLogicalSpecWithoutMembership(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SetTool("ripgrep", "brew", "rg", ""); err != nil {
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
	wantProviders := []config.ToolInstallSpec{{Provider: "brew", Package: "rg"}}
	if !reflect.DeepEqual(spec.Providers, wantProviders) {
		t.Fatalf("providers = %+v, want %+v", spec.Providers, wantProviders)
	}
	if spec.Provider != "" || spec.Package != "" || spec.InstallWith != "" {
		t.Fatalf("legacy spec fields = provider %q package %q install_with %q, want empty", spec.Provider, spec.Package, spec.InstallWith)
	}
	if len(cfg.Groups) != 0 {
		t.Fatalf("SetTool should not add memberships, got groups %+v", cfg.Groups)
	}
}

func TestSetTool_PromotesProviderToDefault(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Providers: []config.ToolInstallSpec{{Provider: "pip"}, {Provider: "uv"}}},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := a.SetTool("black", "uv", "black", ""); err != nil {
		t.Fatalf("SetTool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Tools["black"].Providers
	want := []config.ToolInstallSpec{{Provider: "uv", Package: "black"}, {Provider: "pip"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %+v, want %+v", got, want)
	}
}

func TestSetTool_PreservesToolMetadataWhenPromotingProvider(t *testing.T) {
	a, cfgPath := newImportApp(t)
	fallback := config.FallbackSpec{
		Source:   config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
		Status:   config.FallbackStatusUnverified,
		Binary:   "gh",
		Commands: config.FallbackCommands{Check: "{{bin}} --version"},
	}

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {
				Providers:  []config.ToolInstallSpec{{Provider: "apt", Package: "gh"}, {Provider: "brew", Package: "gh"}},
				Git:        "https://github.com/cli/cli",
				Fallback:   &fallback,
				Quarantine: "7d",
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := a.SetTool("gh", "brew", "gh", ""); err != nil {
		t.Fatalf("SetTool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["gh"]
	wantProviders := []config.ToolInstallSpec{{Provider: "brew", Package: "gh"}, {Provider: "apt", Package: "gh"}}
	if !reflect.DeepEqual(spec.Providers, wantProviders) {
		t.Fatalf("providers = %+v, want %+v", spec.Providers, wantProviders)
	}
	if spec.Git != "https://github.com/cli/cli" {
		t.Fatalf("git = %q, want configured git metadata preserved", spec.Git)
	}
	if spec.Fallback == nil || !reflect.DeepEqual(*spec.Fallback, fallback) {
		t.Fatalf("fallback = %+v, want %+v", spec.Fallback, fallback)
	}
	if spec.Quarantine != "7d" {
		t.Fatalf("quarantine = %q, want preserved", spec.Quarantine)
	}
}

func TestSetTool_RejectsMissingProvider(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.SetTool("ripgrep", "", "ripgrep", "")
	if err == nil {
		t.Fatal("SetTool accepted empty provider")
	}
	if got := err.Error(); got != "provider is required" {
		t.Fatalf("error = %q", got)
	}
}

func TestToolProviderScopeChoices_PlansEcosystemChoice(t *testing.T) {
	a, _ := newImportApp(t)
	tool := &database.ToolCache{Name: "typescript", Provider: "node", InstalledWith: "bun"}

	want := []app.ToolProviderScopeChoice{
		{Kind: app.ToolProviderScopeHost, Label: "this tool on this host", Detail: "bun"},
		{Kind: app.ToolProviderScopeTool, Label: "this tool everywhere", Detail: "bun"},
		{Kind: app.ToolProviderScopeEcosystem, Label: "node manager on this host", Detail: "bun"},
	}
	if got := a.ToolProviderScopeChoices(tool); !slices.Equal(got, want) {
		t.Fatalf("ToolProviderScopeChoices = %+v, want %+v", got, want)
	}
	if got := app.DefaultToolProviderScopeChoices(tool); !slices.Equal(got, want) {
		t.Fatalf("DefaultToolProviderScopeChoices = %+v, want %+v", got, want)
	}
}

func TestSetTool_RejectsEcosystemProviderWithoutConcrete(t *testing.T) {
	a, _ := newImportApp(t)

	err := a.SetTool("ripgrep", "system", "ripgrep", "")
	if err == nil {
		t.Fatal("SetTool accepted ecosystem provider without concrete provider")
	}
	if got := err.Error(); got != `provider "system" requires a concrete provider` {
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
		Tools:  logicalToolSpecs(logicalTool("ripgrep", "brew")),
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
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
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
	if got := memberships["ripgrep\x00brew"]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("memberships = %v, want [work]", got)
	}
}

func TestSetToolGroupMembershipWithStateReturnsStateAfterAddAndRemove(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t, &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "1.0.0", "brew"),
		},
	})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work"},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	added, err := a.SetToolGroupMembershipWithState(context.Background(), "ripgrep", "work", true)
	if err != nil {
		t.Fatalf("SetToolGroupMembershipWithState add: %v", err)
	}
	toolKey := "ripgrep\x00brew"
	if got := added.State.ToolGroups[toolKey]; got != "work" {
		t.Fatalf("ToolGroups[%q] = %q, want work", toolKey, got)
	}
	if got := added.State.ToolMemberships[toolKey]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("ToolMemberships[%q] = %v, want [work]", toolKey, got)
	}
	if !slices.Contains(added.State.GroupNames, "work") {
		t.Fatalf("GroupNames = %v, want work", added.State.GroupNames)
	}
	if added.State.HostInfo == nil || !slices.Contains(added.State.HostInfo.Hosts["testhost"].Groups, "work") {
		t.Fatalf("HostInfo = %#v, want testhost assigned work", added.State.HostInfo)
	}
	found := false
	for _, tool := range added.Tools {
		if tool.Name == "ripgrep" && tool.Provider == "brew" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Tools = %#v, want ripgrep/brew", added.Tools)
	}

	removed, err := a.SetToolGroupMembershipWithState(context.Background(), "ripgrep", "work", false)
	if err != nil {
		t.Fatalf("SetToolGroupMembershipWithState remove: %v", err)
	}
	if got, ok := removed.State.ToolMemberships[toolKey]; ok {
		t.Fatalf("ToolMemberships[%q] = %v, want absent", toolKey, got)
	}
	if got, ok := removed.State.ToolGroups[toolKey]; ok {
		t.Fatalf("ToolGroups[%q] = %q, want absent", toolKey, got)
	}
}

func TestSetToolGroupsCreatesSelectedGroupAndAssignsHost(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
		},
		Hosts: map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SetToolGroups("ripgrep", []string{"work"}, []string{"unused", "work"}, "testhost"); err != nil {
		t.Fatalf("SetToolGroups: %v", err)
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
		t.Fatalf("work group missing ripgrep after SetToolGroups: %+v", cfg.Groups)
	}
	if logicalTestGroupByName(cfg, "unused") != nil {
		t.Fatalf("unused draft group was created: %+v", cfg.Groups)
	}
	if got := cfg.Hosts["testhost"]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("hosts[testhost] = %v, want [work]", got)
	}
}

func TestSetToolGroupsWithStateReturnsUpdatedToolsAndGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t, &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "1.0.0", "brew"),
		},
	})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("ripgrep")},
		},
		Hosts: map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	result, err := a.SetToolGroupsWithState(context.Background(), "ripgrep", []string{"work"}, []string{"work"}, "testhost")
	if err != nil {
		t.Fatalf("SetToolGroupsWithState: %v", err)
	}
	if result.State == nil {
		t.Fatal("State is nil")
	}
	if !slices.Contains(result.State.GroupNames, "work") {
		t.Fatalf("GroupNames = %v, want work", result.State.GroupNames)
	}
	toolKey := "ripgrep\x00brew"
	if got := result.State.ToolGroups[toolKey]; got != "work" {
		t.Fatalf("ToolGroups[%q] = %q, want work", toolKey, got)
	}
	if got := result.State.ToolMemberships[toolKey]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("ToolMemberships[%q] = %v, want [work]", toolKey, got)
	}
	if result.State.HostInfo == nil || !slices.Contains(result.State.HostInfo.Hosts["testhost"].Groups, "work") {
		t.Fatalf("HostInfo = %#v, want testhost assigned work", result.State.HostInfo)
	}
	found := false
	for _, tool := range result.Tools {
		if tool.Name == "ripgrep" && tool.Provider == "brew" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Tools = %#v, want ripgrep/brew", result.Tools)
	}
}

func TestToolGroupLabelsForHostFiltersInactiveGroupsAndCompacts(t *testing.T) {
	labels := app.ToolGroupLabelsForHost(
		map[string][]string{
			"git\x00system": {"testhost", "ops", "dev", "unused"},
			"fd\x00system":  {"unused"},
		},
		&app.HostInfo{
			Active: "testhost",
			Hosts: map[string]config.HostAssignment{
				"testhost": {Groups: []string{"dev", "ops"}},
			},
		},
		"testhost",
	)

	if got := labels["git\x00system"]; got != "dev,ops+1" {
		t.Fatalf("git label = %q, want dev,ops+1", got)
	}
	if got := labels["fd\x00system"]; got != "" {
		t.Fatalf("fd label = %q, want empty inactive label", got)
	}
}

func TestVisibleGroupNamesForHostUsesActiveHostGroups(t *testing.T) {
	got := app.VisibleGroupNamesForHost(
		[]string{"archive", "personal", "work"},
		&app.HostInfo{
			Active: "main",
			Hosts: map[string]config.HostAssignment{
				"main": {Groups: []string{"base", "work"}},
			},
		},
		"testhost",
	)
	if !slices.Equal(got, []string{"work"}) {
		t.Fatalf("visible groups = %v, want [work]", got)
	}
}

func TestVisibleGroupNamesForHostIncludesMachineGroup(t *testing.T) {
	got := app.VisibleGroupNamesForHost(
		[]string{"archive", "testhost", "work"},
		&app.HostInfo{
			Active: "main",
			Hosts: map[string]config.HostAssignment{
				"main": {Groups: []string{"base"}},
			},
		},
		"testhost.example.com",
	)
	if !slices.Equal(got, []string{"testhost"}) {
		t.Fatalf("visible groups = %v, want [testhost]", got)
	}
}

func TestCurrentMachineGroupHelpersUseCurrentHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.local")
	info := &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"work"}},
		},
	}
	groupNames := []string{"archive", "desk", "work"}

	if got := app.VisibleGroupNamesForCurrentMachine(groupNames, info); !slices.Equal(got, []string{"desk", "work"}) {
		t.Fatalf("visible current-machine groups = %v, want [desk work]", got)
	}
	if got := app.AllGroupNamesForCurrentMachine(groupNames); !slices.Equal(got, []string{"desk", "archive", "work"}) {
		t.Fatalf("all current-machine groups = %v, want [desk archive work]", got)
	}
	if got := app.GroupPickerNamesForCurrentMachine(groupNames, info, []string{"draft"}); !slices.Equal(got, []string{"desk", "work", "archive"}) {
		t.Fatalf("current-machine picker groups = %v, want active groups first", got)
	}
	if got := app.PrioritizeGroupNamesForCurrentMachine([]string{"archive", "desk", "draft"}, info, []string{"draft"}); !slices.Equal(got, []string{"desk", "draft", "archive"}) {
		t.Fatalf("current-machine prioritized groups = %v, want active and created first", got)
	}
	if !app.GroupInActiveHostForCurrentMachinePicker("desk", info, nil) {
		t.Fatal("current machine group should be in active host picker context")
	}
	if !app.IsCurrentMachineGroup("desk") {
		t.Fatal("short current hostname group should be current machine group")
	}
	if !app.HasActiveHostGroupContextForCurrentMachine(info) {
		t.Fatal("active host group context should use current machine")
	}
}

func TestSetupHostGroupDraftUsesActiveOrCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")

	active := app.SetupHostGroupDraft(
		[]string{"archive", "work"},
		&app.HostInfo{
			Active: "main",
			Hosts: map[string]config.HostAssignment{
				"main": {Groups: []string{"missing", "work"}},
			},
		},
	)
	if active["archive"] || !active["work"] {
		t.Fatalf("active draft = %#v, want only work selected", active)
	}

	fallback := app.SetupHostGroupDraft(
		[]string{"archive", "work"},
		&app.HostInfo{
			Hosts: map[string]config.HostAssignment{
				"testhost": {Groups: []string{"archive"}},
			},
		},
	)
	if !fallback["archive"] || fallback["work"] {
		t.Fatalf("fallback draft = %#v, want only archive selected", fallback)
	}
}

func TestSetupSelectedHostGroupsPreservesGroupOrder(t *testing.T) {
	got := app.SetupSelectedHostGroups(
		[]string{"archive", "work", "dev"},
		map[string]bool{"dev": true, "work": true, "missing": true},
	)
	if !slices.Equal(got, []string{"work", "dev"}) {
		t.Fatalf("selected groups = %v, want [work dev]", got)
	}
	if got := app.SetupSelectedHostGroups([]string{"work"}, nil); got != nil {
		t.Fatalf("selected groups without draft = %v, want nil", got)
	}
}

func TestDotMembershipNamesReturnsSortedNonEmptyNames(t *testing.T) {
	got := app.DotMembershipNames(map[string][]string{
		"zsh":  {"shell"},
		"":     {"ignored"},
		"nvim": {"dev"},
	})

	if !slices.Equal(got, []string{"nvim", "zsh"}) {
		t.Fatalf("dot membership names = %v, want [nvim zsh]", got)
	}
	if got := app.DotMembershipNames(nil); got != nil {
		t.Fatalf("dot membership names for nil map = %v, want nil", got)
	}
}

func TestAllGroupNamesForMachinePutsHostBeforeSortedReusableGroups(t *testing.T) {
	got := app.AllGroupNamesForMachine([]string{"work", "apps", "testhost", "personal", "apps"}, "testhost")
	want := []string{"testhost", "apps", "personal", "work"}
	if !slices.Equal(got, want) {
		t.Fatalf("all groups = %v, want %v", got, want)
	}
}

func TestHostAssignmentGroupsKeepLocalHostSeparate(t *testing.T) {
	picker := app.HostAssignmentPickerGroups("testhost", []string{"work", "apps", "testhost", "work"})
	if !slices.Equal(picker, []string{"testhost", "apps", "work"}) {
		t.Fatalf("picker groups = %v, want local host then reusable groups", picker)
	}
	draft := app.HostAssignmentDraftGroups("testhost", []string{"work", "testhost", "work", ""})
	if !slices.Equal(draft, []string{"testhost", "work"}) {
		t.Fatalf("draft groups = %v, want local host then work", draft)
	}
	editable := app.EditableHostAssignmentGroups("testhost", draft)
	if !slices.Equal(editable, []string{"work"}) {
		t.Fatalf("editable groups = %v, want only reusable groups", editable)
	}
}

func TestIsLocalHostGroupUsesMachineGroupName(t *testing.T) {
	if !app.IsLocalHostGroup("", "testhost") {
		t.Fatal("empty group should be protected")
	}
	if !app.IsLocalHostGroup("testhost", "testhost.example.com") {
		t.Fatal("short machine host group should be protected")
	}
	if app.IsLocalHostGroup("work", "testhost") {
		t.Fatal("reusable group should not be protected as local")
	}
}

func TestGroupPickerNamesForHostPrioritizesActiveGroups(t *testing.T) {
	got := app.GroupPickerNamesForHost(
		[]string{"archive", "personal", "work"},
		&app.HostInfo{
			Active: "main",
			Hosts: map[string]config.HostAssignment{
				"main": {Groups: []string{"base", "work"}},
			},
		},
		"testhost",
		nil,
	)
	if !slices.Equal(got, []string{"testhost", "work", "archive", "personal"}) {
		t.Fatalf("picker groups = %v, want active groups first", got)
	}
}

func TestActiveHostGroupSetForPickerIncludesCreatedGroupsWithHostContext(t *testing.T) {
	info := &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"work"}},
		},
	}
	if !app.GroupInActiveHostForPicker("draft", info, "testhost", []string{"draft"}) {
		t.Fatal("created group should be active while staged")
	}
	if app.GroupInActiveHostForPicker("draft", nil, "testhost", []string{"draft"}) {
		t.Fatal("created group should not create active-host context by itself")
	}
}

func TestSetGroupToolsAppliesEditorDiff(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("eslint", "npm"),
			logicalTool("ruff", "pip3"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("eslint")},
			{Name: "work", Tools: groupTools("ripgrep")},
		},
		Hosts:  map[string][]string{"testhost": {"work"}},
		Ignore: config.GlobalIgnore{Tools: []string{"ruff"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	changed, err := a.SetGroupTools(
		"work",
		map[string]bool{"ripgrep": false, "eslint": true, "ruff": false},
		map[string]bool{"ripgrep": true, "eslint": false, "ruff": false},
		map[string]bool{"ruff": true, "eslint": true},
		map[string]bool{"ruff": true, "eslint": false},
	)
	if err != nil {
		t.Fatalf("SetGroupTools: %v", err)
	}
	if changed != 3 {
		t.Fatalf("changed = %d, want 3", changed)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := logicalTestGroupByName(cfg, "work")
	if work == nil {
		t.Fatal("missing work group")
	}
	if logicalTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("ripgrep should be removed from work tools: %+v", work.Tools)
	}
	if !logicalTestGroupHasTool(work, "eslint") {
		t.Fatalf("eslint should be added to work tools: %+v", work.Tools)
	}
	host := logicalTestGroupByName(cfg, "testhost")
	if host != nil && logicalTestGroupHasTool(host, "eslint") {
		t.Fatalf("eslint should move out of host group: %+v", host.Tools)
	}
	if !slices.Contains(cfg.Ignore.Tools, "ruff") || !slices.Contains(cfg.Ignore.Tools, "eslint") {
		t.Fatalf("global ignore = %v, want ruff and eslint", cfg.Ignore.Tools)
	}
}

func TestGroupAssignmentChangedDetectsEditorDiff(t *testing.T) {
	if app.GroupAssignmentChanged(map[string]bool{"ripgrep": true}, map[string]bool{"ripgrep": true}) {
		t.Fatal("GroupAssignmentChanged unchanged = true, want false")
	}
	if !app.GroupAssignmentChanged(map[string]bool{"ripgrep": false}, map[string]bool{"ripgrep": true}) {
		t.Fatal("GroupAssignmentChanged toggled = false, want true")
	}
	if !app.GroupAssignmentChanged(map[string]bool{}, map[string]bool{"ripgrep": true}) {
		t.Fatal("GroupAssignmentChanged removed key = false, want true")
	}
	if app.GroupToolsEditorChanged(
		map[string]bool{"ripgrep": true},
		map[string]bool{"ripgrep": true},
		map[string]bool{"ruff": true},
		map[string]bool{"ruff": true},
	) {
		t.Fatal("GroupToolsEditorChanged unchanged = true, want false")
	}
	if !app.GroupToolsEditorChanged(
		map[string]bool{"ripgrep": true},
		map[string]bool{"ripgrep": true},
		map[string]bool{"ruff": false},
		map[string]bool{"ruff": true},
	) {
		t.Fatal("GroupToolsEditorChanged ignored toggle = false, want true")
	}
}

func TestGroupMembershipsChangedAndCreatedGroups(t *testing.T) {
	if app.GroupMembershipsChanged([]string{"work", "base"}, []string{"base", "work"}) {
		t.Fatal("GroupMembershipsChanged reordered = true, want false")
	}
	if !app.GroupMembershipsChanged([]string{"work"}, []string{"base", "work"}) {
		t.Fatal("GroupMembershipsChanged removed = false, want true")
	}

	got := app.CreatedMembershipGroups([]string{"draft", "unused", "apps"}, []string{"work", "draft", "apps"})
	want := []string{"apps", "draft"}
	if !slices.Equal(got, want) {
		t.Fatalf("CreatedMembershipGroups = %v, want %v", got, want)
	}
}

func TestSetGroupToolsWithStateReturnsDisplayState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	a, cfgPath := newImportApp(t, &stubProvider{
		name:      "brew",
		available: true,
		installed: []provider.InstalledTool{
			installedTool("ripgrep", "1.0.0", "brew"),
			installedTool("fd", "1.0.0", "brew"),
			installedTool("eslint", "2.0.0", "brew"),
			installedTool("ruff", "3.0.0", "brew"),
		},
	})
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("fd", "brew"),
			logicalTool("eslint", "brew"),
			logicalTool("ruff", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("eslint")},
			{Name: "work", Tools: groupTools("ripgrep", "fd")},
		},
		Hosts:  map[string][]string{"testhost": {"work"}},
		Ignore: config.GlobalIgnore{Tools: []string{"ruff"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	if err := a.RefreshInstalled(context.Background(), nil); err != nil {
		t.Fatalf("RefreshInstalled: %v", err)
	}

	result, err := a.SetGroupToolsWithState(
		context.Background(),
		"work",
		map[string]bool{"ripgrep": false, "fd": true, "ruff": false},
		map[string]bool{"ripgrep": true, "fd": true, "ruff": false},
		map[string]bool{"ruff": true, "eslint": true},
		map[string]bool{"ruff": true, "eslint": false},
	)
	if err != nil {
		t.Fatalf("SetGroupToolsWithState: %v", err)
	}
	if result.Changed != 2 {
		t.Fatalf("Changed = %d, want 2", result.Changed)
	}
	toolKey := "fd\x00brew"
	if result.State == nil {
		t.Fatal("State is nil")
	}
	if result.State.ToolGroups[toolKey] != "work" {
		t.Fatalf("State.ToolGroups[%q] = %q, want work; groups=%v memberships=%v", toolKey, result.State.ToolGroups[toolKey], result.State.ToolGroups, result.State.ToolMemberships)
	}
	if result.ScopeDisplay == nil {
		t.Fatal("ScopeDisplay is nil")
	}
	if result.ScopeDisplay.IgnoreLabels["eslint"] != "global" || result.ScopeDisplay.IgnoreLabels["ruff"] != "global" {
		t.Fatalf("ScopeDisplay.IgnoreLabels = %v, want eslint and ruff global", result.ScopeDisplay.IgnoreLabels)
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	if slices.Contains(names, "eslint") || slices.Contains(names, "ruff") {
		t.Fatalf("Tools = %v, want ignored tools absent", names)
	}
	if !slices.Contains(names, "fd") {
		t.Fatalf("Tools = %v, want fd present", names)
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
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew"), logicalTool("fd", "brew")),
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
		Provider:    "brew",
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
		Provider:    "brew",
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
	if _, err := a.DB().Get(ctx, "fd", "brew", "fd"); err != nil {
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
