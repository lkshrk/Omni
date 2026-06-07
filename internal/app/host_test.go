package app_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func TestCurrentMachineGroupNameUsesOmniHostname(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "trims domain and space", env: "  workstation.local  ", want: "workstation"},
		{name: "keeps plain hostname", env: "mymachine", want: "mymachine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OMNI_HOSTNAME", tt.env)

			if got := app.CurrentMachineGroupName(); got != tt.want {
				t.Fatalf("CurrentMachineGroupName = %q, want %s", got, tt.want)
			}
		})
	}
}

func TestEnsureHostCreatesSpecialGroupAndHostEntry(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.EnsureHost("testhost.example"); err != nil {
		t.Fatalf("EnsureHost: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findHostTestGroup(cfg.Groups, "testhost")
	if group == nil || !group.IsHost() {
		t.Fatalf("host group = %#v, want special host group", group)
	}
	if groups, ok := cfg.Hosts["testhost"]; !ok || len(groups) != 0 {
		t.Fatalf("hosts[testhost] = %v, ok=%v, want empty assignment", groups, ok)
	}
}

func TestEnsureHostRejectsReusableGroupNameCollision(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "laptop"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.EnsureHost("laptop"); err == nil {
		t.Fatal("EnsureHost converted reusable group into host group")
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findHostTestGroup(cfg.Groups, "laptop")
	if group == nil {
		t.Fatal("missing reusable group")
	}
	if group.IsHost() {
		t.Fatalf("group %q was marked as host", group.Name)
	}
}

func TestInitRepairsLegacyCurrentHostGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "Topaz.local")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("git", "brew")),
		Groups: []*config.GroupConfig{{Name: "Topaz", Tools: groupTools("git")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findHostTestGroup(cfg.Groups, "Topaz")
	if group == nil || !group.IsHost() {
		t.Fatalf("Topaz group = %#v, want repaired special host group", group)
	}
	if groups, ok := cfg.Hosts["Topaz"]; !ok || len(groups) != 0 {
		t.Fatalf("hosts[Topaz] = %v, ok=%v, want empty assignment", groups, ok)
	}
	info, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if info.Active != "Topaz" {
		t.Fatalf("active host = %q, want Topaz", info.Active)
	}
}

func TestHostSummariesReturnSortedDisplayFields(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "beta.local")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "beta", Special: "host"},
			{Name: "zeta", Special: "host"},
			{Name: "base"},
			{Name: "tools"},
			{Name: "work"},
		},
		Hosts: map[string][]string{
			"zeta": {"work", "base"},
			"beta": {"tools", "base"},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	summaries, err := a.HostSummaries()
	if err != nil {
		t.Fatalf("HostSummaries: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}
	if summaries[0].Name != "beta" || !summaries[0].Active || !slices.Equal(summaries[0].Groups, []string{"base", "tools"}) {
		t.Fatalf("summaries[0] = %+v, want active beta with [base tools]", summaries[0])
	}
	if summaries[1].Name != "zeta" || summaries[1].Active || !slices.Equal(summaries[1].Groups, []string{"base", "work"}) {
		t.Fatalf("summaries[1] = %+v, want inactive zeta with [base work]", summaries[1])
	}
}

func TestPrioritizedHostSummariesMoveActiveHostFirst(t *testing.T) {
	info := &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"alpha": {Groups: []string{"zeta", "base"}},
			"work":  {Groups: []string{"dev", "base"}},
			"zeta":  {Groups: []string{"ops"}},
		},
	}

	summaries := app.PrioritizedHostSummaries(info)

	wantNames := []string{"work", "alpha", "zeta"}
	if len(summaries) != len(wantNames) {
		t.Fatalf("got %d summaries, want %d", len(summaries), len(wantNames))
	}
	for i, want := range wantNames {
		if summaries[i].Name != want {
			t.Fatalf("summaries[%d].Name = %q, want %q", i, summaries[i].Name, want)
		}
	}
	if !summaries[0].Active {
		t.Fatalf("summaries[0] = %+v, want active host first", summaries[0])
	}
	if !slices.Equal(summaries[0].Groups, []string{"base", "dev"}) {
		t.Fatalf("summaries[0].Groups = %v, want [base dev]", summaries[0].Groups)
	}
}

