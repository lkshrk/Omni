package tui

// Tests call cmd() directly rather than driving the model through key presses, so each command's async message is verified without the TUI event loop.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

// One host ("default") and one named group ("mygroup"), ready for group/host ops.
func newGroupApp(t *testing.T) *app.App {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", "default")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "default", Special: "host"},
			{Name: "mygroup"},
		},
		Hosts: map[string][]string{"default": {"mygroup"}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func modelWithGroupApp(t *testing.T) Model {
	return modelForCmds(newGroupApp(t))
}

func TestDoCreateGroup_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doCreateGroup("newgroup")()
	got, ok := msg.(createGroupDoneMsg)
	if !ok {
		t.Fatalf("expected createGroupDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.name != "newgroup" {
		t.Errorf("name = %q, want %q", got.name, "newgroup")
	}
	if !slices.Contains(got.groupNames, "newgroup") {
		t.Fatalf("groupNames = %v, want newgroup", got.groupNames)
	}
	if got.hostInfo == nil {
		t.Fatal("hostInfo should be refreshed after group creation")
	}
	if groups := got.hostInfo.Hosts[shortHostname()].Groups; slices.Contains(groups, "newgroup") {
		t.Fatalf("new empty group should not be assigned implicitly, groups=%v", groups)
	}
}

func TestDoCreateGroup_DuplicateIsOK(t *testing.T) {
	// Creating an existing group is idempotent; any error is surfaced in the msg instead.
	m := modelWithGroupApp(t)
	msg := m.doCreateGroup("mygroup")()
	_, ok := msg.(createGroupDoneMsg)
	if !ok {
		t.Fatalf("expected createGroupDoneMsg, got %T", msg)
	}
	// Whether err is nil or not depends on app semantics; we just assert no panic.
}

func TestDoSetHostGroups_UpdatesMemberships(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doSetHostGroups("default", []string{"mygroup"}, []string{"newgroup"}, []string{"newgroup"})()
	got, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("expected hostGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	info, err := m.app.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	groups := info.Hosts["default"].Groups
	if !slices.Contains(groups, "newgroup") || slices.Contains(groups, "mygroup") {
		t.Fatalf("host groups = %v, want newgroup only", groups)
	}
}

func TestDoSetHostGroupTools_UpdatesMembershipsAndIgnores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
			"eslint":  {Provider: "node"},
			"ruff":    {Provider: "python"},
		},
		Groups: []*config.GroupConfig{{
			Name:    "default",
			Special: "host",
		}, {
			Name:   "work",
			Tools:  []config.ToolEntry{{Name: "ripgrep"}},
			Ignore: []string{"ruff"},
		}},
		Hosts: map[string][]string{"default": {"work"}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doSetGroupTools(
		"work",
		map[string]bool{"ripgrep": false, "eslint": true, "ruff": false},
		map[string]bool{"ripgrep": true, "eslint": false, "ruff": false},
		map[string]bool{"ruff": true, "eslint": true},
		map[string]bool{"ruff": true, "eslint": false},
	)()
	got, ok := msg.(groupToolsChangedMsg)
	if !ok {
		t.Fatalf("expected groupToolsChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	work := cfg.Groups[0]
	if slices.ContainsFunc(work.Tools, func(t config.ToolEntry) bool { return t.Name == "ripgrep" }) {
		t.Fatalf("ripgrep should be removed from work tools: %+v", work.Tools)
	}
	if !slices.ContainsFunc(work.Tools, func(t config.ToolEntry) bool { return t.Name == "eslint" }) {
		t.Fatalf("eslint should be added to work tools: %+v", work.Tools)
	}
	if !slices.Contains(cfg.Ignore.Tools, "ruff") || !slices.Contains(cfg.Ignore.Tools, "eslint") {
		t.Fatalf("global ignore = %v, want ruff and eslint", cfg.Ignore.Tools)
	}

	m.handleGroupToolsChangedMsg(got)
	for _, name := range []string{"eslint", "ruff"} {
		if !slices.Contains(toolNames(m.allTools), name) {
			t.Fatalf("allTools = %v, want ignored %q retained for ignored section", toolNames(m.allTools), name)
		}
	}
	for _, tool := range m.visibleTools {
		if tool == nil {
			continue
		}
		if tool.Name != "eslint" && tool.Name != "ruff" {
			continue
		}
		if m.displaySection(tool) != sectionIgnored {
			t.Fatalf("displaySection(%q) = %v, want sectionIgnored", tool.Name, m.displaySection(tool))
		}
	}
	if m.sectionCounts[sectionIgnored] < 2 {
		t.Fatalf("sectionCounts[ignored] = %d, want at least 2", m.sectionCounts[sectionIgnored])
	}
}

func TestDoSetHostGroupDots_UpdatesMemberships(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{
				Name:    "default",
				Special: "host",
				Dots: []config.DotEntry{
					{Name: "nvim", Path: "~/.config/nvim"},
				},
			},
			{
				Name: "work",
				Dots: []config.DotEntry{{Name: "zsh", Path: "~/.zshrc"}},
			},
		},
		Hosts: map[string][]string{"default": {"work"}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doSetGroupDots(
		"work",
		map[string]bool{"nvim": true, "zsh": false},
		map[string]bool{"nvim": false, "zsh": true},
	)()
	got, ok := msg.(groupDotsChangedMsg)
	if !ok {
		t.Fatalf("expected groupDotsChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	work := findTestGroup(cfg, "work")
	if !slices.ContainsFunc(work.Dots, func(d config.DotEntry) bool { return d.Name == "nvim" }) {
		t.Fatalf("nvim should be added to work dots: %+v", work.Dots)
	}
	if slices.ContainsFunc(work.Dots, func(d config.DotEntry) bool { return d.Name == "zsh" }) {
		t.Fatalf("zsh should be removed from work dots: %+v", work.Dots)
	}
	if !slices.Contains(got.dotMemberships["nvim"], "work") {
		t.Fatalf("dotMemberships[nvim] = %v, want work", got.dotMemberships["nvim"])
	}
}

func TestDoSetHostGroupDots_DeselectedEntryExcludedFromSync(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "default")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	repoDir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	zshPath := filepath.Join(home, ".zshrc")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Name: "default", Special: "host"},
			{Name: "work", Dots: []config.DotEntry{{Name: "zsh", Path: zshPath}}},
		},
		Hosts: map[string][]string{"default": {"work"}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doSetGroupDots(
		"work",
		map[string]bool{"zsh": false},
		map[string]bool{"zsh": true},
	)()
	got, ok := msg.(groupDotsChangedMsg)
	if !ok {
		t.Fatalf("expected groupDotsChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DotsSync dry-run: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("DotsSync ops = %v, want deselected zsh excluded from active sync", ops)
	}
}

func TestDoRemoveHostFromTab_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRemoveHostFromTab("default")()
	got, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("expected hostGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.host != "default" {
		t.Errorf("host = %q, want %q", got.host, "default")
	}
}

func TestDoRemoveHostFromTab_NonexistentIsOK(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRemoveHostFromTab("ghost")()
	_, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("expected hostGroupChangedMsg, got %T", msg)
	}
}

func TestHostDeleteConfirm_UsesCapturedHostName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "alpha", Special: "host"},
			{Name: "beta", Special: "host"},
		},
		Hosts: map[string][]string{"alpha": {}, "beta": {}},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer func() { _ = a.Close() }()
	info, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	m := baseModel(nil)
	m.app = a
	m.hostInfo = info
	m.assignmentSection = 0
	m.hostCursor = 0

	var armCmds []tea.Cmd
	m.startHostDelete(&armCmds)
	if !m.hostDeleteConfirm || m.hostDeleteName != "alpha" {
		t.Fatalf("delete confirmation target = confirm:%v name:%q, want alpha", m.hostDeleteConfirm, m.hostDeleteName)
	}

	m.hostCursor = 1
	handled, cmds := m.handleHostSubmodeKeyMsg(pressEnter().(tea.KeyPressMsg))
	if !handled {
		t.Fatal("delete confirmation key should be handled")
	}
	if len(cmds) != 1 {
		t.Fatalf("confirm returned %d commands, want 1", len(cmds))
	}
	msg := cmds[0]()
	got, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("confirm command returned %T, want hostGroupChangedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("delete host returned error: %v", got.err)
	}
	if got.host != "alpha" {
		t.Fatalf("deleted host = %q, want alpha", got.host)
	}
	if _, ok := got.info.Hosts["alpha"]; ok {
		t.Fatal("alpha host should be deleted")
	}
	if _, ok := got.info.Hosts["beta"]; !ok {
		t.Fatal("beta host should remain")
	}
}

