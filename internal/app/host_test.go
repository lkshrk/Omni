package app_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

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
		Tools: map[string]config.ToolSpec{
			"fd": {
				Provider: "system",
				Hosts: map[string]config.ToolInstallSpec{
					"oldhost": {Provider: "system", InstallWith: "brew", Package: "fd-find"},
					"other":   {Provider: "system", InstallWith: "apt"},
				},
			},
		},
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
	fd := cfg.Tools["fd"]
	if _, ok := fd.Hosts["oldhost"]; ok {
		t.Fatal("old tool host override remained")
	}
	if got := fd.Hosts["newhost"]; got.InstallWith != "brew" || got.Package != "fd-find" {
		t.Fatalf("fd newhost override = %#v, want brew fd-find", got)
	}
	if got := fd.Hosts["other"].InstallWith; got != "apt" {
		t.Fatalf("fd other override install_with = %q, want apt", got)
	}
}

func TestCopyHostConfigCopiesHostScopedSettingsAndOverrides(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd": {
				Provider: "system",
				Hosts: map[string]config.ToolInstallSpec{
					"laptop": {
						Provider:    "system",
						InstallWith: "brew",
						Package:     "fd-find",
						Options:     map[string]string{"scope": "source"},
					},
					"desktop": {Provider: "system", InstallWith: "apt", Package: "fd"},
				},
			},
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
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
				Ecosystems: map[string]config.EcosystemSettings{
					"system": {Priority: []string{"brew", "apt"}},
				},
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
	if got := settings.EcosystemPriority("system"); !slices.Equal(got, []string{"brew", "apt"}) {
		t.Fatalf("desktop system priority = %v, want [brew apt]", got)
	}
	settings.DisabledProviders[0] = "python"
	if cfg.HostSettings["laptop"].DisabledProviders[0] != "node" {
		t.Fatal("copied host settings share slices with source settings")
	}
	fd := cfg.Tools["fd"]
	if got := fd.Hosts["desktop"]; got.InstallWith != "brew" || got.Package != "fd-find" || got.Options["scope"] != "source" {
		t.Fatalf("fd desktop override = %#v, want copied laptop override", got)
	}
	desktopOverride := fd.Hosts["desktop"]
	desktopOverride.Options["scope"] = "mutated"
	if cfg.Tools["fd"].Hosts["laptop"].Options["scope"] != "source" {
		t.Fatal("copied tool host override shares options map with source override")
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
		Tools: map[string]config.ToolSpec{
			"fd": {
				Provider: "system",
				Hosts: map[string]config.ToolInstallSpec{
					"laptop": {Provider: "system", InstallWith: "brew"},
					"other":  {Provider: "system", InstallWith: "apt"},
				},
			},
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
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
	fd := cfg.Tools["fd"]
	if _, ok := fd.Hosts["laptop"]; ok {
		t.Fatal("fd laptop override remained after RemoveHost")
	}
	if got := fd.Hosts["other"].InstallWith; got != "apt" {
		t.Fatalf("fd other override install_with = %q, want apt", got)
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
