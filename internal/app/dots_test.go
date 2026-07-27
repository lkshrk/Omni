package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

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

func assertDotState(t *testing.T, got app.DotStatus, wantState dots.State, wantActions []dots.Action) {
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
	if err := os.MkdirAll(filepath.Join(home, ".docker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".kube"), 0o700); err != nil {
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
	for _, name := range []string{"docker", "kube"} {
		if _, ok := byName[name]; ok {
			t.Fatalf("sensitive default %s should not be discovered: %#v", name, byName)
		}
	}
}

func TestDiscoverDotsEntries_IgnoresAgentsSkillLockFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()

	agentsDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, ".skill-lock.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	for _, entry := range got {
		if entry.Name == "agents-skill-lock" {
			t.Fatalf("agent-managed skill lock should not be proposed as a dot: %#v", entry)
		}
	}
}

func TestDiscoverDotsEntries_IgnoresLegacySkillLockRepoPackages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	for _, name := range []string{"agents-skill-lock", "skill-lock.json"} {
		pkgDir := filepath.Join(dotsContentDir(repoDir), name, ".agents")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, ".skill-lock.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	for _, entry := range got {
		if entry.Name == "agents-skill-lock" || entry.Name == "skill-lock.json" {
			t.Fatalf("legacy skill-lock package should not be proposed as a dot: %#v", entry)
		}
	}
}

func TestDiscoverDotsEntries_AddsClaudeAllowlistIgnores(t *testing.T) {
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
	for _, want := range []string{
		"*",
		"!/settings.json",
		"!/CLAUDE.md",
		"!/agents/",
		"!/hooks/",
		"!/scripts/",
		"!/.omc-config.json",
		"!/keybindings.json",
		"!/statusline-command.sh",
	} {
		if !testContainsString(claude.Ignore, want) {
			t.Fatalf("claude ignore = %v, missing %q", claude.Ignore, want)
		}
	}
	for _, managed := range []string{
		"!/mcp.json",
		"!/plugins",
		"!/plugins/installed_plugins.json",
		"!/plugins/configured_plugins.json",
		"!/skills/",
	} {
		if testContainsString(claude.Ignore, managed) {
			t.Fatalf("claude ignore = %v, should not track agent-managed path %q", claude.Ignore, managed)
		}
	}
	for _, duplicateGlobal := range []string{"*.log", ".DS_Store", "cache"} {
		if testContainsString(claude.Ignore, duplicateGlobal) {
			t.Fatalf("claude ignore = %v, should not duplicate global ignore %q", claude.Ignore, duplicateGlobal)
		}
	}
}

func TestDiscoverDotsEntries_AddsCodexAllowlistIgnores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	var codex config.DotEntry
	for _, entry := range got {
		if entry.Name == "codex" {
			codex = entry
			break
		}
	}
	if codex.Name == "" {
		t.Fatalf("codex entry not discovered: %#v", got)
	}
	for _, want := range []string{
		"*",
		"!/config.toml",
		"!/AGENTS.md",
		"!/RTK.md",
		"!/rules/",
	} {
		if !testContainsString(codex.Ignore, want) {
			t.Fatalf("codex ignore = %v, missing %q", codex.Ignore, want)
		}
	}
	if testContainsString(codex.Ignore, "!/mcp.json") {
		t.Fatalf("codex ignore = %v, should not track agent-managed mcp.json", codex.Ignore)
	}
	for _, duplicateGlobal := range []string{"*.log", ".DS_Store", "cache"} {
		if testContainsString(codex.Ignore, duplicateGlobal) {
			t.Fatalf("codex ignore = %v, should not duplicate global ignore %q", codex.Ignore, duplicateGlobal)
		}
	}
}

func TestDiscoverDotsEntries_AddsGrokAllowlistIgnores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	var grok config.DotEntry
	for _, entry := range got {
		if entry.Name == "grok" {
			grok = entry
			break
		}
	}
	if grok.Name == "" {
		t.Fatalf("grok entry not discovered: %#v", got)
	}
	if grok.Path != "~/.grok" {
		t.Fatalf("grok path = %q, want ~/.grok", grok.Path)
	}
	for _, want := range []string{
		"*",
		"!/config.toml",
		"!/commands/",
		"!/hooks/",
	} {
		if !testContainsString(grok.Ignore, want) {
			t.Fatalf("grok ignore = %v, missing %q", grok.Ignore, want)
		}
	}
	if testContainsString(grok.Ignore, "!/skills/") {
		t.Fatalf("grok ignore = %v, should not track agent-managed skills", grok.Ignore)
	}
	for _, duplicateGlobal := range []string{"*.log", ".DS_Store", "cache"} {
		if testContainsString(grok.Ignore, duplicateGlobal) {
			t.Fatalf("grok ignore = %v, should not duplicate global ignore %q", grok.Ignore, duplicateGlobal)
		}
	}
}

func TestDiscoverDotsEntries_AddsOmniAllowlistIgnores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "omni"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := app.DiscoverDotsEntries(repoDir)
	if err != nil {
		t.Fatalf("DiscoverDotsEntries: %v", err)
	}
	var omni config.DotEntry
	for _, entry := range got {
		if entry.Name == "omni" {
			omni = entry
			break
		}
	}
	if omni.Name == "" {
		t.Fatalf("omni entry not discovered: %#v", got)
	}
	if omni.Path != "~/.config/omni" {
		t.Fatalf("omni path = %q, want ~/.config/omni", omni.Path)
	}
	for _, want := range []string{
		"*",
		"!/settings.json",
		"!/settings.d/",
	} {
		if !testContainsString(omni.Ignore, want) {
			t.Fatalf("omni ignore = %v, missing %q", omni.Ignore, want)
		}
	}
	for _, duplicateGlobal := range []string{"*.log", ".DS_Store", "cache"} {
		if testContainsString(omni.Ignore, duplicateGlobal) {
			t.Fatalf("omni ignore = %v, should not duplicate global ignore %q", omni.Ignore, duplicateGlobal)
		}
	}
}

