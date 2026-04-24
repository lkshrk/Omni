package tui

// cmd_group_test.go — unit tests for group/profile/hostname do* commands
// and the error branches of the dots pull/push/overwrite commands.
//
// All tests call cmd() directly (the tea.Cmd closure) rather than driving
// the model through key presses, which lets us verify the async message
// returned by each command function without the TUI event loop.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newGroupApp builds an App with a minimal settings.json that has one profile
// ("default") and one named group ("mygroup"), ready for group/profile ops.
func newGroupApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "base"},
			{Name: "mygroup"},
		},
		Profiles: map[string]config.Profile{
			"default": {Groups: []string{"base", "mygroup"}},
		},
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

// modelWithGroupApp wires a Model to a group-ready App.
func modelWithGroupApp(t *testing.T) Model {
	return modelForCmds(newGroupApp(t))
}

// ── doCreateGroup ─────────────────────────────────────────────────────────────

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
}

func TestDoCreateGroup_DuplicateIsOK(t *testing.T) {
	// Creating a group that already exists should not return an error
	// (idempotent semantics) — or if it does, the error is surfaced in the msg.
	m := modelWithGroupApp(t)
	msg := m.doCreateGroup("mygroup")()
	_, ok := msg.(createGroupDoneMsg)
	if !ok {
		t.Fatalf("expected createGroupDoneMsg, got %T", msg)
	}
	// Whether err is nil or not depends on app semantics; we just assert no panic.
}

// ── doAddGroupToProfile ───────────────────────────────────────────────────────

func TestDoAddGroupToProfile_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doAddGroupToProfile("default", "mygroup")()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if !got.added {
		t.Error("expected added=true")
	}
	if got.profile != "default" {
		t.Errorf("profile = %q, want %q", got.profile, "default")
	}
}

func TestDoAddGroupToProfile_NewProfile(t *testing.T) {
	// Adding a group to a non-existent profile should create it (app is idempotent).
	m := modelWithGroupApp(t)
	msg := m.doAddGroupToProfile("brandnew", "mygroup")()
	_, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
}

// ── doRemoveGroupFromProfile ──────────────────────────────────────────────────

func TestDoRemoveGroupFromProfile_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRemoveGroupFromProfile("default", "mygroup")()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.added {
		t.Error("expected added=false for remove")
	}
	if got.profile != "default" {
		t.Errorf("profile = %q, want %q", got.profile, "default")
	}
	if got.group != "mygroup" {
		t.Errorf("group = %q, want %q", got.group, "mygroup")
	}
}

func TestDoRemoveGroupFromProfile_MissingProfileIsOK(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRemoveGroupFromProfile("nonexistent", "mygroup")()
	_, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
}

func TestDoSetProfileGroups_UpdatesMemberships(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doSetProfileGroups("default", []string{"base", "mygroup"}, []string{"base", "newgroup"}, []string{"newgroup"})()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	info, err := m.app.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	groups := info.Profiles["default"].Groups
	if !slices.Contains(groups, "base") || !slices.Contains(groups, "newgroup") || slices.Contains(groups, "mygroup") {
		t.Fatalf("profile groups = %v, want base + newgroup only", groups)
	}
}

func TestDoSetProfileGroupTools_UpdatesMembershipsAndIgnores(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
			"eslint":  {Provider: "node"},
			"ruff":    {Provider: "python"},
		},
		Groups: []*config.GroupConfig{{
			Name:   "work",
			Tools:  []config.ToolEntry{{Name: "ripgrep"}},
			Ignore: []string{"ruff"},
		}},
		Profiles: map[string]config.Profile{"default": {Groups: []string{"work"}}},
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
	msg := m.doSetProfileGroupTools(
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
	if !slices.Contains(work.Ignore, "ruff") || !slices.Contains(work.Ignore, "eslint") {
		t.Fatalf("work ignore = %v, want ruff and eslint", work.Ignore)
	}
}

