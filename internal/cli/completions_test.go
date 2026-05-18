package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// ─── test helpers ────────────────────────────────────────────────────────────

// newCompletionTestState builds a rootState with a real app initialised in test
// mode against temp dirs.  The returned state has state.app fully wired.
func newCompletionTestState(t *testing.T) *rootState {
	t.Helper()
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	t.Setenv("OMNI_HOSTNAME", "testhost")

	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(),
		&cliStubProvider{name: "brew"},
		&cliStubProvider{name: "pip"},
		&cliStubProvider{name: "node"},
	); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	return &rootState{configPath: cfgPath, cacheDir: cacheDir, app: a}
}

// seedTools upserts tool cache entries into the test DB.
func seedTools(t *testing.T, state *rootState, names ...string) {
	t.Helper()
	for _, name := range names {
		tc := &database.ToolCache{Name: name, Provider: "brew", Package: name}
		if err := state.app.DB().Upsert(context.Background(), tc); err != nil {
			t.Fatalf("Upsert(%s): %v", name, err)
		}
	}
}

// seedGroups writes groups into the config file used by state.app.
func seedGroups(t *testing.T, state *rootState, groupNames ...string) {
	t.Helper()
	cfg, err := config.Load(state.configPath)
	if err != nil {
		cfg = &config.RootConfig{}
	}
	for _, name := range groupNames {
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: name})
	}
	normalizeCLITestRootConfig(cfg)
	if err := config.Save(state.configPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

// seedDots writes dot entries into the config used by state.app and sets up a
// minimal dots_repo so DotsList() can resolve entries.
func seedDots(t *testing.T, state *rootState, dotNames ...string) {
	t.Helper()
	cfg, err := config.Load(state.configPath)
	if err != nil {
		cfg = &config.RootConfig{}
	}

	// Create a temp dots repo with stow package dirs so buildDotsManager works.
	dotsRepo := t.TempDir()
	dotfilesDir := filepath.Join(dotsRepo, "dotfiles")
	for _, name := range dotNames {
		if err := os.MkdirAll(filepath.Join(dotfilesDir, name), 0o755); err != nil {
			t.Fatalf("mkdir stow package %s: %v", name, err)
		}
	}
	cfg.Settings.DotsRepo = dotsRepo

	// Ensure there's a host group to hold dots.
	var hostGroup *config.GroupConfig
	for _, g := range cfg.Groups {
		if g.IsHost() {
			hostGroup = g
			break
		}
	}
	if hostGroup == nil {
		hostGroup = &config.GroupConfig{Name: "testhost", Special: "host"}
		cfg.Groups = append(cfg.Groups, hostGroup)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	for _, name := range dotNames {
		hostGroup.Dots = append(hostGroup.Dots, config.DotEntry{
			Name: name,
			Path: filepath.Join(home, "."+name),
		})
	}
	normalizeCLITestRootConfig(cfg)
	if err := config.Save(state.configPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

// seedHosts writes host entries into the config used by state.app.
func seedHosts(t *testing.T, state *rootState, hostNames ...string) {
	t.Helper()
	cfg, err := config.Load(state.configPath)
	if err != nil {
		cfg = &config.RootConfig{}
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string][]string{}
	}
	for _, name := range hostNames {
		cfg.Hosts[name] = []string{}
		ensureTestHostGroup(cfg, name)
	}
	normalizeCLITestRootConfig(cfg)
	if err := config.Save(state.configPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

// dummyCmd returns a minimal cobra command for passing to completion functions.
func dummyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	return cmd
}

// ─── completeToolNames ───────────────────────────────────────────────────────

func TestCompleteToolNames_NilApp(t *testing.T) {
	state := &rootState{}
	fn := completeToolNames(state)
	names, dir := fn(dummyCmd(), nil, "")
	if names != nil {
		t.Fatalf("expected nil, got %v", names)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
}

func TestCompleteToolNames_ReturnsMatchingTools(t *testing.T) {
	state := newCompletionTestState(t)
	seedTools(t, state, "ripgrep", "redis", "node", "nginx")

	fn := completeToolNames(state)
	names, dir := fn(dummyCmd(), nil, "r")

	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 matches, got %v", names)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["ripgrep"] || !got["redis"] {
		t.Fatalf("expected ripgrep and redis, got %v", names)
	}
}

func TestCompleteToolNames_EmptyPrefix(t *testing.T) {
	state := newCompletionTestState(t)
	seedTools(t, state, "git", "curl")

	fn := completeToolNames(state)
	names, _ := fn(dummyCmd(), nil, "")

	if len(names) != 2 {
		t.Fatalf("expected 2 tools, got %v", names)
	}
}

func TestCompleteToolNames_NoMatch(t *testing.T) {
	state := newCompletionTestState(t)
	seedTools(t, state, "git", "curl")

	fn := completeToolNames(state)
	names, _ := fn(dummyCmd(), nil, "zzz")

	if len(names) != 0 {
		t.Fatalf("expected 0 matches, got %v", names)
	}
}

// ─── completeGroupNames ─────────────────────────────────────────────────────

func TestCompleteGroupNames_NilApp(t *testing.T) {
	state := &rootState{}
	fn := completeGroupNames(state)
	names, dir := fn(dummyCmd(), nil, "")
	if names != nil {
		t.Fatalf("expected nil, got %v", names)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
}

func TestCompleteGroupNames_ReturnsMatchingGroups(t *testing.T) {
	state := newCompletionTestState(t)
	seedGroups(t, state, "work", "personal", "shared")

	fn := completeGroupNames(state)
	names, _ := fn(dummyCmd(), nil, "p")

	if len(names) != 1 || names[0] != "personal" {
		t.Fatalf("expected [personal], got %v", names)
	}
}

func TestCompleteGroupNames_EmptyPrefix(t *testing.T) {
	state := newCompletionTestState(t)
	seedGroups(t, state, "work", "personal")

	fn := completeGroupNames(state)
	names, _ := fn(dummyCmd(), nil, "")

	// Includes testhost (host group) + work + personal
	if len(names) < 2 {
		t.Fatalf("expected at least 2 groups, got %v", names)
	}
}

// ─── completeHostNames ──────────────────────────────────────────────────────

func TestCompleteHostNames_NilApp(t *testing.T) {
	state := &rootState{}
	fn := completeHostNames(state)
	names, dir := fn(dummyCmd(), nil, "")
	if names != nil {
		t.Fatalf("expected nil, got %v", names)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
}

func TestCompleteHostNames_ReturnsMatchingHosts(t *testing.T) {
	state := newCompletionTestState(t)
	seedHosts(t, state, "macbook-pro", "macbook-air", "linux-server")

	fn := completeHostNames(state)
	names, _ := fn(dummyCmd(), nil, "macbook")

	if len(names) != 2 {
		t.Fatalf("expected 2 matches, got %v", names)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["macbook-pro"] || !got["macbook-air"] {
		t.Fatalf("expected macbook-pro and macbook-air, got %v", names)
	}
}

// ─── completeProviderNames ──────────────────────────────────────────────────

func TestCompleteProviderNames_NilApp(t *testing.T) {
	state := &rootState{}
	fn := completeProviderNames(state)
	names, dir := fn(dummyCmd(), nil, "")
	if names != nil {
		t.Fatalf("expected nil, got %v", names)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
}

func TestCompleteProviderNames_ReturnsMatchingProviders(t *testing.T) {
	state := newCompletionTestState(t)

	fn := completeProviderNames(state)
	names, _ := fn(dummyCmd(), nil, "b")

	if len(names) != 1 || names[0] != "brew" {
		t.Fatalf("expected [brew], got %v", names)
	}
}

func TestCompleteProviderNames_EmptyPrefix(t *testing.T) {
	state := newCompletionTestState(t)

	fn := completeProviderNames(state)
	names, _ := fn(dummyCmd(), nil, "")

	// brew, pip, node registered in newCompletionTestState
	if len(names) < 3 {
		t.Fatalf("expected at least 3 providers, got %v", names)
	}
}

// ─── completeDotNames ───────────────────────────────────────────────────────

func TestCompleteDotNames_NilApp(t *testing.T) {
	state := &rootState{}
	fn := completeDotNames(state)
	names, dir := fn(dummyCmd(), nil, "")
	if names != nil {
		t.Fatalf("expected nil, got %v", names)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
}

func TestCompleteDotNames_ReturnsMatchingDots(t *testing.T) {
	state := newCompletionTestState(t)
	seedDots(t, state, "zsh", "vim", "git")

	fn := completeDotNames(state)
	names, _ := fn(dummyCmd(), nil, "v")

	if len(names) != 1 || names[0] != "vim" {
		t.Fatalf("expected [vim], got %v", names)
	}
}

func TestCompleteDotNames_EmptyPrefix(t *testing.T) {
	state := newCompletionTestState(t)
	seedDots(t, state, "zsh", "vim")

	fn := completeDotNames(state)
	names, _ := fn(dummyCmd(), nil, "")

	if len(names) != 2 {
		t.Fatalf("expected 2 dots, got %v", names)
	}
}

// ─── completeSettingsKeys ───────────────────────────────────────────────────

func TestCompleteSettingsKeys_EmptyPrefix(t *testing.T) {
	state := &rootState{} // state.app not needed for settings keys
	fn := completeSettingsKeys(state)
	names, dir := fn(dummyCmd(), nil, "")

	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected NoFileComp, got %v", dir)
	}
	if len(names) != len(knownSettingsKeys) {
		t.Fatalf("expected %d keys, got %d", len(knownSettingsKeys), len(names))
	}
}

func TestCompleteSettingsKeys_PrefixFilter(t *testing.T) {
	fn := completeSettingsKeys(&rootState{})
	names, _ := fn(dummyCmd(), nil, "dots_")

	// dots_repo, dots_disabled, dots_git.auto_commit, dots_git.auto_push
	if len(names) != 4 {
		t.Fatalf("expected 4 dots_ keys, got %v", names)
	}
}

func TestCompleteSettingsKeys_DotPrefixFilter(t *testing.T) {
	fn := completeSettingsKeys(&rootState{})
	names, _ := fn(dummyCmd(), nil, "dots_git.")

	// dots_git.auto_commit, dots_git.auto_push
	if len(names) != 2 {
		t.Fatalf("expected 2 dots_git. keys, got %v", names)
	}
}

func TestCompleteSettingsKeys_NoMatch(t *testing.T) {
	fn := completeSettingsKeys(&rootState{})
	names, _ := fn(dummyCmd(), nil, "zzz")

	if len(names) != 0 {
		t.Fatalf("expected 0 matches, got %v", names)
	}
}

// ─── multi-arg positional dispatch ───────────────────────────────────────────

// findSubCmd locates a subcommand by name.
func findSubCmd(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q not found under %q", name, parent.Name())
	return nil
}

// TestMultiArgPositionalDispatch verifies that commands with positional-aware
// completion switch between entity types based on len(args).
func TestMultiArgPositionalDispatch(t *testing.T) {
	state := newCompletionTestState(t)
	seedTools(t, state, "ripgrep")
	seedGroups(t, state, "work")

	root := NewRootCmd()
	groupsCmd := findSubCmd(t, root, "groups")

	// groups move-tool: arg0 = group, arg1 = tool
	moveCmd := findSubCmd(t, groupsCmd, "move-tool")
	if moveCmd.ValidArgsFunction == nil {
		t.Fatal("groups move-tool should have ValidArgsFunction")
	}
}

// ─── wiring: ValidArgsFunction is set on key commands ────────────────────────

func TestCompletionWiring(t *testing.T) {
	root := NewRootCmd()

	tests := []struct {
		path []string // command path from root
	}{
		{[]string{"tools", "install"}},
		{[]string{"tools", "upgrade"}},
		{[]string{"tools", "delete"}},
		{[]string{"tools", "switch"}},
		{[]string{"tools", "sync"}},
		{[]string{"tools", "set"}},
		{[]string{"tools", "delete-spec"}},
		{[]string{"tools", "ignore"}},
		{[]string{"tools", "unignore"}},
		{[]string{"dots", "delete"}},
		{[]string{"dots", "resolve"}},
		{[]string{"dots", "ignore"}},
		{[]string{"dots", "unignore"}},
		{[]string{"settings", "get"}},
		{[]string{"settings", "set"}},
	}

	for _, tt := range tests {
		t.Run(filepath.Join(tt.path...), func(t *testing.T) {
			cmd := root
			for _, segment := range tt.path {
				found := false
				for _, c := range cmd.Commands() {
					if c.Name() == segment {
						cmd = c
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("subcommand %q not found", segment)
				}
			}
			if cmd.ValidArgsFunction == nil {
				t.Errorf("%s: ValidArgsFunction not set", filepath.Join(tt.path...))
			}
		})
	}
}