func TestDiscoverDotsEntries_ExcludesAgentConfigDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoDir := t.TempDir()

	for _, dir := range []string{".claude", ".codex", ".grok", ".agents", ".config/agents"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("zsh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0o755); err != nil {
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

	if _, ok := byName["agents"]; ok {
		t.Fatalf(".config/agents should not be discovered: %#v", byName)
	}
	for name, wantPath := range map[string]string{
		"claude": "~/.claude",
		"codex":  "~/.codex",
		"grok":   "~/.grok",
		"nvim":   "~/.config/nvim",
		"zshrc":  "~/.zshrc",
	} {
		if gotPath, ok := byName[name]; !ok || gotPath != wantPath {
			t.Fatalf("expected %q => %q to be discovered, got %#v", name, wantPath, byName)
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
	if group == nil || !group.IsHost() {
		t.Fatalf("machine group testhost = %#v, want special host group", group)
	}
	if groups, ok := cfg.Hosts["testhost"]; !ok || len(groups) != 0 {
		t.Fatalf("hosts[testhost] = %v, ok=%v, want empty host assignment", groups, ok)
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
	if !testContainsString(claude.Ignore, "*") || !testContainsString(claude.Ignore, "!/settings.json") {
		t.Fatalf("bootstrapped claude ignore = %v, want allowlist ignores", claude.Ignore)
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
	if len(statuses) != 1 || statuses[0].State != dots.StateIgnored {
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
	if group == nil || !group.IsHost() || len(group.Dots) != 1 || !group.Dots[0].Ignored {
		t.Fatalf("machine group = %#v, want special host group with ignored kitty", group)
	}
	if groups, ok := cfg.Hosts["testhost"]; !ok || len(groups) != 0 {
		t.Fatalf("hosts[testhost] = %v, ok=%v, want empty host assignment", groups, ok)
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

func TestDotsSetEntryIgnored_UntracksConfiguredDotAndKeepsLocalCopy(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	t.Setenv("OMNI_HOSTNAME", "testhost")
	target := filepath.Join(home, ".zshrc")
	source := filepath.Join(dotsContentDir(repoDir), "zshrc", ".zshrc")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("repo zshrc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "settings.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.Hosts = map[string][]string{"testhost": {"work"}}
	cfg.Groups = append(cfg.Groups,
		&config.GroupConfig{Name: "testhost", Special: "host"},
		&config.GroupConfig{
			Name: "work",
			Dots: []config.DotEntry{{Name: "zshrc", Path: "~/.zshrc"}},
		},
	)
	if err := saveAppConfig(t, cfgPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsSetEntryIgnored("zshrc", "~/.zshrc", true); err != nil {
		t.Fatalf("DotsSetEntryIgnored: %v", err)
	}

	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if group := findDotsTestGroup(cfg.Groups, "work"); group != nil && containsDotsTestEntry(group.Dots, "zshrc") {
		t.Fatalf("work dots = %#v, want zshrc removed from tracked group", group.Dots)
	}
	hostGroup := findDotsTestGroup(cfg.Groups, "testhost")
	if hostGroup == nil || len(hostGroup.Dots) != 1 || hostGroup.Dots[0].Name != "zshrc" || !hostGroup.Dots[0].Ignored {
		t.Fatalf("host dots = %#v, want ignored zshrc entry", hostGroup)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is still a symlink, want real local file", target)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if got := string(content); got != "repo zshrc\n" {
		t.Fatalf("local target content = %q, want repo copy", got)
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
	if len(result.Entries) != 1 || result.Entries[0].Name != "kitty" || result.Entries[0].State != dots.StateLocalOnly {
		t.Fatalf("entries = %#v, want local-only kitty", result.Entries)
	}
	if !reflect.DeepEqual(result.Entries[0].Actions, []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore}) {
		t.Fatalf("actions = %#v, want sync+remove+ignore", result.Entries[0].Actions)
	}
}

func TestDotsDeleteLocal_RemovesDiscoveredLocalOnlyPath(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".config", "kitty")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	status := app.DotStatus{Name: "kitty", TargetPath: target, State: dots.StateLocalOnly}
	if err := a.DotsDeleteLocal(context.Background(), status); err != nil {
		t.Fatalf("DotsDeleteLocal: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists after delete: %v", err)
	}
}

func TestDotsDeleteLocal_RejectsTrackedAndUnsafeTargets(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	tracked := app.DotStatus{Name: "kitty", TargetPath: filepath.Join(home, ".config", "kitty"), State: dots.StateLocalOnly, Group: "base"}
	if err := a.DotsDeleteLocal(context.Background(), tracked); err == nil {
		t.Fatal("expected error for tracked entry")
	}
	wrongState := app.DotStatus{Name: "kitty", TargetPath: filepath.Join(home, ".config", "kitty"), State: dots.StateRepoOnly}
	if err := a.DotsDeleteLocal(context.Background(), wrongState); err == nil {
		t.Fatal("expected error for non-local-only state")
	}
	relative := app.DotStatus{Name: "kitty", TargetPath: ".config/kitty", State: dots.StateLocalOnly}
	if err := a.DotsDeleteLocal(context.Background(), relative); err == nil {
		t.Fatal("expected error for relative path")
	}
	homeItself := app.DotStatus{Name: "home", TargetPath: home, State: dots.StateLocalOnly}
	if err := a.DotsDeleteLocal(context.Background(), homeItself); err == nil {
		t.Fatal("expected error for home directory target")
	}
	outside := t.TempDir()
	outsideHome := app.DotStatus{Name: "etc", TargetPath: outside, State: dots.StateLocalOnly}
	if err := a.DotsDeleteLocal(context.Background(), outsideHome); err == nil {
		t.Fatal("expected error for target outside home")
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside-home path was deleted: %v", err)
	}
}

func TestDotsDeleteLocal_StaleMissingPathIsNoOp(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	stale := app.DotStatus{Name: "kitty", TargetPath: filepath.Join(home, ".config", "kitty"), State: dots.StateLocalOnly}
	if err := a.DotsDeleteLocal(context.Background(), stale); err != nil {
		t.Fatalf("DotsDeleteLocal on missing path: %v", err)
	}
}

func TestDotsDeleteLocal_RejectsSymlinkedParentOutsideHome(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}

	status := app.DotStatus{Name: "kitty", TargetPath: filepath.Join(home, ".config", "kitty"), State: dots.StateLocalOnly}
	if err := a.DotsDeleteLocal(context.Background(), status); err == nil {
		t.Fatal("expected error for symlinked parent escaping home")
	}
	if _, err := os.Lstat(filepath.Join(outside, "kitty")); err != nil {
		t.Fatalf("external path was deleted: %v", err)
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
	if len(result.Entries) != 1 || result.Entries[0].Name != "claude" || result.Entries[0].State != dots.StateUntrackedConflict {
		t.Fatalf("entries = %#v, want untracked-conflict claude", result.Entries)
	}
	want := []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionIgnore}
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
	if group == nil || !group.IsHost() || len(group.Dots) != 1 || group.Dots[0].Name != "claude" {
		t.Fatalf("machine group = %#v, want special host group with claude", group)
	}
	if groups, ok := cfg.Hosts["testhost"]; !ok || len(groups) != 0 {
		t.Fatalf("hosts[testhost] = %v, ok=%v, want empty host assignment", groups, ok)
	}
	if !testContainsString(group.Dots[0].Ignore, "*") || !testContainsString(group.Dots[0].Ignore, "!/settings.json") {
		t.Fatalf("claude ignore = %#v, want allowlist ignores", group.Dots[0].Ignore)
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
	if got := byName["node_modules"]; got.State != dots.StateIgnored || !reflect.DeepEqual(got.Actions, []dots.Action{dots.ActionUnignore}) {
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
		if entry.Name == "node_modules" && entry.State == dots.StateIgnored {
			seenIgnored = true
		}
		if entry.Name == "zshrc" && entry.Group == dotsTestHostGroupName() {
			seenTracked = true
		}
	}
	if !seenIgnored || !seenTracked {
		t.Fatalf("entries = %#v, want ignored node_modules and tracked zshrc present", result.Entries)
	}
}

func TestRefreshDotsStatePersistsCachedSnapshot(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua"), []byte("set number"), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := a.RefreshDotsState(context.Background())
	if err != nil {
		t.Fatalf("RefreshDotsState: %v", err)
	}
	if state == nil || state.DiscoveredCount != 1 {
		t.Fatalf("RefreshDotsState = %+v, want one discovered candidate", state)
	}
	var refreshed app.DotStatus
	for _, entry := range state.Entries {
		if entry.Name == "nvim" {
			refreshed = entry
			break
		}
	}
	if refreshed.Name == "" || refreshed.State != dots.StateRepoOnly || len(refreshed.Children) == 0 {
		t.Fatalf("refreshed nvim = %+v, want repo-only entry with children", refreshed)
	}

	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	if cached == nil || !cached.Loaded || cached.DiscoveredCount != 1 {
		t.Fatalf("CachedDotsState = %+v, want loaded cached candidate", cached)
	}
	var persisted app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "nvim" {
			persisted = entry
			break
		}
	}
	if persisted.State != dots.StateRepoOnly || len(persisted.Children) != len(refreshed.Children) {
		t.Fatalf("persisted nvim = %+v, want cached repo-only entry matching refreshed children", persisted)
	}
}

func TestRefreshDotsStateIncludesDotMemberships(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, home)

	state, err := a.RefreshDotsState(context.Background())
	if err != nil {
		t.Fatalf("RefreshDotsState: %v", err)
	}
	if !reflect.DeepEqual(state.DotMemberships["nvim"], []string{dotsTestHostGroupName()}) {
		t.Fatalf("dot memberships = %v, want nvim in host group", state.DotMemberships)
	}
}

func TestQueryDotsStatusPersistsCachedSnapshot(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("set number"), 0o644); err != nil {
		t.Fatal(err)
	}
	zshSource := filepath.Join(dotsContentDir(repoDir), "zshrc", ".zshrc")
	if err := os.MkdirAll(filepath.Dir(zshSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zshSource, []byte("export PATH=$PATH"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: target},
		{Name: "zshrc", Path: filepath.Join(home, ".zshrc")},
	}, home)

	result, err := a.QueryDotsStatus(context.Background(), app.DotsQueryOptions{Name: "nvim"})
	if err != nil {
		t.Fatalf("QueryDotsStatus: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Name != "nvim" {
		t.Fatalf("QueryDotsStatus entries = %#v, want nvim only", result.Entries)
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	if cached == nil || !cached.Loaded {
		t.Fatalf("CachedDotsState = %+v, want loaded snapshot after status query", cached)
	}
	if !hasDotStatusNamed(cached.Entries, "nvim") || !hasDotStatusNamed(cached.Entries, "zshrc") {
		t.Fatalf("cached entries = %#v, want unfiltered nvim and zshrc snapshot", cached.Entries)
	}
}

func TestDotsSyncContextPersistsCachedSnapshot(t *testing.T) {
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
	target := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: target}}, home)

	if _, err := a.DotsSyncContext(context.Background(), dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSyncContext: %v", err)
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	var synced app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "nvim" {
			synced = entry
			break
		}
	}
	if !cached.Loaded || synced.State != dots.StateSynced {
		t.Fatalf("CachedDotsState = %+v, want synced nvim snapshot", cached)
	}
}

func TestDotsSyncWithStateReturnsRefreshedSnapshot(t *testing.T) {
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
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}, home)

	all, err := a.DotsSyncContextWithState(context.Background(), dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSyncContextWithState: %v", err)
	}
	if all == nil || all.State == nil || !all.State.Loaded || !hasDotStatusNamed(all.State.Entries, "nvim") {
		t.Fatalf("DotsSyncContextWithState = %+v, want loaded nvim state", all)
	}

	entry, err := a.DotsSyncEntryWithState(context.Background(), "nvim", dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSyncEntryWithState: %v", err)
	}
	if entry == nil || entry.State == nil || !entry.State.Loaded || !hasDotStatusNamed(entry.State.Entries, "nvim") {
		t.Fatalf("DotsSyncEntryWithState = %+v, want loaded nvim state", entry)
	}
}

func TestDotsSyncWithStateProgressReturnsRefreshedSnapshots(t *testing.T) {
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
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}, home)

	var progress []app.DotsOperationStateProgressEvent
	result, err := a.DotsSyncContextWithStateProgress(context.Background(), dots.SyncOptions{}, func(event app.DotsOperationStateProgressEvent) {
		if event.Done {
			progress = append(progress, event)
		}
	})
	if err != nil {
		t.Fatalf("DotsSyncContextWithStateProgress: %v", err)
	}
	if result == nil || result.State == nil || !result.State.Loaded || !hasDotStatusNamed(result.State.Entries, "nvim") {
		t.Fatalf("DotsSyncContextWithStateProgress = %+v, want loaded final nvim state", result)
	}
	if len(progress) != 1 {
		t.Fatalf("progress events = %d, want one done event", len(progress))
	}
	if progress[0].Entry != "nvim" || !progress[0].Done || progress[0].Text == "" {
		t.Fatalf("progress event = %+v, want named done event with display text", progress[0])
	}
	status := requireDotsStateWithEntry(t, progress[0].State, "nvim")
	if status.State != dots.StateSynced {
		t.Fatalf("progress state = %q, want synced", status.State)
	}
}

func TestSaveDotsRepoAndSyncUsesSavedRepo(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "dotspickertest")
	ctx := context.Background()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	oldRepo := t.TempDir()
	newRepo := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	targetDir := filepath.Join(homeDir, ".config", "picked")
	target := filepath.Join(targetDir, "settings.json")
	source := filepath.Join(dotsContentDir(newRepo), "picked", ".config", "picked", "settings.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("selected repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		HostSettings: map[string]config.Settings{
			"dotspickertest": {DotsRepo: oldRepo},
		},
		Groups: []*config.GroupConfig{{Name: dotsTestHostGroupName(), Special: "host", Dots: []config.DotEntry{{Name: "picked", Path: "~/.config/picked"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(ctx); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	result, err := a.SaveDotsRepoAndSync(ctx, newRepo, dots.SyncOptions{})
	if err != nil {
		t.Fatalf("SaveDotsRepoAndSync: %v", err)
	}
	status := requireDotsStateWithEntry(t, result.State, "picked")
	if status.State != dots.StateSynced {
		t.Fatalf("picked state = %q, want synced", status.State)
	}
	if !result.HasSettings {
		t.Fatal("result should include saved settings")
	}
	if result.Settings.DotsRepo != newRepo {
		t.Fatalf("result settings DotsRepo = %q, want selected repo %q", result.Settings.DotsRepo, newRepo)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target was not populated from selected repo: %v", err)
	}
	if string(got) != "selected repo" {
		t.Fatalf("target content = %q, want selected repo content", string(got))
	}
	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.DotsRepo != newRepo {
		t.Fatalf("DotsRepo = %q, want selected repo %q", settings.DotsRepo, newRepo)
	}
	if !settings.AutoImport {
		t.Fatal("AutoImport should be preserved from current config")
	}
}

func TestDotsMutationWithStateReturnsRefreshedSnapshots(t *testing.T) {
	ctx := context.Background()

	t.Run("add", func(t *testing.T) {
		a, _, _ := newDotsApp(t)
		target := filepath.Join(os.Getenv("HOME"), ".zshrc")
		if err := os.WriteFile(target, []byte("export PATH=$PATH\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result, err := a.DotsAddWithState(ctx, target, app.DotsAddOptions{Name: "zshrc", Adopt: true})
		if err != nil {
			t.Fatalf("DotsAddWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "zshrc")
		if status.State != dots.StateSynced {
			t.Fatalf("zshrc state = %q, want synced", status.State)
		}
	})

	t.Run("delete", func(t *testing.T) {
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
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}, home)
		if _, err := a.DotsSyncContext(ctx, dots.SyncOptions{}); err != nil {
			t.Fatalf("DotsSyncContext: %v", err)
		}

		result, err := a.DotsDeleteWithState(ctx, "nvim", app.DotsDeleteOptions{KeepLocal: true})
		if err != nil {
			t.Fatalf("DotsDeleteWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "nvim")
		if result.State.DiscoveredCount != 1 || status.Group != "" || status.State != dots.StateLocalOnly {
			t.Fatalf("DotsDeleteWithState state = %+v, want untracked local nvim candidate", result.State)
		}
	})

	t.Run("resolve", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(home, ".config", "nvim")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "init.lua"), []byte("-- local"), 0o644); err != nil {
			t.Fatal(err)
		}
		setDotTestModTime(t, filepath.Join(srcDir, "init.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
		setDotTestModTime(t, filepath.Join(target, "init.lua"), time.Unix(1_700_000_000, 0))
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: target}}, home)

		result, err := a.DotsResolveConflictWithState(ctx, "nvim", app.DotResolveUseRepo)
		if err != nil {
			t.Fatalf("DotsResolveConflictWithState: %v", err)
		}
		if len(result.Ops) != 1 || result.Ops[0].Kind != dots.OpRepair {
			t.Fatalf("ops = %v, want OpRepair", result.Ops)
		}
		requireDotsStateWithEntry(t, result.State, "nvim")
	})

	t.Run("ignore", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}, home)

		result, err := a.DotsAddIgnorePatternWithState(ctx, "nvim", "*.log")
		if err != nil {
			t.Fatalf("DotsAddIgnorePatternWithState: %v", err)
		}
		requireDotsStateWithEntry(t, result.State, "nvim")
	})

	t.Run("include ignored path", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		localPath := filepath.Join(home, ".claude", "projects", "session.json")
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(localPath, []byte("local session"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
			Name:   "claude",
			Path:   filepath.Join(home, ".claude"),
			Ignore: []string{"projects/**"},
		}}, home)

		result, err := a.DotsIncludeIgnoredPathWithState(ctx, "claude", "projects/session.json")
		if err != nil {
			t.Fatalf("DotsIncludeIgnoredPathWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "claude")
		if status.State != dots.StateSynced {
			t.Fatalf("claude state = %q, want synced after include", status.State)
		}
		sourcePath := filepath.Join(dotsContentDir(repoDir), "claude", ".claude", "projects", "session.json")
		assertSymlinkResolvesTo(t, localPath, sourcePath)
	})

	t.Run("include path below ignored directory", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		localPath := filepath.Join(home, ".claude", "projects", "session.json")
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(localPath, []byte("local session"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
			Name:   "claude",
			Path:   filepath.Join(home, ".claude"),
			Ignore: []string{".claude/projects"},
		}}, home)

		result, err := a.DotsIncludeIgnoredPathWithState(ctx, "claude", "projects/session.json")
		if err != nil {
			t.Fatalf("DotsIncludeIgnoredPathWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "claude")
		if status.State != dots.StateSynced {
			t.Fatalf("claude state = %q, want synced after include", status.State)
		}
		sourcePath := filepath.Join(dotsContentDir(repoDir), "claude", ".claude", "projects", "session.json")
		assertSymlinkResolvesTo(t, localPath, sourcePath)
	})

	t.Run("include directory below ignored ancestor", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		localPath := filepath.Join(home, ".claude", "projects", "acme", "session.json")
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(localPath, []byte("local session"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
			Name:   "claude",
			Path:   filepath.Join(home, ".claude"),
			Ignore: []string{"*"},
		}}, home)

		result, err := a.DotsIncludeIgnoredPathWithState(ctx, "claude", "projects/acme")
		if err != nil {
			t.Fatalf("DotsIncludeIgnoredPathWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "claude")
		if status.State != dots.StateSynced {
			t.Fatalf("claude state = %q, want synced after include", status.State)
		}
		sourcePath := filepath.Join(dotsContentDir(repoDir), "claude", ".claude", "projects", "acme", "session.json")
		assertSymlinkResolvesTo(t, localPath, sourcePath)
	})

	t.Run("include repo-only directory below ignored ancestor", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		targetPath := filepath.Join(home, ".claude", "projects", "acme", "session.json")
		sourcePath := filepath.Join(dotsContentDir(repoDir), "claude", ".claude", "projects", "acme", "session.json")
		if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sourcePath, []byte("repo session"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
			Name:   "claude",
			Path:   filepath.Join(home, ".claude"),
			Ignore: []string{"*"},
		}}, home)

		result, err := a.DotsIncludeIgnoredPathWithState(ctx, "claude", "projects/acme")
		if err != nil {
			t.Fatalf("DotsIncludeIgnoredPathWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "claude")
		if status.State != dots.StateSynced {
			t.Fatalf("claude state = %q, want synced after include", status.State)
		}
		assertSymlinkResolvesTo(t, targetPath, sourcePath)
	})

	t.Run("include ignored entry", func(t *testing.T) {
		a, cfgDir, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		target := filepath.Join(home, ".config", "kitty")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "kitty.conf"), []byte("font_size 12"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "kitty", Path: target, Ignored: true}}, home)

		result, err := a.DotsSetEntryIgnoredWithState(ctx, "kitty", target, false)
		if err != nil {
			t.Fatalf("DotsSetEntryIgnoredWithState include: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "kitty")
		if status.State != dots.StateSynced {
			t.Fatalf("kitty state = %q, want synced after include", status.State)
		}
		sourcePath := filepath.Join(dotsContentDir(repoDir), "kitty", ".config", "kitty", "kitty.conf")
		assertSymlinkResolvesTo(t, filepath.Join(target, "kitty.conf"), sourcePath)
	})

	t.Run("entry ignored", func(t *testing.T) {
		a, _, _ := newDotsApp(t)
		target := filepath.Join(os.Getenv("HOME"), ".config", "kitty")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}

		result, err := a.DotsSetEntryIgnoredWithState(ctx, "kitty", target, true)
		if err != nil {
			t.Fatalf("DotsSetEntryIgnoredWithState: %v", err)
		}
		status := requireDotsStateWithEntry(t, result.State, "kitty")
		if status.State != dots.StateIgnored {
			t.Fatalf("kitty state = %q, want ignored", status.State)
		}
	})
}

func TestDotsIgnoreWithStateDoesNotRefreshCacheAfterCanceledContext(t *testing.T) {
	canceledContext := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	tests := []struct {
		name   string
		run    func(t *testing.T, a *app.App, cfgDir, repoDir string) (string, error)
		assert func(t *testing.T, cfg *config.RootConfig)
	}{
		{
			name: "add ignore pattern",
			run: func(t *testing.T, a *app.App, cfgDir, repoDir string) (string, error) {
				home := os.Getenv("HOME")
				cfgPath := writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")}}, home)
				_, err := a.DotsAddIgnorePatternWithState(canceledContext(), "nvim", "*.log")
				return cfgPath, err
			},
			assert: func(t *testing.T, cfg *config.RootConfig) {
				if got := cfg.Groups[0].Dots[0].Ignore; len(got) != 1 || got[0] != "*.log" {
					t.Fatalf("ignore = %v, want persisted [*.log]", got)
				}
			},
		},
		{
			name: "include ignored path",
			run: func(t *testing.T, a *app.App, cfgDir, repoDir string) (string, error) {
				home := os.Getenv("HOME")
				cfgPath := writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
					Name:   "claude",
					Path:   filepath.Join(home, ".claude"),
					Ignore: []string{"projects/**"},
				}}, home)
				_, err := a.DotsIncludeIgnoredPathWithState(canceledContext(), "claude", "projects/session.json")
				return cfgPath, err
			},
			assert: func(t *testing.T, cfg *config.RootConfig) {
				if !testContainsString(cfg.Groups[0].Dots[0].Ignore, "!/projects/session.json") {
					t.Fatalf("ignore = %v, want persisted include override", cfg.Groups[0].Dots[0].Ignore)
				}
			},
		},
		{
			name: "entry ignored",
			run: func(t *testing.T, a *app.App, cfgDir, repoDir string) (string, error) {
				home := os.Getenv("HOME")
				target := filepath.Join(home, ".config", "kitty")
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
				cfgPath := writeGroupWithDots(t, cfgDir, repoDir, nil, home)
				_, err := a.DotsSetEntryIgnoredWithState(canceledContext(), "kitty", target, true)
				return cfgPath, err
			},
			assert: func(t *testing.T, cfg *config.RootConfig) {
				if len(cfg.Groups) == 0 || len(cfg.Groups[0].Dots) != 1 || cfg.Groups[0].Dots[0].Name != "kitty" || !cfg.Groups[0].Dots[0].Ignored {
					t.Fatalf("groups = %#v, want persisted ignored kitty entry", cfg.Groups)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, cfgDir, repoDir := newDotsApp(t)
			cfgPath, err := tc.run(t, a, cfgDir, repoDir)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s error = %v, want context.Canceled", tc.name, err)
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			tc.assert(t, cfg)
			cached, err := a.CachedDotsState(context.Background())
			if err != nil {
				t.Fatalf("CachedDotsState: %v", err)
			}
			if cached.Loaded {
				t.Fatalf("CachedDotsState = %+v, want no background refresh after canceled context", cached)
			}
		})
	}
}

func TestDotsDiscoveredOperationWithStateReturnsRefreshedSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("sync", func(t *testing.T) {
		a, _, repoDir := newDotsApp(t)
		srcDir := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "settings.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}

		result, err := a.DotsSyncDiscoveredWithState(ctx, "claude", "work")
		if err != nil {
			t.Fatalf("DotsSyncDiscoveredWithState: %v", err)
		}
		if result.Added.Name != "claude" {
			t.Fatalf("added = %+v, want claude", result.Added)
		}
		status := requireDotsStateWithEntry(t, result.State, "claude")
		if result.State.DiscoveredCount != 0 || status.Group != "work" {
			t.Fatalf("DotsSyncDiscoveredWithState state = %+v, want tracked claude in work", result.State)
		}
	})

	t.Run("resolve", func(t *testing.T) {
		a, _, repoDir := newDotsApp(t)
		home := os.Getenv("HOME")
		srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(home, ".config", "nvim")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "init.lua"), []byte("-- local"), 0o644); err != nil {
			t.Fatal(err)
		}
		setDotTestModTime(t, filepath.Join(srcDir, "init.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
		setDotTestModTime(t, filepath.Join(target, "init.lua"), time.Unix(1_700_000_000, 0))

		result, err := a.DotsResolveDiscoveredWithState(ctx, "nvim", "", app.DotResolveUseRepo)
		if err != nil {
			t.Fatalf("DotsResolveDiscoveredWithState: %v", err)
		}
		if result.Added.Name != "nvim" {
			t.Fatalf("added = %+v, want nvim", result.Added)
		}
		requireDotsStateWithEntry(t, result.State, "nvim")
	})
}