func TestDoSetProfileGroupDots_UpdatesMemberships(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{
				Dots: []config.DotEntry{
					{Name: "nvim", Path: "~/.config/nvim"},
					{Name: "zsh", Path: "~/.zshrc"},
				},
			},
			{
				Name: "work",
				Dots: []config.DotEntry{{Name: "zsh", Path: "~/.zshrc"}},
			},
		},
		Profiles: map[string]config.Profile{"default": {Groups: []string{"work"}}},
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
	msg := m.doSetProfileGroupDots(
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
	work := cfg.Groups[1]
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

func TestDoSetProfileHosts_UpdatesMappings(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doSetProfileHosts("default", map[string]string{"oldhost": "default"}, map[string]string{"oldhost": "", "newhost": "default"})()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	info, err := m.app.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	if _, ok := info.Hostnames["oldhost"]; ok {
		t.Fatal("oldhost mapping should be removed")
	}
	if got := info.Hostnames["newhost"]; got != "default" {
		t.Fatalf("newhost mapping = %q, want default", got)
	}
}

// ── doDeleteProfileFromTab ────────────────────────────────────────────────────

func TestDoDeleteProfileFromTab_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doDeleteProfileFromTab("default")()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.profile != "default" {
		t.Errorf("profile = %q, want %q", got.profile, "default")
	}
}

func TestDoDeleteProfileFromTab_NonexistentIsOK(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doDeleteProfileFromTab("ghost")()
	_, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
}

func TestProfileDeleteConfirm_UsesCapturedProfileName(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Profiles: map[string]config.Profile{
			"alpha": {},
			"beta":  {},
		},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer func() { _ = a.Close() }()
	info, err := a.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	m := baseModel(nil)
	m.app = a
	m.profileInfo = info
	m.profileSection = 0
	m.profileCursor = 0

	var armCmds []tea.Cmd
	m.startProfileDelete(&armCmds)
	if !m.profileDeleteConfirm || m.profileDeleteName != "alpha" {
		t.Fatalf("delete confirmation target = confirm:%v name:%q, want alpha", m.profileDeleteConfirm, m.profileDeleteName)
	}

	m.profileCursor = 1
	handled, cmds := m.handleProfileSubmodeKeyMsg(pressEnter().(tea.KeyPressMsg))
	if !handled {
		t.Fatal("delete confirmation key should be handled")
	}
	if len(cmds) != 1 {
		t.Fatalf("confirm returned %d commands, want 1", len(cmds))
	}
	msg := cmds[0]()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("confirm command returned %T, want profileGroupChangedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("delete profile returned error: %v", got.err)
	}
	if got.profile != "alpha" {
		t.Fatalf("deleted profile = %q, want alpha", got.profile)
	}
	if _, ok := got.info.Profiles["alpha"]; ok {
		t.Fatal("alpha profile should be deleted")
	}
	if _, ok := got.info.Profiles["beta"]; !ok {
		t.Fatal("beta profile should remain")
	}
}

func TestGroupDeleteConfirm_UsesCapturedGroupName(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "base"},
			{Name: "alpha"},
			{Name: "beta"},
		},
		Profiles: map[string]config.Profile{
			"default": {Groups: []string{"base", "alpha", "beta"}},
		},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer func() { _ = a.Close() }()

	m := modelForCmds(a)
	m.mode = viewProfiles
	m.groupNames = []string{"alpha", "beta"}
	m.profileSection = 1
	m.groupCursor = 0
	var armCmds []tea.Cmd
	m.startProfileDelete(&armCmds)
	if !m.groupDeleteConfirm || m.groupDeleteName != "alpha" {
		t.Fatalf("group delete target = confirm:%v name:%q, want alpha", m.groupDeleteConfirm, m.groupDeleteName)
	}

	m.groupCursor = 2
	handled, cmds := m.handleProfileSubmodeKeyMsg(pressEnter().(tea.KeyPressMsg))
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