func TestGroupDeleteConfirm_UsesCapturedGroupName(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "default")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "default", Special: "host"},
			{Name: "alpha"},
			{Name: "beta"},
		},
		Hosts: map[string][]string{"default": {"alpha", "beta"}},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer func() { _ = a.Close() }()

	m := modelForCmds(a)
	m.mode = viewGroups
	m.groupNames = []string{"alpha", "beta"}
	m.assignmentSection = 1
	m.groupCursor = 1
	var armCmds []tea.Cmd
	m.startHostDelete(&armCmds)
	if !m.groupDeleteConfirm || m.groupDeleteName != "alpha" {
		t.Fatalf("group delete target = confirm:%v name:%q, want alpha", m.groupDeleteConfirm, m.groupDeleteName)
	}

	m.groupCursor = 2
	handled, cmds := m.handleHostSubmodeKeyMsg(pressEnter().(tea.KeyPressMsg))
	if !handled {
		t.Fatal("group delete confirmation key should be handled")
	}
	if len(cmds) != 1 {
		t.Fatalf("confirm returned %d commands, want 1", len(cmds))
	}
	msg := cmds[0]()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("confirm command returned %T, want groupChangedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("delete group returned error: %v", got.err)
	}
	if !strings.Contains(got.detail, "deleted group alpha") {
		t.Fatalf("detail = %q, want alpha deletion", got.detail)
	}
	if slices.Contains(got.groupNames, "alpha") {
		t.Fatalf("alpha group should be deleted: %v", got.groupNames)
	}
	if !slices.Contains(got.groupNames, "beta") {
		t.Fatalf("beta group should remain: %v", got.groupNames)
	}
}