func TestDotsVariantWithStateReturnsRefreshedSnapshot(t *testing.T) {
	ctx := context.Background()

	t.Run("add", func(t *testing.T) {
		t.Setenv("OMNI_HOSTNAME", "work")
		a, cfgDir, repoDir := newDotsApp(t)
		source := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
		if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("default"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, "")

		result, err := a.DotsAddHostVariantWithState(ctx, "nvim", app.DotsAddVariantOptions{Host: "work.local"})
		if err != nil {
			t.Fatalf("DotsAddHostVariantWithState: %v", err)
		}
		if result.Info.Host != "work" || result.Info.Package != "nvim@work" {
			t.Fatalf("variant info = %+v, want work nvim@work", result.Info)
		}
		requireDotsStateWithEntry(t, result.State, "nvim")
	})

	t.Run("remove", func(t *testing.T) {
		t.Setenv("OMNI_HOSTNAME", "laptop")
		a, cfgDir, repoDir := newDotsApp(t)
		defaultSource := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
		variantSource := filepath.Join(dotsContentDir(repoDir), "nvim@work", ".config", "nvim", "init.lua")
		if err := os.MkdirAll(filepath.Dir(defaultSource), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(variantSource), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(defaultSource, []byte("default"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(variantSource, []byte("work"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
			Name: "nvim",
			Path: "~/.config/nvim",
			Hosts: map[string]config.DotVariant{
				"work": {Package: "nvim@work"},
			},
		}}, "")

		result, err := a.DotsRemoveHostVariantWithState(ctx, "nvim", app.DotsRemoveVariantOptions{Host: "work.local"})
		if err != nil {
			t.Fatalf("DotsRemoveHostVariantWithState: %v", err)
		}
		if result.Info.Host != "work" || result.Info.Package != "nvim@work" {
			t.Fatalf("variant info = %+v, want work nvim@work", result.Info)
		}
		requireDotsStateWithEntry(t, result.State, "nvim")
	})
}

func TestDotsAddPersistsCachedSnapshot(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(target, []byte("export PATH=$PATH\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := a.DotsAdd(context.Background(), target, app.DotsAddOptions{Name: "zshrc", Adopt: true}); err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	var status app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "zshrc" {
			status = entry
			break
		}
	}
	if !cached.Loaded || status.State != dots.StateSynced {
		t.Fatalf("CachedDotsState = %+v, want synced zshrc snapshot", cached)
	}
}

func TestDotsDeleteRefreshesCachedSnapshot(t *testing.T) {
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
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)
	if _, err := a.DotsSyncContext(context.Background(), dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSyncContext: %v", err)
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState before delete: %v", err)
	}
	if !hasDotStatusNamed(cached.Entries, "nvim") {
		t.Fatalf("cached entries before delete = %#v, want nvim", cached.Entries)
	}

	if err := a.DotsDeleteWithOptions(context.Background(), "nvim", app.DotsDeleteOptions{KeepLocal: true}); err != nil {
		t.Fatalf("DotsDeleteWithOptions: %v", err)
	}
	cached, err = a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState after delete: %v", err)
	}
	var candidate app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "nvim" {
			candidate = entry
			break
		}
	}
	if !cached.Loaded || cached.DiscoveredCount != 1 || candidate.Group != "" || candidate.State != dots.StateLocalOnly {
		t.Fatalf("CachedDotsState = %+v, want untracked local nvim candidate after delete", cached)
	}
}

func TestDotsAddDiscoveredEntryContextPersistsCachedSnapshot(t *testing.T) {
	a, _, repoDir := newDotsApp(t)
	srcDir := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := a.RefreshDotsState(context.Background())
	if err != nil {
		t.Fatalf("RefreshDotsState: %v", err)
	}
	if before.DiscoveredCount != 1 {
		t.Fatalf("RefreshDotsState DiscoveredCount = %d, want discovered claude", before.DiscoveredCount)
	}

	if _, err := a.DotsAddDiscoveredEntryContext(context.Background(), "claude", "work"); err != nil {
		t.Fatalf("DotsAddDiscoveredEntryContext: %v", err)
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	var tracked app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "claude" {
			tracked = entry
			break
		}
	}
	if !cached.Loaded || cached.DiscoveredCount != 0 || tracked.Group != "work" {
		t.Fatalf("CachedDotsState = %+v, want tracked claude in work group", cached)
	}
}

func TestDotsSetEntryIgnoredPersistsCachedSnapshot(t *testing.T) {
	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".config", "kitty")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := a.RefreshDotsState(context.Background())
	if err != nil {
		t.Fatalf("RefreshDotsState: %v", err)
	}
	if before.DiscoveredCount != 1 {
		t.Fatalf("RefreshDotsState DiscoveredCount = %d, want discovered kitty", before.DiscoveredCount)
	}

	if err := a.DotsSetEntryIgnored("kitty", target, true); err != nil {
		t.Fatalf("DotsSetEntryIgnored: %v", err)
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	var ignored app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "kitty" {
			ignored = entry
			break
		}
	}
	if !cached.Loaded || cached.DiscoveredCount != 0 || ignored.State != dots.StateIgnored {
		t.Fatalf("CachedDotsState = %+v, want ignored kitty snapshot", cached)
	}
}

func TestSetDotGroupsPersistsCachedSnapshot(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
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
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)
	if _, err := a.QueryDotsStatus(context.Background(), app.DotsQueryOptions{}); err != nil {
		t.Fatalf("QueryDotsStatus: %v", err)
	}

	change, err := a.SetDotGroupsWithState(context.Background(), "nvim", []string{"work"}, []string{"work"}, "testhost")
	if err != nil {
		t.Fatalf("SetDotGroups: %v", err)
	}
	if !reflect.DeepEqual(change.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("dot memberships = %v, want nvim in work", change.DotMemberships)
	}
	if change.DotsState == nil {
		t.Fatal("DotsState is nil, want refreshed state")
	}
	cached, err := a.CachedDotsState(context.Background())
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	var moved app.DotStatus
	for _, entry := range cached.Entries {
		if entry.Name == "nvim" {
			moved = entry
			break
		}
	}
	if !cached.Loaded || moved.Group != "work" {
		t.Fatalf("CachedDotsState = %+v, want nvim moved to work group", cached)
	}
}

func hasDotStatusNamed(entries []app.DotStatus, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func requireDotsStateWithEntry(t *testing.T, state *app.DotsState, name string) app.DotStatus {
	t.Helper()
	if state == nil || !state.Loaded {
		t.Fatalf("DotsState = %+v, want loaded state", state)
	}
	for _, entry := range state.Entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("DotsState entries = %#v, want %q", state.Entries, name)
	return app.DotStatus{}
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
	if err := a.CreateGroup("work"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if err := a.AddGroupToHost(dotsTestHostGroupName(), "work"); err != nil {
		t.Fatalf("AddGroupToHost: %v", err)
	}

	if err := a.MoveDotToGroup("nvim", "work"); err != nil {
		t.Fatalf("MoveDotToGroup: %v", err)
	}
	memberships, err := a.DotMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("DotMembershipMap: %v", err)
	}
	if !reflect.DeepEqual(memberships["nvim"], []string{"work"}) {
		t.Fatalf("memberships = %v, want [work]", memberships["nvim"])
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Group != "work" {
		t.Fatalf("statuses = %#v, want one work-owned row", statuses)
	}
}

func TestMoveDotToGroup_MovesBetweenReusableGroups(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: "ai", Dots: []config.DotEntry{{Name: "com.corsair", Path: "~/.config/com.corsair"}}},
		{Name: "gaming"},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.MoveDotToGroup("com.corsair", "gaming"); err != nil {
		t.Fatalf("MoveDotToGroup: %v", err)
	}

	memberships, err := a.DotMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("DotMembershipMap: %v", err)
	}
	if !reflect.DeepEqual(memberships["com.corsair"], []string{"gaming"}) {
		t.Fatalf("memberships = %v, want [gaming]", memberships["com.corsair"])
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load updated: %v", err)
	}
	if group := findDotsTestGroup(updated.Groups, "ai"); group == nil || len(group.Dots) != 0 {
		t.Fatalf("ai group dots = %+v, want empty", group)
	}
	group := findDotsTestGroup(updated.Groups, "gaming")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "com.corsair" {
		t.Fatalf("gaming group = %+v, want com.corsair dot", group)
	}
}

func TestSetDotGroupsPersistsExactMembershipsAndHostAssignment(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")
	host := dotsTestHostGroupName()

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: host, Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
		{Name: "work"},
	}
	rootCfg.Hosts = map[string][]string{host: {}}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if _, err := a.SetDotGroups("nvim", []string{"work"}, nil, host); err != nil {
		t.Fatalf("SetDotGroups: %v", err)
	}

	memberships, err := a.DotMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("DotMembershipMap: %v", err)
	}
	if !reflect.DeepEqual(memberships["nvim"], []string{"work"}) {
		t.Fatalf("memberships = %v, want [work]", memberships["nvim"])
	}
	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load updated: %v", err)
	}
	if !reflect.DeepEqual(updated.Hosts[host], []string{"work"}) {
		t.Fatalf("hosts[%s] = %v, want [work]", host, updated.Hosts[host])
	}
}

func TestSetDotGroupsEmptyActiveHostDoesNotAssignLocalhost(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")
	host := dotsTestHostGroupName()

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: host, Special: "host"},
		{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
	}
	rootCfg.Hosts = map[string][]string{host: {"work"}}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if _, err := a.SetDotGroups("nvim", []string{host}, nil, ""); err != nil {
		t.Fatalf("SetDotGroups: %v", err)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load updated: %v", err)
	}
	if _, ok := updated.Hosts["localhost"]; ok {
		t.Fatalf("unexpected localhost host assignment: %+v", updated.Hosts)
	}
	hostGroup := findDotsTestGroup(updated.Groups, host)
	if hostGroup == nil || !containsDotsTestEntry(hostGroup.Dots, "nvim") {
		t.Fatalf("host group %q missing moved nvim entry: %+v", host, updated.Groups)
	}
}

func TestDotGroupsQueryAndNoopRemoveAtAppBoundary(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	groups, err := a.DotGroups("nvim")
	if err != nil {
		t.Fatalf("DotGroups: %v", err)
	}
	if !reflect.DeepEqual(groups, []string{"work"}) {
		t.Fatalf("DotGroups = %v, want work", groups)
	}

	change, err := a.RemoveDotGroupsWithState(context.Background(), "nvim", []string{"missing"})
	if err != nil {
		t.Fatalf("RemoveDotGroups: %v", err)
	}
	if !reflect.DeepEqual(change.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("dot memberships = %v, want nvim in work", change.DotMemberships)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load updated: %v", err)
	}
	workGroup := findDotsTestGroup(updated.Groups, "work")
	if workGroup == nil || !containsDotsTestEntry(workGroup.Dots, "nvim") {
		t.Fatalf("groups after RemoveDotGroups = %+v, want nvim in work", updated.Groups)
	}
}

func TestRemoveDotGroupsRejectsLastMembership(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if _, err := a.RemoveDotGroups("nvim", []string{"work"}); err == nil || !strings.Contains(err.Error(), "needs at least one group") {
		t.Fatalf("RemoveDotGroups err = %v, want last-membership guard", err)
	}
}

func TestRemoveDotGroupsNormalizesRequestedGroups(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
	}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	change, err := a.RemoveDotGroups("nvim", []string{" missing ", "missing", ""})
	if err != nil {
		t.Fatalf("RemoveDotGroups: %v", err)
	}
	if !reflect.DeepEqual(change.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("dot memberships = %v, want work", change.DotMemberships["nvim"])
	}
}

func TestNormalizeGroupNamesTrimsDedupesAndSorts(t *testing.T) {
	t.Parallel()
	got := app.NormalizeGroupNames([]string{" work ", "base", "work", "", "base"})
	if !reflect.DeepEqual(got, []string{"base", "work"}) {
		t.Fatalf("NormalizeGroupNames = %v, want [base work]", got)
	}
}

func TestSetGroupDotsAppliesEditorDiff(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")
	host := dotsTestHostGroupName()

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: host, Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
		{Name: "work", Dots: []config.DotEntry{{Name: "zsh", Path: "~/.zshrc"}}},
	}
	rootCfg.Hosts = map[string][]string{host: {"work"}}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	change, err := a.SetGroupDotsWithState(
		context.Background(),
		"work",
		map[string]bool{"nvim": true, "zsh": false},
		map[string]bool{"nvim": false, "zsh": true},
	)
	if err != nil {
		t.Fatalf("SetGroupDots: %v", err)
	}
	if change.Changed != 2 {
		t.Fatalf("changed = %d, want 2", change.Changed)
	}
	if !reflect.DeepEqual(change.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("dot memberships = %v, want nvim in work", change.DotMemberships)
	}
	if change.DotsState == nil {
		t.Fatal("DotsState is nil, want refreshed state")
	}
	if !reflect.DeepEqual(change.DotsState.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("state dot memberships = %v, want nvim in work", change.DotsState.DotMemberships)
	}

	updated, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load updated: %v", err)
	}
	work := findDotsTestGroup(updated.Groups, "work")
	if work == nil {
		t.Fatal("missing work group")
	}
	if !containsDotsTestEntry(work.Dots, "nvim") {
		t.Fatalf("nvim should be added to work dots: %+v", work.Dots)
	}
	if containsDotsTestEntry(work.Dots, "zsh") {
		t.Fatalf("zsh should be removed from work dots: %+v", work.Dots)
	}
}

func TestSetGroupDotsAppliesEditorDiffWithDefaultContext(t *testing.T) {
	a, cfgDir, _ := newDotsApp(t)
	cfgPath := filepath.Join(cfgDir, "settings.json")
	host := dotsTestHostGroupName()

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: host, Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
		{Name: "work", Dots: []config.DotEntry{{Name: "zsh", Path: "~/.zshrc"}}},
	}
	rootCfg.Hosts = map[string][]string{host: {"work"}}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	change, err := a.SetGroupDots(
		"work",
		map[string]bool{"nvim": true, "zsh": false},
		map[string]bool{"nvim": false, "zsh": true},
	)
	if err != nil {
		t.Fatalf("SetGroupDots: %v", err)
	}
	if change.Changed != 2 {
		t.Fatalf("changed = %d, want 2", change.Changed)
	}
	if !reflect.DeepEqual(change.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("dot memberships = %v, want nvim in work", change.DotMemberships)
	}
	if change.DotsState == nil {
		t.Fatal("DotsState is nil, want refreshed state")
	}
	if !reflect.DeepEqual(change.DotsState.DotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("state dot memberships = %v, want nvim in work", change.DotsState.DotMemberships)
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

func assertRealDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %q: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("%q mode = %v, want real directory", path, info.Mode())
	}
}

func assertSymlinkResolvesTo(t *testing.T, linkPath, sourcePath string) {
	t.Helper()
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat %q: %v", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%q mode = %v, want symlink", linkPath, info.Mode())
	}
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", linkPath, err)
	}
	wantResolved, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", sourcePath, err)
	}
	if resolved != wantResolved {
		t.Fatalf("%q resolves to %q, want %q", linkPath, resolved, wantResolved)
	}
}

func setDotTestModTime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes %q: %v", path, err)
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
	t.Setenv("HOME", filepath.VolumeName(os.TempDir())+string(os.PathSeparator))
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

	_, err := a.DotsSync(dots.SyncOptions{})
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

func containsDotsTestEntry(entries []config.DotEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func dotsTestHostGroupName() string {
	hostname := os.Getenv("OMNI_HOSTNAME")
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	return shortHostnameForTest(hostname)
}

func writeGroupWithDots(t *testing.T, cfgDir, _ string, entries []config.DotEntry, _ string) string {
	t.Helper()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	hostGroupName := dotsTestHostGroupName()
	var baseGroup *config.GroupConfig
	for _, g := range rootCfg.Groups {
		if g.BaseName() == hostGroupName {
			baseGroup = g
			break
		}
	}
	if baseGroup == nil {
		baseGroup = &config.GroupConfig{Name: hostGroupName, Special: "host"}
		rootCfg.Groups = append(rootCfg.Groups, baseGroup)
	}
	baseGroup.Dots = entries

	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	return cfgPath
}

func TestDotsConfigured_False(t *testing.T) {
	t.Parallel()
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

func TestDotsMutationsRejectWhenHostDotsDisabled(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	liveFile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(liveFile, []byte("zsh"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: target, Ignore: []string{"cache"}},
	}, home)
	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled(true): %v", err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "sync all", run: func() error {
			_, err := a.DotsSync(dots.SyncOptions{DryRun: true})
			return err
		}},
		{name: "sync entry", run: func() error {
			_, err := a.DotsSyncEntry(context.Background(), "nvim", dots.SyncOptions{DryRun: true})
			return err
		}},
		{name: "add", run: func() error {
			_, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Adopt: true})
			return err
		}},
		{name: "delete", run: func() error {
			return a.DotsDelete(context.Background(), "nvim")
		}},
		{name: "resolve", run: func() error {
			_, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseRepo)
			return err
		}},
		{name: "add ignore pattern", run: func() error {
			return a.DotsAddIgnorePattern("nvim", "*.log")
		}},
		{name: "remove ignore pattern", run: func() error {
			return a.DotsRemoveIgnorePattern("nvim", "cache")
		}},
		{name: "set entry ignored", run: func() error {
			return a.DotsSetEntryIgnored("nvim", target, true)
		}},
		{name: "add discovered entry", run: func() error {
			_, err := a.DotsAddDiscoveredEntry("nvim", "")
			return err
		}},
		{name: "bootstrap discovered entries", run: func() error {
			_, err := a.BootstrapDotsEntries()
			return err
		}},
		{name: "move group", run: func() error {
			return a.MoveDotToGroup("nvim", "work")
		}},
		{name: "remove group", run: func() error {
			return a.RemoveDotFromGroup("nvim", dotsTestHostGroupName())
		}},
		{name: "pull", run: func() error {
			_, err := a.DotsPull(context.Background())
			return err
		}},
		{name: "commit", run: func() error {
			return a.DotsCommit(context.Background(), "dots: test")
		}},
		{name: "push", run: func() error {
			return a.DotsPush(context.Background(), "dots: test")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), "dots are disabled for this host") {
				t.Fatalf("error = %v, want disabled-host guard", err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(repoDir, "dotfiles")); !os.IsNotExist(err) {
		t.Fatalf("dotfiles content dir should not be created while disabled, stat err = %v", err)
	}
}

