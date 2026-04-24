package app_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/testguard"
)

func TestMain(m *testing.M) {
	origHome, hadHome := os.LookupEnv("HOME")
	testHome, err := os.MkdirTemp("", "omni-app-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", testHome); err != nil {
		panic(err)
	}
	code := m.Run()
	if hadHome {
		_ = os.Setenv("HOME", origHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	_ = os.RemoveAll(testHome)
	os.Exit(code)
}

// newDotsApp builds an App with a dots repo configured in settings.json.
// Returns the app, the config dir, and the dots repo dir.
func newDotsApp(t *testing.T) (*app.App, string, string) {
	t.Helper()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgDir, repoDir
}

func dotsContentDir(repoDir string) string {
	return filepath.Join(repoDir, "dotfiles")
}

func assertDotState(t *testing.T, got app.DotStatus, wantState app.DotState, wantActions []app.DotAction) {
	t.Helper()
	if got.State != wantState {
		t.Fatalf("state = %q, want %q (status: %+v)", got.State, wantState, got)
	}
	if !reflect.DeepEqual(got.Actions, wantActions) {
		t.Fatalf("actions = %v, want %v", got.Actions, wantActions)
	}
}

func TestDiscoverDotsEntries_CombinesRepoLocalConfigAndOutliers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotsContentDir(repoDir), ".zshrc"), []byte("zsh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	byName := make(map[string]string, len(got))
	for _, entry := range got {
		byName[entry.Name] = entry.Path
	}
	want := map[string]string{
		"kitty": "~/.config/kitty",
		"nvim":  "~/.config/nvim",
		"ssh":   "~/.ssh",
		"zshrc": "~/.zshrc",
	}
	if !reflect.DeepEqual(byName, want) {
		t.Fatalf("entries = %#v, want %#v", byName, want)
	}
	if _, ok := byName["cache"]; ok {
		t.Fatal("static ignored ~/.config/cache should not be discovered")
	}
}

func TestDiscoverDotsEntries_AddsClaudeEntryIgnores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	var claude config.DotEntry
	for _, entry := range got {
		if entry.Name == "claude" {
			claude = entry
			break
		}
	}
	if claude.Name == "" {
		t.Fatalf("claude entry not discovered: %#v", got)
	}
	for _, want := range []string{"projects", "transcripts", "file-history", "plugins", "history.jsonl", "*.log"} {
		if !testContainsString(claude.Ignore, want) {
			t.Fatalf("claude ignore = %v, missing %q", claude.Ignore, want)
		}
	}
}

func TestDiscoverDotsEntries_RejectsSymlinkedRepoDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realRepoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realRepoDir, "dotfiles", "nvim", ".config", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkRepo := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(realRepoDir, symlinkRepo); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := app.DiscoverDotsEntries(symlinkRepo); err == nil {
		t.Fatal("DiscoverDotsEntries: expected error for symlinked repo path")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("DiscoverDotsEntries error = %q, want symlink rejection", err)
	}
}