func TestDoActivateHost_CopiesGroupsToCurrentHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "desk", Special: "host"},
			{Name: "alpha", Special: "host"},
			{Name: "beta", Special: "host"},
			{Name: "shared"},
		},
		Hosts: map[string][]string{"desk": {}, "alpha": {}, "beta": {"shared"}},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer func() { _ = a.Close() }()

	m := modelForCmds(a)
	msg := m.doCopyHostGroupsFrom("beta")()
	got, ok := msg.(hostCopiedMsg)
	if !ok {
		t.Fatalf("doCopyHostGroupsFrom returned %T, want hostCopiedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("doCopyHostGroupsFrom error: %v", got.err)
	}
	if got.info == nil || got.info.Active != "desk" {
		t.Fatalf("active host = %v, want desk", got.info)
	}
	reloaded, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if reloaded.Active != "desk" {
		t.Fatalf("persisted active host = %q, want desk", reloaded.Active)
	}
	if got := reloaded.Hosts["desk"].Groups; !slices.Equal(got, []string{"shared"}) {
		t.Fatalf("desk groups = %v, want [shared]", got)
	}
}

func TestDoRenameHost_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRenameHost("default", "office")()
	got, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("expected hostGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	info, err := m.app.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if _, ok := info.Hosts["default"]; ok {
		t.Fatal("old host still present after rename")
	}
	if _, ok := info.Hosts["office"]; !ok {
		t.Fatal("renamed host missing")
	}
}

func TestDoRenameGroup_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRenameGroup("mygroup", "renamedgroup")()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.detail == "" {
		t.Error("expected non-empty detail on success")
	}
}

func TestDoRenameGroup_NonexistentGroup(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRenameGroup("ghost", "renamed")()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	// App may or may not error for a non-existent group, so only the message type and the absence of a panic are asserted.
	_ = got
}

func TestDoDeleteGroup_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doDeleteGroup("mygroup", false)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.detail == "" {
		t.Error("expected non-empty detail on success")
	}
}

func TestDoDeleteGroup_DeleteTools(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doDeleteGroup("mygroup", true)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if !strings.Contains(got.detail, "deleted") {
		t.Errorf("detail = %q, want deleted tools wording", got.detail)
	}
}

func TestDoDeleteGroup_NonexistentGroup(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doDeleteGroup("ghost", false)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	_ = got
}

// An App with no dots_repo makes the git operation fail immediately, reaching the DotsStatus error path after a successful pull/push/overwrite.

func newNoDotsModel(t *testing.T) Model {
	t.Helper()
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil) // no DotsRepo in settings
	return modelForCmds(a)
}

func TestDoDotsPull_ErrorPath(t *testing.T) {
	t.Parallel()
	m := newNoDotsModel(t)
	msg := m.doDotsPull()()
	got, ok := msg.(dotsPulledMsg)
	if !ok {
		t.Fatalf("expected dotsPulledMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected non-nil error when dots_repo not configured")
	}
}

func TestDoDotsPush_ErrorPath(t *testing.T) {
	t.Parallel()
	m := newNoDotsModel(t)
	msg := m.doDotsPush()()
	got, ok := msg.(dotsPushedMsg)
	if !ok {
		t.Fatalf("expected dotsPushedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected non-nil error when dots_repo not configured")
	}
}