func TestDotsAddIgnorePattern_AppendsToExistingEntry(t *testing.T) {
	t.Parallel()
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

func TestDotsAddIgnorePatternAllowsMissingConfiguredRepo(t *testing.T) {
	t.Parallel()
	a, cfgPath := newImportApp(t)
	missingRepo := filepath.Join(t.TempDir(), "missing-dotfiles")
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: missingRepo},
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
	t.Parallel()
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
	t.Parallel()
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

func TestDotsIncludeIgnoredPath_AppendsIncludeOverrideForBroadIgnore(t *testing.T) {
	t.Parallel()
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: t.TempDir()},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "myapp", Path: "~/.myapp", Ignore: []string{"*", "!/settings.json"}}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsIncludeIgnoredPath("myapp", "projects/session.json"); err != nil {
		t.Fatalf("DotsIncludeIgnoredPath: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Groups[0].Dots[0].Ignore
	if !testContainsString(got, "!/projects/session.json") {
		t.Fatalf("ignore = %v, want include override for projects/session.json", got)
	}
	patterns := append(dots.DefaultIgnores(), got...)
	if dots.ShouldIgnorePath("projects/session.json", "session.json", patterns) {
		t.Fatalf("projects/session.json still ignored by %v", got)
	}
}

func TestDotsIncludeIgnoredPath_RemovesExactPatternWhenEnough(t *testing.T) {
	t.Parallel()
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: t.TempDir()},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Ignore: []string{"custom-state", "*.log"}}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsIncludeIgnoredPath("nvim", "custom-state"); err != nil {
		t.Fatalf("DotsIncludeIgnoredPath: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Groups[0].Dots[0].Ignore
	if testContainsString(got, "custom-state") || testContainsString(got, "!/custom-state") {
		t.Fatalf("ignore = %v, want custom-state removed without include override", got)
	}
	if len(got) != 1 || got[0] != "*.log" {
		t.Fatalf("ignore = %v, want [*.log]", got)
	}
}

func TestDotsIncludeIgnoredPath_MovesShadowedIncludeAfterBroadIgnore(t *testing.T) {
	t.Parallel()
	a, cfgPath := newImportApp(t)
	if err := saveAppConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: t.TempDir()},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "myapp", Path: "~/.myapp", Ignore: []string{"!/projects/session.json", "*"}}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	if err := a.DotsIncludeIgnoredPath("myapp", "projects/session.json"); err != nil {
		t.Fatalf("DotsIncludeIgnoredPath: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	got := cfg.Groups[0].Dots[0].Ignore
	wantLast := "!/projects/session.json"
	if len(got) == 0 || got[len(got)-1] != wantLast {
		t.Fatalf("ignore = %v, want %q moved after broad ignore", got, wantLast)
	}
	patterns := append(dots.DefaultIgnores(), got...)
	if dots.ShouldIgnorePath("projects/session.json", "session.json", patterns) {
		t.Fatalf("projects/session.json still ignored by %v", got)
	}
}

func TestDotsSync_NoActiveEntries_ReturnsNilWithoutError(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{}, home)
	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if ops != nil {
		t.Fatalf("expected nil ops for no active entries, got %v", ops)
	}
}

func TestDotsSync_LinksFiles(t *testing.T) {
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

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpLink {
		t.Errorf("got ops %v, want [OpLink]", ops)
	}
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))
}

func TestDotsSync_ReportsEntryProgress(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	nvimSrc := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(nvimSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nvimSrc, []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	zshSrc := filepath.Join(dotsContentDir(repoDir), "zshrc", ".zshrc")
	if err := os.MkdirAll(filepath.Dir(zshSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zshSrc, []byte("export PATH=$PATH"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
		{Name: "zshrc", Path: filepath.Join(home, ".zshrc")},
	}, home)

	var events []dots.SyncProgressEvent
	if _, err := a.DotsSync(dots.SyncOptions{
		EntryOrder: []string{"zshrc", "nvim"},
		Progress: func(event dots.SyncProgressEvent) {
			events = append(events, event)
		},
	}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}

	if len(events) != 4 {
		t.Fatalf("progress events len = %d, want 4: %#v", len(events), events)
	}
	want := []struct {
		name  string
		index int
		total int
		done  bool
	}{
		{"zshrc", 1, 2, false},
		{"zshrc", 1, 2, true},
		{"nvim", 2, 2, false},
		{"nvim", 2, 2, true},
	}
	for i, event := range events {
		if event.Entry != want[i].name || event.Index != want[i].index || event.Total != want[i].total || event.Done != want[i].done {
			t.Fatalf("event[%d] = %#v, want %#v", i, event, want[i])
		}
		if event.Done && event.Err != nil {
			t.Fatalf("event[%d] Err = %v", i, event.Err)
		}
	}
}

func TestDotsSync_UnfoldsExistingDirectorySymlink(t *testing.T) {
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

	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(nvimPath), 0o755); err != nil {
		t.Fatal(err)
	}
	relTarget, err := filepath.Rel(filepath.Dir(nvimPath), srcDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, nvimPath); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpLink {
		t.Fatalf("ops = %v, want OpLink repair", ops)
	}
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))
}

func TestDotsSync_HealsAbsoluteSymlinkBeforeRestow(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "zsh", ".config", "zsh")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envSrc := filepath.Join(srcDir, "10-env.zsh")
	completionSrc := filepath.Join(srcDir, "15-completion-paths.zsh")
	if err := os.WriteFile(envSrc, []byte("env"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(completionSrc, []byte("completion"), 0o644); err != nil {
		t.Fatal(err)
	}

	zshTargetDir := filepath.Join(home, ".config", "zsh")
	if err := os.MkdirAll(zshTargetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envTarget := filepath.Join(zshTargetDir, "10-env.zsh")
	if err := os.Symlink(envSrc, envTarget); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zsh", Path: zshTargetDir},
	}, home)

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("DotsSync: want at least one op")
	}
	for _, op := range ops {
		if op.Kind == dots.OpConflict {
			t.Fatalf("DotsSync: unexpected conflict op %+v", op)
		}
	}

	assertSymlinkResolvesTo(t, envTarget, envSrc)
	assertSymlinkResolvesTo(t, filepath.Join(zshTargetDir, "15-completion-paths.zsh"), completionSrc)

	envLinkText, err := os.Readlink(envTarget)
	if err != nil {
		t.Fatalf("Readlink(%q): %v", envTarget, err)
	}
	if filepath.IsAbs(envLinkText) {
		t.Fatalf("healed link %q is still absolute: %q", envTarget, envLinkText)
	}
}

func TestDotsSync_DryRun(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

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

func TestDotsSync_AllowsIgnoredRepoSource(t *testing.T) {
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
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpLink {
		t.Fatalf("ops = %v, want one link", ops)
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthOK {
		t.Fatalf("statuses = %#v, want one healthy entry", statuses)
	}
	if statuses[0].FileCount != 1 {
		t.Fatalf("file count = %d, want only non-ignored files counted", statuses[0].FileCount)
	}
}

func TestDotsSync_AllowsIgnoredRepoSourceWhenAlreadySynced(t *testing.T) {
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
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpSkip {
		t.Fatalf("ops = %v, want one skip", ops)
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthOK {
		t.Fatalf("statuses = %#v, want one healthy entry", statuses)
	}
	if statuses[0].FileCount != 1 {
		t.Fatalf("file count = %d, want ignored log excluded", statuses[0].FileCount)
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
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), sourceFile)
}

func TestDotsSyncEntry_LocalOnlyFollowsSymlinkAdoption(t *testing.T) {
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
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want one adopt", ops)
	}
	repoPath := filepath.Join(dotsContentDir(repoDir), "gitconfig", ".gitconfig")
	if got, err := os.ReadFile(repoPath); err != nil || string(got) != "[user]\n" {
		t.Fatalf("repo source = %q err=%v, want followed symlink content", got, err)
	}
	if info, err := os.Lstat(repoPath); err != nil {
		t.Fatalf("repo source stat: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("repo source is still a symlink")
	}
	if got, err := os.ReadFile(externalTarget); err != nil || string(got) != "[user]\n" {
		t.Fatalf("external target changed: body=%q err=%v", got, err)
	}
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(linkPath): %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(repoPath): %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("link resolves to %q, want repo source %q", resolved, wantResolved)
	}
}

func TestDotsList_UsesHostVariantPackage(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "work.local")
	a, cfgDir, repoDir := newDotsApp(t)

	source := filepath.Join(dotsContentDir(repoDir), "nvim-work", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
		Name: "nvim",
		Path: "~/.config/nvim",
		Hosts: map[string]config.DotVariant{
			"work": {Package: "nvim-work"},
		},
	}}, "")

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if got := statuses[0].Package; got != "nvim-work" {
		t.Fatalf("Package = %q, want nvim-work", got)
	}
	if !statuses[0].Variant {
		t.Fatal("Variant = false, want true for active host package")
	}
	wantSource := filepath.Join(dotsContentDir(repoDir), "nvim-work", ".config", "nvim")
	if got := statuses[0].SourcePath; got != wantSource {
		t.Fatalf("SourcePath = %q, want %q", got, wantSource)
	}
}

func TestDotsHasActiveHostVariant(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "work.local")
	a, cfgDir, repoDir := newDotsApp(t)

	source := filepath.Join(dotsContentDir(repoDir), "nvim-work", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{
			Name: "nvim",
			Path: "~/.config/nvim",
			Hosts: map[string]config.DotVariant{
				"work": {Package: "nvim-work"},
			},
		},
		{Name: "tmux", Path: "~/.tmux.conf"},
	}, "")

	hasVariant, err := a.DotsHasActiveHostVariant("nvim")
	if err != nil {
		t.Fatalf("DotsHasActiveHostVariant(nvim): %v", err)
	}
	if !hasVariant {
		t.Fatal("DotsHasActiveHostVariant(nvim) = false, want true")
	}

	hasVariant, err = a.DotsHasActiveHostVariant("tmux")
	if err != nil {
		t.Fatalf("DotsHasActiveHostVariant(tmux): %v", err)
	}
	if hasVariant {
		t.Fatal("DotsHasActiveHostVariant(tmux) = true, want false")
	}
}

func TestDotsAddHostVariant_SeedsFromDefaultWhenLocalTargetMissing(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "work")
	a, cfgDir, repoDir := newDotsApp(t)

	source := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, "")

	info, ops, err := a.DotsAddHostVariant(context.Background(), "nvim", app.DotsAddVariantOptions{
		Host: "work.local",
	})
	if err != nil {
		t.Fatalf("DotsAddHostVariant: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("ops = %v, want none without sync", ops)
	}
	if info.Host != "work" || info.Package != "nvim@work" {
		t.Fatalf("variant info = %+v, want host work package nvim@work", info)
	}
	seeded := filepath.Join(dotsContentDir(repoDir), "nvim@work", ".config", "nvim", "init.lua")
	got, err := os.ReadFile(seeded)
	if err != nil {
		t.Fatalf("ReadFile seeded variant: %v", err)
	}
	if string(got) != "default" {
		t.Fatalf("seeded content = %q, want default", string(got))
	}

	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "work")
	if group == nil || len(group.Dots) != 1 {
		t.Fatalf("work group dots = %#v", group)
	}
	if got := group.Dots[0].Hosts["work"].Package; got != "nvim@work" {
		t.Fatalf("host variant package = %q, want nvim@work", got)
	}
}

func TestDotsAddHostVariant_SeedsFromLocalTargetWhenPresent(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "work")
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")

	source := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(home, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, "")

	info, _, err := a.DotsAddHostVariant(context.Background(), "nvim", app.DotsAddVariantOptions{
		Host: "work.local",
	})
	if err != nil {
		t.Fatalf("DotsAddHostVariant: %v", err)
	}
	if info.Host != "work" || info.Package != "nvim@work" {
		t.Fatalf("variant info = %+v, want host work package nvim@work", info)
	}
	seeded := filepath.Join(dotsContentDir(repoDir), "nvim@work", ".config", "nvim", "init.lua")
	got, err := os.ReadFile(seeded)
	if err != nil {
		t.Fatalf("ReadFile seeded variant: %v", err)
	}
	if string(got) != "local" {
		t.Fatalf("seeded content = %q, want local", string(got))
	}
}

func TestDotsAddDiscoveredHostVariant_TracksLocalContentAsHostSpecificAndRestows(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "work.local")

	tests := []struct {
		name    string
		path    string
		content string
	}{
		{name: "gitconfig", path: ".gitconfig", content: "[user]\n\tname = Local\n"},
		{name: "kitty", path: filepath.Join(".config", "kitty", "kitty.conf"), content: "font_size 12\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, cfgDir, repoDir := newDotsApp(t)
			target := filepath.Join(os.Getenv("HOME"), tt.path)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			result, err := a.DotsAddDiscoveredHostVariantWithState(context.Background(), tt.name, app.DotsAddVariantOptions{Sync: true})
			if err != nil {
				t.Fatalf("DotsAddDiscoveredHostVariantWithState: %v", err)
			}
			if result.Info.Package != tt.name+"@work" {
				t.Fatalf("variant package = %q, want %q", result.Info.Package, tt.name+"@work")
			}

			variantSource := filepath.Join(repoDir, "dotfiles", tt.name+"@work", tt.path)
			if got, readErr := os.ReadFile(variantSource); readErr != nil || string(got) != tt.content {
				t.Fatalf("variant content = %q, %v; want %q", got, readErr, tt.content)
			}
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil {
				t.Fatalf("EvalSymlinks target: %v", err)
			}
			wantResolved, err := filepath.EvalSymlinks(variantSource)
			if err != nil {
				t.Fatalf("EvalSymlinks variant source: %v", err)
			}
			if resolved != wantResolved {
				t.Fatalf("target resolves to %q, want %q", resolved, wantResolved)
			}

			cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			group := findDotsTestGroup(cfg.Groups, "work")
			if group == nil || len(group.Dots) != 1 || group.Dots[0].Hosts["work"].Package != tt.name+"@work" {
				t.Fatalf("work group dots = %#v, want host variant %q", group, tt.name+"@work")
			}
		})
	}
}

func TestDotsAddDiscoveredHostVariant_RestowFailurePreservesLocalContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test stow shim requires a POSIX shell")
	}
	t.Setenv("OMNI_HOSTNAME", "work.local")
	a, cfgDir, repoDir := newDotsApp(t)

	binDir := t.TempDir()
	stowPath := filepath.Join(binDir, "stow")
	stowScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\necho forced restow failure >&2\nexit 1\n"
	if err := os.WriteFile(stowPath, []byte(stowScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	target := filepath.Join(os.Getenv("HOME"), ".gitconfig")
	content := "[user]\n\tname = Local\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := a.DotsAddDiscoveredHostVariantWithState(context.Background(), "gitconfig", app.DotsAddVariantOptions{Sync: true})
	if err == nil || !strings.Contains(err.Error(), "forced restow failure") {
		t.Fatalf("error = %v, want forced restow failure", err)
	}
	info, statErr := os.Lstat(target)
	if statErr != nil {
		t.Fatalf("restored target stat: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("restored target is still a symlink")
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != content {
		t.Fatalf("restored local content = %q, %v; want %q", got, readErr, content)
	}

	variantSource := filepath.Join(repoDir, "dotfiles", "gitconfig@work", ".gitconfig")
	if got, readErr := os.ReadFile(variantSource); readErr != nil || string(got) != content {
		t.Fatalf("retryable variant content = %q, %v; want %q", got, readErr, content)
	}
	cfg, loadErr := config.Load(filepath.Join(cfgDir, "settings.json"))
	if loadErr != nil {
		t.Fatalf("config.Load: %v", loadErr)
	}
	group := findDotsTestGroup(cfg.Groups, "work")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Hosts["work"].Package != "gitconfig@work" {
		t.Fatalf("work group dots = %#v, want retryable gitconfig@work variant", group)
	}
}

func TestDotsAddDiscoveredHostVariant_GitAutoActions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "work.local")
	const wantCommit = "dots: add gitconfig variant for work"

	t.Run("auto commit", func(t *testing.T) {
		a, _, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoCommit: true})
		initDotsTestGitRepo(t, repoDir)
		writeDotsTestGitconfig(t)

		if _, err := a.DotsAddDiscoveredHostVariantWithState(context.Background(), "gitconfig", app.DotsAddVariantOptions{Sync: true}); err != nil {
			t.Fatalf("DotsAddDiscoveredHostVariantWithState: %v", err)
		}
		if got := dotsTestGit(t, repoDir, "log", "-1", "--pretty=%s"); got != wantCommit {
			t.Fatalf("commit subject = %q, want %q", got, wantCommit)
		}
	})

	t.Run("auto push", func(t *testing.T) {
		a, _, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoPush: true})
		remoteDir := t.TempDir()
		dotsTestGit(t, remoteDir, "init", "--bare", "-q")
		initDotsTestGitRepo(t, repoDir)
		dotsTestGit(t, repoDir, "remote", "add", "origin", remoteDir)
		dotsTestGit(t, repoDir, "commit", "--allow-empty", "-m", "init")
		dotsTestGit(t, repoDir, "push", "-u", "origin", "HEAD")
		writeDotsTestGitconfig(t)

		if _, err := a.DotsAddDiscoveredHostVariantWithState(context.Background(), "gitconfig", app.DotsAddVariantOptions{Sync: true}); err != nil {
			t.Fatalf("DotsAddDiscoveredHostVariantWithState: %v", err)
		}
		if got := dotsTestGit(t, remoteDir, "log", "-1", "--pretty=%s", "--all"); got != wantCommit {
			t.Fatalf("remote commit subject = %q, want %q", got, wantCommit)
		}
	})
}