func TestSetupCopyHostNamesUsesPrioritizedHosts(t *testing.T) {
	info := &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"alpha": {Groups: []string{"base"}},
			"work":  {Groups: []string{"dev"}},
			"zeta":  {Groups: []string{"ops"}},
		},
	}

	got := app.SetupCopyHostNames(info)
	want := []string{"work", "alpha", "zeta"}
	if !slices.Equal(got, want) {
		t.Fatalf("SetupCopyHostNames = %v, want %v", got, want)
	}
	if got := app.SetupCopyHostNames(nil); len(got) != 0 {
		t.Fatalf("SetupCopyHostNames(nil) = %v, want empty", got)
	}
}

func TestSetupHostAlreadyConfiguredRequiresActiveExistingHost(t *testing.T) {
	info := &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"other": {Groups: []string{"base"}},
			"work":  {Groups: []string{"dev"}},
		},
	}

	if !app.SetupHostAlreadyConfigured(info, "work") {
		t.Fatal("SetupHostAlreadyConfigured(active host) = false, want true")
	}
	if app.SetupHostAlreadyConfigured(info, "other") {
		t.Fatal("SetupHostAlreadyConfigured(existing inactive host) = true, want false")
	}
	if app.SetupHostAlreadyConfigured(info, "missing") {
		t.Fatal("SetupHostAlreadyConfigured(missing host) = true, want false")
	}
	if app.SetupHostAlreadyConfigured(nil, "work") {
		t.Fatal("SetupHostAlreadyConfigured(nil) = true, want false")
	}
}

func TestHostNamesForGroupReturnsSortedHosts(t *testing.T) {
	info := &app.HostInfo{
		Hosts: map[string]config.HostAssignment{
			"zeta":  {Groups: []string{"work", "base"}},
			"alpha": {Groups: []string{"base"}},
			"beta":  {Groups: []string{"work"}},
		},
	}

	hosts := app.HostNamesForGroup(info, "work")

	if !slices.Equal(hosts, []string{"beta", "zeta"}) {
		t.Fatalf("HostNamesForGroup = %v, want [beta zeta]", hosts)
	}
}

func TestHasHostAssignmentsReportsConfiguredHosts(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{}); err != nil {
		t.Fatalf("config.Save empty: %v", err)
	}
	hasHosts, err := a.HasHostAssignments()
	if err != nil {
		t.Fatalf("HasHostAssignments empty: %v", err)
	}
	if hasHosts {
		t.Fatal("HasHostAssignments empty config = true, want false")
	}

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "laptop", Special: "host"}},
		Hosts:  map[string][]string{"laptop": {}},
	}); err != nil {
		t.Fatalf("config.Save host: %v", err)
	}
	hasHosts, err = a.HasHostAssignments()
	if err != nil {
		t.Fatalf("HasHostAssignments host: %v", err)
	}
	if !hasHosts {
		t.Fatal("HasHostAssignments configured host = false, want true")
	}
}