func TestDiscoverDotsEntries_RejectsSymlinkedContentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	contentTarget := filepath.Join(t.TempDir(), "content")
	if err := os.MkdirAll(filepath.Join(contentTarget, "dotfiles", "nvim", ".config", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentTarget, "dotfiles", "nvim", ".config", "nvim", "init.lua"), []byte("zsh"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(contentTarget, "dotfiles"), filepath.Join(repoDir, "dotfiles")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := app.DiscoverDotsEntries(repoDir); err == nil {
		t.Fatal("DiscoverDotsEntries: expected error for symlinked dotfiles dir")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("DiscoverDotsEntries error = %q, want symlink rejection", err)
	}
}

func TestBootstrapDotsEntries_AddsCandidatesToMachineGroup(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	added, err := a.BootstrapDotsEntries()
	if err != nil {
		t.Fatalf("BootstrapDotsEntries: %v", err)
	}
	if len(added) != 3 {
		t.Fatalf("added len = %d, want 3: %#v", len(added), added)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "testhost")
	if group == nil {
		t.Fatalf("machine group testhost was not created: %#v", cfg.Groups)
	}
	byName := make(map[string]string, len(group.Dots))
	byEntry := make(map[string]config.DotEntry, len(group.Dots))
	for _, entry := range group.Dots {
		byName[entry.Name] = entry.Path
		byEntry[entry.Name] = entry
	}
	if byName["nvim"] != "~/.config/nvim" || byName["kitty"] != "~/.config/kitty" {
		t.Fatalf("machine group dots = %#v", byName)
	}
	claude := byEntry["claude"]
	if !testContainsString(claude.Ignore, "projects") || !testContainsString(claude.Ignore, "history.jsonl") {
		t.Fatalf("bootstrapped claude ignore = %v, want runtime ignores", claude.Ignore)
	}
}

func TestDiscoverUntrackedDotsEntries_DoesNotMutateConfig(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := a.DiscoverUntrackedDotsEntries()
	if err != nil {
		t.Fatalf("DiscoverUntrackedDotsEntries: %v", err)
	}
	byName := make(map[string]string, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry.Path
	}
	if byName["nvim"] != "~/.config/nvim" || byName["kitty"] != "~/.config/kitty" {
		t.Fatalf("entries = %#v, want nvim and kitty", byName)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if group := findDotsTestGroup(cfg.Groups, "testhost"); group != nil && len(group.Dots) > 0 {
		t.Fatalf("discover should not persist candidates, got %#v", group.Dots)
	}
}

func TestDiscoverUntrackedDotsEntries_SkipsTrackedCandidates(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(filepath.Join(cfgDir, "settings.json"), &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name: "testhost",
			Dots: []config.DotEntry{{Name: "kitty", Path: "~/.config/kitty"}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	entries, err := a.DiscoverUntrackedDotsEntries()
	if err != nil {
		t.Fatalf("DiscoverUntrackedDotsEntries: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == "kitty" || entry.Path == "~/.config/kitty" {
			t.Fatalf("tracked kitty should not be rediscovered: %#v", entries)
		}
	}
}

func TestDiscoverUntrackedDotsEntries_SkipsIgnoredEntries(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	kittyPath := filepath.Join(home, ".config", "kitty")
	if err := os.MkdirAll(kittyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(filepath.Join(cfgDir, "settings.json"), &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name: "testhost",
			Dots: []config.DotEntry{{Name: "kitty", Path: "~/.config/kitty", Ignored: true}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	entries, err := a.DiscoverUntrackedDotsEntries()
	if err != nil {
		t.Fatalf("DiscoverUntrackedDotsEntries: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == "kitty" {
			t.Fatalf("ignored kitty should not be rediscovered: %#v", entries)
		}
	}

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != app.DotStateIgnored {
		t.Fatalf("statuses = %#v, want ignored kitty visible", statuses)
	}
}

func TestDotsSetEntryIgnored_PersistsDiscoveryCandidateToMachineGroup(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := a.DotsSetEntryIgnored("kitty", "~/.config/kitty", true); err != nil {
		t.Fatalf("DotsSetEntryIgnored: %v", err)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 || !group.Dots[0].Ignored {
		t.Fatalf("machine group dots = %#v, want ignored kitty", cfg.Groups)
	}

	entries, err := a.DiscoverUntrackedDotsEntries()
	if err != nil {
		t.Fatalf("DiscoverUntrackedDotsEntries: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == "kitty" {
			t.Fatalf("ignored kitty should not be rediscovered: %#v", entries)
		}
	}
}

func TestDotsSetEntryIgnored_NormalizesHomePath(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	target := filepath.Join(home, ".config", "kitty")

	if err := a.DotsSetEntryIgnored("kitty", target, true); err != nil {
		t.Fatalf("DotsSetEntryIgnored: %v", err)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 {
		t.Fatalf("machine group dots = %#v, want ignored kitty", cfg.Groups)
	}
	if got := group.Dots[0].Path; got != "~/.config/kitty" {
		t.Fatalf("path = %q, want ~/.config/kitty", got)
	}
}

func TestDiscoverDotsStatus_AddsTransientCandidates(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	if result.DiscoveredCount != 1 {
		t.Fatalf("DiscoveredCount = %d, want 1", result.DiscoveredCount)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "kitty" || result.Entries[0].State != app.DotStateLocalOnly {
		t.Fatalf("entries = %#v, want local-only kitty", result.Entries)
	}
	if !reflect.DeepEqual(result.Entries[0].Actions, []app.DotAction{app.DotActionSync, app.DotActionIgnore}) {
		t.Fatalf("actions = %#v, want sync+ignore", result.Entries[0].Actions)
	}
}

func TestDiscoverDotsStatus_ConflictCandidateExposesResolutions(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "claude", ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "claude" || result.Entries[0].State != app.DotStateUntrackedConflict {
		t.Fatalf("entries = %#v, want untracked-conflict claude", result.Entries)
	}
	want := []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionIgnore}
	if !reflect.DeepEqual(result.Entries[0].Actions, want) {
		t.Fatalf("actions = %#v, want %#v", result.Entries[0].Actions, want)
	}
}

func TestDotsAddDiscoveredEntry_PersistsCandidateOnly(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	added, err := a.DotsAddDiscoveredEntry("claude", "")
	if err != nil {
		t.Fatalf("DotsAddDiscoveredEntry: %v", err)
	}
	if added.Name != "claude" || added.Path != "~/.claude" {
		t.Fatalf("added = %#v, want claude ~/.claude", added)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "claude" {
		t.Fatalf("machine group dots = %#v, want claude", cfg.Groups)
	}
	if !testContainsString(group.Dots[0].Ignore, "projects") {
		t.Fatalf("claude ignore = %#v, want runtime ignores", group.Dots[0].Ignore)
	}
}

func TestDiscoverDotsStatus_ShowsStaticIgnoredCandidates(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := a.DiscoverUntrackedDotsEntries()
	if err != nil {
		t.Fatalf("DiscoverUntrackedDotsEntries: %v", err)
	}
	for _, entry := range entries {
		if entry.Name == "node_modules" {
			t.Fatalf("static ignored candidate should not be returned for syncing: %#v", entries)
		}
	}

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	if result.DiscoveredCount != 1 {
		t.Fatalf("DiscoveredCount = %d, want 1 non-ignored candidate", result.DiscoveredCount)
	}
	byName := make(map[string]app.DotStatus)
	for _, entry := range result.Entries {
		byName[entry.Name] = entry
	}
	if got := byName["node_modules"]; got.State != app.DotStateIgnored || !reflect.DeepEqual(got.Actions, []app.DotAction{app.DotActionUnignore}) {
		t.Fatalf("node_modules status = %+v, want ignored with include action", got)
	}
	if err := a.DotsSetEntryIgnored("node_modules", "~/.config/node_modules", false); err != nil {
		t.Fatalf("DotsSetEntryIgnored include: %v", err)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "node_modules" || group.Dots[0].Ignored {
		t.Fatalf("machine group dots = %#v, want included node_modules", cfg.Groups)
	}
}

func TestDiscoverDotsStatus_CountExcludesIgnoredAndTracked(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost.local")
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "zshrc", Path: "~/.zshrc"}}, home)

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	if result.DiscoveredCount != 1 {
		t.Fatalf("DiscoveredCount = %d, want only visible non-ignored transient candidate", result.DiscoveredCount)
	}
	seenIgnored := false
	seenTracked := false
	for _, entry := range result.Entries {
		if entry.Name == "node_modules" && entry.State == app.DotStateIgnored {
			seenIgnored = true
		}
		if entry.Name == "zshrc" && entry.Group == "base" {
			seenTracked = true
		}
	}
	if !seenIgnored || !seenTracked {
		t.Fatalf("entries = %#v, want ignored node_modules and tracked zshrc present", result.Entries)
	}
}

func TestDotGroupMemberships_UpdateConfigAndListOnce(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: target}}, home)

	if err := a.AddDotToGroup("nvim", "work"); err != nil {
		t.Fatalf("AddDotToGroup: %v", err)
	}
	memberships, err := a.DotMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("DotMembershipMap: %v", err)
	}
	if !reflect.DeepEqual(memberships["nvim"], []string{"base", "work"}) {
		t.Fatalf("memberships = %v, want [base work]", memberships["nvim"])
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Group != "base,work" {
		t.Fatalf("statuses = %#v, want one compact multi-group row", statuses)
	}
	if err := a.RemoveDotFromGroup("nvim", "base"); err != nil {
		t.Fatalf("RemoveDotFromGroup: %v", err)
	}
	memberships, err = a.DotMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("DotMembershipMap after remove: %v", err)
	}
	if !reflect.DeepEqual(memberships["nvim"], []string{"work"}) {
		t.Fatalf("memberships after remove = %v, want [work]", memberships["nvim"])
	}
}

func testContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertNoOldDotTempInHome(t *testing.T, home string) {
	t.Helper()
	if err := filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(filepath.Base(path), ".old-") {
			t.Fatalf("old source temp path leaked into HOME: %s", path)
		}
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, readErr := os.Readlink(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(target, ".old-") {
			t.Fatalf("old source temp symlink leaked into HOME: %s -> %s", path, target)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk HOME looking for old source temps: %v", err)
	}
}

func shortDotsTempDir(t *testing.T, prefix string) string {
	t.Helper()
	root := os.TempDir()
	if _, err := os.Stat("/private/tmp"); err == nil {
		root = "/private/tmp"
	}
	dir, err := os.MkdirTemp(root, prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func setDotsRepoForTest(t *testing.T, cfgDir, repoDir string) {
	t.Helper()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Settings.DotsRepo = repoDir
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

func makeIgnoredSpecialFile(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo %q: %v", path, err)
	}
}

func TestDotsSync_DoesNotBootstrapLocalCandidates(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	kittyPath := filepath.Join(home, ".config", "kitty")
	if err := os.MkdirAll(kittyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kittyPath, "kitty.conf"), []byte("font_size 13"), 0o644); err != nil {
		t.Fatal(err)
	}

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops = %v, want none for untracked local candidate", ops)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "testhost")
	if group != nil && len(group.Dots) != 0 {
		t.Fatalf("machine group dots = %#v, want no auto-added kitty entry", group)
	}
	repoFile := filepath.Join(dotsContentDir(repoDir), "kitty", ".config", "kitty", "kitty.conf")
	if _, err := os.Stat(repoFile); !os.IsNotExist(err) {
		t.Fatalf("repo file exists after sync-all: %v", err)
	}
}

func TestDotsSync_DryRunDoesNotBootstrapCandidates(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DotsSync dry-run: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("dry-run ops = %v, want none for unconfigured candidates", ops)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if group := findDotsTestGroup(cfg.Groups, "testhost"); group != nil && len(group.Dots) > 0 {
		t.Fatalf("dry-run should not persist bootstrap candidates, got %#v", group.Dots)
	}
	if _, err := os.Stat(dotsContentDir(repoDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not create dotfiles content dir, stat err=%v", err)
	}
}

func TestDotsSync_TestModeRejectsNonTempHome(t *testing.T) {
	if testguard.Isolated() {
		t.Skip("Docker-isolated tests do not enforce local live-path rejection")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", cwd)
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	_, err = a.DotsSync(dots.SyncOptions{})
	if err == nil {
		t.Fatal("DotsSync should reject non-temp HOME in InitTestMode")
	}
	if !strings.Contains(err.Error(), "unsafe local test setup") {
		t.Fatalf("DotsSync error = %v, want unsafe local test setup", err)
	}
}

func findDotsTestGroup(groups []*config.GroupConfig, name string) *config.GroupConfig {
	for _, group := range groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

// writeGroupWithDots writes dot entries to the base group in settings.json.
// The config file must already exist (created by newDotsApp or newDotsAppWithGitCfg).
// Callers must set t.Setenv("HOME", home) before this call and before any
// App method that invokes dots.New (so path derivation uses the temp home).
func writeGroupWithDots(t *testing.T, cfgDir, _ string, entries []config.DotEntry, _ string) string {
	t.Helper()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// Find or create the base group.
	var baseGroup *config.GroupConfig
	for _, g := range rootCfg.Groups {
		if g.IsBase() {
			baseGroup = g
			break
		}
	}
	if baseGroup == nil {
		baseGroup = &config.GroupConfig{}
		rootCfg.Groups = append(rootCfg.Groups, baseGroup)
	}
	baseGroup.Dots = entries

	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	return cfgPath
}

func TestDotsConfigured_False(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.DotsConfigured() {
		t.Error("expected DotsConfigured() == false when dots_repo not set")
	}
}

func TestDotsConfigured_True(t *testing.T) {
	a, _, _ := newDotsApp(t)
	if !a.DotsConfigured() {
		t.Error("expected DotsConfigured() == true when dots_repo is set")
	}
}

func TestDotsAddIgnorePattern_AppendsToExistingEntry(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsAddIgnorePattern("nvim", "*.log"); err != nil {
		t.Fatalf("DotsAddIgnorePattern: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Groups[0].Dots[0].Ignore
	if len(got) != 1 || got[0] != "*.log" {
		t.Fatalf("ignore = %v, want [*.log]", got)
	}
}

func TestDotsAddIgnorePattern_DuplicateIsNoop(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Ignore: []string{"*.log"}}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsAddIgnorePattern("nvim", "*.log"); err != nil {
		t.Fatalf("DotsAddIgnorePattern: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Groups[0].Dots[0].Ignore
	if len(got) != 1 || got[0] != "*.log" {
		t.Fatalf("ignore = %v, want single [*.log]", got)
	}
}

func TestDotsDeleteIgnorePattern_RemovesExistingEntry(t *testing.T) {
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Ignore: []string{"cache", "*.log"}}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsRemoveIgnorePattern("nvim", "cache"); err != nil {
		t.Fatalf("DotsRemoveIgnorePattern: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Groups[0].Dots[0].Ignore
	if len(got) != 1 || got[0] != "*.log" {
		t.Fatalf("ignore = %v, want [*.log]", got)
	}
}

func TestDotsSync_LinksFiles(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stow package tree: repoDir/nvim/.config/nvim/init.lua
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create real ~/.config so stow folds at nvim level (not config level).
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpLink {
		t.Errorf("got ops %v, want [OpLink]", ops)
	}
	// Stow creates a directory-level symlink (may be relative); resolve both
	// sides via EvalSymlinks to handle OS-level indirections (e.g. /var→/private/var on macOS).
	resolved, err := filepath.EvalSymlinks(nvimPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(nvimPath): %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(srcDir): %v", err)
	}
	if resolved != wantResolved {
		t.Errorf("symlink → %q, want %q", resolved, wantResolved)
	}
}

func TestDotsSync_DryRun(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stow package tree: repoDir/git/.config/git/config
	srcDir := filepath.Join(dotsContentDir(repoDir), "git", ".config", "git")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "config"), []byte("[core]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	gitPath := filepath.Join(home, ".config", "git")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "git", Path: gitPath},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DotsSync dry-run: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpDryLink {
		t.Errorf("got ops %v, want [OpDryLink]", ops)
	}
	// Dry-run: no symlink should exist.
	if _, err := os.Lstat(gitPath); !os.IsNotExist(err) {
		t.Error("dry-run: symlink should not have been created")
	}
}

func TestDotsSyncEntry_DryRunDoesNotCreateContentDir(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	targetPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetPath},
	}, home)

	_, _ = a.DotsSyncEntry(context.Background(), "nvim", dots.SyncOptions{DryRun: true})
	if _, err := os.Lstat(dotsContentDir(repoDir)); !os.IsNotExist(err) {
		t.Fatalf("dry-run created content dir, stat err=%v", err)
	}
}

func TestDotsSync_RefusesIgnoredRepoSource(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), []byte(`{"secret":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetPath},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err == nil {
		t.Fatal("DotsSync returned nil, want conflict for ignored repo source")
	}
	if !strings.Contains(err.Error(), "auth.json") || !strings.Contains(err.Error(), "refusing to stow") {
		t.Fatalf("DotsSync error = %q, want ignored path refusal", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want one conflict", ops)
	}
	if _, err := os.Lstat(filepath.Join(targetPath, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("ignored auth.json should not be linked into HOME, stat err=%v", err)
	}
}

func TestDotsSync_RefusesIgnoredRepoSourceWhenAlreadySynced(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetPath},
	}, home)
	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("initial DotsSync: %v", err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "secret.log"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetPath, Ignore: []string{"*.log"}},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err == nil {
		t.Fatal("DotsSync returned nil, want conflict for ignored source on synced entry")
	}
	if !strings.Contains(err.Error(), "secret.log") || !strings.Contains(err.Error(), "refusing to stow") {
		t.Fatalf("DotsSync error = %q, want ignored path refusal", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want one conflict", ops)
	}
}

func TestDotsSyncEntry_LocalOnlyCopiesRepoBacksUpAndLinks(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsSyncEntry(context.Background(), "nvim", dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSyncEntry: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want OpAdopt", ops)
	}
	sourceFile := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if got, err := os.ReadFile(sourceFile); err != nil || string(got) != "-- local" {
		t.Fatalf("source file = %q, %v; want copied local content", got, err)
	}
	backupFile := filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")
	if got, err := os.ReadFile(backupFile); err != nil || string(got) != "-- local" {
		t.Fatalf("backup file = %q, %v; want copied local content", got, err)
	}
	resolved, err := filepath.EvalSymlinks(nvimPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(target): %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(filepath.Dir(sourceFile))
	if err != nil {
		t.Fatalf("EvalSymlinks(source): %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("target resolves to %q, want %q", resolved, wantResolved)
	}
}

func TestDotsSyncEntry_LocalOnlyRefusesSymlinkAdoption(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	externalDir := t.TempDir()
	externalTarget := filepath.Join(externalDir, "real.gitconfig")
	if err := os.WriteFile(externalTarget, []byte("[user]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(home, ".gitconfig")
	if err := os.Symlink(externalTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "gitconfig", Path: linkPath},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err == nil {
		t.Fatal("DotsSync returned nil, want conflict for symlink adoption")
	}
	if !strings.Contains(err.Error(), "refusing to adopt") {
		t.Fatalf("DotsSync error = %q, want refusing to adopt", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want one conflict", ops)
	}
	repoPath := filepath.Join(dotsContentDir(repoDir), "gitconfig", ".gitconfig")
	if _, err := os.Lstat(repoPath); !os.IsNotExist(err) {
		t.Fatalf("repo source should not be created from symlink target, stat err=%v", err)
	}
}

func TestDotsSyncEntry_ConflictRequiresChoice(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsSyncEntry(context.Background(), "nvim", dots.SyncOptions{})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want OpConflict", ops)
	}
	if _, statErr := os.Stat(filepath.Join(home, dots.BackupDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("backup dir exists after choice-based conflict: %v", statErr)
	}
}

func TestDotsList_HealthOK(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// SourcePath = repoDir/nvim/.config/nvim; TargetPath = home/.config/nvim
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create stow-managed directory symlink.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcDir, filepath.Join(home, ".config", "nvim")); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].Health != app.HealthOK {
		t.Errorf("health = %v, want HealthOK", statuses[0].Health)
	}
	if statuses[0].FileCount != 1 {
		t.Errorf("file count = %d, want 1", statuses[0].FileCount)
	}
}

func TestDotsList_FileCountAndChildrenSkipIgnoredPaths(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	repoDir := shortDotsTempDir(t, "omni-dots-repo-")
	setDotsRepoForTest(t, cfgDir, repoDir)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(srcDir, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeIgnoredSpecialFile(t, filepath.Join(srcDir, "agent.sock"))
	files := map[string]string{
		filepath.Join(srcDir, "init.lua"):                      "-- cfg",
		filepath.Join(srcDir, "lua", "config.lua"):             "-- lua",
		filepath.Join(srcDir, "lua", "auth.json"):              "secret",
		filepath.Join(srcDir, "node_modules", "pkg", "mod.js"): "module",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcDir, filepath.Join(home, ".config", "nvim")); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].FileCount != 2 {
		t.Fatalf("file count = %d, want 2", statuses[0].FileCount)
	}
	childNames := make([]string, 0, len(statuses[0].Children))
	ignoredChildren := make(map[string]bool)
	for _, child := range statuses[0].Children {
		childNames = append(childNames, child.Name)
		ignoredChildren[child.Name] = child.Ignored
	}
	if !reflect.DeepEqual(childNames, []string{"lua", "auth.json", "config.lua", "node_modules", "init.lua"}) {
		t.Fatalf("children = %v, want depth-4 flattened children", childNames)
	}
	if !ignoredChildren["node_modules"] {
		t.Fatalf("node_modules child should be listed as ignored: %#v", statuses[0].Children)
	}
	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	foundIgnoredSectionEntry := false
	for _, entry := range result.Entries {
		if entry.Name == "nvim/node_modules" && entry.State == app.DotStateIgnored {
			foundIgnoredSectionEntry = true
			break
		}
	}
	if !foundIgnoredSectionEntry {
		t.Fatalf("DiscoverDotsStatus entries = %#v, want ignored nvim/node_modules row", result.Entries)
	}
}

func TestDotsSync_LocalOnlyDirectorySkipsSocketFiles(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, _ := newDotsApp(t)
	repoDir := shortDotsTempDir(t, "omni-dots-repo-")
	setDotsRepoForTest(t, cfgDir, repoDir)
	home := shortDotsTempDir(t, "omni-dots-home-")
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeIgnoredSpecialFile(t, filepath.Join(nvimPath, "agent.sock"))

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")); err != nil {
		t.Fatalf("repo regular file missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "agent.sock")); !os.IsNotExist(err) {
		t.Fatalf("socket should not be copied into repo, stat err=%v", err)
	}
}

func TestDotsList_HealthMissing(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source dir exists but no symlink at target yet → HealthMissing.
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthMissing {
		t.Errorf("health = %v, want HealthMissing", statuses[0].Health)
	}
}

func TestDotsList_HealthNoSource(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// No source dir in repo → HealthNoSource.
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthNoSource {
		t.Errorf("health = %v, want HealthNoSource", statuses[0].Health)
	}
}

func TestDotsList_LocalOnlyDirectoryShowsChildren(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	localDir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(localDir, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "lua", "config.lua"), []byte("-- lua"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: localDir},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if statuses[0].State != app.DotStateLocalOnly {
		t.Fatalf("state = %q, want local-only", statuses[0].State)
	}
	if statuses[0].FileCount != 2 {
		t.Fatalf("file count = %d, want 2", statuses[0].FileCount)
	}
	childNames := make([]string, 0, len(statuses[0].Children))
	for _, child := range statuses[0].Children {
		childNames = append(childNames, child.Name)
	}
	if !reflect.DeepEqual(childNames, []string{"lua", "config.lua", "init.lua"}) {
		t.Fatalf("children = %v, want depth-4 flattened children", childNames)
	}
}

func TestDotsList_IgnoresPackageTreeAtRepoRoot(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	rootSrc := filepath.Join(repoDir, "nvim", ".config", "nvim")
	if err := os.MkdirAll(rootSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootSrc, "init.lua"), []byte("-- should be ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthNoSource {
		t.Fatalf("health = %v, want HealthNoSource for repo-root package tree", statuses)
	}
}

func TestDotsDelete_RemovesSymlinkAndEntry(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stow package tree: repoDir/nvim/.config/nvim
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create real ~/.config so stow folds at nvim level.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	// Use DotsSync to create the stow-managed relative symlink.
	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "node_modules", "pkg", "mod.js"), []byte("module"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeIgnoredSpecialFile(t, filepath.Join(srcDir, "agent.sock"))

	if err := a.DotsDelete(context.Background(), "nvim"); err != nil {
		t.Fatalf("DotsDelete: %v", err)
	}

	if info, err := os.Lstat(nvimPath); err != nil {
		t.Fatalf("expected local directory to be restored: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("expected restored local directory, got mode %v", info.Mode())
	}
	if body, err := os.ReadFile(filepath.Join(nvimPath, "init.lua")); err != nil {
		t.Fatalf("expected copied regular file: %v", err)
	} else if string(body) != "-- cfg" {
		t.Fatalf("copied file = %q, want %q", body, "-- cfg")
	}
	if _, err := os.Lstat(filepath.Join(nvimPath, "agent.sock")); !os.IsNotExist(err) {
		t.Fatalf("ignored socket should not be copied back, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(nvimPath, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("ignored node_modules should not be copied back, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(dotsContentDir(repoDir), "nvim")); !os.IsNotExist(err) {
		t.Fatalf("repo package should be removed, stat err=%v", err)
	}

	backupPath := filepath.Join(home, dots.BackupDirName, ".config", "nvim")
	if info, err := os.Lstat(backupPath); err != nil {
		t.Fatalf("expected backup before removing symlink: %v", err)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("backup %q is not a symlink", backupPath)
	}

	// Entry should be gone from settings.json.
	updated, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, g := range updated.Groups {
		for _, d := range g.Dots {
			if d.Name == "nvim" {
				t.Error("expected nvim dot entry to be removed from config")
			}
		}
	}
}

func TestDotsDeleteWithoutKeepLocalRemovesLocalAndRepo(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if err := a.DotsDeleteWithOptions(context.Background(), "nvim", app.DotsDeleteOptions{KeepLocal: false}); err != nil {
		t.Fatalf("DotsDeleteWithOptions: %v", err)
	}
	if _, err := os.Lstat(nvimPath); !os.IsNotExist(err) {
		t.Fatalf("local target should be removed, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(dotsContentDir(repoDir), "nvim")); !os.IsNotExist(err) {
		t.Fatalf("repo package should be removed, stat err=%v", err)
	}

	updated, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, g := range updated.Groups {
		for _, d := range g.Dots {
			if d.Name == "nvim" {
				t.Error("expected nvim dot entry to be removed from config")
			}
		}
	}
}

func TestDotsDeleteRejectsSymlinkedContentDir(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	externalContent := t.TempDir()
	externalPkg := filepath.Join(externalContent, "nvim", ".config", "nvim")
	if err := os.MkdirAll(externalPkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(externalPkg, "init.lua"), []byte("-- external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalContent, dotsContentDir(repoDir)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	err := a.DotsDeleteWithOptions(context.Background(), "nvim", app.DotsDeleteOptions{KeepLocal: false})
	if err == nil {
		t.Fatal("expected symlinked content dir to be rejected")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want symlink rejection", err.Error())
	}
	if _, err := os.Lstat(filepath.Join(externalContent, "nvim")); err != nil {
		t.Fatalf("external package should not be removed: %v", err)
	}

	updated, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(updated.Groups) != 1 || len(updated.Groups[0].Dots) != 1 || updated.Groups[0].Dots[0].Name != "nvim" {
		t.Fatalf("config entry should remain after rejected delete: %+v", updated.Groups)
	}
}

func TestDotsDelete_NotFound(t *testing.T) {
	a, _, _ := newDotsApp(t)
	if err := a.DotsDelete(context.Background(), "nonexistent"); err == nil {
		t.Error("expected error for missing entry")
	}
}

func TestDotsDelete_SkipsUnmanagedSymlink(t *testing.T) {
	// A symlink pointing somewhere other than the repo must not be removed.
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Package must exist in the repo for stow -D to run without error.
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	// Directory-level symlink pointing elsewhere (absolute) — stow ignores it.
	if err := os.Symlink("/some/other/path", nvimPath); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	if err := a.DotsDelete(context.Background(), "nvim"); err != nil {
		t.Fatalf("DotsDelete: %v", err)
	}
	// Unmanaged symlink must remain.
	if _, err := os.Lstat(nvimPath); err != nil {
		t.Error("unmanaged symlink should not have been removed")
	}
}

func TestDotsAdd_ExplicitName(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	liveFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(liveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveFile, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Adopt with an explicit Name — the infer branch must be skipped.
	ops, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Name: "myvim", Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd with explicit name: %v", err)
	}
	if len(ops) == 0 {
		t.Error("expected at least one op")
	}
	// Adopted file must now exist in the repo under the explicit name,
	// mirroring the home directory structure: <repo>/myvim/.config/nvim/init.lua.
	adopted := filepath.Join(dotsContentDir(repoDir), "myvim", ".config", "nvim", "init.lua")
	if _, err := os.Lstat(adopted); err != nil {
		t.Errorf("adopted file not found at %q: %v", adopted, err)
	}
}

func TestDotsAdd_RequiresAdoptBeforeMutatingLocalPath(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	liveFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(liveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveFile, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Name: "myvim"})
	if err == nil || !strings.Contains(err.Error(), "--adopt") {
		t.Fatalf("DotsAdd error = %v, want --adopt guidance", err)
	}
	if got, readErr := os.ReadFile(liveFile); readErr != nil || string(got) != "-- cfg" {
		t.Fatalf("local file changed after non-adopt add: body=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(dotsContentDir(repoDir), "myvim")); !os.IsNotExist(statErr) {
		t.Fatalf("repo package should not be created without adopt, stat err=%v", statErr)
	}
}

func TestDotsAdd_AdoptRejectsNestedSymlinkBeforeMutating(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	liveDir := filepath.Join(home, ".config", "myapp")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(liveDir, "settings.json")
	if err := os.WriteFile(liveFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(home, "external.conf")
	if err := os.WriteFile(externalFile, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalLink := filepath.Join(liveDir, "external.conf")
	if err := os.Symlink(externalFile, externalLink); err != nil {
		t.Fatal(err)
	}

	_, err := a.DotsAdd(context.Background(), liveDir, app.DotsAddOptions{Name: "myapp", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("DotsAdd error = %v, want symlink refusal", err)
	}
	if got, readErr := os.ReadFile(liveFile); readErr != nil || string(got) != "{}" {
		t.Fatalf("local file changed after rejected adopt: body=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(externalLink); statErr != nil {
		t.Fatalf("local symlink removed after rejected adopt: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(dotsContentDir(repoDir), "myapp")); !os.IsNotExist(statErr) {
		t.Fatalf("repo package should not be created after rejected adopt, stat err=%v", statErr)
	}
}

func TestDotsAddRejectsConfiguredDuplicateBeforeMutation(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	liveDir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(liveDir, "init.lua")
	if err := os.WriteFile(liveFile, []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: liveDir},
	}, home)

	emptyPath := t.TempDir()
	t.Setenv("PATH", emptyPath)

	_, err := a.DotsAdd(context.Background(), liveDir, app.DotsAddOptions{Name: "nvim", Adopt: true})
	if err == nil {
		t.Fatal("expected duplicate dots entry to be rejected")
	}
	if !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("DotsAdd error = %v, want already configured", err)
	}
	if _, err := os.Lstat(dotsContentDir(repoDir)); !os.IsNotExist(err) {
		t.Fatalf("content dir should not be created for duplicate add, stat err=%v", err)
	}
	if body, err := os.ReadFile(liveFile); err != nil || string(body) != "-- local" {
		t.Fatalf("local file changed after duplicate add: body=%q err=%v", body, err)
	}
}

func TestDotsAdd_RejectsPathLikeName(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	liveFile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(liveFile, []byte("zsh"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Name: "../evil", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "invalid entry name") {
		t.Fatalf("DotsAdd error = %v, want invalid entry name", err)
	}
	if got, readErr := os.ReadFile(liveFile); readErr != nil || string(got) != "zsh" {
		t.Fatalf("local file changed after invalid name: body=%q err=%v", got, readErr)
	}
}

func TestDotsAdd_AdoptSkipsDefaultIgnoredChildren(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	appDir := filepath.Join(home, ".config", "myapp")
	if err := os.MkdirAll(filepath.Join(appDir, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appDir, "profiles", "work", "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(appDir, "settings.json"):                                   "{}",
		filepath.Join(appDir, "profiles", "work", "config.json"):                 "work",
		filepath.Join(appDir, "profiles", "work", "auth.json"):                   "secret",
		filepath.Join(appDir, "profiles", "work", "node_modules", "pkg", "x.js"): "module",
		filepath.Join(appDir, "node_modules", "pkg", "x.js"):                     "module",
		filepath.Join(appDir, ".git", "config"):                                  "git",
		filepath.Join(appDir, "auth.json"):                                       "secret",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := a.DotsAdd(context.Background(), appDir, app.DotsAddOptions{Adopt: true}); err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}
	repoAppDir := filepath.Join(dotsContentDir(repoDir), "myapp", ".config", "myapp")
	if _, err := os.Lstat(filepath.Join(repoAppDir, "settings.json")); err != nil {
		t.Fatalf("settings.json should be copied into repo: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repoAppDir, "profiles", "work", "config.json")); err != nil {
		t.Fatalf("nested non-ignored file should be copied into repo: %v", err)
	}
	for _, rel := range []string{
		"node_modules",
		".git",
		"auth.json",
		filepath.Join("profiles", "work", "node_modules"),
		filepath.Join("profiles", "work", "auth.json"),
	} {
		if _, err := os.Lstat(filepath.Join(repoAppDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should be skipped while copying into repo, stat err=%v", rel, err)
		}
	}
}

// ─── coverage gap tests ───────────────────────────────────────────────────────

func TestDotsSync_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.DotsSync(dots.SyncOptions{}); err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDotsDelete_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.DotsDelete(context.Background(), "nvim"); err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDotsDelete_TestModeRejectsLiveHome(t *testing.T) {
	if testguard.Isolated() {
		t.Skip("Docker-isolated tests do not enforce local live-path rejection")
	}
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	t.Setenv("HOME", "/Users/not-a-test-home")
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close()

	err := a.DotsDelete(context.Background(), "nvim")
	if err == nil || !strings.Contains(err.Error(), "HOME for dots mutation") {
		t.Fatalf("DotsDelete err = %v, want unsafe HOME guard", err)
	}
}

func TestDotsSync_TestModeRejectsLiveRepoBeforeCreatingContentDir(t *testing.T) {
	if testguard.Isolated() {
		t.Skip("Docker-isolated tests do not enforce local live-path rejection")
	}
	cfgDir := t.TempDir()
	home := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	t.Setenv("HOME", home)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: "/"},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	defer a.Close()

	_, err := a.DotsSync(dots.SyncOptions{})
	if err == nil || !strings.Contains(err.Error(), "dots_repo=") {
		t.Fatalf("DotsSync err = %v, want unsafe dots_repo guard", err)
	}
}

func TestDotsAdd_NonExistentPath(t *testing.T) {
	a, _, _ := newDotsApp(t)
	if _, err := a.DotsAdd(context.Background(), "/nonexistent/path/that/does/not/exist", app.DotsAddOptions{}); err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestDotsList_HealthConflict(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source dir exists at SourcePath.
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Real directory at TargetPath (not a symlink) → conflict.
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthConflict {
		t.Errorf("health = %v, want HealthConflict", statuses[0].Health)
	}
}

func TestDotsList_ClassifiesTrackedStateMatrix(t *testing.T) {
	syncable := []app.DotAction{app.DotActionSync, app.DotActionRemove, app.DotActionIgnore}
	conflict := []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove, app.DotActionIgnore}
	noSource := []app.DotAction{app.DotActionRemove, app.DotActionIgnore}
	healthy := []app.DotAction{app.DotActionRemove, app.DotActionIgnore}

	tests := []struct {
		name        string
		source      bool
		setupTarget func(t *testing.T, sourcePath, targetPath string)
		wantState   app.DotState
		wantActions []app.DotAction
	}{
		{
			name:        "source exists local missing",
			source:      true,
			wantState:   app.DotStateMissing,
			wantActions: syncable,
		},
		{
			name:   "source exists expected link",
			source: true,
			setupTarget: func(t *testing.T, sourcePath, targetPath string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sourcePath, targetPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateSynced,
			wantActions: healthy,
		},
		{
			name:   "source exists local content",
			source: true,
			setupTarget: func(t *testing.T, _, targetPath string) {
				t.Helper()
				if err := os.MkdirAll(targetPath, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateConflict,
			wantActions: conflict,
		},
		{
			name:   "source exists wrong link",
			source: true,
			setupTarget: func(t *testing.T, _, targetPath string) {
				t.Helper()
				other := filepath.Join(filepath.Dir(targetPath), "other")
				if err := os.MkdirAll(other, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, targetPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateConflict,
			wantActions: conflict,
		},
		{
			name:   "source exists broken link",
			source: true,
			setupTarget: func(t *testing.T, _, targetPath string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(filepath.Dir(targetPath), "missing"), targetPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateBroken,
			wantActions: syncable,
		},
		{
			name:        "source missing local missing",
			wantState:   app.DotStateNoSource,
			wantActions: noSource,
		},
		{
			name: "source missing local content",
			setupTarget: func(t *testing.T, _, targetPath string) {
				t.Helper()
				if err := os.MkdirAll(targetPath, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateLocalOnly,
			wantActions: syncable,
		},
		{
			name: "source missing expected link",
			setupTarget: func(t *testing.T, sourcePath, targetPath string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(sourcePath, targetPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateNoSource,
			wantActions: noSource,
		},
		{
			name: "source missing wrong link",
			setupTarget: func(t *testing.T, _, targetPath string) {
				t.Helper()
				other := filepath.Join(filepath.Dir(targetPath), "other")
				if err := os.MkdirAll(other, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, targetPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateLocalOnly,
			wantActions: syncable,
		},
		{
			name: "source missing broken link",
			setupTarget: func(t *testing.T, _, targetPath string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(filepath.Dir(targetPath), "missing"), targetPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState:   app.DotStateNoSource,
			wantActions: noSource,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, cfgDir, repoDir := newDotsApp(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			sourcePath := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
			targetPath := filepath.Join(home, ".config", "nvim")
			if tt.source {
				if err := os.MkdirAll(sourcePath, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tt.setupTarget != nil {
				tt.setupTarget(t, sourcePath, targetPath)
			}
			writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
				{Name: "nvim", Path: targetPath},
			}, home)

			statuses, err := a.DotsList()
			if err != nil {
				t.Fatalf("DotsList: %v", err)
			}
			if len(statuses) != 1 {
				t.Fatalf("got %d statuses, want 1", len(statuses))
			}
			assertDotState(t, statuses[0], tt.wantState, tt.wantActions)
		})
	}
}

func TestDotsStatus_ReturnsEntries(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Source dir exists (HealthMissing — just need the entry to be returned).
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)

	result, err := a.DotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DotsStatus: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(result.Entries))
	}
	if result.Entries[0].Name != "nvim" {
		t.Errorf("entry name = %q, want nvim", result.Entries[0].Name)
	}
	// repoDir is not a git repo, so GitStatus is empty.
	if result.GitStatus != "" {
		t.Errorf("GitStatus = %q, want empty for non-git repo", result.GitStatus)
	}
}

func TestDotsStatus_DoesNotCreateContentDir(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	contentDir := dotsContentDir(repoDir)
	if _, err := os.Lstat(contentDir); !os.IsNotExist(err) {
		t.Fatalf("content dir exists before status, stat err=%v", err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)

	result, err := a.DotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DotsStatus: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(result.Entries))
	}
	if _, err := os.Lstat(contentDir); !os.IsNotExist(err) {
		t.Fatalf("read-only status created content dir, stat err=%v", err)
	}
}

func TestDotsStatus_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.DotsStatus(context.Background()); err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDotsPull_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.DotsPull(context.Background()); err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDotsPull_NonGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	a, _, _ := newDotsApp(t) // repoDir is a plain dir, not a git repo
	if _, err := a.DotsPull(context.Background()); err == nil {
		t.Error("expected error pulling from non-git directory")
	}
}

func TestDotsPush_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.DotsPush(context.Background(), "msg"); err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDotsStatus_WithGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)

	gitCmd := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitCmd("init", "-q")
	gitCmd("config", "user.email", "test@example.com")
	gitCmd("config", "user.name", "Test")

	// Stage a file without committing so git status --short returns output.
	untracked := filepath.Join(repoDir, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, nil, t.TempDir())

	result, err := a.DotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DotsStatus: %v", err)
	}
	// IsRepo() is true → g.Status() was called; untracked file shows in output.
	if result.GitStatus == "" {
		t.Error("expected non-empty GitStatus for repo with untracked file")
	}
}

func TestDotsAdd_WithExistingGroup(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-populate settings.json with an existing dot entry.
	existingSrc := filepath.Join(dotsContentDir(repoDir), "git", "config")
	if err := os.MkdirAll(filepath.Dir(existingSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existingSrc, []byte("[core]"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "git", Path: filepath.Join(home, ".config", "git")},
	}, home)

	// Now add a second entry — DotsAdd must load the existing settings and append.
	liveFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(liveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveFile, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	updated, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	var baseGroup *config.GroupConfig
	for _, g := range updated.Groups {
		if g.IsBase() {
			baseGroup = g
			break
		}
	}
	if baseGroup == nil || len(baseGroup.Dots) != 2 {
		dots := 0
		if baseGroup != nil {
			dots = len(baseGroup.Dots)
		}
		t.Errorf("got %d dots, want 2 (old + new)", dots)
	}
}

func TestDotsPush_EmptyMessageDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// repoDir is not a git repo → push fails, but the empty-message default branch is hit.
	a, _, _ := newDotsApp(t)
	if err := a.DotsPush(context.Background(), ""); err == nil {
		t.Error("expected error pushing from non-git directory")
	}
}

// ─── git config tests ─────────────────────────────────────────────────────────

// newDotsAppWithGitCfg is like newDotsApp but also writes DotsGit settings.
func newDotsAppWithGitCfg(t *testing.T, gitCfg config.DotsGitConfig) (*app.App, string, string) {
	t.Helper()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir, DotsGit: gitCfg},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgDir, repoDir
}

func TestDotsGitConfig_AutoCommit(t *testing.T) {
	// Requires a real git binary.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	a, _, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoCommit: true})

	// Initialise a real bare git repo so CommitAll succeeds.
	gitCmd := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitCmd("init", "-q")
	gitCmd("config", "user.email", "test@example.com")
	gitCmd("config", "user.name", "Test")

	// Create a "live" config file under a fake home .config dir.
	home := t.TempDir()
	t.Setenv("HOME", home)
	liveFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(liveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveFile, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	// DotsAdd with Adopt moves the file into the repo, then syncs and auto-commits.
	_, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	// Verify at least one commit exists in the repo.
	c := exec.Command("git", "log", "--oneline")
	c.Dir = repoDir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Error("expected at least one commit after DotsAdd with auto_commit=true, got none")
	}
}

func TestDotsGitConfig_AutoPush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	a, _, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoPush: true})
	remoteDir := t.TempDir()

	gitCmd := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
	}
	gitCmd(remoteDir, "init", "--bare", "-q")
	gitCmd(repoDir, "init", "-q")
	gitCmd(repoDir, "config", "user.email", "test@example.com")
	gitCmd(repoDir, "config", "user.name", "Test")
	gitCmd(repoDir, "remote", "add", "origin", remoteDir)
	gitCmd(repoDir, "commit", "--allow-empty", "-m", "init")
	gitCmd(repoDir, "push", "-u", "origin", "HEAD")

	home := t.TempDir()
	t.Setenv("HOME", home)
	liveFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(liveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveFile, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Adopt: true}); err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	c := exec.Command("git", "--git-dir", remoteDir, "log", "--oneline", "--all")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git remote log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "dots: add init.lua") {
		t.Fatalf("remote log missing auto-pushed dots commit:\n%s", out)
	}
}

func TestDotsGitConfig_NoAutoCommit(t *testing.T) {
	// Requires a real git binary.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// auto_commit=false (default) — no commit should be made.
	a, _, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoCommit: false})

	gitCmd := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitCmd("init", "-q")
	gitCmd("config", "user.email", "test@example.com")
	gitCmd("config", "user.name", "Test")

	home := t.TempDir()
	t.Setenv("HOME", home)
	liveFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(liveFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveFile, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	// No commits expected — git log should fail (no HEAD).
	c := exec.Command("git", "log", "--oneline")
	c.Dir = repoDir
	if out, err := c.CombinedOutput(); err == nil && len(out) > 0 {
		t.Errorf("expected no commits with auto_commit=false, got:\n%s", out)
	}
}

// ─── DotsResolveConflict tests ────────────────────────────────────────────────

func TestDotsResolveConflict_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseRepo); err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDotsResolveConflict_NotFound(t *testing.T) {
	a, _, _ := newDotsApp(t) // no entries added to config
	if _, err := a.DotsResolveConflict(context.Background(), "does-not-exist", app.DotResolveUseRepo); err == nil {
		t.Error("expected error for unknown dots entry name")
	}
}

func TestDotsResolveConflict_UseRepoBacksUpLocalAndRelinks(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stow package tree: repoDir/nvim/.config/nvim/init.lua
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a conflicting real directory at the target path.
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseRepo)
	if err != nil {
		t.Fatalf("DotsResolveConflict: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpRepair {
		t.Fatalf("ops = %v, want OpRepair", ops)
	}

	// The target path should now be a directory-level symlink.
	fi, err := os.Lstat(nvimPath)
	if err != nil {
		t.Fatalf("Lstat %q: %v", nvimPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%q is not a symlink after DotsFixConflict", nvimPath)
	}

	backupDir := filepath.Join(home, dots.BackupDirName, ".config", "nvim")
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		t.Errorf("backup %q not found after DotsFixConflict", backupDir)
	}
	got, err := os.ReadFile(filepath.Join(backupDir, "init.lua"))
	if err != nil {
		t.Errorf("backup init.lua missing: %v", err)
	}
	if string(got) != "-- existing" {
		t.Errorf("backup content = %q, want -- existing", string(got))
	}
}

func TestDotsResolveConflict_UseRepoRefusesIgnoredSourceBeforeRemovingLocal(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "secret.log"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath, Ignore: []string{"*.log"}},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseRepo)
	if err == nil {
		t.Fatal("DotsResolveConflict returned nil, want ignored source error")
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want one conflict", ops)
	}
	got, readErr := os.ReadFile(filepath.Join(nvimPath, "init.lua"))
	if readErr != nil {
		t.Fatalf("local target was removed before validation: %v", readErr)
	}
	if string(got) != "-- local" {
		t.Fatalf("local target content = %q, want -- local", got)
	}
}

func TestDotsResolveConflict_UseLocalCommitsRepoCopiesLocalAndRelinks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	gitCmd := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repoDir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitCmd("init", "-q")
	gitCmd("config", "user.email", "test@example.com")
	gitCmd("config", "user.name", "Test")

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", "-A")
	gitCmd("commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- dirty repo"), 0o644); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseLocal)
	if err != nil {
		t.Fatalf("DotsResolveConflict use-local: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want OpAdopt", ops)
	}
	if got, err := os.ReadFile(filepath.Join(srcDir, "init.lua")); err != nil || string(got) != "-- local" {
		t.Fatalf("repo source = %q, %v; want local content", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")); err != nil || string(got) != "-- local" {
		t.Fatalf("backup = %q, %v; want local content", got, err)
	}
	resolved, err := filepath.EvalSymlinks(nvimPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(target): %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(source): %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("target resolves to %q, want %q", resolved, wantResolved)
	}
	assertNoOldDotTempInHome(t, home)
	c := exec.Command("git", "-C", repoDir, "log", "--oneline", "-1")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "dots: pre-resolve nvim") {
		t.Fatalf("latest commit = %q, want pre-resolve commit", out)
	}
}

func TestDotsResolveConflict_UseLocalRefusesIgnoredSourceBeforeRemovingLocal(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcFile := filepath.Join(dotsContentDir(repoDir), "applog", ".app.log")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	localFile := filepath.Join(home, ".app.log")
	if err := os.WriteFile(localFile, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "applog", Path: localFile, Ignore: []string{"*.log"}},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "applog", app.DotResolveUseLocal)
	if err == nil {
		t.Fatal("DotsResolveConflict returned nil, want ignored source error")
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want one conflict", ops)
	}
	if got, readErr := os.ReadFile(localFile); readErr != nil || string(got) != "local" {
		t.Fatalf("local target changed after validation failure: body=%q err=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(srcFile); readErr != nil || string(got) != "repo" {
		t.Fatalf("repo source not rolled back after validation failure: body=%q err=%v", got, readErr)
	}
}

// ─── DotsDisable ─────────────────────────────────────────────────────────────

func TestDotsDisable_UnlinksManagedSymlinks(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	const content = "-- nvim init"

	// Stow package tree: repoDir/nvim/.config/nvim/init.lua
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-create real ~/.config so stow folds at nvim level.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	// Create the managed symlink first.
	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}

	ops, err := a.DotsDisable(app.DisableDotsOptions{})
	if err != nil {
		t.Fatalf("DotsDisable: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Errorf("got ops %v, want [OpUnlink]", ops)
	}

	// nvimPath should now be a real directory (not a symlink) with the repo content.
	fi, err := os.Lstat(nvimPath)
	if err != nil {
		t.Fatalf("Lstat %q: %v", nvimPath, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("nvimPath is still a symlink after DotsDisable")
	}
	dstFile := filepath.Join(nvimPath, "init.lua")
	got, _ := os.ReadFile(dstFile)
	if string(got) != content {
		t.Errorf("file content = %q, want %q", string(got), content)
	}
}

func TestDotsDisable_ConflictKeepLocal(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stow path for ~/.zshrc: repoDir/zsh/.zshrc
	srcFile := filepath.Join(dotsContentDir(repoDir), "zsh", ".zshrc")
	dstFile := filepath.Join(home, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("# repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Place a real (non-managed) file at the destination.
	if err := os.WriteFile(dstFile, []byte("# local"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zsh", Path: dstFile},
	}, home)

	ops, err := a.DotsDisable(app.DisableDotsOptions{ConflictOverwrite: false})
	if err != nil {
		t.Fatalf("DotsDisable: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlinkConflict {
		t.Errorf("got ops %v, want [OpUnlinkConflict]", ops)
	}
	// Local file must be preserved.
	got, _ := os.ReadFile(dstFile)
	if string(got) != "# local" {
		t.Errorf("local file overwritten: content = %q", string(got))
	}
}

func TestDotsDisable_ConflictOverwrite(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stow path for ~/.zshrc: repoDir/zsh/.zshrc
	srcFile := filepath.Join(dotsContentDir(repoDir), "zsh", ".zshrc")
	dstFile := filepath.Join(home, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("# repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstFile, []byte("# local"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zsh", Path: dstFile},
	}, home)

	ops, err := a.DotsDisable(app.DisableDotsOptions{ConflictOverwrite: true})
	if err != nil {
		t.Fatalf("DotsDisable: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Errorf("got ops %v, want [OpUnlink]", ops)
	}
	// File must have been overwritten with repo content.
	got, _ := os.ReadFile(dstFile)
	if string(got) != "# repo" {
		t.Errorf("file not overwritten: content = %q, want # repo", string(got))
	}
	backupFile := filepath.Join(home, dots.BackupDirName, ".zshrc")
	backup, err := os.ReadFile(backupFile)
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(backup) != "# local" {
		t.Errorf("backup content = %q, want # local", string(backup))
	}
}

func TestDotsDisable_RemoveLocal(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcFile := filepath.Join(dotsContentDir(repoDir), "zsh", ".zshrc")
	dstFile := filepath.Join(home, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("# repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcFile, dstFile); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zsh", Path: dstFile},
	}, home)

	ops, err := a.DotsDisable(app.DisableDotsOptions{RemoveLocal: true})
	if err != nil {
		t.Fatalf("DotsDisable: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Errorf("got ops %v, want [OpUnlink]", ops)
	}
	if _, err := os.Lstat(dstFile); !os.IsNotExist(err) {
		t.Fatalf("local target exists after RemoveLocal disable: %v", err)
	}
}

func TestDisableDotsForHost_PersistsDisabledAfterUnlinkError(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcFile := filepath.Join(dotsContentDir(repoDir), "zsh", ".zshrc")
	dstFile := filepath.Join(home, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("# repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstFile, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zsh", Path: dstFile},
	}, home)

	_, err := a.DisableDotsForHost(context.Background(), app.DisableDotsOptions{ConflictOverwrite: true})
	if err == nil {
		t.Fatal("expected unlink error")
	}
	cfg, loadErr := config.Load(filepath.Join(cfgDir, "settings.json"))
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	hostname, _ := os.Hostname()
	short := shortHostnameForTest(hostname)
	if !config.BoolVal(cfg.HostSettings[short].DotsDisabled) {
		t.Fatal("DotsDisabled should be true after DisableDotsForHost partial failure")
	}
}

func TestDotsDisable_NotConfigured(t *testing.T) {
	cfgDir := t.TempDir()
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	_, err := a.DotsDisable(app.DisableDotsOptions{})
	if err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

// ─── additional coverage tests ────────────────────────────────────────────────

// TestDotsSyncEntry_StowNotOnPath tests the "stow not installed" branch of
// requireStow by hiding all binaries from PATH.
func TestDotsSyncEntry_StowNotOnPath(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a minimal stow package tree so the entry lookup succeeds and
	// requireStow is the first thing that would reject the call.
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	// Hide stow (and everything else) by pointing PATH to an empty directory.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	_, err := a.DotsSyncEntry(context.Background(), "nvim", dots.SyncOptions{})
	if err == nil {
		t.Fatal("expected error when stow is not on PATH, got nil")
	}
	if !strings.Contains(err.Error(), "stow is required") {
		t.Errorf("error %q does not contain %q", err.Error(), "stow is required")
	}
}

type stowInstallerProvider struct {
	binDir string
}

func (p *stowInstallerProvider) Name() string                                   { return "brew" }
func (p *stowInstallerProvider) Description() string                            { return "brew stub" }
func (p *stowInstallerProvider) Available(context.Context) (bool, error)        { return true, nil }
func (p *stowInstallerProvider) Uninstall(context.Context, provider.Tool) error { return nil }
func (p *stowInstallerProvider) Upgrade(context.Context, provider.Tool) error   { return nil }
func (p *stowInstallerProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}
func (p *stowInstallerProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return true, "1.0.0", nil
}
func (p *stowInstallerProvider) Install(_ context.Context, tool provider.Tool) error {
	if tool.Name != "stow" {
		return fmt.Errorf("unexpected install %q", tool.Name)
	}
	path := filepath.Join(p.binDir, "stow")
	return os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)
}

func TestInstallDotsStow_InstallsThroughSystemProvider(t *testing.T) {
	cfgDir := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	a := app.New(filepath.Join(cfgDir, "settings.json"))
	if err := a.InitTestMode(context.Background(), &stowInstallerProvider{binDir: binDir}); err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if a.DotsStowInstalled(context.Background()) {
		t.Fatal("stow should not be available before install")
	}
	if err := a.InstallDotsStow(context.Background()); err != nil {
		t.Fatalf("InstallDotsStow: %v", err)
	}
	if !a.DotsStowInstalled(context.Background()) {
		t.Fatal("stow should be available after install")
	}
}

// TestDotsDelete_EntryNotFound tests that DotsDelete returns a descriptive error
// when the named entry does not exist in any configured group.
func TestDotsDelete_EntryNotFound(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, _, _ := newDotsApp(t) // configured repo, no entries
	err := a.DotsDelete(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
	if !strings.Contains(err.Error(), `"ghost" not found`) {
		t.Errorf("error %q does not mention entry name", err.Error())
	}
}

// TestDotsAdd_PathDoesNotExist tests that DotsAdd returns an error when the
// given path does not exist on disk (expandAndStat error path).
func TestDotsAdd_PathDoesNotExist(t *testing.T) {
	a, _, _ := newDotsApp(t)
	_, err := a.DotsAdd(context.Background(), "/this/path/does/not/exist/at/all", app.DotsAddOptions{})
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

// TestDotsAdd_BackupDirectoryBranch exercises the directory backup path
// indirectly via DotsAdd with a directory as the source path. The whole
// directory tree must be copied into ~/dotfiles.bkp.
func TestBackupPath_DirectoryBranch(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a directory tree under home that will be adopted.
	nvimDir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(nvimDir, "lua", "user")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "config.lua"), []byte("return {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// DotsAdd with Adopt moves the directory; BackupLocalPath copies it first.
	_, err := a.DotsAdd(context.Background(), nvimDir, app.DotsAddOptions{Name: "nvim", Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	backupDir := filepath.Join(home, dots.BackupDirName, ".config", "nvim")
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		t.Errorf("backup directory %q not created", backupDir)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "init.lua")); err != nil {
		t.Errorf("init.lua missing from backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "lua", "user", "config.lua")); err != nil {
		t.Errorf("nested config.lua missing from backup: %v", err)
	}
}

func TestDotsSync_SkipsNoSourceWithoutChoice(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	missingTarget := filepath.Join(home, ".config", "ghost", "conf")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "ghost", Path: missingTarget},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: false})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpSkip {
		t.Fatalf("ops = %v, want OpSkip for no-source entry", ops)
	}
}

// TestLstatOp_DryRun_ExistingManagedSymlink exercises the lstatOp dryRun=true
// branch where the target IS a symlink pointing into the repo (OpSkip).
func TestLstatOp_DryRun_ExistingManagedSymlink(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	// First sync to create the real symlink.
	if _, err := a.DotsSync(dots.SyncOptions{DryRun: false}); err != nil {
		t.Fatalf("DotsSync (first): %v", err)
	}

	// Second sync in dry-run mode: symlink already exists → OpSkip.
	ops, err := a.DotsSync(dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DotsSync dry-run: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpSkip {
		t.Errorf("got ops %v, want [OpSkip]", ops)
	}
}

func TestDotsSync_RealFileAtTargetReportsChoiceConflict(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-create a real directory at the target path (conflict condition).
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: false})
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpConflict {
		t.Fatalf("ops = %v, want OpConflict", ops)
	}
}

func TestDotsSync_ContinuesAfterEntryConflict(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	nvimSrc := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(nvimSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimSrc, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	zshSrc := filepath.Join(dotsContentDir(repoDir), "zshrc")
	if err := os.MkdirAll(zshSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zshSrc, ".zshrc"), []byte("export ZDOTDIR=$HOME"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
		{Name: "zshrc", Path: filepath.Join(home, ".zshrc")},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: false})
	if err == nil {
		t.Fatal("expected aggregate sync error for nvim conflict")
	}
	errText := err.Error()
	if !strings.Contains(errText, "nvim:") || !strings.Contains(errText, "requires choosing") {
		t.Fatalf("error = %q, want named conflict detail", errText)
	}
	if strings.Contains(errText, "zshrc:") {
		t.Fatalf("error = %q, should not include successfully synced zshrc", errText)
	}
	if len(ops) < 2 {
		t.Fatalf("ops = %v, want conflict op plus continued sync op", ops)
	}
	if info, statErr := os.Lstat(filepath.Join(home, ".zshrc")); statErr != nil {
		t.Fatalf("zshrc should be linked despite nvim conflict: %v", statErr)
	} else if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("zshrc should be a symlink despite nvim conflict, mode=%v", info.Mode())
	}
}

// TestDotsSync_ProfileFiltering verifies that DotsSync only syncs the groups
// belonging to the active profile (as determined by the hostname→profile
// mapping), not all groups.
//
// Strategy: two groups — "mac" (has a dot entry) and "empty-group" (no dots).
// A profile "macbook" covers only "empty-group". When the hostname maps to
// "macbook", collectDots should receive only the empty group and return zero
// entries, causing DotsSync to return nil without invoking stow.
// Without the profile-awareness fix, collectDots would receive all groups
// (including "mac") and attempt to run stow.
func TestDotsSync_ProfileFiltering(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	// Create the stow package source so it's valid if stow were called.
	srcDir := filepath.Join(dotsContentDir(repoDir), "vimrc")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".vimrc"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	vimrcPath := filepath.Join(home, ".vimrc")

	// Config: "mac" group has a dot entry; "empty-group" has none.
	// Profile "macbook" covers only "empty-group".
	// Hostname "testhost" maps to "macbook".
	cfgPath := filepath.Join(cfgDir, "settings.json")
	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: "mac", Dots: []config.DotEntry{{Name: "vimrc", Path: vimrcPath}}},
		{Name: "empty-group"},
	}
	rootCfg.Profiles = map[string]config.Profile{
		"macbook": {Groups: []string{"empty-group"}},
	}
	rootCfg.Hostnames = map[string]string{"testhost": "macbook"}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	// Active profile covers only "empty-group" (no dots) → zero ops.
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (profile filtering should exclude mac group); ops=%v", len(ops), ops)
	}
}