func initDotsTestGitRepo(t *testing.T, repoDir string) {
	t.Helper()
	dotsTestGit(t, repoDir, "init", "-q")
	dotsTestGit(t, repoDir, "config", "user.email", "test@example.com")
	dotsTestGit(t, repoDir, "config", "user.name", "Test")
	dotsTestGit(t, repoDir, "config", "commit.gpgsign", "false")
	dotsTestGit(t, repoDir, "config", "tag.gpgsign", "false")
}

func writeDotsTestGitconfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dotsTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDotsAddHostVariant_UsesExistingRepoPackageWhenPresent(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "work")
	a, cfgDir, repoDir := newDotsApp(t)

	defaultSource := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	variantSource := filepath.Join(dotsContentDir(repoDir), "nvim@work", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(defaultSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(variantSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultSource, []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variantSource, []byte("existing variant"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, "")

	info, _, err := a.DotsAddHostVariant(context.Background(), "nvim", app.DotsAddVariantOptions{
		Host: "work.local",
	})
	if err != nil {
		t.Fatalf("DotsAddHostVariant: %v", err)
	}
	if info.Host != "work" || info.Package != "nvim@work" {
		t.Fatalf("variant info = %+v, want host work package nvim@work", info)
	}
	got, err := os.ReadFile(variantSource)
	if err != nil {
		t.Fatalf("ReadFile existing variant: %v", err)
	}
	if string(got) != "existing variant" {
		t.Fatalf("variant content = %q, want existing repo content preserved", string(got))
	}
}

func TestDotsRemoveHostVariant_RemovesUnreferencedRepoPackage(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop")
	a, cfgDir, repoDir := newDotsApp(t)

	defaultSource := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	variantRoot := filepath.Join(dotsContentDir(repoDir), "nvim@work")
	variantSource := filepath.Join(variantRoot, ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(defaultSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(variantSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultSource, []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variantSource, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
		Name: "nvim",
		Path: "~/.config/nvim",
		Hosts: map[string]config.DotVariant{
			"work": {Package: "nvim@work"},
		},
	}}, "")

	info, err := a.DotsRemoveHostVariant(context.Background(), "nvim", app.DotsRemoveVariantOptions{
		Host: "work.local",
	})
	if err != nil {
		t.Fatalf("DotsRemoveHostVariant: %v", err)
	}
	if info.Host != "work" || info.Package != "nvim@work" {
		t.Fatalf("variant info = %+v, want work nvim@work", info)
	}
	if _, err := os.Stat(variantRoot); !os.IsNotExist(err) {
		t.Fatalf("variant root stat = %v, want missing", err)
	}
	cfg, err := config.Load(filepath.Join(cfgDir, "settings.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findDotsTestGroup(cfg.Groups, "laptop")
	if group == nil || group.Dots[0].Hosts != nil {
		t.Fatalf("host variants after remove = %#v, want none", group)
	}
}

func TestDotsSync_HostVariantRepairsManagedLinkFromDefaultPackage(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	t.Setenv("OMNI_HOSTNAME", "work")
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")

	defaultSource := filepath.Join(dotsContentDir(repoDir), "gitconfig", ".gitconfig")
	variantSource := filepath.Join(dotsContentDir(repoDir), "gitconfig@work", ".gitconfig")
	if err := os.MkdirAll(filepath.Dir(defaultSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(variantSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultSource, []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variantSource, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".gitconfig")
	if err := os.Symlink(defaultSource, target); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
		Name: "gitconfig",
		Path: "~/.gitconfig",
		Hosts: map[string]config.DotVariant{
			"work": {Package: "gitconfig@work"},
		},
	}}, "")

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) == 0 || ops[0].Kind != dots.OpRepair {
		t.Fatalf("ops = %v, want repair", ops)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks target: %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(variantSource)
	if err != nil {
		t.Fatalf("EvalSymlinks variant source: %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("target resolves to %q, want %q", resolved, wantResolved)
	}
}

func TestDotsDelete_RemovesAllVariantPackagesAndKeepsLinkedLocal(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "work")
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")

	defaultSource := filepath.Join(dotsContentDir(repoDir), "gitconfig", ".gitconfig")
	variantSource := filepath.Join(dotsContentDir(repoDir), "gitconfig@work", ".gitconfig")
	if err := os.MkdirAll(filepath.Dir(defaultSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(variantSource), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaultSource, []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variantSource, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".gitconfig")
	if err := os.Symlink(defaultSource, target); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{
		Name: "gitconfig",
		Path: "~/.gitconfig",
		Hosts: map[string]config.DotVariant{
			"work": {Package: "gitconfig@work"},
		},
	}}, "")

	if err := a.DotsDelete(context.Background(), "gitconfig"); err != nil {
		t.Fatalf("DotsDelete: %v", err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat target: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("target is still a symlink after delete")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(got) != "default" {
		t.Fatalf("target content = %q, want default", string(got))
	}
	for _, pkg := range []string{"gitconfig", "gitconfig@work"} {
		if _, err := os.Lstat(filepath.Join(dotsContentDir(repoDir), pkg)); !os.IsNotExist(err) {
			t.Fatalf("package %s still exists or stat failed: %v", pkg, err)
		}
	}
}

func TestDotsSync_FileOverwrittenByNewerLocalAdoptsAndRelinks(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	sourcePath := filepath.Join(dotsContentDir(repoDir), "gitconfig", ".gitconfig")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("[repo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(home, ".gitconfig")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "gitconfig", Path: targetPath},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("initial DotsSync: %v", err)
	}
	assertSymlinkResolvesTo(t, targetPath, sourcePath)

	oldTime := time.Unix(1_700_000_000, 0)
	newTime := oldTime.Add(time.Hour)
	if err := os.Chtimes(sourcePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(targetPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("[local]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(targetPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	assertDotState(t, statuses[0], dots.StateModified, []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore})

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want OpAdopt", ops)
	}
	if got, readErr := os.ReadFile(sourcePath); readErr != nil || string(got) != "[local]\n" {
		t.Fatalf("source file = %q, %v; want newer local content", got, readErr)
	}
	assertSymlinkResolvesTo(t, targetPath, sourcePath)
}

func TestDotsSync_DirectoryChildOverwrittenByNewerLocalAdoptsAndRelinks(t *testing.T) {
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
	sourcePath := filepath.Join(srcDir, "init.lua")
	if err := os.WriteFile(sourcePath, []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetDir},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("initial DotsSync: %v", err)
	}
	targetPath := filepath.Join(targetDir, "init.lua")
	assertRealDirectory(t, targetDir)
	assertSymlinkResolvesTo(t, targetPath, sourcePath)

	oldTime := time.Unix(1_700_000_000, 0)
	newTime := oldTime.Add(time.Hour)
	if err := os.Chtimes(sourcePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(targetPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(targetPath, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	localOnlyPath := filepath.Join(targetDir, "local.lua")
	if err := os.WriteFile(localOnlyPath, []byte("-- local only"), 0o644); err != nil {
		t.Fatal(err)
	}

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	assertDotState(t, statuses[0], dots.StateModified, []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore})
	childStates := make(map[string]dots.State)
	for _, child := range statuses[0].Children {
		childStates[child.Name] = child.State
	}
	wantChildStates := map[string]dots.State{
		"init.lua":  dots.StateModified,
		"local.lua": dots.StateLocalOnly,
	}
	if !reflect.DeepEqual(childStates, wantChildStates) {
		t.Fatalf("child states = %#v, want %#v", childStates, wantChildStates)
	}

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want OpAdopt", ops)
	}
	if got, readErr := os.ReadFile(sourcePath); readErr != nil || string(got) != "-- local" {
		t.Fatalf("source file = %q, %v; want newer local content", got, readErr)
	}
	localOnlySourcePath := filepath.Join(srcDir, "local.lua")
	if got, readErr := os.ReadFile(localOnlySourcePath); readErr != nil || string(got) != "-- local only" {
		t.Fatalf("local-only source file = %q, %v; want copied local content", got, readErr)
	}
	assertRealDirectory(t, targetDir)
	assertSymlinkResolvesTo(t, targetPath, sourcePath)
	assertSymlinkResolvesTo(t, localOnlyPath, localOnlySourcePath)
}

func TestDotsSync_DirectoryLocalOnlyNewFileAdoptsAndRelinks(t *testing.T) {
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
	sourcePath := filepath.Join(srcDir, "init.lua")
	if err := os.WriteFile(sourcePath, []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetDir},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("initial DotsSync: %v", err)
	}
	localOnlyPath := filepath.Join(targetDir, "local.lua")
	if err := os.WriteFile(localOnlyPath, []byte("-- local only"), 0o644); err != nil {
		t.Fatal(err)
	}

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	assertDotState(t, statuses[0], dots.StateModified, []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore})

	ops, err := a.DotsSync(dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want OpAdopt", ops)
	}
	localOnlySourcePath := filepath.Join(srcDir, "local.lua")
	if got, readErr := os.ReadFile(localOnlySourcePath); readErr != nil || string(got) != "-- local only" {
		t.Fatalf("local-only source file = %q, %v; want copied local content", got, readErr)
	}
	assertSymlinkResolvesTo(t, filepath.Join(targetDir, "init.lua"), sourcePath)
	assertSymlinkResolvesTo(t, localOnlyPath, localOnlySourcePath)
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

func TestDotsSyncEntry_OnConflictUseRepoAutoResolves(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "codex", ".config", "codex")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "config.toml"), []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(home, ".config", "codex")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "config.toml"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, filepath.Join(srcDir, "config.toml"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(targetDir, "config.toml"), time.Unix(1_700_000_000, 0))

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "codex", Path: targetDir, OnConflict: "use_repo"},
	}, home)

	ops, err := a.DotsSyncEntry(context.Background(), "codex", dots.SyncOptions{})
	if err != nil {
		t.Fatalf("DotsSyncEntry with on_conflict=use_repo: unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpRepair {
		t.Fatalf("ops = %v, want OpRepair", ops)
	}
	assertSymlinkResolvesTo(t, filepath.Join(targetDir, "config.toml"), filepath.Join(srcDir, "config.toml"))
}

func TestDotsList_HealthOK(t *testing.T) {
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
	if want := (app.DotFileCounts{Synced: 2, Ignored: 2}); statuses[0].Counts != want {
		t.Fatalf("counts = %#v, want %#v", statuses[0].Counts, want)
	}
	childNames := make([]string, 0, len(statuses[0].Children))
	childByName := make(map[string]app.DotChild)
	for _, child := range statuses[0].Children {
		childNames = append(childNames, child.Name)
		childByName[child.Name] = child
	}
	if !reflect.DeepEqual(childNames, []string{"lua", "node_modules", "init.lua"}) {
		t.Fatalf("children = %v, want full direct child tree", childNames)
	}
	if childByName["lua"].Ignored || childByName["init.lua"].Ignored {
		t.Fatalf("tracked direct children should not be marked ignored: %#v", statuses[0].Children)
	}
	if child := childByName["node_modules"]; !child.Ignored || child.State != dots.StateIgnored {
		t.Fatalf("ignored direct child should stay in the full tree as ignored, got: %#v", child)
	}
	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	var mergedIgnored *app.DotStatus
	for i := range result.Entries {
		if result.Entries[i].Name == "nvim" && result.Entries[i].State == dots.StateIgnored {
			mergedIgnored = &result.Entries[i]
			break
		}
	}
	if mergedIgnored == nil {
		t.Fatalf("DiscoverDotsStatus entries = %#v, want merged ignored nvim entry", result.Entries)
	}
	if len(mergedIgnored.Children) == 0 {
		t.Fatal("merged ignored nvim entry has no children tree")
	}
	if !dotChildTreeHasRel(mergedIgnored.Children, "node_modules") {
		t.Fatalf("merged ignored nvim entry should contain node_modules: %#v", mergedIgnored.Children)
	}
}

func TestDotsList_IgnoredDirectoriesExposeNestedChildren(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	deepIgnoredPath := filepath.Join("node_modules", "a", "b", "c", "d", "e", "mod.js")
	if err := os.MkdirAll(filepath.Join(srcDir, filepath.Dir(deepIgnoredPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, deepIgnoredPath), []byte("module"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "nvim")
	if err := os.Symlink(srcDir, target); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: target}}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || !dotChildTreeHasRel(statuses[0].Children, filepath.ToSlash(deepIgnoredPath)) {
		t.Fatalf("ignored directory descendants should stay available for expansion: %#v", statuses)
	}
	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	for _, status := range result.Entries {
		if status.Name == "nvim" && status.State == dots.StateIgnored {
			if !dotChildTreeHasRel(status.Children, filepath.ToSlash(deepIgnoredPath)) {
				t.Fatalf("ignored section should expose nested descendants: %#v", status.Children)
			}
			return
		}
	}
	t.Fatalf("ignored nvim status not found: %#v", result.Entries)
}

func dotChildTreeHasRel(children []app.DotChild, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, child := range children {
		if filepath.ToSlash(child.RelPath) == rel {
			return true
		}
		if dotChildTreeHasRel(child.Children, rel) {
			return true
		}
	}
	return false
}

func TestDotsList_ClaudeAllowlistKeepsAllowedFilesAndListsIgnoredChildren(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
	for _, dir := range []string{
		filepath.Join(srcDir, "plugins"),
		filepath.Join(srcDir, "skills", "example"),
		filepath.Join(srcDir, "agents"),
		filepath.Join(srcDir, "hooks"),
		filepath.Join(srcDir, "scripts"),
		filepath.Join(srcDir, "projects"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(srcDir, "settings.json"):                      "{}",
		filepath.Join(srcDir, "CLAUDE.md"):                          "# Claude",
		filepath.Join(srcDir, "mcp.json"):                           "{}",
		filepath.Join(srcDir, "plugins", "installed_plugins.json"):  "[]",
		filepath.Join(srcDir, "plugins", "configured_plugins.json"): "[]",
		filepath.Join(srcDir, "skills", "example", "SKILL.md"):      "# skill",
		filepath.Join(srcDir, "agents", "review.md"):                "# agent",
		filepath.Join(srcDir, "hooks", "pre.sh"):                    "#!/bin/sh\n",
		filepath.Join(srcDir, "scripts", "status.sh"):               "#!/bin/sh\n",
		filepath.Join(srcDir, ".omc-config.json"):                   "{}",
		filepath.Join(srcDir, "keybindings.json"):                   "{}",
		filepath.Join(srcDir, "statusline-command.sh"):              "#!/bin/sh\n",
		filepath.Join(srcDir, "history.jsonl"):                      "{}",
		filepath.Join(srcDir, "projects", "session.json"):           "{}",
		filepath.Join(srcDir, "plugins", "cache.json"):              "{}",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	assertMergedIgnoredEntry := func(result *app.DotsStatusResult) *app.DotStatus {
		t.Helper()
		var found *app.DotStatus
		for i := range result.Entries {
			if result.Entries[i].State == dots.StateIgnored && result.Entries[i].Name == "claude" {
				found = &result.Entries[i]
				break
			}
		}
		if found == nil {
			names := make([]string, 0)
			for _, e := range result.Entries {
				if e.State == dots.StateIgnored {
					names = append(names, e.Name)
				}
			}
			t.Fatalf("no merged ignored entry named 'claude', got: %v", names)
		}
		if len(found.Children) == 0 {
			t.Fatal("merged ignored entry 'claude' has no children tree")
		}
		return found
	}
	discovered, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus before add: %v", err)
	}
	assertMergedIgnoredEntry(discovered)

	if _, err := a.DotsAddDiscoveredEntry("claude", ""); err != nil {
		t.Fatalf("DotsAddDiscoveredEntry: %v", err)
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v, want one claude entry", statuses)
	}
	if statuses[0].FileCount != 8 {
		t.Fatalf("file count = %d, want the eight user-authored Claude proposal files", statuses[0].FileCount)
	}
	childIgnored := make(map[string]bool, len(statuses[0].Children))
	for _, child := range statuses[0].Children {
		childIgnored[filepath.ToSlash(child.RelPath)] = child.Ignored
	}
	for _, rel := range []string{
		"settings.json",
		"CLAUDE.md",
		"agents",
		"hooks",
		"scripts",
		".omc-config.json",
		"keybindings.json",
		"statusline-command.sh",
	} {
		ignored, ok := childIgnored[rel]
		if !ok || ignored {
			t.Fatalf("%s should be included in Claude default proposal: %#v", rel, statuses[0].Children)
		}
	}
	for _, rel := range []string{"history.jsonl", "projects", "skills", "mcp.json", "plugins"} {
		ignored, ok := childIgnored[rel]
		if !ok || !ignored {
			t.Fatalf("%s should remain in the full Claude child tree as ignored: %#v", rel, statuses[0].Children)
		}
	}

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	mergedIgnored := assertMergedIgnoredEntry(result)
	for _, rel := range []string{"history.jsonl", "projects", "skills"} {
		if !dotChildTreeHasRel(mergedIgnored.Children, rel) {
			t.Fatalf("%s should be listed in the merged ignored Claude tree: %#v", rel, mergedIgnored.Children)
		}
	}
}

func TestDotsList_CodexAllowlistKeepsAllowedFilesAndListsIgnoredChildren(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	a, _, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "codex", ".codex")
	for _, dir := range []string{
		filepath.Join(srcDir, "rules", "nested"),
		filepath.Join(srcDir, "skills", "example"),
		filepath.Join(srcDir, "skills", ".system", "generated"),
		filepath.Join(srcDir, "sessions"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(srcDir, "config.toml"):                                "model = \"gpt\"",
		filepath.Join(srcDir, "mcp.json"):                                   "{}",
		filepath.Join(srcDir, "AGENTS.md"):                                  "# Agents",
		filepath.Join(srcDir, "RTK.md"):                                     "# RTK",
		filepath.Join(srcDir, "rules", "global.md"):                         "# rule",
		filepath.Join(srcDir, "rules", "nested", "rule.md"):                 "# nested",
		filepath.Join(srcDir, "skills", "example", "SKILL.md"):              "# skill",
		filepath.Join(srcDir, "skills", ".system", "generated", "SKILL.md"): "# generated",
		filepath.Join(srcDir, "history.jsonl"):                              "{}",
		filepath.Join(srcDir, "sessions", "latest.json"):                    "{}",
		filepath.Join(srcDir, "tmp.json"):                                   "{}",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	assertMergedIgnoredEntry := func(result *app.DotsStatusResult) *app.DotStatus {
		t.Helper()
		var found *app.DotStatus
		for i := range result.Entries {
			if result.Entries[i].State == dots.StateIgnored && result.Entries[i].Name == "codex" {
				found = &result.Entries[i]
				break
			}
		}
		if found == nil {
			names := make([]string, 0)
			for _, e := range result.Entries {
				if e.State == dots.StateIgnored {
					names = append(names, e.Name)
				}
			}
			t.Fatalf("no merged ignored entry named 'codex', got: %v", names)
		}
		if len(found.Children) == 0 {
			t.Fatal("merged ignored entry 'codex' has no children tree")
		}
		return found
	}
	discovered, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus before add: %v", err)
	}
	assertMergedIgnoredEntry(discovered)

	if _, err := a.DotsAddDiscoveredEntry("codex", ""); err != nil {
		t.Fatalf("DotsAddDiscoveredEntry: %v", err)
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v, want one codex entry", statuses)
	}
	if statuses[0].FileCount != 5 {
		t.Fatalf("file count = %d, want the five user-authored Codex proposal files", statuses[0].FileCount)
	}
	childIgnored := make(map[string]bool, len(statuses[0].Children))
	for _, child := range statuses[0].Children {
		childIgnored[filepath.ToSlash(child.RelPath)] = child.Ignored
	}
	for _, rel := range []string{
		"config.toml",
		"AGENTS.md",
		"RTK.md",
		"rules",
	} {
		ignored, ok := childIgnored[rel]
		if !ok || ignored {
			t.Fatalf("%s should be included in Codex default proposal: %#v", rel, statuses[0].Children)
		}
	}
	for _, rel := range []string{"history.jsonl", "sessions", "tmp.json", "skills", "mcp.json"} {
		ignored, ok := childIgnored[rel]
		if !ok || !ignored {
			t.Fatalf("%s should remain in the full Codex child tree as ignored: %#v", rel, statuses[0].Children)
		}
	}

	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	mergedIgnored := assertMergedIgnoredEntry(result)
	for _, rel := range []string{"history.jsonl", "sessions", "tmp.json", "skills"} {
		if !dotChildTreeHasRel(mergedIgnored.Children, rel) {
			t.Fatalf("%s should be listed in the merged ignored Codex tree: %#v", rel, mergedIgnored.Children)
		}
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
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua"))
	if _, err := os.Lstat(filepath.Join(nvimPath, "agent.sock")); err != nil {
		t.Fatalf("ignored local socket should be preserved: %v", err)
	}
}

func TestDotsList_HealthMissing(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

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
	if statuses[0].State != dots.StateLocalOnly {
		t.Fatalf("state = %q, want local-only", statuses[0].State)
	}
	if statuses[0].FileCount != 2 {
		t.Fatalf("file count = %d, want 2", statuses[0].FileCount)
	}
	if want := (app.DotFileCounts{OutOfSync: 2}); statuses[0].Counts != want {
		t.Fatalf("counts = %#v, want %#v", statuses[0].Counts, want)
	}
	childNames := make([]string, 0, len(statuses[0].Children))
	for _, child := range statuses[0].Children {
		childNames = append(childNames, child.Name)
	}
	if !reflect.DeepEqual(childNames, []string{"lua", "init.lua"}) {
		t.Fatalf("children = %v, want direct children only", childNames)
	}
	var luaChild app.DotChild
	for _, child := range statuses[0].Children {
		if child.Name == "lua" {
			luaChild = child
			break
		}
	}
	if len(luaChild.Children) != 1 || luaChild.Children[0].Name != "config.lua" {
		t.Fatalf("lua children = %#v, want nested config.lua", luaChild.Children)
	}
	if luaChild.Children[0].State != dots.StateLocalOnly {
		t.Fatalf("nested config.lua state = %q, want local-only", luaChild.Children[0].State)
	}
	if want := (app.DotFileCounts{OutOfSync: 1}); luaChild.Counts != want {
		t.Fatalf("lua counts = %#v, want %#v", luaChild.Counts, want)
	}
}

func TestDotsList_DirectoryChildrenReportIndividualStates(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(srcDir, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(srcDir, "init.lua"):           "-- cfg",
		filepath.Join(srcDir, "lua", "config.lua"):  "-- repo",
		filepath.Join(srcDir, "lua", "missing.lua"): "-- missing",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	localDir := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(localDir, "lua"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(srcDir, "init.lua"), filepath.Join(localDir, "init.lua")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "lua", "config.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, filepath.Join(srcDir, "lua", "config.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(localDir, "lua", "config.lua"), time.Unix(1_700_000_000, 0))
	if err := os.WriteFile(filepath.Join(localDir, "local.lua"), []byte("-- local only"), 0o644); err != nil {
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
	if statuses[0].State != dots.StateConflict {
		t.Fatalf("state = %q, want conflict", statuses[0].State)
	}
	if want := (app.DotFileCounts{Synced: 1, OutOfSync: 3}); statuses[0].Counts != want {
		t.Fatalf("counts = %#v, want %#v", statuses[0].Counts, want)
	}
	childStates := make(map[string]dots.State)
	childCounts := make(map[string]app.DotFileCounts)
	for _, child := range statuses[0].Children {
		childStates[filepath.ToSlash(child.RelPath)] = child.State
		childCounts[filepath.ToSlash(child.RelPath)] = child.Counts
	}
	want := map[string]dots.State{
		"init.lua":  dots.StateSynced,
		"lua":       dots.StateConflict,
		"local.lua": dots.StateLocalOnly,
	}
	if len(childStates) != len(want) {
		t.Fatalf("child states = %#v, want direct children only: %#v", childStates, want)
	}
	for rel, state := range want {
		if childStates[rel] != state {
			t.Fatalf("child states = %#v, want %s=%s", childStates, rel, state)
		}
	}
	wantCounts := map[string]app.DotFileCounts{
		"init.lua":  {Synced: 1},
		"lua":       {OutOfSync: 2},
		"local.lua": {OutOfSync: 1},
	}
	if !reflect.DeepEqual(childCounts, wantCounts) {
		t.Fatalf("child counts = %#v, want %#v", childCounts, wantCounts)
	}
	var luaChild app.DotChild
	for _, child := range statuses[0].Children {
		if child.RelPath == "lua" {
			luaChild = child
			break
		}
	}
	nestedStates := make(map[string]dots.State)
	for _, child := range luaChild.Children {
		nestedStates[filepath.ToSlash(child.RelPath)] = child.State
	}
	wantNested := map[string]dots.State{
		"lua/config.lua":  dots.StateConflict,
		"lua/missing.lua": dots.StateMissing,
	}
	if !reflect.DeepEqual(nestedStates, wantNested) {
		t.Fatalf("nested child states = %#v, want %#v", nestedStates, wantNested)
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

	backupPath := filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")
	if got, err := os.ReadFile(backupPath); err != nil {
		t.Fatalf("expected backup before removing managed link: %v", err)
	} else if string(got) != "-- cfg" {
		t.Fatalf("backup content = %q, want -- cfg", got)
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

func TestDotsDelete_SyncedDirectoryKeepsIgnoredLocalChildren(t *testing.T) {
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
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))

	ignoredPath := filepath.Join(nvimPath, "debug.log")
	if err := os.WriteFile(ignoredPath, []byte("local only"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := a.DotsDelete(context.Background(), "nvim"); err != nil {
		t.Fatalf("DotsDelete: %v", err)
	}

	assertRealDirectory(t, nvimPath)
	if info, err := os.Lstat(filepath.Join(nvimPath, "init.lua")); err != nil {
		t.Fatalf("expected managed file to remain local: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("managed file mode = %v, want regular local file", info.Mode())
	}
	if body, err := os.ReadFile(filepath.Join(nvimPath, "init.lua")); err != nil {
		t.Fatalf("ReadFile init.lua: %v", err)
	} else if string(body) != "-- cfg" {
		t.Fatalf("init.lua = %q, want -- cfg", body)
	}
	if body, err := os.ReadFile(ignoredPath); err != nil {
		t.Fatalf("ignored local child should survive delete: %v", err)
	} else if string(body) != "local only" {
		t.Fatalf("ignored local child = %q, want local only", body)
	}
	if _, err := os.Lstat(filepath.Join(dotsContentDir(repoDir), "nvim")); !os.IsNotExist(err) {
		t.Fatalf("repo package should be removed, stat err=%v", err)
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
	backup := filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("ReadFile backup: %v", err)
	}
	if string(got) != "-- cfg" {
		t.Fatalf("backup content = %q, want -- cfg", string(got))
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
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.Symlink("/some/other/path", nvimPath); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

	if err := a.DotsDelete(context.Background(), "nvim"); err != nil {
		t.Fatalf("DotsDelete: %v", err)
	}
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
	ops, err := a.DotsAdd(context.Background(), liveFile, app.DotsAddOptions{Name: "myvim", Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd with explicit name: %v", err)
	}
	if len(ops) == 0 {
		t.Error("expected at least one op")
	}
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

func TestDotsAdd_AdoptFollowsNestedSymlink(t *testing.T) {
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

	ops, err := a.DotsAdd(context.Background(), liveDir, app.DotsAddOptions{Name: "myapp", Adopt: true})
	if err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpLink {
		t.Fatalf("ops = %v, want OpLink", ops)
	}
	repoFile := filepath.Join(dotsContentDir(repoDir), "myapp", ".config", "myapp", "settings.json")
	if got, readErr := os.ReadFile(repoFile); readErr != nil || string(got) != "{}" {
		t.Fatalf("repo regular file = %q err=%v, want local content", got, readErr)
	}
	repoExternal := filepath.Join(dotsContentDir(repoDir), "myapp", ".config", "myapp", "external.conf")
	if got, readErr := os.ReadFile(repoExternal); readErr != nil || string(got) != "external" {
		t.Fatalf("repo followed symlink file = %q err=%v, want external content", got, readErr)
	}
	info, statErr := os.Lstat(repoExternal)
	if statErr != nil {
		t.Fatalf("repo followed symlink stat: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("repo followed symlink file is still a symlink")
	}
	if got, readErr := os.ReadFile(externalFile); readErr != nil || string(got) != "external" {
		t.Fatalf("external symlink target changed: body=%q err=%v", got, readErr)
	}
	assertRealDirectory(t, liveDir)
	assertSymlinkResolvesTo(t, filepath.Join(liveDir, "settings.json"), repoFile)
	assertSymlinkResolvesTo(t, filepath.Join(liveDir, "external.conf"), repoExternal)
}

func TestDotsAdd_AdoptPreservesTopLevelSymlinkBackup(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	external := filepath.Join(home, "shared", "vimrc")
	if err := os.MkdirAll(filepath.Dir(external), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("set number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".vimrc")
	if err := os.Symlink(external, target); err != nil {
		t.Fatal(err)
	}

	if _, err := a.DotsAdd(context.Background(), target, app.DotsAddOptions{Name: "vimrc", Adopt: true}); err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	backup := filepath.Join(home, dots.BackupDirName, ".vimrc")
	info, err := os.Lstat(backup)
	if err != nil {
		t.Fatalf("backup symlink stat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("backup mode = %v, want symlink", info.Mode())
	}
	if got, err := os.Readlink(backup); err != nil || got != external {
		t.Fatalf("backup symlink target = %q, %v; want %q", got, err, external)
	}
	repoFile := filepath.Join(dotsContentDir(repoDir), "vimrc", ".vimrc")
	if got, err := os.ReadFile(repoFile); err != nil || string(got) != "set number\n" {
		t.Fatalf("repo file = %q, %v", got, err)
	}
	assertSymlinkResolvesTo(t, target, repoFile)
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
	if err := os.MkdirAll(filepath.Join(appDir, "workspaces", "work", "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(appDir, "settings.json"):                                     "{}",
		filepath.Join(appDir, "workspaces", "work", "config.json"):                 "work",
		filepath.Join(appDir, "workspaces", "work", "auth.json"):                   "secret",
		filepath.Join(appDir, "workspaces", "work", "node_modules", "pkg", "x.js"): "module",
		filepath.Join(appDir, "node_modules", "pkg", "x.js"):                       "module",
		filepath.Join(appDir, ".git", "config"):                                    "git",
		filepath.Join(appDir, "auth.json"):                                         "secret",
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
	if _, err := os.Lstat(filepath.Join(repoAppDir, "workspaces", "work", "config.json")); err != nil {
		t.Fatalf("nested non-ignored file should be copied into repo: %v", err)
	}
	for _, rel := range []string{
		"node_modules",
		".git",
		"auth.json",
		filepath.Join("workspaces", "work", "node_modules"),
		filepath.Join("workspaces", "work", "auth.json"),
	} {
		if _, err := os.Lstat(filepath.Join(repoAppDir, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s should be skipped while copying into repo, stat err=%v", rel, err)
		}
	}
}

func TestDotsAdd_AdoptAllowlistedDirectoryPreservesIgnoredRuntimeData(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, ".claude")
	files := map[string]string{
		"settings.json":                   `{"theme":"dark"}`,
		"projects/session.jsonl":          "project history",
		"plugins/cache/blob":              "plugin cache",
		"sessions/current/transcript.log": "session data",
	}
	for rel, content := range files {
		path := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := a.DotsAdd(context.Background(), target, app.DotsAddOptions{Name: "claude", Adopt: true}); err != nil {
		t.Fatalf("DotsAdd: %v", err)
	}

	repoTarget := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
	backupTarget := filepath.Join(home, dots.BackupDirName, ".claude")
	for _, root := range []string{repoTarget, backupTarget} {
		if got, err := os.ReadFile(filepath.Join(root, "settings.json")); err != nil || string(got) != files["settings.json"] {
			t.Fatalf("%s settings.json = %q, %v", root, got, err)
		}
		for _, rel := range []string{"projects", filepath.Join("plugins", "cache"), "sessions"} {
			if _, err := os.Lstat(filepath.Join(root, rel)); !os.IsNotExist(err) {
				t.Fatalf("%s copied ignored path %s, stat err = %v", root, rel, err)
			}
		}
	}
	for _, rel := range []string{"projects/session.jsonl", "plugins/cache/blob", "sessions/current/transcript.log"} {
		if got, err := os.ReadFile(filepath.Join(target, rel)); err != nil || string(got) != files[rel] {
			t.Fatalf("ignored %s = %q, %v", rel, got, err)
		}
	}
	assertRealDirectory(t, target)
	assertSymlinkResolvesTo(t, filepath.Join(target, "settings.json"), filepath.Join(repoTarget, "settings.json"))
}

func TestDotsAdd_AdoptRejectsDirectoryWithNoManagedContent(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, ".claude")
	runtimePath := filepath.Join(target, "projects", "session.jsonl")
	cachePath := filepath.Join(target, "plugins", "cache", "blob")
	for path, content := range map[string]string{
		runtimePath: "project history",
		cachePath:   "plugin cache",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := a.DotsAdd(context.Background(), target, app.DotsAddOptions{Name: "claude", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "no managed files") {
		t.Fatalf("DotsAdd error = %v, want no managed files", err)
	}
	if got, readErr := os.ReadFile(runtimePath); readErr != nil || string(got) != "project history" {
		t.Fatalf("ignored runtime content = %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(cachePath); readErr != nil || string(got) != "plugin cache" {
		t.Fatalf("ignored cache content = %q, %v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(dotsContentDir(repoDir), "claude")); !os.IsNotExist(statErr) {
		t.Fatalf("empty package remains, stat err = %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(home, dots.BackupDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("backup created for empty managed set, stat err = %v", statErr)
	}
}

func TestDotsAdd_AdoptAllowlistedDirectoryRestowFailurePreservesIgnoredRuntimeData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test stow shim requires a POSIX shell")
	}
	a, _, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, ".claude")
	managedPath := filepath.Join(target, "settings.json")
	ignoredPath := filepath.Join(target, "projects", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(ignoredPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoredPath, []byte("project history"), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	stowPath := filepath.Join(binDir, "stow")
	stowScript := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then exit 0; fi\necho forced restow failure >&2\nexit 1\n"
	if err := os.WriteFile(stowPath, []byte(stowScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := a.DotsAdd(context.Background(), target, app.DotsAddOptions{Name: "claude", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "forced restow failure") {
		t.Fatalf("DotsAdd error = %v, want forced restow failure", err)
	}
	assertRealDirectory(t, target)
	if info, statErr := os.Lstat(managedPath); statErr != nil {
		t.Fatalf("managed path stat: %v", statErr)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("managed path remains a symlink after rollback")
	}
	if got, readErr := os.ReadFile(managedPath); readErr != nil || string(got) != `{"theme":"dark"}` {
		t.Fatalf("managed content = %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(ignoredPath); readErr != nil || string(got) != "project history" {
		t.Fatalf("ignored content = %q, %v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(dotsContentDir(repoDir), "claude")); !os.IsNotExist(statErr) {
		t.Fatalf("incomplete package remains, stat err = %v", statErr)
	}
}

func TestDotsAdd_AdoptAllowlistedDirectoryConfigFailurePreservesIgnoredRuntimeData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission failure requires POSIX permissions")
	}
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	target := filepath.Join(home, ".claude")
	managedPath := filepath.Join(target, "settings.json")
	ignoredPath := filepath.Join(target, "projects", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(ignoredPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoredPath, []byte("project history"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o755) })

	_, err := a.DotsAdd(context.Background(), target, app.DotsAddOptions{Name: "claude", Adopt: true})
	if err == nil || !strings.Contains(err.Error(), "save config") {
		t.Fatalf("DotsAdd error = %v, want save config failure", err)
	}
	assertRealDirectory(t, target)
	if info, statErr := os.Lstat(managedPath); statErr != nil {
		t.Fatalf("managed path stat: %v", statErr)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("managed path remains a symlink after rollback")
	}
	if got, readErr := os.ReadFile(managedPath); readErr != nil || string(got) != `{"theme":"dark"}` {
		t.Fatalf("managed content = %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(ignoredPath); readErr != nil || string(got) != "project history" {
		t.Fatalf("ignored content = %q, %v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(dotsContentDir(repoDir), "claude")); !os.IsNotExist(statErr) {
		t.Fatalf("incomplete package remains, stat err = %v", statErr)
	}
}

func TestDotsSync_NotConfigured(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != app.HealthConflict {
		t.Errorf("health = %v, want HealthConflict", statuses[0].Health)
	}
}

func TestDotsList_ClassifiesTrackedStateMatrix(t *testing.T) {
	syncable := []dots.Action{dots.ActionSync, dots.ActionRemove, dots.ActionIgnore}
	conflict := []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove, dots.ActionIgnore}
	noSource := []dots.Action{dots.ActionRemove, dots.ActionIgnore}
	healthy := []dots.Action{dots.ActionRemove, dots.ActionIgnore}

	tests := []struct {
		name        string
		source      bool
		setupTarget func(t *testing.T, sourcePath, targetPath string)
		wantState   dots.State
		wantActions []dots.Action
	}{
		{
			name:        "source exists local missing",
			source:      true,
			wantState:   dots.StateMissing,
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
			wantState:   dots.StateSynced,
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
			wantState:   dots.StateConflict,
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
			wantState:   dots.StateConflict,
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
			wantState:   dots.StateBroken,
			wantActions: syncable,
		},
		{
			name:        "source missing local missing",
			wantState:   dots.StateNoSource,
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
			wantState:   dots.StateLocalOnly,
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
			wantState:   dots.StateNoSource,
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
			wantState:   dots.StateLocalOnly,
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
			wantState:   dots.StateNoSource,
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

func TestDotsCommitRefreshesCachedGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	sourceFile := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("-- seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}, home)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	git("init")
	git("config", "user.email", "t@t.com")
	git("config", "user.name", "T")
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")
	git("add", "dotfiles")
	git("commit", "-m", "seed")
	if err := os.WriteFile(sourceFile, []byte("-- changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := a.RefreshDotsState(ctx)
	if err != nil {
		t.Fatalf("RefreshDotsState: %v", err)
	}
	if strings.TrimSpace(dirty.GitStatus) == "" {
		t.Fatalf("RefreshDotsState GitStatus = %q, want dirty repo before commit", dirty.GitStatus)
	}

	committed, err := a.DotsCommitWithState(ctx, "dots: cache refresh")
	if err != nil {
		t.Fatalf("DotsCommitWithState: %v", err)
	}
	if committed == nil || committed.State == nil || !committed.State.Loaded || strings.TrimSpace(committed.State.GitStatus) != "" {
		t.Fatalf("DotsCommitWithState = %+v, want clean loaded state", committed)
	}
	cached, err := a.CachedDotsState(ctx)
	if err != nil {
		t.Fatalf("CachedDotsState: %v", err)
	}
	if !cached.Loaded || strings.TrimSpace(cached.GitStatus) != "" {
		t.Fatalf("CachedDotsState = %+v, want clean git status after commit", cached)
	}
}

func TestDotsStatus_NotConfigured(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	a, _, _ := newDotsApp(t)
	if _, err := a.DotsPull(context.Background()); err == nil {
		t.Error("expected error pulling from non-git directory")
	}
}

func TestDotsPullAndPush_WithGitRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	gitCmd := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		if dir != "" {
			c.Dir = dir
		}
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
		}
	}
	configGitUser := func(dir string) {
		t.Helper()
		gitCmd(dir, "config", "user.email", "test@example.com")
		gitCmd(dir, "config", "user.name", "Test")
		gitCmd(dir, "config", "commit.gpgsign", "false")
		gitCmd(dir, "config", "tag.gpgsign", "false")
	}

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	seedDir := filepath.Join(t.TempDir(), "seed")
	otherDir := filepath.Join(t.TempDir(), "other")
	gitCmd("", "init", "--bare", "--initial-branch=main", remoteDir)
	gitCmd("", "clone", remoteDir, seedDir)
	configGitUser(seedDir)
	seedFile := filepath.Join(seedDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(seedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedFile, []byte("-- seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(seedDir, "add", "dotfiles")
	gitCmd(seedDir, "commit", "-m", "seed")
	gitCmd(seedDir, "push", "-u", "origin", "main")

	gitCmd("", "clone", remoteDir, repoDir)
	configGitUser(repoDir)
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
	}, home)
	if _, err := a.DotsSyncContext(context.Background(), dots.SyncOptions{}); err != nil {
		t.Fatalf("initial DotsSyncContext: %v", err)
	}

	gitCmd("", "clone", remoteDir, otherDir)
	configGitUser(otherDir)
	remoteFile := filepath.Join(otherDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.WriteFile(remoteFile, []byte("-- remote change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(otherDir, "add", "dotfiles/nvim/.config/nvim/init.lua")
	gitCmd(otherDir, "commit", "-m", "remote-change")
	gitCmd(otherDir, "push")

	pulled, err := a.DotsPullWithState(context.Background())
	if err != nil {
		t.Fatalf("DotsPullWithState: %v", err)
	}
	if pulled == nil || pulled.State == nil || !pulled.State.Loaded || !hasDotStatusNamed(pulled.State.Entries, "nvim") {
		t.Fatalf("DotsPullWithState = %+v, want loaded nvim state", pulled)
	}
	localFile := filepath.Join(home, ".config", "nvim", "init.lua")
	if got, err := os.ReadFile(localFile); err != nil || string(got) != "-- remote change\n" {
		t.Fatalf("local file after pull = %q, %v; want remote change", got, err)
	}

	repoFile := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.WriteFile(repoFile, []byte("-- local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushed, err := a.DotsPushWithState(context.Background(), "dots: local change")
	if err != nil {
		t.Fatalf("DotsPushWithState: %v", err)
	}
	if pushed == nil || pushed.State == nil || !pushed.State.Loaded || !hasDotStatusNamed(pushed.State.Entries, "nvim") {
		t.Fatalf("DotsPushWithState = %+v, want loaded nvim state", pushed)
	}
	verifyDir := filepath.Join(t.TempDir(), "verify")
	gitCmd("", "clone", remoteDir, verifyDir)
	verifyFile := filepath.Join(verifyDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if got, err := os.ReadFile(verifyFile); err != nil || string(got) != "-- local change\n" {
		t.Fatalf("remote file after push = %q, %v; want local change", got, err)
	}
}

func TestDotsPush_NotConfigured(t *testing.T) {
	t.Parallel()
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

	untracked := filepath.Join(repoDir, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, nil, t.TempDir())

	result, err := a.DotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DotsStatus: %v", err)
	}
	if result.GitStatus == "" {
		t.Error("expected non-empty GitStatus for repo with untracked file")
	}
}

func TestDotsAdd_WithExistingGroup(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

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
		if g.BaseName() == dotsTestHostGroupName() {
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
	a, _, _ := newDotsApp(t)
	if err := a.DotsPush(context.Background(), ""); err == nil {
		t.Error("expected error pushing from non-git directory")
	}
}

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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	a, _, repoDir := newDotsAppWithGitCfg(t, config.DotsGitConfig{AutoCommit: true})

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
	gitCmd("config", "commit.gpgsign", "false")
	gitCmd("config", "tag.gpgsign", "false")

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
	gitCmd(repoDir, "config", "commit.gpgsign", "false")
	gitCmd(repoDir, "config", "tag.gpgsign", "false")
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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

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

	c := exec.Command("git", "log", "--oneline")
	c.Dir = repoDir
	if out, err := c.CombinedOutput(); err == nil && len(out) > 0 {
		t.Errorf("expected no commits with auto_commit=false, got:\n%s", out)
	}
}

func TestDotsResolveConflict_NotConfigured(t *testing.T) {
	t.Parallel()
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
	a, _, _ := newDotsApp(t)
	if _, err := a.DotsResolveConflict(context.Background(), "does-not-exist", app.DotResolveUseRepo); err == nil {
		t.Error("expected error for unknown dots entry name")
	}
}

func TestDotsResolveConflict_UseRepoBacksUpLocalAndRelinks(t *testing.T) {
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

	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, filepath.Join(srcDir, "init.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(nvimPath, "init.lua"), time.Unix(1_700_000_000, 0))

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

	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))

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

func TestDotsResolveConflict_UseRepoAllowsIgnoredSource(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(nvimPath, "local.log"), []byte("local secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, filepath.Join(srcDir, "init.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(nvimPath, "init.lua"), time.Unix(1_700_000_000, 0))
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath, Ignore: []string{"*.log"}},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseRepo)
	if err != nil {
		t.Fatalf("DotsResolveConflict use-repo: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpRepair {
		t.Fatalf("ops = %v, want one repair", ops)
	}
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))
	if got, err := os.ReadFile(filepath.Join(nvimPath, "init.lua")); err != nil || string(got) != "-- cfg" {
		t.Fatalf("target content = %q, %v; want repo content", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(nvimPath, "local.log")); err != nil || string(got) != "local secret" {
		t.Fatalf("ignored local log = %q, %v; want preserved local content", got, err)
	}
	backupPath := filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")
	if got, err := os.ReadFile(backupPath); err != nil || string(got) != "-- local" {
		t.Fatalf("backup = %q, %v; want local content", got, err)
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
	gitCmd("config", "commit.gpgsign", "false")
	gitCmd("config", "tag.gpgsign", "false")

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
	setDotTestModTime(t, filepath.Join(srcDir, "init.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(nvimPath, "init.lua"), time.Unix(1_700_000_000, 0))
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
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))
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

func TestDotsResolveConflict_UseLocalFollowsSymlinkTarget(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcFile := filepath.Join(dotsContentDir(repoDir), "gitconfig", ".gitconfig")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("[repo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalDir := t.TempDir()
	externalTarget := filepath.Join(externalDir, "real.gitconfig")
	if err := os.WriteFile(externalTarget, []byte("[local]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(home, ".gitconfig")
	if err := os.Symlink(externalTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "gitconfig", Path: linkPath},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "gitconfig", app.DotResolveUseLocal)
	if err != nil {
		t.Fatalf("DotsResolveConflict use-local: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want one adopt", ops)
	}
	if got, err := os.ReadFile(srcFile); err != nil || string(got) != "[local]\n" {
		t.Fatalf("repo source = %q err=%v, want followed symlink content", got, err)
	}
	if info, err := os.Lstat(srcFile); err != nil {
		t.Fatalf("repo source stat: %v", err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("repo source is still a symlink")
	}
	if got, err := os.ReadFile(externalTarget); err != nil || string(got) != "[local]\n" {
		t.Fatalf("external target changed: body=%q err=%v", got, err)
	}
	resolved, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(linkPath): %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(srcFile)
	if err != nil {
		t.Fatalf("EvalSymlinks(srcFile): %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("link resolves to %q, want repo source %q", resolved, wantResolved)
	}
}

func TestDotsResolveConflict_UseLocalAllowsIgnoredSource(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "secret.log"), []byte("repo secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "local.log"), []byte("local secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, filepath.Join(srcDir, "init.lua"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(nvimPath, "init.lua"), time.Unix(1_700_000_000, 0))
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath, Ignore: []string{"*.log"}},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "nvim", app.DotResolveUseLocal)
	if err != nil {
		t.Fatalf("DotsResolveConflict use-local: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want one adopt", ops)
	}
	if got, err := os.ReadFile(filepath.Join(srcDir, "init.lua")); err != nil || string(got) != "-- local" {
		t.Fatalf("repo source = %q, %v; want local content", got, err)
	}
	if _, err := os.Lstat(filepath.Join(srcDir, "secret.log")); !os.IsNotExist(err) {
		t.Fatalf("ignored repo log should be removed by local adoption, stat err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(srcDir, "local.log")); !os.IsNotExist(err) {
		t.Fatalf("ignored local log should not be copied to repo, stat err=%v", err)
	}
	assertRealDirectory(t, nvimPath)
	assertSymlinkResolvesTo(t, filepath.Join(nvimPath, "init.lua"), filepath.Join(srcDir, "init.lua"))
	if got, err := os.ReadFile(filepath.Join(nvimPath, "local.log")); err != nil || string(got) != "local secret" {
		t.Fatalf("ignored local log = %q, %v; want preserved local content", got, err)
	}
}

func TestDotsResolveConflict_UseLocalIgnoresPackageRootDSStore(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	packageRoot := filepath.Join(dotsContentDir(repoDir), "claude")
	srcDir := filepath.Join(packageRoot, ".claude")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "settings.json"), []byte(`{"repo":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, ".DS_Store"), []byte("finder metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".DS_Store"), []byte("local finder metadata"), 0o644); err != nil {
		t.Fatal(err)
	}

	claudePath := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudePath, "settings.json"), []byte(`{"local":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	setDotTestModTime(t, filepath.Join(srcDir, "settings.json"), time.Unix(1_700_000_000, 0).Add(time.Hour))
	setDotTestModTime(t, filepath.Join(claudePath, "settings.json"), time.Unix(1_700_000_000, 0))
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "claude", Path: claudePath},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "claude", app.DotResolveUseLocal)
	if err != nil {
		t.Fatalf("DotsResolveConflict use-local: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want one adopt", ops)
	}
	if got, err := os.ReadFile(filepath.Join(srcDir, "settings.json")); err != nil || string(got) != `{"local":true}` {
		t.Fatalf("repo settings = %q, %v; want local content", got, err)
	}
	assertRealDirectory(t, claudePath)
	assertSymlinkResolvesTo(t, filepath.Join(claudePath, "settings.json"), filepath.Join(srcDir, "settings.json"))
	if got, err := os.ReadFile(filepath.Join(home, ".DS_Store")); err != nil || string(got) != "local finder metadata" {
		t.Fatalf("home .DS_Store = %q, %v; want preserved local metadata", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(packageRoot, ".DS_Store")); err != nil || string(got) != "finder metadata" {
		t.Fatalf("package .DS_Store = %q, %v; want ignored package metadata left untouched", got, err)
	}
}

func TestDotsDisable_UnlinksManagedSymlinks(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	const content = "-- nvim init"

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte(content), 0o644); err != nil {
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

	ops, err := a.DotsDisable(app.DisableDotsOptions{})
	if err != nil {
		t.Fatalf("DotsDisable: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlink {
		t.Errorf("got ops %v, want [OpUnlink]", ops)
	}

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

	ops, err := a.DotsDisable(app.DisableDotsOptions{ConflictOverwrite: false})
	if err != nil {
		t.Fatalf("DotsDisable: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpUnlinkConflict {
		t.Errorf("got ops %v, want [OpUnlinkConflict]", ops)
	}
	got, _ := os.ReadFile(dstFile)
	if string(got) != "# local" {
		t.Errorf("local file overwritten: content = %q", string(got))
	}
}

func TestDotsDisable_ConflictOverwrite(t *testing.T) {
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
	secret := filepath.Join(home, "secret")
	if err := os.WriteFile(secret, []byte("# external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, srcFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstFile, []byte("# local"), 0o644); err != nil {
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
	if !config.BoolVal(cfg.HostSettings[dotsTestHostGroupName()].DotsDisabled) {
		t.Fatal("DotsDisabled should be true after DisableDotsForHost partial failure")
	}
}

func TestDotsDisable_NotConfigured(t *testing.T) {
	t.Parallel()
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

func TestDotsSyncEntry_StowNotOnPath(t *testing.T) {
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
	nvimPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: nvimPath},
	}, home)

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

func TestDotsDelete_EntryNotFound(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, _, _ := newDotsApp(t)
	err := a.DotsDelete(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
	if !strings.Contains(err.Error(), `"ghost" not found`) {
		t.Errorf("error %q does not mention entry name", err.Error())
	}
}

func TestDotsAdd_PathDoesNotExist(t *testing.T) {
	a, _, _ := newDotsApp(t)
	_, err := a.DotsAdd(context.Background(), "/this/path/does/not/exist/at/all", app.DotsAddOptions{})
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

func TestBackupPath_DirectoryBranch(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}

	a, _, _ := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

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

	if _, err := a.DotsSync(dots.SyncOptions{DryRun: false}); err != nil {
		t.Fatalf("DotsSync (first): %v", err)
	}

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

func TestDotsSync_HostFiltering(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	srcDir := filepath.Join(dotsContentDir(repoDir), "vimrc")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".vimrc"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	vimrcPath := filepath.Join(home, ".vimrc")

	cfgPath := filepath.Join(cfgDir, "settings.json")
	rootCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rootCfg.Groups = []*config.GroupConfig{
		{Name: "testhost", Special: "host"},
		{Name: "mac", Dots: []config.DotEntry{{Name: "vimrc", Path: vimrcPath}}},
		{Name: "empty-group"},
	}
	rootCfg.Hosts = map[string][]string{"testhost": {"empty-group"}}
	if err := saveAppConfig(t, cfgPath, rootCfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ops, err := a.DotsSync(dots.SyncOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("got %d ops, want 0 (host filtering should exclude mac group); ops=%v", len(ops), ops)
	}
}

func TestDotsResolveConflict_ModifiedEntryUseLocalAdopts(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "zsh", ".config", "zsh")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "tracked.zsh"), []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}

	zshPath := filepath.Join(home, ".config", "zsh")
	if err := os.MkdirAll(zshPath, 0o755); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(zshPath, filepath.Join(srcDir, "tracked.zsh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(zshPath, "tracked.zsh")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zshPath, "local-extra.zsh"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zsh", Path: zshPath},
	}, home)

	ops, err := a.DotsResolveConflict(context.Background(), "zsh", app.DotResolveUseLocal)
	if err != nil {
		t.Fatalf("DotsResolveConflict use-local on modified entry: %v", err)
	}
	if len(ops) != 1 || ops[0].Kind != dots.OpAdopt {
		t.Fatalf("ops = %v, want OpAdopt", ops)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "local-extra.zsh")); err != nil {
		t.Errorf("local-extra.zsh not adopted into repo: %v", err)
	}
	assertSymlinkResolvesTo(t, filepath.Join(zshPath, "local-extra.zsh"), filepath.Join(srcDir, "local-extra.zsh"))
}

func TestDotsSync_PurgeMovesIgnoredRepoSourceToTrashWithSnapshot(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	runGit("config", "commit.gpgsign", "false")

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	purgedPath := filepath.Join(srcDir, "secret.txt")
	if err := os.WriteFile(purgedPath, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(home, ".config", "nvim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: targetPath, Ignore: []string{"secret.txt"}},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}
	if _, err := os.Lstat(purgedPath); !os.IsNotExist(err) {
		t.Fatalf("purged source still present: %v", err)
	}
	trashRoot := filepath.Join(home, ".Trash")
	switch runtime.GOOS {
	case "darwin":
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			trashRoot = filepath.Join(localAppData, "Trash", "files")
		}
	default:
		trashRoot = filepath.Join(home, ".local", "share", "Trash", "files")
	}
	trashed := filepath.Join(trashRoot, "secret.txt")
	if got, err := os.ReadFile(trashed); err != nil || string(got) != "precious" {
		t.Fatalf("trash copy = %q, %v; want purged content preserved in trash", got, err)
	}
	logCmd := exec.Command("git", "-C", repoDir, "log", "--format=%s", "--", "dotfiles/nvim/.config/nvim/secret.txt")
	out, err := logCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "dots: pre-purge nvim") {
		t.Fatalf("git log = %q, want pre-purge snapshot containing purged file", out)
	}
}

func TestDotsStatus_AttachesLastSyncErrorToOutOfSyncEntry(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "zshrc")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(srcDir, ".zshrc")
	if err := os.WriteFile(sourcePath, []byte("repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(targetPath, []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(targetPath, past, past); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "zshrc", Path: targetPath},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err == nil {
		t.Fatal("DotsSync should fail on unresolved conflict")
	}

	ctx := context.Background()
	result, err := a.DotsStatus(ctx)
	if err != nil {
		t.Fatalf("DotsStatus: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(result.Entries))
	}
	entry := result.Entries[0]
	if !strings.Contains(entry.LastError, "use repo version or use local version") {
		t.Fatalf("LastError = %q, want recorded conflict reason", entry.LastError)
	}

	if _, err := a.DotsResolveConflict(ctx, "zshrc", app.DotResolveUseRepo); err != nil {
		t.Fatalf("DotsResolveConflict: %v", err)
	}
	result, err = a.DotsStatus(ctx)
	if err != nil {
		t.Fatalf("DotsStatus after resolve: %v", err)
	}
	if got := result.Entries[0].LastError; got != "" {
		t.Fatalf("LastError after resolve = %q, want empty", got)
	}
}

func TestDotsExtractThenAddHostVariant_MissingParentErrorsWithoutVariant(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "vim", Path: filepath.Join(home, ".vim")},
	}, home)

	_, ops, err := a.DotsExtractThenAddHostVariant(context.Background(), "nope", "colors", app.DotsAddVariantOptions{})
	if err == nil {
		t.Fatal("expected error extracting from a missing parent")
	}
	if len(ops) != 0 {
		t.Fatalf("failed extract should yield no ops, got %d", len(ops))
	}
	if variants, verr := a.DotsListVariants(app.DotExtractName("nope", "colors")); verr == nil && len(variants) > 0 {
		t.Fatalf("variant created despite failed extract: %v", variants)
	}
}

func TestExtractedFragments_AppMutationsStayInFragments(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	targetPath := filepath.Join(home, ".vim")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "vim", Path: targetPath},
	}, home)
	cfgPath := filepath.Join(cfgDir, "settings.json")
	if _, err := config.ExtractIncludeFragments(cfgPath); err != nil {
		t.Fatalf("ExtractIncludeFragments: %v", err)
	}

	if err := a.DotsAddIgnorePattern("vim", ".netrwhist"); err != nil {
		t.Fatalf("DotsAddIgnorePattern: %v", err)
	}

	mainData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var mainRaw map[string]json.RawMessage
	if err := json.Unmarshal(mainData, &mainRaw); err != nil {
		t.Fatal(err)
	}
	if _, ok := mainRaw["groups"]; ok {
		t.Fatalf("groups re-inlined into main settings.json after mutation:\n%s", mainData)
	}
	dotsData, err := os.ReadFile(filepath.Join(cfgDir, "settings.d", "dots.json"))
	if err != nil {
		t.Fatalf("read dots.json: %v", err)
	}
	if !strings.Contains(string(dotsData), ".netrwhist") {
		t.Fatalf("dots.json missing mutation:\n%s", dotsData)
	}
	if groupsData, err := os.ReadFile(filepath.Join(cfgDir, "settings.d", "groups.json")); err == nil {
		if strings.Contains(string(groupsData), ".netrwhist") {
			t.Fatalf("groups.json received dot mutation:\n%s", groupsData)
		}
	}

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
}

func TestDotsExtractThenAddHostVariant_CreatesVariantForChild(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
	if err := os.MkdirAll(filepath.Join(srcDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "CLAUDE.md"), []byte("md"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "agents", "analyst.md"), []byte("agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".claude")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "claude", Path: target},
	}, home)
	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}

	childName := app.DotExtractName("claude", "agents")
	info, _, err := a.DotsExtractThenAddHostVariant(context.Background(), "claude", "agents", app.DotsAddVariantOptions{Sync: true})
	if err != nil {
		t.Fatalf("DotsExtractThenAddHostVariant: %v", err)
	}
	if info.Name != childName || info.Host == "" {
		t.Fatalf("variant info = %+v, want name %q with a host", info, childName)
	}
	variants, err := a.DotsListVariants(childName)
	if err != nil {
		t.Fatalf("DotsListVariants(%q): %v", childName, err)
	}
	hasHostVariant := false
	for _, v := range variants {
		if !v.Default && v.Host != "" {
			hasHostVariant = true
		}
	}
	if !hasHostVariant {
		t.Fatalf("extracted entry %q has no host variant: %+v", childName, variants)
	}
	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	foundChild := false
	for _, s := range statuses {
		if s.Name == childName {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("extracted entry %q not configured; statuses=%+v", childName, statuses)
	}
}

func TestDotsAddHostVariant_IgnoredEntryStillWorks(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "init.lua"), []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "nvim", Path: target, Ignore: []string{"*"}},
	}, home)

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != dots.StateIgnored {
		t.Fatalf("state = %+v, want a single ignored entry", statuses)
	}
	if !app.DotStatusVariantEligible(statuses[0]) {
		t.Fatal("ignored entry should be variant-eligible")
	}

	info, _, err := a.DotsAddHostVariant(context.Background(), "nvim", app.DotsAddVariantOptions{})
	if err != nil {
		t.Fatalf("DotsAddHostVariant on ignored entry: %v", err)
	}
	if info.Host == "" {
		t.Fatalf("variant info = %+v, want a host", info)
	}
	variants, err := a.DotsListVariants("nvim")
	if err != nil || len(variants) == 0 {
		t.Fatalf("DotsListVariants = %+v, err=%v; want a registered variant", variants, err)
	}
}

func TestDiscoverDotsStatus_TrackedChildUnderIgnoredDirSurfaces(t *testing.T) {
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
	if err := os.MkdirAll(filepath.Join(srcDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "data", "keep.json"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "data", "drop.json"), []byte("drop"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".claude")
	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "claude", Path: target, Ignore: []string{"*", "!/data/keep.json"}},
	}, home)

	res, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}

	var keep *app.DotChild
	var walk func(children []app.DotChild)
	walk = func(children []app.DotChild) {
		for i := range children {
			if children[i].Name == "keep.json" {
				keep = &children[i]
			}
			walk(children[i].Children)
		}
	}
	for _, e := range res.Entries {
		if e.Name == "claude" && e.State == dots.StateIgnored {
			walk(e.Children)
		}
	}
	if keep == nil {
		t.Fatalf("re-included keep.json not surfaced in the Ignored-section tree; entries=%+v", res.Entries)
	}
	if keep.Ignored {
		t.Errorf("keep.json should be tracked (Ignored=false)")
	}
	if keep.State == dots.StateIgnored {
		t.Errorf("keep.json should carry a real state, got %q", keep.State)
	}
}

func TestDotsSync_WhitelistDirectoryConvergesMixedStates(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	a, cfgDir, repoDir := newDotsApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	srcDir := filepath.Join(dotsContentDir(repoDir), "claude", ".claude")
	for _, dir := range []string{filepath.Join(srcDir, "agents")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSrc := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(srcDir, rel)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	settingsSrc := writeSrc("settings.json", "repo-settings")
	claudeMdSrc := writeSrc("CLAUDE.md", "claude-md")
	agentSrc := writeSrc("agents/analyst.md", "agent")

	target := filepath.Join(home, ".claude")
	for _, dir := range []string{
		filepath.Join(target, "agents"),
		filepath.Join(target, "plugins", "cache"),
		filepath.Join(target, "projects"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustLink := func(src, dst string) {
		t.Helper()
		rel, err := filepath.Rel(filepath.Dir(dst), src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(rel, dst); err != nil {
			t.Fatal(err)
		}
	}
	mustLink(claudeMdSrc, filepath.Join(target, "CLAUDE.md"))
	mustLink(agentSrc, filepath.Join(target, "agents", "analyst.md"))
	mustLink(filepath.Join(srcDir, "RTK.md"), filepath.Join(target, "RTK.md"))

	writeLocal := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(target, rel)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeLocal(filepath.Join("plugins", "cache", "blob"), "machine-state")
	writeLocal(filepath.Join("projects", "p.json"), "machine-state")
	keybindingsLocal := writeLocal("keybindings.json", "my-keys")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(settingsSrc, past, past); err != nil {
		t.Fatal(err)
	}
	settingsLocal := writeLocal("settings.json", "local-newer-settings")

	writeGroupWithDots(t, cfgDir, repoDir, []config.DotEntry{
		{Name: "claude", Path: target, Ignore: []string{
			"*",
			"!/settings.json",
			"!/CLAUDE.md",
			"!/RTK.md",
			"!/keybindings.json",
			"!/agents/",
		}},
	}, home)

	if _, err := a.DotsSync(dots.SyncOptions{}); err != nil {
		t.Fatalf("DotsSync: %v", err)
	}

	assertSymlinkResolvesTo(t, settingsLocal, settingsSrc)
	if got, err := os.ReadFile(settingsSrc); err != nil || string(got) != "local-newer-settings" {
		t.Fatalf("repo settings = %q, %v; want adopted local content", got, err)
	}
	assertSymlinkResolvesTo(t, keybindingsLocal, filepath.Join(srcDir, "keybindings.json"))
	if got, err := os.ReadFile(filepath.Join(srcDir, "keybindings.json")); err != nil || string(got) != "my-keys" {
		t.Fatalf("repo keybindings = %q, %v; want adopted local-only file", got, err)
	}
	assertSymlinkResolvesTo(t, filepath.Join(target, "CLAUDE.md"), claudeMdSrc)
	assertSymlinkResolvesTo(t, filepath.Join(target, "agents", "analyst.md"), agentSrc)
	for _, keep := range []string{
		filepath.Join(target, "plugins", "cache", "blob"),
		filepath.Join(target, "projects", "p.json"),
	} {
		if info, err := os.Lstat(keep); err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("ignored machine state %s must stay a local regular file (err=%v)", keep, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(target, "RTK.md")); !os.IsNotExist(err) {
		t.Fatalf("dangling whitelisted link should be cleaned up, got err=%v", err)
	}

	statuses, err := a.DotsList()
	if err != nil {
		t.Fatalf("DotsList: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != dots.StateSynced {
		t.Fatalf("state = %+v, want synced entry", statuses)
	}
}