func TestGroupContentCountsReturnsCountsAndErrors(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("ripgrep", "brew"), logicalTool("fd", "brew")),
		Groups: []*config.GroupConfig{
			{
				Name:  "work",
				Tools: groupTools("ripgrep", "fd"),
				Dots: []config.DotEntry{
					{Name: "nvim", Path: "~/.config/nvim"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	tools, dots, err := a.GroupContentCounts(" work ")
	if err != nil {
		t.Fatalf("GroupContentCounts: %v", err)
	}
	if tools != 2 || dots != 1 {
		t.Fatalf("counts = %d tools, %d dots; want 2 tools, 1 dot", tools, dots)
	}

	if _, _, err := a.GroupContentCounts(" "); err == nil || err.Error() != "group name cannot be empty" {
		t.Fatalf("empty group error = %v, want group name cannot be empty", err)
	}
	if _, _, err := a.GroupContentCounts("missing"); err == nil || err.Error() != `group "missing" not found` {
		t.Fatalf("missing group error = %v, want missing group error", err)
	}
}

func TestHostStateWrappersPropagateConfigLoadErrors(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	a := app.New(cfgPath)

	if _, err := a.HostStatus(); err == nil {
		t.Fatal("HostStatus error = nil, want config load error")
	}
	if _, err := a.HasHostAssignments(); err == nil {
		t.Fatal("HasHostAssignments error = nil, want config load error")
	}
	if _, err := a.HostSummaries(); err == nil {
		t.Fatal("HostSummaries error = nil, want config load error")
	}
}

func TestSetHostGroupsPersistsReusableGroups(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := a.CreateGroup("work"); err != nil {
		t.Fatalf("CreateGroup(work): %v", err)
	}
	if err := a.CreateGroup("dev"); err != nil {
		t.Fatalf("CreateGroup(dev): %v", err)
	}
	if err := a.CreateGroup("base"); err != nil {
		t.Fatalf("CreateGroup(base): %v", err)
	}

	if err := a.SetHostGroups("testhost", []string{"work", "dev", "base", "work"}); err != nil {
		t.Fatalf("SetHostGroups: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Hosts["testhost"]; !slices.Equal(got, []string{"base", "dev", "work"}) {
		t.Fatalf("hosts[testhost] = %v, want [base dev work]", got)
	}
}

func TestSetHostGroupsWithCreatedCreatesSelectedDraftGroups(t *testing.T) {
	a, cfgPath := newImportApp(t)

	if err := a.SetHostGroupsWithCreated("testhost", []string{"work"}, []string{"unused", "work"}); err != nil {
		t.Fatalf("SetHostGroupsWithCreated: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Hosts["testhost"]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("hosts[testhost] = %v, want [work]", got)
	}
	if group := findHostTestGroup(cfg.Groups, "work"); group == nil || group.IsHost() {
		t.Fatalf("work group = %#v, want reusable group", group)
	}
	if group := findHostTestGroup(cfg.Groups, "unused"); group != nil {
		t.Fatalf("unused draft group was created: %#v", group)
	}
}

func TestHostGroupMutationsWithStateReturnUpdatedState(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "current.local")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "current", Special: "host"},
			{Name: "work"},
		},
		Hosts: map[string][]string{"current": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	created, err := a.CreateGroupWithState(context.Background(), "newgroup")
	if err != nil {
		t.Fatalf("CreateGroupWithState: %v", err)
	}
	if !slices.Contains(created.GroupNames, "newgroup") {
		t.Fatalf("GroupNames = %v, want newgroup", created.GroupNames)
	}
	if got := created.HostInfo.Hosts["current"].Groups; !slices.Contains(got, "newgroup") {
		t.Fatalf("current host groups = %v, want newgroup", got)
	}

	added, err := a.AddGroupToHostWithState(context.Background(), "other", "newgroup")
	if err != nil {
		t.Fatalf("AddGroupToHostWithState: %v", err)
	}
	if got := added.HostInfo.Hosts["other"].Groups; !slices.Contains(got, "newgroup") {
		t.Fatalf("other host groups = %v, want newgroup", got)
	}

	removed, err := a.RemoveGroupFromHostWithState(context.Background(), "other", "newgroup")
	if err != nil {
		t.Fatalf("RemoveGroupFromHostWithState: %v", err)
	}
	if got := removed.HostInfo.Hosts["other"].Groups; slices.Contains(got, "newgroup") {
		t.Fatalf("other host groups = %v, want newgroup removed", got)
	}

	set, err := a.SetHostGroupsWithState(context.Background(), "current", []string{"fresh"}, []string{"fresh"})
	if err != nil {
		t.Fatalf("SetHostGroupsWithState: %v", err)
	}
	if !slices.Contains(set.GroupNames, "fresh") {
		t.Fatalf("GroupNames = %v, want fresh", set.GroupNames)
	}
	if got := set.HostInfo.Hosts["current"].Groups; !slices.Equal(got, []string{"fresh"}) {
		t.Fatalf("current host groups = %v, want [fresh]", got)
	}

	renamed, err := a.RenameHostWithState(context.Background(), "other", "renamed")
	if err != nil {
		t.Fatalf("RenameHostWithState: %v", err)
	}
	if _, ok := renamed.HostInfo.Hosts["other"]; ok {
		t.Fatalf("HostInfo still contains other: %#v", renamed.HostInfo.Hosts)
	}
	if _, ok := renamed.HostInfo.Hosts["renamed"]; !ok {
		t.Fatalf("HostInfo = %#v, want renamed host", renamed.HostInfo.Hosts)
	}

	deleted, err := a.RemoveHostWithState(context.Background(), "renamed")
	if err != nil {
		t.Fatalf("RemoveHostWithState: %v", err)
	}
	if _, ok := deleted.HostInfo.Hosts["renamed"]; ok {
		t.Fatalf("HostInfo still contains renamed: %#v", deleted.HostInfo.Hosts)
	}
}

func TestSetHostGroupsRejectsHostGroupAssignment(t *testing.T) {
	a, _ := newImportApp(t)
	if err := a.EnsureHost("testhost"); err != nil {
		t.Fatalf("EnsureHost: %v", err)
	}

	err := a.SetHostGroups("otherhost", []string{"testhost"})
	if err == nil {
		t.Fatal("SetHostGroups accepted assigning another host group")
	}
}

func TestActiveHostInfoAlwaysIncludesProtectedHostGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	host, groups, ok := a.ActiveHostInfo()
	if !ok {
		t.Fatal("ActiveHostInfo did not find configured host")
	}
	if host != "testhost" {
		t.Fatalf("host = %q, want testhost", host)
	}
	if !slices.Equal(groups, []string{"testhost", "work"}) {
		t.Fatalf("groups = %v, want protected host group first plus reusable work", groups)
	}

	if err := a.RemoveGroupFromHost("testhost", "testhost"); err != nil {
		t.Fatalf("RemoveGroupFromHost(host group): %v", err)
	}
	if err := a.RemoveGroupFromHost("testhost", "work"); err != nil {
		t.Fatalf("RemoveGroupFromHost(work): %v", err)
	}

	_, groups, ok = a.ActiveHostInfo()
	if !ok {
		t.Fatal("ActiveHostInfo lost host after removing reusable groups")
	}
	if !slices.Equal(groups, []string{"testhost"}) {
		t.Fatalf("groups = %v, want protected host group only", groups)
	}
	info, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if got := info.Hosts["testhost"].Groups; len(got) != 0 {
		t.Fatalf("persisted reusable groups = %v, want none", got)
	}
}

func TestRenameHostMovesSpecialHostGroup(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalToolPackage("fd", "brew", "fd-find")),
		Groups: []*config.GroupConfig{
			{
				Name:    "oldhost",
				Special: "host",
				Tools:   groupTools("fd"),
				Dots: []config.DotEntry{{
					Name: "nvim",
					Path: "~/.config/nvim",
					Hosts: map[string]config.DotVariant{
						"oldhost": {Package: "nvim-oldhost"},
						"other":   {Package: "nvim-other"},
					},
				}},
			},
		},
		Hosts: map[string][]string{"oldhost": {}},
		HostSettings: map[string]config.Settings{
			"oldhost": {DotsRepo: "~/old-dotfiles", DotsDisabled: config.BoolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RenameHost("oldhost", "newhost"); err != nil {
		t.Fatalf("RenameHost: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Hosts["oldhost"]; ok {
		t.Fatal("old host assignment remained")
	}
	if groups, ok := cfg.Hosts["newhost"]; !ok || len(groups) != 0 {
		t.Fatalf("hosts[newhost] = %v, ok=%v, want empty assignment", groups, ok)
	}
	if oldGroup := findHostTestGroup(cfg.Groups, "oldhost"); oldGroup != nil {
		t.Fatalf("old host group remained: %#v", oldGroup)
	}
	newGroup := findHostTestGroup(cfg.Groups, "newhost")
	if newGroup == nil || !newGroup.IsHost() {
		t.Fatalf("new host group = %#v, want special host group", newGroup)
	}
	if !containsToolMembershipForTest(newGroup.Tools, "fd") {
		t.Fatal("renamed host group lost tool membership")
	}
	if len(newGroup.Dots) != 1 || newGroup.Dots[0].Name != "nvim" {
		t.Fatalf("renamed host group dots = %#v, want nvim", newGroup.Dots)
	}
	if _, ok := newGroup.Dots[0].Hosts["oldhost"]; ok {
		t.Fatal("old dot host variant remained")
	}
	if got := newGroup.Dots[0].Hosts["newhost"].Package; got != "nvim-oldhost" {
		t.Fatalf("nvim newhost variant package = %q, want nvim-oldhost", got)
	}
	if got := newGroup.Dots[0].Hosts["other"].Package; got != "nvim-other" {
		t.Fatalf("nvim other variant package = %q, want nvim-other", got)
	}
	if _, ok := cfg.HostSettings["oldhost"]; ok {
		t.Fatal("old host settings remained")
	}
	if got := cfg.HostSettings["newhost"].DotsRepo; got != "~/old-dotfiles" {
		t.Fatalf("host_settings[newhost].dots_repo = %q, want old repo", got)
	}
	if !config.BoolVal(cfg.HostSettings["newhost"].DotsDisabled) {
		t.Fatal("renamed host settings lost dots_disabled")
	}
}

func TestCopyHostConfigCopiesHostScopedSettingsAndOverrides(t *testing.T) {
	a, cfgPath := newImportApp(t)
	tools := logicalToolSpecs(
		logicalFixtureTool{Name: "fd", Provider: "brew", Package: "fd-find", Options: map[string]string{"scope": "source"}},
		logicalTool("ripgrep", "brew"),
	)
	fdSpec := tools["fd"]
	fdSpec.Hosts = map[string]config.ToolInstallSpec{
		"laptop":  {Provider: "apt", Package: "fd-find", Options: map[string]string{"scope": "laptop"}},
		"desktop": {Provider: "brew", Package: "old-fd"},
	}
	tools["fd"] = fdSpec
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: tools,
		Groups: []*config.GroupConfig{
			{Name: "laptop", Special: "host", Tools: groupTools("fd"), Dots: []config.DotEntry{{
				Name: "private",
				Path: "~/.private",
				Hosts: map[string]config.DotVariant{
					"laptop": {Package: "private-laptop"},
				},
			}}},
			{Name: "desktop", Special: "host", Tools: groupTools("ripgrep")},
			{Name: "work", Dots: []config.DotEntry{{
				Name: "nvim",
				Path: "~/.config/nvim",
				Hosts: map[string]config.DotVariant{
					"laptop":  {Package: "nvim-laptop"},
					"desktop": {Package: "nvim-desktop"},
				},
			}}},
		},
		Hosts: map[string][]string{
			"laptop":  {"work"},
			"desktop": {},
		},
		HostSettings: map[string]config.Settings{
			"laptop": {
				DotsRepo:          "~/dotfiles-laptop",
				DotsDisabled:      config.BoolPtr(true),
				DisabledProviders: []string{"node"},
				ProviderPriority:  []string{"brew", "apt"},
			},
			"desktop": {DotsRepo: "~/old-desktop"},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.CopyHostConfig("laptop", "desktop.example"); err != nil {
		t.Fatalf("CopyHostConfig: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Hosts["desktop"]; !slices.Equal(got, []string{"work"}) {
		t.Fatalf("hosts[desktop] = %v, want [work]", got)
	}
	desktopGroup := findHostTestGroup(cfg.Groups, "desktop")
	if desktopGroup == nil || !desktopGroup.IsHost() {
		t.Fatalf("desktop host group = %#v, want ensured special host group", desktopGroup)
	}
	if !containsToolMembershipForTest(desktopGroup.Tools, "ripgrep") {
		t.Fatal("copy should not delete existing target host-local tools")
	}
	laptopGroup := findHostTestGroup(cfg.Groups, "laptop")
	if laptopGroup == nil || len(laptopGroup.Dots) != 1 {
		t.Fatalf("source host group = %#v, want private dot preserved only on source", laptopGroup)
	}
	if _, ok := laptopGroup.Dots[0].Hosts["desktop"]; ok {
		t.Fatal("source host-local dot variant was copied into target host")
	}
	settings := cfg.HostSettings["desktop"]
	if settings.DotsRepo != "~/dotfiles-laptop" {
		t.Fatalf("desktop dots_repo = %q, want copied laptop repo", settings.DotsRepo)
	}
	if !config.BoolVal(settings.DotsDisabled) {
		t.Fatal("desktop dots_disabled was not copied")
	}
	if !slices.Equal(settings.DisabledProviders, []string{"node"}) {
		t.Fatalf("desktop disabled providers = %v, want [node]", settings.DisabledProviders)
	}
	if got := settings.ProviderPriority; !slices.Equal(got, []string{"brew", "apt"}) {
		t.Fatalf("desktop provider priority = %v, want [brew apt]", got)
	}
	copiedFD := cfg.Tools["fd"].Hosts["desktop"]
	if copiedFD.Provider != "apt" || copiedFD.Package != "fd-find" || copiedFD.Options["scope"] != "laptop" {
		t.Fatalf("desktop fd override = %+v, want copied laptop override", copiedFD)
	}
	if got := cfg.Tools["fd"].Hosts["laptop"].Options["scope"]; got != "laptop" {
		t.Fatalf("laptop fd override option = %q, want preserved source override", got)
	}
	settings.DisabledProviders[0] = "python"
	if cfg.HostSettings["laptop"].DisabledProviders[0] != "node" {
		t.Fatal("copied host settings share slices with source settings")
	}
	workGroup := findHostTestGroup(cfg.Groups, "work")
	if workGroup == nil || len(workGroup.Dots) != 1 {
		t.Fatalf("work group = %#v, want nvim dot", workGroup)
	}
	if got := workGroup.Dots[0].Hosts["desktop"].Package; got != "nvim-laptop" {
		t.Fatalf("nvim desktop variant = %q, want copied laptop variant", got)
	}
}

func TestRemoveHostDeletesSpecialHostGroup(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(logicalTool("fd", "brew"), logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{
			{
				Name:    "laptop",
				Special: "host",
				Tools:   groupTools("fd"),
				Dots: []config.DotEntry{{
					Name: "nvim",
					Path: "~/.config/nvim",
					Hosts: map[string]config.DotVariant{
						"laptop": {Package: "nvim-laptop"},
						"other":  {Package: "nvim-other"},
					},
				}},
			},
			{
				Name:  "work",
				Tools: groupTools("ripgrep"),
				Dots: []config.DotEntry{{
					Name: "zsh",
					Path: "~/.zshrc",
					Hosts: map[string]config.DotVariant{
						"laptop": {Package: "zsh-laptop"},
						"other":  {Package: "zsh-other"},
					},
				}},
			},
		},
		Hosts: map[string][]string{"laptop": {"work"}},
		HostSettings: map[string]config.Settings{
			"laptop": {DotsRepo: "~/dotfiles", DotsDisabled: config.BoolPtr(true)},
			"other":  {DotsRepo: "~/other-dotfiles"},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.RemoveHost("laptop"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Hosts["laptop"]; ok {
		t.Fatal("host assignment remained after RemoveHost")
	}
	if group := findHostTestGroup(cfg.Groups, "laptop"); group != nil {
		t.Fatalf("special host group remained after RemoveHost: %#v", group)
	}
	if group := findHostTestGroup(cfg.Groups, "work"); group == nil || group.IsHost() {
		t.Fatalf("reusable group = %#v, want preserved reusable work group", group)
	}
	if _, ok := cfg.HostSettings["laptop"]; ok {
		t.Fatal("host_settings[laptop] remained after RemoveHost")
	}
	if got := cfg.HostSettings["other"].DotsRepo; got != "~/other-dotfiles" {
		t.Fatalf("host_settings[other].dots_repo = %q, want preserved", got)
	}
	workGroup := findHostTestGroup(cfg.Groups, "work")
	if workGroup == nil {
		t.Fatal("work group missing")
	}
	// The deleted host group is removed entirely, but reusable dots elsewhere
	// should still lose only the deleted host's scoped variant.
	for _, group := range cfg.Groups {
		for _, dot := range group.Dots {
			if _, ok := dot.Hosts["laptop"]; ok {
				t.Fatalf("dot %q retained laptop variant after RemoveHost", dot.Name)
			}
		}
	}
	if len(workGroup.Dots) != 1 || workGroup.Dots[0].Hosts["other"].Package != "zsh-other" {
		t.Fatalf("work dots after RemoveHost = %#v, want zsh other variant preserved", workGroup.Dots)
	}
}

func TestClaimFromMachineGroupAssignsHostAndPrunesClaimedTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: logicalToolSpecs(
			logicalTool("ripgrep", "brew"),
			logicalTool("fd", "brew"),
		),
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: groupTools("fd")},
			{Name: "dev", Tools: groupTools("ripgrep")},
		},
		Hosts: map[string][]string{"testhost": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.ClaimFromMachineGroup("dev"); err != nil {
		t.Fatalf("ClaimFromMachineGroup: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Hosts["testhost"]; !slices.Equal(got, []string{"dev"}) {
		t.Fatalf("hosts[testhost] = %v, want [dev]", got)
	}
	hostGroup := findHostTestGroup(cfg.Groups, "testhost")
	if hostGroup == nil {
		t.Fatal("missing host group")
	}
	if containsToolMembershipForTest(hostGroup.Tools, "ripgrep") {
		t.Fatal("claimed tool ripgrep remained in host group")
	}
	if !containsToolMembershipForTest(hostGroup.Tools, "fd") {
		t.Fatal("unclaimed tool fd was removed from host group")
	}
}

func TestRequireActiveHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, _ := newImportApp(t)
	if err := a.RequireActiveHost(); err == nil {
		t.Fatal("RequireActiveHost without host config succeeded")
	}
	if err := a.EnsureHost("testhost"); err != nil {
		t.Fatalf("EnsureHost: %v", err)
	}
	if err := a.RequireActiveHost(); err != nil {
		t.Fatalf("RequireActiveHost after EnsureHost: %v", err)
	}
}

func TestSetGlobalToolIgnore(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools:  logicalToolSpecs(logicalTool("node", "brew"), logicalTool("ripgrep", "brew")),
		Groups: []*config.GroupConfig{{Name: "tools", Tools: groupTools("node", "ripgrep")}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.SetGlobalToolIgnore("node", true); err != nil {
		t.Fatalf("SetGlobalToolIgnore(true): %v", err)
	}
	if err := a.SetGlobalToolIgnore("node", true); err != nil {
		t.Fatalf("SetGlobalToolIgnore(true duplicate): %v", err)
	}
	if err := a.SetGlobalToolIgnore("ripgrep", true); err != nil {
		t.Fatalf("SetGlobalToolIgnore(ripgrep true): %v", err)
	}
	if err := a.SetGlobalToolIgnore("node", false); err != nil {
		t.Fatalf("SetGlobalToolIgnore(node false): %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Ignore.Tools) != 1 || cfg.Ignore.Tools[0] != "ripgrep" {
		t.Fatalf("ignore.tools = %v, want [ripgrep]", cfg.Ignore.Tools)
	}
}

func findHostTestGroup(groups []*config.GroupConfig, name string) *config.GroupConfig {
	for _, group := range groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

func containsToolMembershipForTest(tools []config.ToolEntry, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestHostGroupsAllGroupsUsesContext(t *testing.T) {
	a, _ := newImportApp(t)
	if _, err := a.HostGroups(context.Background(), "missing"); err == nil {
		t.Fatal("HostGroups should reject missing host")
	}
}