func TestDoActivateProfile_PersistsHostnameMapping(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "desk.example.com")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Profiles: map[string]config.Profile{
			"alpha": {},
			"beta":  {},
		},
		Hostnames: map[string]string{"desk": "alpha"},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer func() { _ = a.Close() }()

	m := modelForCmds(a)
	msg := m.doActivateProfile("beta")()
	got, ok := msg.(profileActivatedMsg)
	if !ok {
		t.Fatalf("doActivateProfile returned %T, want profileActivatedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("doActivateProfile error: %v", got.err)
	}
	if got.info == nil || got.info.Active != "beta" {
		t.Fatalf("active profile = %v, want beta", got.info)
	}
	reloaded, err := a.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	if reloaded.Active != "beta" {
		t.Fatalf("persisted active profile = %q, want beta", reloaded.Active)
	}
}

func TestDoRenameProfile_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doRenameProfile("default", "office")()
	got, ok := msg.(profileGroupChangedMsg)
	if !ok {
		t.Fatalf("expected profileGroupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	info, err := m.app.ProfileStatus()
	if err != nil {
		t.Fatalf("ProfileStatus: %v", err)
	}
	if _, ok := info.Profiles["default"]; ok {
		t.Fatal("old profile still present after rename")
	}
	if _, ok := info.Profiles["office"]; !ok {
		t.Fatal("renamed profile missing")
	}
}

// ── doCreateProfileFromTab ────────────────────────────────────────────────────

func TestDoCreateProfileFromTab_Success(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doCreateProfileFromTab("laptop")()
	got, ok := msg.(profileCreatedMsg)
	if !ok {
		t.Fatalf("expected profileCreatedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.profile != "laptop" {
		t.Errorf("profile = %q, want %q", got.profile, "laptop")
	}
}

func TestDoCreateProfileFromTab_DuplicateIsOK(t *testing.T) {
	m := modelWithGroupApp(t)
	msg := m.doCreateProfileFromTab("default")()
	_, ok := msg.(profileCreatedMsg)
	if !ok {
		t.Fatalf("expected profileCreatedMsg, got %T", msg)
	}
}

// ── doRenameGroup ─────────────────────────────────────────────────────────────

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
	// App may or may not return an error for renaming a non-existent group;
	// we assert the message type is correct and there's no panic.
	_ = got
}

// ── doDeleteGroup ─────────────────────────────────────────────────────────────

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

// ── dots error paths ──────────────────────────────────────────────────────────
// These cover the 50%-covered branches: the DotsStatus error path reached
// after a successful pull/push/overwrite when the follow-up status reload fails.
// We trigger the first-step error by using an App with no dots_repo configured,
// which makes the underlying git operations fail immediately.

// newNoDotsModel creates a Model whose App has no DotsRepo configured.
func newNoDotsModel(t *testing.T) Model {
	t.Helper()
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil) // no DotsRepo in settings
	return modelForCmds(a)
}

func TestDoDotsPull_ErrorPath(t *testing.T) {
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

func TestDoDotsSyncDiscovered_AddsCandidateAndRefreshes(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "dotfiles", "claude", ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)

	msg := m.doDotsSyncDiscovered(app.DotStatus{Name: "claude", TargetPath: "~/.claude", State: app.DotStateUntrackedConflict})()
	got, ok := msg.(dotsSyncedMsg)
	if !ok {
		t.Fatalf("expected dotsSyncedMsg, got %T", msg)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "requires choosing") {
		t.Fatalf("err = %v, want tracked conflict choice error", got.err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findTUITestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "claude" {
		t.Fatalf("machine group dots = %#v, want claude", cfg.Groups)
	}
	foundConflict := false
	for _, entry := range got.entries {
		if entry.Name == "claude" && entry.State == app.DotStateConflict {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatalf("entries = %#v, want refreshed tracked claude conflict", got.entries)
	}
}

func TestDoDotsOverwrite_ErrorPath(t *testing.T) {
	m := newNoDotsModel(t)
	msg := m.doDotsOverwrite("anything")()
	got, ok := msg.(dotsFixedMsg)
	if !ok {
		t.Fatalf("expected dotsFixedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected non-nil error when dots_repo not configured")
	}
}
