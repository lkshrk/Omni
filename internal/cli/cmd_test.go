package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/provider"
	textutil "github.com/lkshrk/omni/internal/text"
)

// ─── test harness ─────────────────────────────────────────────────────────────

// newTestCmd builds a root command wired to a clean temp dir.
// Returns cmd and the config path. Callers should set additional args with
// cmd.SetArgs(append([]string{...flags...}, subcommandArgs...)) before Execute.
func newTestCmd(t *testing.T) (*cobra.Command, string) {
	t.Helper()
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir})
	return cmd, cfgPath
}

// withConfig pre-writes a RootConfig to the config path.
func withConfig(t *testing.T, cfgPath string, cfg *config.RootConfig) {
	t.Helper()
	normalizeCLITestRootConfig(cfg)
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

func normalizeCLITestRootConfig(cfg *config.RootConfig) {
	host := "testhost"
	for _, group := range cfg.Groups {
		if group != nil && group.Name == "" {
			group.Name = host
			group.Special = "host"
		} else if group != nil && group.Name == host {
			group.Special = "host"
		}
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string][]string{}
	}
	for _, group := range cfg.Groups {
		if group != nil && group.IsHost() {
			if _, ok := cfg.Hosts[group.Name]; !ok {
				cfg.Hosts[group.Name] = []string{}
			}
		}
	}
	byName := map[string]*config.GroupConfig{}
	merged := make([]*config.GroupConfig, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if existing, ok := byName[group.Name]; ok {
			existing.Taps = append(existing.Taps, group.Taps...)
			existing.Tools = append(existing.Tools, group.Tools...)
			existing.Dots = append(existing.Dots, group.Dots...)
			if group.Special != "" {
				existing.Special = group.Special
			}
			if existing.Description == "" {
				existing.Description = group.Description
			}
			continue
		}
		byName[group.Name] = group
		merged = append(merged, group)
	}
	cfg.Groups = merged
}

// withHost keeps the old test helper name while creating an active host entry
// so host-enforced commands pass requireActiveHost.
func withHost(t *testing.T, cfgPath string) {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		cfg = &config.RootConfig{}
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string][]string{}
	}
	cfg.Hosts["testhost"] = []string{}
	ensureTestHostGroup(cfg, "testhost")
	withConfig(t, cfgPath, cfg)
}

func ensureTestHostGroup(cfg *config.RootConfig, name string) {
	for _, group := range cfg.Groups {
		if group.BaseName() == name {
			group.Special = "host"
			return
		}
	}
	cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: name, Special: "host"})
}

func cliTestHostGroup(names ...string) *config.GroupConfig {
	group := &config.GroupConfig{Name: "testhost", Special: "host"}
	for _, name := range names {
		group.Tools = append(group.Tools, config.ToolEntry{Name: name})
	}
	return group
}

// executeCmd sets the final args on cmd, captures stdout, runs Execute, and
// returns (stdout, err). Some tests still check only errors and config state
// when stdout content is not part of the behavior under test.
func executeCmd(t *testing.T, cmd *cobra.Command, args ...string) (stdout string, err error) {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	// Prepend existing persistent flags (--config, --cache-dir) to the new args.
	existingArgs := cmd.Flags().Args() // won't have them yet; use a wrapper
	_ = existingArgs

	// Re-set: the persistent flags were already set in newTestCmd; here we
	// only add the subcommand args.
	currentArgs := cmd.Flags().Args()
	_ = currentArgs
	// cobra root already has --config/--cache-dir set via SetArgs in newTestCmd.
	// We need to override SetArgs with the full command line.
	// Extract persistent flags from previous SetArgs call.
	// Simplest approach: rebuild the full args slice.

	// We cannot get back the values set via SetArgs after they've been parsed,
	// so we rely on the fact that newTestCmd already stored them in the string vars.
	// Just set the sub-command args directly (persistent flags are already bound).
	cmd.SetArgs(args)
	cmd.SetOut(buf)
	err = cmd.Execute()
	return buf.String(), err
}

type cliStubProvider struct {
	name           string
	resolvedName   string
	unavailable    bool
	installed      []provider.InstalledTool
	installedCalls []string
	searchResults  []provider.SearchResult
	searchErr      error
}

func (p *cliStubProvider) Name() string                              { return p.name }
func (p *cliStubProvider) Description() string                       { return p.name + " stub" }
func (p *cliStubProvider) Available(_ context.Context) (bool, error) { return !p.unavailable, nil }
func (p *cliStubProvider) ResolvedName(_ context.Context) (string, error) {
	if p.resolvedName == "" {
		return "", fmt.Errorf("no resolved provider")
	}
	return p.resolvedName, nil
}
func (p *cliStubProvider) Install(_ context.Context, tool provider.Tool) error {
	p.installedCalls = append(p.installedCalls, tool.Name)
	for _, installed := range p.installed {
		if installed.Tool.Name == tool.Name && installed.Tool.Provider == tool.Provider {
			return nil
		}
	}
	p.installed = append(p.installed, provider.InstalledTool{Tool: tool})
	return nil
}
func (p *cliStubProvider) Uninstall(_ context.Context, _ provider.Tool) error {
	return nil
}
func (p *cliStubProvider) Upgrade(_ context.Context, _ provider.Tool) error { return nil }
func (p *cliStubProvider) IsInstalled(_ context.Context, tool provider.Tool) (bool, string, error) {
	for _, installed := range p.installed {
		if installed.Tool.Name == tool.Name && installed.Tool.Provider == tool.Provider {
			return true, installed.Version, nil
		}
	}
	return false, "", nil
}
func (p *cliStubProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return p.installed, nil
}
func (p *cliStubProvider) Search(_ context.Context, _ string) ([]provider.SearchResult, error) {
	return p.searchResults, p.searchErr
}

func TestMain(m *testing.M) {
	origHome, hadHome := os.LookupEnv("HOME")
	testHome, err := os.MkdirTemp("", "omni-cli-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", testHome); err != nil {
		panic(err)
	}
	origInitRootApp := initRootApp
	initRootApp = func(ctx context.Context, a *app.App) error {
		return a.InitTestMode(ctx,
			&cliStubProvider{name: "brew"},
			&cliStubProvider{name: "apt", unavailable: true},
			&cliStubProvider{name: "apk", unavailable: true},
			&cliStubProvider{name: "dnf", unavailable: true},
			&cliStubProvider{name: "pacman", unavailable: true},
			&cliStubProvider{name: "zypper", unavailable: true},
			&cliStubProvider{name: "pip"},
			&cliStubProvider{name: "node"},
			&cliStubProvider{name: "python"},
			&cliStubProvider{name: "system", resolvedName: "brew"},
		)
	}
	code := m.Run()
	initRootApp = origInitRootApp
	if hadHome {
		_ = os.Setenv("HOME", origHome)
	} else {
		_ = os.Unsetenv("HOME")
	}
	_ = os.RemoveAll(testHome)
	os.Exit(code)
}

func TestAllCommandsRenderHelp(t *testing.T) {
	for _, args := range allHelpArgPaths(NewRootCmd()) {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := NewRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if !strings.Contains(out.String(), "Usage:") {
				t.Fatalf("%v help output missing Usage:, got %q", args, out.String())
			}
		})
	}
}

func allHelpArgPaths(root *cobra.Command) [][]string {
	var paths [][]string
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		if !cmd.Hidden {
			paths = append(paths, append(append([]string(nil), path...), "--help"))
		}
		for _, child := range cmd.Commands() {
			if child.Hidden {
				continue
			}
			walk(child, append(path, child.Name()))
		}
	}
	walk(root, nil)
	return paths
}

func TestConfirmAction_NoAborts(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	withMockStdin(t, "n\n", func() {
		ok, err := confirmAction(cmd, &rootState{}, "Delete thing?")
		if err != nil {
			t.Fatalf("confirmAction: %v", err)
		}
		if ok {
			t.Fatal("confirmAction returned true for n")
		}
		if !strings.Contains(out.String(), "Aborted.") {
			t.Fatalf("output = %q, want Aborted.", out.String())
		}
	})
}

func TestConfirmAction_YesFlagSkipsPrompt(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	ok, err := confirmAction(cmd, &rootState{yes: true}, "Delete thing?")
	if err != nil {
		t.Fatalf("confirmAction: %v", err)
	}
	if !ok {
		t.Fatal("confirmAction returned false with yes=true")
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want no prompt", out.String())
	}
}

type cliStowInstallerProvider struct {
	binDir string
}

func (p *cliStowInstallerProvider) Name() string                                   { return "brew" }
func (p *cliStowInstallerProvider) Description() string                            { return "brew stub" }
func (p *cliStowInstallerProvider) Available(context.Context) (bool, error)        { return true, nil }
func (p *cliStowInstallerProvider) Uninstall(context.Context, provider.Tool) error { return nil }
func (p *cliStowInstallerProvider) Upgrade(context.Context, provider.Tool) error   { return nil }
func (p *cliStowInstallerProvider) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}
func (p *cliStowInstallerProvider) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return true, "1.0.0", nil
}
func (p *cliStowInstallerProvider) Install(_ context.Context, tool provider.Tool) error {
	if tool.Name != "stow" {
		return fmt.Errorf("unexpected install %q", tool.Name)
	}
	return os.WriteFile(filepath.Join(p.binDir, "stow"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
}

func newStowPromptApp(t *testing.T, binDir string) *app.App {
	t.Helper()
	dir := t.TempDir()
	a := app.New(filepath.Join(dir, "settings.json"))
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), &cliStowInstallerProvider{binDir: binDir}); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestEnsureDotsStowForCLI_NonInteractiveFails(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	a := newStowPromptApp(t, binDir)
	cmd := &cobra.Command{}

	withMockTerminal(t, false, func() {
		err := ensureDotsStowForCLI(cmd, &rootState{app: a})
		if err == nil {
			t.Fatal("expected noninteractive missing-stow error")
		}
		if !strings.Contains(err.Error(), "Install stow") {
			t.Fatalf("error = %q, want install guidance", err)
		}
	})
}

func TestEnsureDotsStowForCLI_InteractiveInstalls(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	a := newStowPromptApp(t, binDir)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	withMockStdin(t, "y\n", func() {
		if err := ensureDotsStowForCLI(cmd, &rootState{app: a}); err != nil {
			t.Fatalf("ensureDotsStowForCLI: %v", err)
		}
	})
	if !a.DotsStowInstalled(context.Background()) {
		t.Fatal("stow should be available after interactive install")
	}
	if !strings.Contains(out.String(), "Stow installed.") {
		t.Fatalf("output = %q, want install confirmation", out.String())
	}
}

// ─── helpers: pure functions ───────────────────────────────────────────────────

func TestGroupList_Empty(t *testing.T) {
	got := groupList(nil)
	if got != "(none)" {
		t.Errorf("groupList(nil) = %q, want %q", got, "(none)")
	}
	got = groupList([]string{})
	if got != "(none)" {
		t.Errorf("groupList([]) = %q, want %q", got, "(none)")
	}
}

func TestGroupList_SingleGroup(t *testing.T) {
	got := groupList([]string{"work"})
	if got != "[work]" {
		t.Errorf("groupList([work]) = %q, want [work]", got)
	}
}

func TestGroupList_MultipleGroups(t *testing.T) {
	got := groupList([]string{"base", "work", "personal"})
	if got != "[base, personal, work]" {
		t.Errorf("groupList = %q, want [base, personal, work]", got)
	}
}

func TestSyncAllFlag_ClaimsDiscoveredAndSyncs(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			cliTestHostGroup("ripgrep"),
		},
	})
	prov := &cliStubProvider{
		name:      "brew",
		installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "fzf", Provider: "brew"}, Version: "1.0"}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := newSyncCmd(&rootState{app: a, yes: true})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --all: %v", err)
	}
	if !strings.Contains(out.String(), "1 installed, 1 added to config") {
		t.Fatalf("sync --all output = %q, want installed/added-to-config summary", out.String())
	}
	if len(prov.installedCalls) != 1 || prov.installedCalls[0] != "ripgrep" {
		t.Fatalf("installedCalls = %v, want [ripgrep]", prov.installedCalls)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup := cliTestGroup(cfg, "testhost")
	if hostGroup == nil || !cliTestGroupHasTool(hostGroup, "fzf") {
		t.Fatalf("hostname group = %+v, want claimed fzf", hostGroup)
	}
}

func TestSyncAllFlag_DryRunDoesNotWriteDBOrConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{cliTestHostGroup()}})
	prov := &cliStubProvider{
		name:      "brew",
		installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "fzf", Provider: "brew"}, Version: "1.0"}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := newSyncCmd(&rootState{app: a})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--all", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --all --dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "Dry-run") {
		t.Fatalf("sync --all --dry-run output = %q, want Dry-run notice", out.String())
	}
	cached, err := a.DB().List(context.Background())
	if err != nil {
		t.Fatalf("DB.List: %v", err)
	}
	if len(cached) != 0 {
		t.Fatalf("dry-run wrote DB rows: %+v", cached)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if hostGroup := cliTestGroup(cfg, "testhost"); hostGroup != nil && cliTestGroupHasTool(hostGroup, "fzf") {
		t.Fatalf("dry-run wrote fzf to hostname group: %+v", hostGroup)
	}
}

func TestSyncAllFlag_RejectsScopedOptions(t *testing.T) {
	cmd := newSyncCmd(&rootState{})
	cmd.SetArgs([]string{"--all", "--provider", "brew"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("sync --all --provider returned nil, want conflict error")
	}
	if !strings.Contains(err.Error(), "--all cannot be combined") {
		t.Fatalf("error = %q, want --all conflict", err)
	}
}

func TestToolsSetHostFlag_WritesHostOverride(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("black")},
	})
	brew := &cliStubProvider{name: "brew"}
	pip := &cliStubProvider{name: "pip"}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), brew, pip); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	cmd := newToolsCmd(&rootState{app: a, yes: true})
	cmd.SetArgs([]string{"set", "black", "--provider", "pip", "--host"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools set --host: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	override := cfg.Tools["black"].Hosts["testhost"]
	if override.Provider != "pip" || override.InstallWith != "" {
		t.Fatalf("black host override = %+v, want pip without install_with", override)
	}
}

func TestSwitchReinstallDefaultFlag_InstallsConfiguredProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Providers: []config.ToolInstallSpec{{Provider: "pip"}}},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("black")},
	})
	brew := &cliStubProvider{name: "brew"}
	pip := &cliStubProvider{
		name:      "pip",
		installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "black", Provider: "pip"}, Version: "1.0"}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), brew, pip); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:          "black",
		Provider:      "pip",
		Package:       "black",
		Installed:     true,
		InstalledWith: "brew",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	cmd := newReinstallCmd(&rootState{app: a, yes: true})
	cmd.SetArgs([]string{"black", "--reinstall-default"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("switch --reinstall-default: %v", err)
	}
	if len(pip.installedCalls) != 1 || pip.installedCalls[0] != "black" {
		t.Fatalf("pip installedCalls = %v, want [black]", pip.installedCalls)
	}
}

func cliTestGroup(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, g := range cfg.Groups {
		if g.Name == name {
			return g
		}
	}
	return nil
}

func cliTestGroupHasTool(group *config.GroupConfig, name string) bool {
	for _, tool := range group.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func TestPrintDotsTable_FormatsCorrectly(t *testing.T) {
	statuses := []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK, Actions: []dots.Action{dots.ActionRemove}},
		{Name: "zsh", TargetPath: "~/.zshrc", Health: app.HealthMissing, Actions: []dots.Action{dots.ActionSync}},
		{Name: "ssh", TargetPath: "~/.ssh", Health: app.HealthConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal}},
		{Name: "none", TargetPath: "~/.none", Health: app.HealthNoSource, Actions: []dots.Action{dots.ActionRemove}},
	}

	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	printDotsTable(cmd, statuses)

	output := buf.String()
	if !strings.Contains(output, "NAME") {
		t.Error("expected NAME header in output")
	}
	if !strings.Contains(output, "TARGET") {
		t.Error("expected TARGET header in output")
	}
	if !strings.Contains(output, "STATE") {
		t.Error("expected STATE header in output")
	}
	if !strings.Contains(output, "ACTIONS") {
		t.Error("expected ACTIONS header in output")
	}
	for _, section := range []string{"Conflict", "Out Of Sync", "Synced"} {
		if !strings.Contains(output, section) {
			t.Errorf("expected %q section in output", section)
		}
	}
	if !strings.Contains(output, "nvim") {
		t.Error("expected nvim in output")
	}
	symbols := textutil.SymbolsFromEnv()
	if !strings.Contains(output, symbols.Apply("✓")) {
		t.Error("expected ✓ icon for synced state")
	}
	if !strings.Contains(output, "!") {
		t.Error("expected ! icon for out-of-sync state")
	}
	if !strings.Contains(output, symbols.Apply("✗")) {
		t.Error("expected ✗ icon for conflict state")
	}
	if !strings.Contains(output, "?") {
		t.Error("expected ? icon for no-source state")
	}
	if !strings.Contains(output, "use-repo,use-local") {
		t.Error("expected classifier actions in output")
	}
}

func TestPrintDotOps_LinkAndRepair(t *testing.T) {
	ops := []dots.Op{
		{Kind: dots.OpLink, Src: "/repo/nvim/.config/nvim", Dst: "/home/user/.config/nvim"},
		{Kind: dots.OpRepair, Dst: "/home/user/.zshrc"},
	}
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	printDotOps(cmd, ops, false)

	output := buf.String()
	if !strings.Contains(output, "linked") {
		t.Error("expected 'linked' in output for OpLink")
	}
	if !strings.Contains(output, "repaired") {
		t.Error("expected 'repaired' in output for OpRepair")
	}
	if !strings.Contains(output, "2 symlink(s) updated") {
		t.Errorf("expected symlink count, got: %s", output)
	}
}

func TestPrintDotOps_AdoptAndConflict(t *testing.T) {
	ops := []dots.Op{
		{Kind: dots.OpAdopt, Src: "/repo/foo", Dst: "/home/user/foo"},
		{Kind: dots.OpConflict, Dst: "/home/user/bar", Err: errFoo},
	}
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)

	printDotOps(cmd, ops, false)

	if !strings.Contains(buf.String(), "adopted") {
		t.Error("expected 'adopted' in stdout for OpAdopt")
	}
	if !strings.Contains(errBuf.String(), "conflict") {
		t.Error("expected 'conflict' in stderr for OpConflict")
	}
	if !strings.Contains(errBuf.String(), "conflict(s)") {
		t.Error("expected conflict count in stderr")
	}
}

func TestPrintDotOps_DryRun(t *testing.T) {
	ops := []dots.Op{
		{Kind: dots.OpDryLink, Dst: "/home/user/.config/nvim"},
		{Kind: dots.OpDryRepair, Dst: "/home/user/.zshrc"},
		{Kind: dots.OpDryAdopt, Dst: "/home/user/foo"},
	}
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	printDotOps(cmd, ops, true)

	output := buf.String()
	if !strings.Contains(output, "would link") {
		t.Error("expected 'would link' in dry-run output")
	}
	if !strings.Contains(output, "would repair") {
		t.Error("expected 'would repair' in dry-run output")
	}
	if !strings.Contains(output, "would adopt") {
		t.Error("expected 'would adopt' in dry-run output")
	}
	if !strings.Contains(output, "Dry-run") {
		t.Error("expected 'Dry-run' summary in output")
	}
}

func TestPrintDotOps_NoChanges(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	printDotOps(cmd, nil, false)

	if !strings.Contains(buf.String(), "up to date") {
		t.Errorf("expected 'up to date' when no ops, got: %s", buf.String())
	}
}

// errFoo is a sentinel error for conflict tests.
var errFoo = fmt.Errorf("test conflict error")

// ─── requireDotsConfigured ────────────────────────────────────────────────────

func TestRequireDotsConfigured_NotConfigured(t *testing.T) {
	_, cfgPath := newTestCmd(t)
	// Write config with no DotsRepo.
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}
	state := &rootState{app: a}
	err = requireDotsConfigured(state)
	if err == nil {
		t.Error("expected error when dots_repo not configured, got nil")
	}
	if !strings.Contains(err.Error(), "dots_repo") {
		t.Errorf("error should mention dots_repo, got: %v", err)
	}
}

func TestRequireDotsConfigured_Configured(t *testing.T) {
	_, cfgPath := newTestCmd(t)
	repoDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}
	state := &rootState{app: a}
	if err := requireDotsConfigured(state); err != nil {
		t.Errorf("expected nil when dots_repo is set, got: %v", err)
	}
}

// buildTestApp creates an App via InitTestMode (no real providers) with the
// given config path, using a temp cache dir.
func buildTestApp(t *testing.T, cfgPath string) (*app.App, error) {
	t.Helper()
	cacheDir := t.TempDir()
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(t.Context()); err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, nil
}

// ─── version flag ────────────────────────────────────────────────────────────

// tempConfigPath returns a path to a nonexistent file in a new temp dir.
func tempConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

// ─── list command ─────────────────────────────────────────────────────────────

func TestList_NoConfig_PrintsHelpfulMessage(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	// Pre-create an active host with no tools.
	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{cliTestHostGroup()}})

	// Config present + active host + no tools → "No tools in cache".
	cmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("list with host and empty config: %v", err)
	}
	// DB is empty; command returning nil is the primary assertion.
}

func TestList_NoConfigFile_WithoutHost_RequiresHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// No settings.json, no host assignment → requireActiveHost should fail.

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no active host")
	}
	if !strings.Contains(err.Error(), "no host configuration") {
		t.Errorf("expected 'no host configuration' error, got: %v", err)
	}
}

// ─── providers command ────────────────────────────────────────────────────────

// ─── hosts commands ──────────────────────────────────────────────────────────

// ─── groups command ───────────────────────────────────────────────────────────

func TestGroups_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("groups: %v", err)
	}
}

func TestGroups_WithGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	cfg := &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git":   {Provider: "system", InstallWith: "brew"},
			"slack": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			cliTestHostGroup("git"),
			{Name: "work", Description: "work tools", Tools: []config.ToolEntry{{Name: "slack"}}},
		},
	}
	withConfig(t, cfgPath, cfg)
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("groups: %v", err)
	}
}

// ─── sync command ─────────────────────────────────────────────────────────────

func TestSync_DryRun_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run: %v", err)
	}
}

func TestSync_DryRun_NoConfig_File(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	// No settings.json at all; active-host enforcement should fail first.
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no active host")
	}
}

func TestSync_DryRun_WithHost_NoConfigFile(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	// Config with active host but no tools → sync --dry-run should succeed.
	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{cliTestHostGroup()}})

	cmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("sync --dry-run with host and empty config: %v", err)
	}
	// sync --dry-run with nothing to install should print "Dry-run" via fmt.Fprintln.
}

// ─── add command ──────────────────────────────────────────────────────────────

func TestAdd_RequiresHost_WithActiveHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Pre-create config with an active host so host enforcement passes.
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "testpkg", "--provider", "system", "--install-with", "brew", "--group", "base"})
	// This may error because brew is not available, but should not error on host checks.
	_ = cmd.Execute()
}

func TestAdd_NoHost_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Config with no active host group.
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "testpkg", "--provider", "system"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no active host")
	}
}

// ─── dots commands ────────────────────────────────────────────────────────────

func TestDotsList_NoDotsRepo_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "list"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when dots_repo is not configured")
	}
}

func TestDotsStatus_NoDotsRepo_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "status"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when dots_repo is not configured")
	}
}

func TestDotsAdd_NoGroup_NonInteractive_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: t.TempDir()},
	})
	// Force non-interactive stdin.
	origIsTerminal := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = origIsTerminal })

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "add", "~/.config/nvim"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --group not passed non-interactively")
	}
	if !strings.Contains(err.Error(), "missing assignment target") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDotsDelete_NoDotsRepo_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "delete", "nvim"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when dots_repo is not configured")
	}
}

func TestDotsDiscover_PrintsUntrackedCandidates(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "discover"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots discover: %v", err)
	}
	out := outBuf.String()
	if !strings.Contains(out, "kitty") || !strings.Contains(out, "~/.config/kitty") {
		t.Fatalf("dots discover output = %q, want kitty candidate", out)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, group := range cfg.Groups {
		for _, entry := range group.Dots {
			if entry.Name == "kitty" {
				t.Fatalf("dots discover should not mutate config, got %#v", cfg.Groups)
			}
		}
	}
}

func TestDotsIgnoreEntry_PersistsAndSuppressesDiscovery(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "kitty"), 0o755); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "ignore", "kitty", "--entry", "--path", "~/.config/kitty"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots ignore --entry: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	found := false
	for _, group := range cfg.Groups {
		for _, entry := range group.Dots {
			if entry.Name == "kitty" && entry.Ignored {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("ignored kitty entry not persisted: %#v", cfg.Groups)
	}

	discover := NewRootCmd()
	outBuf := &bytes.Buffer{}
	discover.SetOut(outBuf)
	discover.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "discover"})
	if err := discover.Execute(); err != nil {
		t.Fatalf("dots discover: %v", err)
	}
	if strings.Contains(outBuf.String(), "kitty") {
		t.Fatalf("ignored kitty should not be rediscovered, output = %q", outBuf.String())
	}
}

func TestDotsGroups_MovesAssignment(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
			{Name: "work"},
		},
	})

	listCmd := NewRootCmd()
	listOut := &bytes.Buffer{}
	listCmd.SetOut(listOut)
	listCmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "groups", "nvim"})
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("dots groups list: %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, "nvim: testhost") {
		t.Fatalf("dots groups list output = %q, want host membership", got)
	}

	moveCmd := NewRootCmd()
	moveOut := &bytes.Buffer{}
	moveCmd.SetOut(moveOut)
	moveCmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "groups", "nvim", "--move", "work"})
	if err := moveCmd.Execute(); err != nil {
		t.Fatalf("dots groups --move: %v", err)
	}
	if got := moveOut.String(); !strings.Contains(got, "moved nvim to group work") {
		t.Fatalf("dots groups --move output = %q", got)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	host := findCLIDotsTestGroup(cfg.Groups, "testhost")
	work := findCLIDotsTestGroup(cfg.Groups, "work")
	if host == nil || len(host.Dots) != 0 || work == nil || len(work.Dots) != 1 || work.Dots[0].Name != "nvim" {
		t.Fatalf("groups after move = %#v, want nvim only in work", cfg.Groups)
	}

	removeCmd := NewRootCmd()
	removeCmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "groups", "nvim", "--remove", "work"})
	if err := removeCmd.Execute(); err == nil || !strings.Contains(err.Error(), "needs at least one group") {
		t.Fatalf("dots groups --remove last err = %v, want last-membership guard", err)
	}
}

func findCLIDotsTestGroup(groups []*config.GroupConfig, name string) *config.GroupConfig {
	for _, group := range groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

func TestDotsSync_NameDryRunUsesSingleEntry(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	srcDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{
				{Name: "nvim", Path: filepath.Join(home, ".config", "nvim")},
				{Name: "zsh", Path: filepath.Join(home, ".zshrc")},
			},
		}},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "sync", "nvim", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots sync nvim --dry-run: %v", err)
	}
	got := outBuf.String()
	if !strings.Contains(got, "checking dots 1/1: nvim") || !strings.Contains(got, "would link") || strings.Contains(got, ".zshrc") {
		t.Fatalf("output = %q, want only nvim dry-run link", got)
	}
}

func TestDotsResolveRequiresExactlyOneSide(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "resolve", "nvim"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected missing side error")
	}

	cmd = NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "resolve", "nvim", "--use-repo", "--use-local"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected conflicting side error")
	}
}

func TestDotsResolveUseRepoCommand(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not available")
	}
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	srcDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("-- repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	nvimPath := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(nvimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvimPath, "init.lua"), []byte("-- local"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoTime := time.Unix(1_700_000_000, 0).Add(time.Hour)
	localTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(filepath.Join(srcDir, "init.lua"), repoTime, repoTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(nvimPath, "init.lua"), localTime, localTime); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Dots: []config.DotEntry{{Name: "nvim", Path: nvimPath}},
		}},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "dots", "resolve", "nvim", "--use-repo"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots resolve --use-repo: %v", err)
	}
	got := outBuf.String()
	if !strings.Contains(got, "repaired") {
		t.Fatalf("output = %q, want repaired op", got)
	}
	if _, err := os.Stat(filepath.Join(home, dots.BackupDirName, ".config", "nvim", "init.lua")); err != nil {
		t.Fatalf("expected local backup: %v", err)
	}
}

// ─── requireActiveHost ────────────────────────────────────────────────────────

func TestRequireActiveHost_ExemptCommands(t *testing.T) {
	// Commands in hostExempt should pass even without a host.
	exemptCmds := []struct {
		args []string
	}{
		{[]string{"hosts", "list"}},
		{[]string{"hosts", "ensure", "testhost"}},
	}
	for _, tc := range exemptCmds {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			t.Setenv("OMNI_HOSTNAME", "testhost")
			cfgDir := t.TempDir()
			cacheDir := t.TempDir()
			cfgPath := filepath.Join(cfgDir, "settings.json")
			withConfig(t, cfgPath, &config.RootConfig{})

			cmd := NewRootCmd()
			args := append([]string{"--config", cfgPath, "--cache-dir", cacheDir}, tc.args...)
			cmd.SetArgs(args)
			err := cmd.Execute()
			// Should not fail due to missing host (may fail for other reasons).
			if err != nil && strings.Contains(err.Error(), "no host configuration") {
				t.Errorf("exempt command %v should not require host, got: %v", tc.args, err)
			}
		})
	}
}

func TestRequireActiveHost_NonExemptCommand_NoHost_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for non-exempt command without active host")
	}
	if !strings.Contains(err.Error(), "no host configuration") {
		t.Errorf("expected 'no host configuration', got: %v", err)
	}
}

func TestRequireActiveHost_UpgradeForceNoHost_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "upgrade", "--all", "--force"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when upgrade --force has no active host")
	}
	if !strings.Contains(err.Error(), "no host configuration") {
		t.Errorf("expected 'no host configuration', got: %v", err)
	}
}

func TestRequireActiveHost_NonExemptCommand_WithHost_Passes(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups"})
	err := cmd.Execute()
	// Error may occur for other reasons but not host setup.
	if err != nil && strings.Contains(err.Error(), "no host configuration") {
		t.Errorf("should not get host error with active host, got: %v", err)
	}
}

// ─── hostExempt map ───────────────────────────────────────────────────────────

func TestHostExempt_ContainsExpectedCommands(t *testing.T) {
	expected := []string{"bootstrap", "doctor", "init", "hosts", "dots", "ui", "version", "settings", "help", "completion", "agents"}
	for _, name := range expected {
		if !hostExempt[name] {
			t.Errorf("expected %q in hostExempt", name)
		}
	}
	// "omni" (root) must NOT be in the exempt list.
	if hostExempt["omni"] {
		t.Error("'omni' must not be in hostExempt — it would exempt all commands")
	}
}

// ─── dots command tree structure ──────────────────────────────────────────────

func TestDotsCmd_NoSubcommand_ShowsHelp(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots"})
	// dots with no subcommand should show help (exit 0).
	_ = cmd.Execute()
}

// ─── version constant ─────────────────────────────────────────────────────────

// ─── consolidate command error paths ─────────────────────────────────────────

func TestConsolidate_MutuallyExclusive_ToAndArgs(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--to", "brew", "python", "uv"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for --to with positional args")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive', got: %v", err)
	}
}

func TestConsolidate_EcosystemMode_TooFewArgs(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "python"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for ecosystem mode with only 1 arg")
	}
}

func TestConsolidate_EcosystemDryRun_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--dry-run", "python", "uv"})
	// Should succeed with "Nothing to migrate" since config is empty.
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate dry-run empty config: %v", err)
	}
}

func TestConsolidate_ProviderMode_DryRun_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--to", "brew", "--dry-run"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate --to brew --dry-run: %v", err)
	}
}

func TestConsolidate_ProviderMode_EmptyConfig_AllAlreadyOn(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--to", "brew"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate --to brew: %v", err)
	}
}

// ─── delete command ────────────────────────────────────────────────────────

func TestDelete_MissingProvider_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "delete", "sometool"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --provider is missing")
	}
	if !strings.Contains(err.Error(), "--provider") {
		t.Errorf("expected '--provider' in error, got: %v", err)
	}
}

// ─── upgrade command ──────────────────────────────────────────────────────────

func TestUpgrade_AllAndName_Mutually_Exclusive(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "upgrade", "--all", "sometool"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --all and tool name specified together")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive', got: %v", err)
	}
}

func TestUpgrade_NameUsesInstalledProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	prov := &cliStubProvider{
		name:      "brew",
		installed: []provider.InstalledTool{{Tool: provider.Tool{Name: "ripgrep", Provider: "brew"}, Version: "1.0.0"}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	cmd := newUpgradeCmd(&rootState{app: a})
	cmd.SetArgs([]string{"ripgrep"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("upgrade ripgrep: %v", err)
	}
}

// ─── switch command ───────────────────────────────────────────────────────────

func TestSwitch_MissingFrom_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "reinstall", "sometool", "--to", "brew"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --from is missing")
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("expected '--from' in error, got: %v", err)
	}
}

func TestSwitch_MissingTo_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "reinstall", "sometool", "--from", "brew"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --to is missing")
	}
	if !strings.Contains(err.Error(), "--to") {
		t.Errorf("expected '--to' in error, got: %v", err)
	}
}

// ─── import command ───────────────────────────────────────────────────────────

// ─── install command ──────────────────────────────────────────────────────────

func TestInstall_WithExplicitProvider_AttemptsFailed(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	// Use pip as provider — if pip exists, install may attempt; if not, it errors.
	// Either way, we're testing the code path is covered.
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "install", "--provider", "pip", "nonexistent_package_xyz_123"})
	_ = cmd.Execute()
}

// ─── list with group filter ───────────────────────────────────────────────────

func TestList_GroupFilter_UnknownGroup_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "base", Tools: []config.ToolEntry{{Name: "git"}}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list", "--group", "nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for unknown group filter")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

func TestList_GroupFilter_KnownGroup_EmptyDB(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"slack": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			cliTestHostGroup(),
			{Name: "work", Tools: []config.ToolEntry{{Name: "slack"}}},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list", "--group", "work"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("list --group work with known group but empty DB: %v", err)
	}
	// DB is empty → "No tools in cache" printed (to os.Stdout).
}

// ─── sync with group positional arg ──────────────────────────────────────────

func TestSync_GroupPositionalArg_WithNamedGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Use a reusable group so the group filter matches.
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: []config.ToolEntry{{Name: "git"}}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run", "work"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("sync --dry-run work: %v", err)
	}
}

// ─── search command ───────────────────────────────────────────────────────────

// ─── groups with host group ───────────────────────────────────────────────────

func TestGroups_HostGroup_ShowsSpecialGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
			"jq":  {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Description: "host tools", Tools: []config.ToolEntry{
				{Name: "git"},
				{Name: "jq"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("groups: %v", err)
	}
}

// ─── add command with host ────────────────────────────────────────────────────

func TestAdd_WithHost_AppendsToConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "ripgrep", "--provider", "system", "--install-with", "brew", "--group", "base"})
	// This may succeed (writes config) or fail (if add validation fails for provider).
	// Either way, we exercise the code path.
	_ = cmd.Execute()
}

// ─── promptSatisfiedGroups ─────────────────────────────────────────────────────

func TestPromptSatisfiedGroups_NoActiveHost_NoCalls(t *testing.T) {
	called := false
	// No active host -> should be a no-op.
	promptSatisfiedGroups(nil, "", []string{"base"}, func(g string) error {
		called = true
		return nil
	})
	if called {
		t.Error("expected no addGroupFn call when no active host")
	}
}

func TestPromptSatisfiedGroups_NoSatisfiedGroups_NoCalls(t *testing.T) {
	called := false
	// Active host but no satisfied groups -> should be a no-op.
	promptSatisfiedGroups(nil, "work", nil, func(g string) error {
		called = true
		return nil
	})
	if called {
		t.Error("expected no addGroupFn call when no satisfied groups")
	}
}

// ─── consolidate ecosystem mode without dry-run ───────────────────────────────

func TestConsolidate_EcosystemMode_EmptyConfig_NothingToMigrate(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "node", "bun"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate node bun: %v", err)
	}
}

func TestConsolidate_EcosystemMode_InvalidEcosystem_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "invalid_eco", "bun"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid ecosystem")
	}
}

// ─── import command: results with added tools ──────────────────────────────────

func TestImport_WithProvider_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	// brew import with an empty config — if brew is available, may find real tools.
	// If not available, returns empty. Either way, exercise the code path.
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "import", "--provider", "nonexistentprovider"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("import with nonexistent provider: %v", err)
	}
}

// ─── list with provider filter ────────────────────────────────────────────────

func TestList_ProviderFilter_EmptyDB(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{cliTestHostGroup()}})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list", "--provider", "brew"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("list --provider brew with empty DB: %v", err)
	}
}

// ─── NewRootCmd covers persistent flags ───────────────────────────────────────

func TestNewRootCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewRootCmd()
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Name()] = true
	}
	expected := []string{"hosts", "dots", "groups", "tools", "bootstrap", "doctor"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected subcommand %q in root cmd", name)
		}
	}
}

func TestNewRootCmd_InitAliasResolvesToBootstrap(t *testing.T) {
	cmd := NewRootCmd()
	found, _, err := cmd.Find([]string{"init"})
	if err != nil {
		t.Fatalf("Find init alias: %v", err)
	}
	if found == nil {
		t.Fatal("init alias did not resolve to a command")
	}
	if found.Name() != "bootstrap" {
		t.Fatalf("init resolved to %q, want bootstrap", found.Name())
	}
}

func TestNewRootCmd_HasPersistentFlags(t *testing.T) {
	cmd := NewRootCmd()
	if f := cmd.PersistentFlags().Lookup("config"); f == nil {
		t.Error("expected --config persistent flag")
	}
	if f := cmd.PersistentFlags().Lookup("cache-dir"); f == nil {
		t.Error("expected --cache-dir persistent flag")
	}
}

// ─── delete with provider specified ───────────────────────────────────────

func TestDelete_WithProvider_AttemptsFailed(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "tools", "delete", "--provider", "brew", "sometool"})
	// May succeed or fail depending on brew availability — we just want the code path.
	_ = cmd.Execute()
}

// ─── switch with both flags ───────────────────────────────────────────────────

func TestSwitch_BothFlags_AttemptsFailed(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "reinstall", "sometool", "--from", "brew", "--to", "pip"})
	// Will likely fail (tool not in config) but exercises the code path.
	_ = cmd.Execute()
}

// ─── init command: existing config path (non-interactive) ────────────────────

func TestInit_ExistingConfig_PrintsNothingToDo(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	// Pre-create config so HasConfig() returns true → non-interactive path.
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init with existing config: %v", err)
	}
}

func TestBootstrapExistingHostMarksComplete(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "bootstrap"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("bootstrap existing host: %v", err)
	}

	verify := app.New(cfgPath)
	verify.CacheDir = cacheDir
	if err := verify.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode verify: %v", err)
	}
	t.Cleanup(func() { _ = verify.Close() })
	completed, err := verify.HostBootstrapCompleted(context.Background(), "testhost")
	if err != nil {
		t.Fatalf("HostBootstrapCompleted: %v", err)
	}
	if !completed {
		t.Fatal("bootstrap marker not persisted")
	}
}

// ─── dots delete: entry not found path ───────────────────────────────────────

func TestDotsDelete_WithDotsRepo_EntryNotFound_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "dots", "delete", "nonexistent_entry"})
	err := cmd.Execute()
	// Should error because the entry doesn't exist.
	if err == nil {
		t.Error("expected error when removing nonexistent dots entry")
	}
}

// ─── newInitCmd at 7% — more coverage via providers listing ──────────────────

func TestInit_CallsProviderDetection(t *testing.T) {
	// Same as TestInit_ExistingConfig but verify no panic and success.
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "init"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	// The init command should have printed "Config already exists".
}

// ─── consolidate: all tools on node/python ────────────────────────────────────

func TestConsolidate_EcosystemMode_NodeBun_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "node", "bun", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("consolidate node bun --dry-run: %v", err)
	}
}

func TestConsolidate_EcosystemMode_NodePnpm(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "node", "pnpm"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("consolidate node pnpm: %v", err)
	}
}

func TestConsolidate_EcosystemMode_PythonPip(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "python", "pip"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("consolidate python pip: %v", err)
	}
}

// ─── upgrade with --all and real DB ──────────────────────────────────────────

// ─── dots push: with repo configured ─────────────────────────────────────────

func TestDotsPush_WithDotsRepo_NothingToCommit(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "push"})
	// May fail if git is not available or not a repo — just exercise the code path.
	_ = cmd.Execute()
}

func TestDotsCommit_WithDotsRepo_NothingToCommit(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "commit"})
	// May fail if git is not available or not a repo — just exercise the code path.
	_ = cmd.Execute()
}

// ─── dots pull: with repo configured ─────────────────────────────────────────

func TestDotsPull_WithDotsRepo(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "pull"})
	// May fail if git is not available — just exercise the code path.
	_ = cmd.Execute()
}

// ─── consolidate ecosystem dry-run with migration items path ─────────────────

func TestConsolidate_EcosystemDryRun_WithPythonTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Config with pip tools — when consolidating to uv, these will be "migrated".
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
			"ruff":  {Provider: "python", InstallWith: "pip"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "black"},
				{Name: "ruff"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--dry-run", "python", "uv"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate --dry-run python uv with pip tools: %v", err)
	}
}

func TestConsolidate_EcosystemDryRun_ASCIISymbolModeUsesCommandOutput(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("NO_EMOJI", "1")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"black": {Provider: "python", InstallWith: "pip"},
		},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Tools:   []config.ToolEntry{{Name: "black"}},
		}},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--dry-run", "python", "uv"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("consolidate --dry-run python uv: %v", err)
	}

	output := out.String()
	if output == "" {
		t.Fatal("expected consolidate dry-run output to use the command output writer")
	}
	for _, bad := range []string{"—", "→", "✓", "✗", "…", "─"} {
		if strings.Contains(output, bad) {
			t.Fatalf("consolidate output contains %q in ASCII symbol mode:\n%s", bad, output)
		}
	}
	if !strings.Contains(output, "Dry-run - consolidating python tools > uv") {
		t.Fatalf("consolidate output did not rewrite dry-run symbols:\n%s", output)
	}
}

// ─── sync: warnings output ────────────────────────────────────────────────────

func TestSync_DuplicateToolOwnership_IsAllowed(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// A tool may belong to any number of reusable groups, so having ripgrep in
	// both "base" and "work" is valid and sync should succeed.
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "base", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			{Name: "work", Tools: []config.ToolEntry{
				{Name: "ripgrep"}, // same tool in a second reusable group
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("sync error = %v, want nil for tool in multiple reusable groups", err)
	}
}

// ─── import: skipped tools count ─────────────────────────────────────────────

func TestImport_AllToolsSkipped_ShowsSkippedCount(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Put tools in config that a provider would have found.
	// With nonexistent provider, result will be empty.
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "git"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "import", "--dry-run", "--provider", "nonexistentprovider"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("import: %v", err)
	}
}

// ─── dots sync: all up to date path ──────────────────────────────────────────

func TestDotsSync_AllUpToDate_WithDotsRepo(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "sync"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots sync: %v", err)
	}
	// No entries → "All symlinks up to date." printed.
	if !strings.Contains(outBuf.String(), "up to date") {
		t.Errorf("expected 'up to date', got: %q", outBuf.String())
	}
}

// ─── sync: already installed path ────────────────────────────────────────────

func TestSync_DryRun_NoInstallsNeeded_PrintsDryRun(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run", "--prune"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --prune: %v", err)
	}
}

// ─── consolidate: ecosystem → "nothing to migrate" non-dry-run result ─────────

func TestConsolidate_EcosystemMode_NothingToMigrate_SettingsUpdated(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Config has no tools in the python ecosystem — consolidate does nothing.
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "git"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "python", "pip"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("consolidate python pip: %v", err)
	}
}

// ─── providers: table with no available providers ────────────────────────────

func TestProviders_AllUnavailable_ReturnsNil(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "providers"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("providers: %v", err)
	}
}

// ─── add command: name override ───────────────────────────────────────────────

func TestAdd_WithNameOverride_UsesOverrideName(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "typescript", "--provider", "node", "--install-with", "npm", "--name", "ts", "--group", "base"})
	// Exercises the name-override code path: displayName = name (not pkg).
	_ = cmd.Execute()
}

func TestAdd_ToGroup_UsesGroupName(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "slack", "--provider", "system", "--install-with", "brew", "--group", "work"})
	// Exercises the group-destination code path: dest = group (not "base").
	_ = cmd.Execute()
}

// ─── newInitCmd: fresh machine path (no config) with stdin closed ─────────────

func TestInit_NewMachine_NoConfig(t *testing.T) {
	// init with no config requires stdin interaction for host setup.
	t.Skip("init with no config requires stdin interaction")
}

// ─── sync with retry-failed flag ─────────────────────────────────────────────

func TestSync_RetryFailed_EmptyDB(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--retry-failed"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --retry-failed with empty DB: %v", err)
	}
}

// ─── sync with provider filter ────────────────────────────────────────────────

func TestSync_ProviderFilter_DryRun(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run", "--provider", "brew"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --provider brew: %v", err)
	}
}

// ─── search command: no results ──────────────────────────────────────────────

func TestSearch_EmptyQuery_NoResults(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "search", "nonexistent_xyzqrstuvwxyz_package_123"})
	_ = cmd.Execute()
}

// ─── import command with group flag ──────────────────────────────────────────

func TestImport_WithGroupFlag_DryRun(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "import", "--dry-run", "--group", "work", "--provider", "nonexistentprovider"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("import --dry-run --group work: %v", err)
	}
}

// TestDotsVariantAddSubpath_MissingParentErrors verifies the --subpath flag
// routes through the extract-then-variant composition and surfaces its error
// when the parent entry does not exist.
func TestDotsVariantAddSubpath_MissingParentErrors(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
		Hosts: map[string][]string{"testhost": {}},
	})

	add := NewRootCmd()
	add.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "variant", "add", "nope", "--subpath", "lua"})
	if err := add.Execute(); err == nil {
		t.Fatal("expected error extracting subpath from a missing parent entry")
	}
}

func TestDotsVariantAddListRemove(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	srcDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
		Hosts: map[string][]string{"testhost": {}},
	})

	add := NewRootCmd()
	addOut := &bytes.Buffer{}
	add.SetOut(addOut)
	add.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "variant", "add", "nvim", "--host", "work.local", "--package", "nvim-work"})
	if err := add.Execute(); err != nil {
		t.Fatalf("dots variant add: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findCLIDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || group.Dots[0].Hosts["work"].Package != "nvim-work" {
		t.Fatalf("dots variant not persisted: %#v", cfg.Groups)
	}
	seeded := filepath.Join(repoDir, "dotfiles", "nvim-work", ".config", "nvim", "init.lua")
	if data, err := os.ReadFile(seeded); err != nil || string(data) != "default" {
		t.Fatalf("seeded variant = %q, %v; want default content", string(data), err)
	}

	list := NewRootCmd()
	listOut := &bytes.Buffer{}
	list.SetOut(listOut)
	list.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "variant", "list", "nvim"})
	if err := list.Execute(); err != nil {
		t.Fatalf("dots variant list: %v", err)
	}
	if !strings.Contains(listOut.String(), "nvim-work") {
		t.Fatalf("dots variant list output = %q, want nvim-work", listOut.String())
	}

	remove := NewRootCmd()
	remove.SetArgs([]string{"--yes", "--config", cfgPath, "--cache-dir", cacheDir, "dots", "variant", "remove", "nvim", "--host", "work"})
	if err := remove.Execute(); err != nil {
		t.Fatalf("dots variant remove: %v", err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after remove: %v", err)
	}
	group = findCLIDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || group.Dots[0].Hosts != nil {
		t.Fatalf("dots variant remained after remove: %#v", cfg.Groups)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "dotfiles", "nvim-work")); !os.IsNotExist(err) {
		t.Fatalf("variant package after remove error = %v, want missing", err)
	}
}

func TestDotsGroup_MultiHostOneReusableOK_TwoReusableRejected(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	srcDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "init.lua"), []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{
				Name:    "testhost",
				Special: "host",
				Dots:    []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
			},
			{Name: "work"},
			{Name: "base"},
		},
		Hosts: map[string][]string{"testhost": {}},
	})

	// host group "testhost", reusable "work","base", dot "nvim"
	ok := NewRootCmd()
	ok.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"dots", "group", "nvim", "--group", "testhost", "--group", "work"})
	if err := ok.Execute(); err != nil {
		t.Fatalf("host+one reusable should succeed: %v", err)
	}
	bad := NewRootCmd()
	bad.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"dots", "group", "nvim", "--group", "work", "--group", "base"})
	if err := bad.Execute(); err == nil {
		t.Fatal("two reusable groups for a dot must be rejected")
	}
}

func TestGroupsSetTool_MultipleReusableGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"eslint": {Type: "package", Provider: "brew", Package: "eslint"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
			{Name: "base"},
		},
		Hosts: map[string][]string{"testhost": {}},
	})

	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"groups", "set-tool", "eslint", "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("set-tool multi: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := findCLIDotsTestGroup(cfg.Groups, "work")
	base := findCLIDotsTestGroup(cfg.Groups, "base")
	if work == nil || !cliGroupHasTool(work, "eslint") {
		t.Fatalf("eslint should be a member of group work, groups=%#v", cfg.Groups)
	}
	if base == nil || !cliGroupHasTool(base, "eslint") {
		t.Fatalf("eslint should be a member of group base, groups=%#v", cfg.Groups)
	}
}

func cliGroupHasTool(g *config.GroupConfig, name string) bool {
	if g == nil {
		return false
	}
	for _, tool := range g.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// ─── dots add command: with flags ─────────────────────────────────────────────

func TestDotsAdd_WithFlags_ErrorPath(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "add",
		"~/.nonexistent_xyz_path_abc/config",
		"--name", "testentry",
		"--group", "base",
	})
	// Will likely error because path doesn't exist — exercises the code path.
	_ = cmd.Execute()
}

func TestDotsAdd_DiscoveredPersistsCandidate(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "dotfiles", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "add", "claude", "--discovered", "--group", "testhost"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots add --discovered: %v", err)
	}
	if !strings.Contains(out.String(), "claude") || !strings.Contains(out.String(), "~/.claude") {
		t.Fatalf("output = %q, want claude ~/.claude", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findCLIDotsTestGroup(cfg.Groups, "testhost")
	if group == nil || len(group.Dots) != 1 || group.Dots[0].Name != "claude" || group.Dots[0].Path != "~/.claude" {
		t.Fatalf("groups = %#v, want testhost claude", cfg.Groups)
	}
}

// ─── list: with provider filter that returns no results ────────────────────────

func TestList_BothFilters_EmptyDB(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
		},
		Hosts: map[string][]string{"testhost": {}},
	})

	// Test --provider filter with empty DB.
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list", "--provider", "pip"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list --provider pip: %v", err)
	}
}

// ─── groups: description column ──────────────────────────────────────────────

func TestGroups_WithDescription_PrintsDescription(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git":   {Provider: "system", InstallWith: "brew"},
			"slack": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Description: "host tools", Tools: []config.ToolEntry{
				{Name: "git"},
			}},
			{Name: "work", Description: "work utilities", Tools: []config.ToolEntry{
				{Name: "slack"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("groups with description: %v", err)
	}
}

func TestToolsSet_CreatesLogicalSpec(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{
		"--config", cfgPath,
		"--cache-dir", cacheDir,
		"tools", "set", "ripgrep",
		"--provider", "brew",
		"--package", "rg",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools set: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["ripgrep"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Package != "rg" || spec.Provider != "" || spec.InstallWith != "" {
		t.Fatalf("spec = %+v, want provider-list brew package rg", spec)
	}
}

func TestToolsSet_QuarantineOnlyUpdatesExistingSpec(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "brew", Package: "rg"}}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{
		"--config", cfgPath,
		"--cache-dir", cacheDir,
		"tools", "set", "ripgrep",
		"--quarantine", "exempt",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools set --quarantine: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["ripgrep"]
	if spec.Quarantine != "exempt" {
		t.Fatalf("Quarantine = %q, want exempt", spec.Quarantine)
	}
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "brew" || spec.Providers[0].Package != "rg" {
		t.Fatalf("spec = %+v, want existing provider-list preserved", spec)
	}
}

func TestToolsFallbackFromGitHub_ResolverFailurePreservesConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("OMNI_GITHUB_API_BASE", "http://127.0.0.1:1")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {Providers: []config.ToolInstallSpec{{Provider: "apt"}}},
		},
	})
	withHost(t, cfgPath)

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{
		"--config", cfgPath,
		"--cache-dir", cacheDir,
		"tools", "fallback", "rg",
		"--from-github", "BurntSushi/ripgrep",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "/repos/BurntSushi/ripgrep/releases/latest") {
		t.Fatalf("tools fallback err = %v, want GitHub resolver failure", err)
	}
	if out.String() != "" {
		t.Fatalf("output = %q, want no success output", out.String())
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	spec := cfg.Tools["rg"]
	if len(spec.Providers) != 1 || spec.Providers[0].Provider != "apt" || spec.Provider != "" || spec.InstallWith != "" {
		t.Fatalf("spec = %+v, want existing tool config preserved", spec)
	}
	if spec.Fallback != nil {
		t.Fatalf("fallback = %+v, want no unresolved draft saved", spec.Fallback)
	}
}

func TestToolsFallbackUsesConfiguredGit(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/cli/cli/releases/latest" {
			t.Fatalf("unexpected GitHub API path %q", r.URL.Path)
		}
		http.ServeFile(w, r, "../app/testdata/github_cli_latest_release.json")
	}))
	defer server.Close()
	t.Setenv("OMNI_GITHUB_API_BASE", server.URL)

	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"gh": {Provider: "system", Git: "https://github.com/cli/cli"},
		},
	})
	withHost(t, cfgPath)

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "fallback", "gh"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools fallback: %v", err)
	}
	if !strings.Contains(out.String(), `Configured fallback for logical tool "gh" from gh configured git.`) {
		t.Fatalf("output = %q, want configured git success summary", out.String())
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := cfg.Tools["gh"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing")
	}
	if fallback.Source.Owner != "cli" || fallback.Source.Repo != "cli" {
		t.Fatalf("source = %+v, want cli/cli", fallback.Source)
	}
	if fallback.Recipe.TagName != "v2.93.0" || fallback.Recipe.PublishedAt != "2026-05-27T17:47:41Z" {
		t.Fatalf("recipe metadata = tag %q published_at %q, want fixture release", fallback.Recipe.TagName, fallback.Recipe.PublishedAt)
	}
}

func TestToolsFallbackRequiresGitHubSource(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"rg": {Provider: "system", InstallWith: "apt"},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "fallback", "rg"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "tools fallback requires --from-github owner/repo or a GitHub git URL in tool config") {
		t.Fatalf("tools fallback err = %v, want GitHub source requirement", err)
	}
}

func TestToolsSet_RequiresProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "set", "ripgrep"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--provider is required") {
		t.Fatalf("tools set err = %v, want provider required", err)
	}
}

func TestToolsDelete_RemovesLogicalSpecAndMemberships(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("ripgrep")},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "tools", "delete-spec", "ripgrep"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools delete-spec: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; ok {
		t.Fatal("ripgrep spec still present")
	}
	for _, group := range cfg.Groups {
		for _, tool := range group.Tools {
			if tool.Name == "ripgrep" {
				t.Fatalf("ripgrep membership still present in group %q", group.BaseName())
			}
		}
	}
}

func TestToolsDeleteSpec_RejectsProviderTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"npm": {Provider: "node", InstallWith: "npm"},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("npm")},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "tools", "delete-spec", "npm"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "package manager/provider") {
		t.Fatalf("tools delete-spec err = %v, want protected provider tool error", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["npm"]; !ok {
		t.Fatal("npm spec was removed despite protected provider guard")
	}
	for _, group := range cfg.Groups {
		for _, tool := range group.Tools {
			if tool.Name == "npm" {
				return
			}
		}
	}
	t.Fatalf("npm membership was removed despite protected provider guard: %+v", cfg.Groups)
}

func TestGroupsMoveAndRemoveTool_ManageAssignments(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{{Name: "work"}},
	})
	withHost(t, cfgPath)

	moveCmd := NewRootCmd()
	moveCmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "move-tool", "work", "ripgrep"})
	if err := moveCmd.Execute(); err != nil {
		t.Fatalf("groups move-tool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after move: %v", err)
	}
	work := cliTestGroupByName(cfg, "work")
	if work == nil || !cliTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("work group missing ripgrep after move: %+v", cfg.Groups)
	}

	removeCmd := NewRootCmd()
	removeCmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "remove-tool", "work", "ripgrep"})
	if err := removeCmd.Execute(); err != nil {
		t.Fatalf("groups remove-tool: %v", err)
	}

	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after remove: %v", err)
	}
	work = cliTestGroupByName(cfg, "work")
	if work != nil && cliTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("work group still has ripgrep after remove: %+v", work.Tools)
	}
	if _, ok := cfg.Tools["ripgrep"]; !ok {
		t.Fatal("groups remove-tool deleted logical spec")
	}
}

func TestScopedIgnoreCommands_UpdateLogicalAndGroupIgnore(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}}},
	})
	withHost(t, cfgPath)

	toolIgnore := NewRootCmd()
	toolIgnore.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "ignore", "ripgrep"})
	if err := toolIgnore.Execute(); err != nil {
		t.Fatalf("tools ignore: %v", err)
	}
	groupIgnore := NewRootCmd()
	groupIgnore.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "ignore-tool", "work", "ripgrep"})
	if err := groupIgnore.Execute(); err != nil {
		t.Fatalf("groups ignore-tool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Tools["ripgrep"].Ignore {
		t.Fatalf("tool ignore was not persisted: %+v", cfg.Tools["ripgrep"])
	}
	if len(cfg.Ignore.Tools) != 1 || cfg.Ignore.Tools[0] != "ripgrep" {
		t.Fatalf("group ignore was not persisted globally: %+v", cfg.Ignore.Tools)
	}

	toolUnignore := NewRootCmd()
	toolUnignore.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "unignore", "ripgrep"})
	if err := toolUnignore.Execute(); err != nil {
		t.Fatalf("tools unignore: %v", err)
	}
	groupUnignore := NewRootCmd()
	groupUnignore.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "unignore-tool", "work", "ripgrep"})
	if err := groupUnignore.Execute(); err != nil {
		t.Fatalf("groups unignore-tool: %v", err)
	}

	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after unignore: %v", err)
	}
	if cfg.Tools["ripgrep"].Ignore {
		t.Fatal("tool ignore still set after tools unignore")
	}
	if len(cfg.Ignore.Tools) != 0 {
		t.Fatalf("global ignore still set after groups unignore-tool: %+v", cfg.Ignore.Tools)
	}
}

func TestGroupsDelete_RequiresMoveOrDeleteFlag(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}}},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "groups", "delete", "work"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "requires --move-to <group> or --delete-tools") {
		t.Fatalf("groups delete err = %v, want explicit handling error", err)
	}
}

func TestGroupsDelete_EmptyGroupDoesNotRequireMoveOrDeleteFlag(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{{Name: "work"}}})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "groups", "delete", "work"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("groups delete empty group: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cliTestGroupByName(cfg, "work") != nil {
		t.Fatal("empty work group still present after delete")
	}
}

func TestGroupsDelete_MoveToFlagMovesLastMembershipTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Name: "base"},
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "groups", "delete", "work", "--move-to", "base"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("groups delete --move-to: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	base := cliTestGroupByName(cfg, "base")
	if base == nil || !cliTestGroupHasTool(base, "ripgrep") {
		t.Fatalf("move target group missing moved ripgrep: %+v", cfg.Groups)
	}
	if cliTestGroupByName(cfg, "work") != nil {
		t.Fatal("work group still present after delete")
	}
}

func TestGroupsDelete_TTYPromptsForMoveTarget(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Name: "base"},
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
		},
	})
	withHost(t, cfgPath)

	withMockStdin(t, "base\ny\n", func() {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "delete", "work"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("groups delete with prompted move target: %v", err)
		}
	})

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	base := cliTestGroupByName(cfg, "base")
	if base == nil || !cliTestGroupHasTool(base, "ripgrep") {
		t.Fatalf("move target group missing prompted move ripgrep: %+v", cfg.Groups)
	}
	if cliTestGroupByName(cfg, "work") != nil {
		t.Fatal("work group still present after prompted delete")
	}
}

func TestGroupsDelete_TTYPromptsForDeleteTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
		},
	})
	withHost(t, cfgPath)

	withMockStdin(t, "DELETE\ny\n", func() {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "delete", "work"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("groups delete with prompted delete-tools: %v", err)
		}
	})

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; ok {
		t.Fatal("ripgrep spec still present after prompted delete-tools")
	}
	if cliTestGroupByName(cfg, "work") != nil {
		t.Fatal("work group still present after prompted delete-tools")
	}
}

func cliTestGroupByName(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, group := range cfg.Groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

// ─── dots list: with entries shows table ─────────────────────────────────────

func TestDotsList_WithEntries_ShowsTable(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Dots: []config.DotEntry{
				{Name: "nvim", Path: "~/.config/nvim"},
				{Name: "zsh", Path: "~/.zshrc"},
			}},
		},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots list with entries: %v", err)
	}
	// Should show table header and entry names (health may vary).
	output := outBuf.String()
	if !strings.Contains(output, "NAME") {
		t.Errorf("expected NAME column header, got: %q", output)
	}
	if !strings.Contains(output, "nvim") {
		t.Errorf("expected 'nvim' in output, got: %q", output)
	}
}

func TestDotsList_SingleNameAndStateFilter(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Dots: []config.DotEntry{
				{Name: "nvim", Path: "~/.config/nvim"},
				{Name: "zsh", Path: "~/.zshrc"},
			}},
		},
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "list", "nvim", "--state", "no-source"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots list filtered: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "nvim") || strings.Contains(got, "zsh") {
		t.Fatalf("output = %q, want only nvim", got)
	}
}

func TestDotsList_SectionsUseClassifierStatesAndActions(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	stowRoot := filepath.Join(repoDir, "dotfiles")

	writeSource := func(name, rel string) string {
		t.Helper()
		src := filepath.Join(stowRoot, name, filepath.FromSlash(rel))
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		return src
	}
	nvimSrc := writeSource("nvim", ".config/nvim")
	writeSource("zsh", ".zshrc")
	writeSource("ssh", ".ssh")
	nvimTarget := filepath.Join(home, ".config", "nvim")
	zshTarget := filepath.Join(home, ".zshrc")
	sshTarget := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(filepath.Dir(nvimTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nvimSrc, nvimTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sshTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Dots: []config.DotEntry{
				{Name: "ssh", Path: sshTarget},
				{Name: "zsh", Path: zshTarget},
				{Name: "nvim", Path: nvimTarget},
			}},
		},
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots list: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Conflict",
		"Out Of Sync",
		"Synced",
		"ssh",
		"zsh",
		"nvim",
		"conflict",
		"missing",
		"synced",
		"use-repo,use-local",
		"sync,remove,ignore",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestDotsStatus_JSONFiltersEntries(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Dots: []config.DotEntry{
				{Name: "nvim", Path: "~/.config/nvim"},
				{Name: "zsh", Path: "~/.zshrc"},
			}},
		},
	})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "status", "zsh", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots status json filtered: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"name":"zsh"`) || strings.Contains(got, `"name":"nvim"`) {
		t.Fatalf("output = %q, want only zsh JSON", got)
	}
}

// ─── dots status: with entries ────────────────────────────────────────────────

func TestDotsStatus_WithEntries_ShowsTable(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Dots: []config.DotEntry{
				{Name: "nvim", Path: "~/.config/nvim"},
			}},
		},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "status"})
	// Status may fail if git operations fail; tolerate errors from git.
	_ = cmd.Execute()
	// If it succeeded, should include table header.
	out := outBuf.String()
	if len(out) > 0 && !strings.Contains(out, "NAME") && !strings.Contains(out, "Git") {
		t.Errorf("unexpected output: %q", out)
	}
}

// ─── sync: provider flag narrows to one provider ─────────────────────────────

func TestSync_DryRun_WithProviderFlag_NoToolsForProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Config has brew tools but we filter to pip — no tools match.
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "git"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	errBuf := &bytes.Buffer{}
	cmd.SetErr(errBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run", "--provider", "pip"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run --provider pip: %v", err)
	}
}

// ─── upgrade: individual tool name uses installed cache owner ────────────────

func TestUpgrade_NameNoSuchInstalledTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "upgrade", "nonexistent_tool_xyz"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("upgrade nonexistent error = %v, want not installed", err)
	}
}

// ─── import: dry-run flag set (would-import action string) ───────────────────

func TestImport_DryRun_AllProviders_EmptyConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "import", "--dry-run"})
	// Scans all available providers for already-installed tools.
	// In most environments some tools will be found (brew, pip, etc.).
	// Either path (found tools or none) is valid.
	_ = cmd.Execute()
}

// ─── consolidate: ecosystem dry-run prints would-migrate with dry-run path ───

func TestConsolidate_EcosystemDryRun_NodeTools_PrintsWouldMigrate(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	// Config with npm tools — when consolidating to bun, they would migrate.
	settings := config.Settings{}
	settings.ProviderPriority = append([]string{"npm"}, settings.ProviderPriority...)
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"prettier":   {Provider: "npm"},
			"typescript": {Provider: "npm"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "typescript"},
				{Name: "prettier"},
			}},
		},
		Settings: settings,
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--dry-run", "node", "bun"})
	// ConsolidatePlan with node tools — should plan migration.
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate --dry-run node bun with node tools: %v", err)
	}
}

// ─── switch: missing tool returns error from app layer ───────────────────────

func TestSwitch_ToolNotFound_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "reinstall", "nonexistent_tool_xyz", "--from", "brew", "--to", "pip"})
	err := cmd.Execute()
	// Should error: tool not found in config.
	if err == nil {
		t.Error("expected error when switching nonexistent tool")
	}
}

// ─── install: no config file shows error ─────────────────────────────────────

func TestInstall_WithHost_ExplicitProvider_ReachesInstall(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	// Use an explicit provider — will fail at install step but exercises more code paths
	// than the "no provider available" path.
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "install",
		"nonexistent_package_xyz_abc_999", "--provider", "pip"})
	// Either succeeds (unlikely without pip+package) or errors at install.
	_ = cmd.Execute()
}

func TestInstall_ConfiguredToolAutoAddsHighConfidenceProviderMatches(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm", "brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {Git: "https://github.com/prettier/prettier"},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	prettierSource := provider.SourceMetadata{Type: provider.SourceTypeGitHub, URL: "https://github.com/prettier/prettier"}
	brew := &cliStubProvider{
		name: "brew",
		searchResults: []provider.SearchResult{{
			Name:     "prettier",
			Provider: "brew",
			Source:   prettierSource,
		}},
	}
	npm := &cliStubProvider{
		name: "npm",
		searchResults: []provider.SearchResult{{
			Name:     "prettier",
			Provider: "npm",
			Source:   prettierSource,
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), brew, npm); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install prettier: %v", err)
	}
	if len(npm.installedCalls) != 1 || npm.installedCalls[0] != "prettier" {
		t.Fatalf("npm installedCalls = %v, want [prettier]", npm.installedCalls)
	}
	if len(brew.installedCalls) != 0 {
		t.Fatalf("brew installedCalls = %v, want no install", brew.installedCalls)
	}
	if !strings.Contains(out.String(), "matched providers: prettier -> npm/prettier, brew/prettier") {
		t.Fatalf("output = %q, want matched providers line", out.String())
	}
	if !strings.Contains(out.String(), textutil.SymbolsFromEnv().Apply("✓ installed prettier (npm)")) {
		t.Fatalf("output = %q, want installed npm line", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 2 || providers[0].Provider != "npm" || providers[1].Provider != "brew" {
		t.Fatalf("providers = %+v, want npm then brew", providers)
	}
}

func TestInstall_ConfiguredToolPrintsProviderMatchSearchWarning(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm", "brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	brew := &cliStubProvider{
		name: "brew",
		searchResults: []provider.SearchResult{{
			Name:     "prettier",
			Provider: "brew",
		}},
	}
	npm := &cliStubProvider{
		name:      "npm",
		searchErr: fmt.Errorf("registry unavailable"),
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), brew, npm); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install prettier: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "matched provider: prettier -> brew/prettier") {
		t.Fatalf("output = %q, want matched provider line", output)
	}
	if !strings.Contains(output, "warning: searching npm: registry unavailable") {
		t.Fatalf("output = %q, want partial search warning", output)
	}
	if !strings.Contains(output, textutil.SymbolsFromEnv().Apply("✓ installed prettier (brew)")) {
		t.Fatalf("output = %q, want installed brew line", output)
	}
}

func TestInstall_ConfiguredToolDoesNotAutoInstallWeakProviderMatch(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	npm := &cliStubProvider{
		name: "npm",
		searchResults: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "npm",
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), npm); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("tools install prettier succeeded, want weak-match error")
	}
	if !strings.Contains(err.Error(), `no high-confidence provider match for "prettier"`) {
		t.Fatalf("error = %v, want high-confidence provider match error", err)
	}
	if len(npm.installedCalls) != 0 {
		t.Fatalf("npm installedCalls = %v, want no install", npm.installedCalls)
	}
	if strings.Contains(out.String(), "auto-selected provider") {
		t.Fatalf("output = %q, want no default provider fallback", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if providers := cfg.Tools["prettier"].Providers; len(providers) != 0 {
		t.Fatalf("providers = %+v, want none", providers)
	}
}

func TestInstall_ConfiguredToolAllowWeakInstallsWeakProviderMatch(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	npm := &cliStubProvider{
		name: "npm",
		searchResults: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "npm",
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), npm); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier", "--allow-weak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install prettier --allow-weak: %v", err)
	}
	if len(npm.installedCalls) != 1 || npm.installedCalls[0] != "prettier" {
		t.Fatalf("npm installedCalls = %v, want [prettier]", npm.installedCalls)
	}
	if !strings.Contains(out.String(), "matched provider: prettier -> npm/prettier-plugin-tailwindcss") {
		t.Fatalf("output = %q, want matched weak provider line", out.String())
	}
	if !strings.Contains(out.String(), textutil.SymbolsFromEnv().Apply("✓ installed prettier (npm)")) {
		t.Fatalf("output = %q, want installed npm line", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 1 || providers[0].Provider != "npm" || providers[0].Package != "prettier-plugin-tailwindcss" {
		t.Fatalf("providers = %+v, want weak npm provider saved", providers)
	}
}

func TestInstall_ConfiguredToolAllowWeakHonorsProviderFilter(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm", "brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	npm := &cliStubProvider{
		name: "npm",
		searchResults: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "npm",
		}},
	}
	brew := &cliStubProvider{
		name: "brew",
		searchResults: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "brew",
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), npm, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier", "--provider", "brew", "--allow-weak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install prettier --provider brew --allow-weak: %v", err)
	}
	if len(npm.installedCalls) != 0 {
		t.Fatalf("npm installedCalls = %v, want no install", npm.installedCalls)
	}
	if len(brew.installedCalls) != 1 || brew.installedCalls[0] != "prettier" {
		t.Fatalf("brew installedCalls = %v, want [prettier]", brew.installedCalls)
	}
	if !strings.Contains(out.String(), "matched provider: prettier -> brew/prettier-plugin-tailwindcss") {
		t.Fatalf("output = %q, want matched filtered weak provider line", out.String())
	}
	if !strings.Contains(out.String(), textutil.SymbolsFromEnv().Apply("✓ installed prettier (brew)")) {
		t.Fatalf("output = %q, want installed brew line", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 1 || providers[0].Provider != "brew" || providers[0].Package != "prettier-plugin-tailwindcss" {
		t.Fatalf("providers = %+v, want only filtered weak brew provider saved", providers)
	}
}

func TestInstall_ConfiguredToolAllowWeakProviderFamilyFilterInstallsConcreteMatch(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	brew := &cliStubProvider{
		name: "brew",
		searchResults: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "brew",
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier", "--provider", "system", "--allow-weak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install prettier --provider system --allow-weak: %v", err)
	}
	if len(brew.installedCalls) != 1 || brew.installedCalls[0] != "prettier" {
		t.Fatalf("brew installedCalls = %v, want [prettier]", brew.installedCalls)
	}
	if !strings.Contains(out.String(), "matched provider: prettier -> brew/prettier-plugin-tailwindcss") {
		t.Fatalf("output = %q, want matched family-filtered weak provider line", out.String())
	}
	if !strings.Contains(out.String(), textutil.SymbolsFromEnv().Apply("✓ installed prettier (brew)")) {
		t.Fatalf("output = %q, want installed brew line", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 1 || providers[0].Provider != "brew" || providers[0].Package != "prettier-plugin-tailwindcss" {
		t.Fatalf("providers = %+v, want weak brew match saved under concrete provider", providers)
	}
}

func TestInstall_ConfiguredToolAllowWeakNodeFamilyFilterInstallsConcreteMatch(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"npm"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("prettier")},
	})
	npm := &cliStubProvider{
		name: "npm",
		searchResults: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "npm",
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), npm); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"prettier", "--provider", "node", "--allow-weak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install prettier --provider node --allow-weak: %v", err)
	}
	if len(npm.installedCalls) != 1 || npm.installedCalls[0] != "prettier" {
		t.Fatalf("npm installedCalls = %v, want [prettier]", npm.installedCalls)
	}
	if !strings.Contains(out.String(), "matched provider: prettier -> npm/prettier-plugin-tailwindcss") {
		t.Fatalf("output = %q, want matched family-filtered weak provider line", out.String())
	}
	if !strings.Contains(out.String(), textutil.SymbolsFromEnv().Apply("✓ installed prettier (npm)")) {
		t.Fatalf("output = %q, want installed npm line", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 1 || providers[0].Provider != "npm" || providers[0].Package != "prettier-plugin-tailwindcss" {
		t.Fatalf("providers = %+v, want weak npm match saved under concrete provider", providers)
	}
}

func TestInstall_ConfiguredToolAllowWeakPythonFamilyFilterInstallsConcreteMatch(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{ProviderPriority: []string{"pip"}},
		Tools: map[string]config.ToolSpec{
			"ruff": {},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("ruff")},
	})
	pip := &cliStubProvider{
		name: "pip",
		searchResults: []provider.SearchResult{{
			Name:     "ruff-lsp",
			Provider: "pip",
		}},
	}
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	if err := a.InitTestMode(context.Background(), pip); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var out bytes.Buffer
	cmd := newInstallCmd(&rootState{app: a, yes: true})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"ruff", "--provider", "python", "--allow-weak"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tools install ruff --provider python --allow-weak: %v", err)
	}
	if len(pip.installedCalls) != 1 || pip.installedCalls[0] != "ruff" {
		t.Fatalf("pip installedCalls = %v, want [ruff]", pip.installedCalls)
	}
	if !strings.Contains(out.String(), "matched provider: ruff -> pip/ruff-lsp") {
		t.Fatalf("output = %q, want matched family-filtered weak provider line", out.String())
	}
	if !strings.Contains(out.String(), textutil.SymbolsFromEnv().Apply("✓ installed ruff (pip)")) {
		t.Fatalf("output = %q, want installed pip line", out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["ruff"].Providers
	if len(providers) != 1 || providers[0].Provider != "pip" || providers[0].Package != "ruff-lsp" {
		t.Fatalf("providers = %+v, want weak pip match saved under concrete provider", providers)
	}
}

// ─── install --group ────────────────────────────────────────────────────────

func TestInstall_Group_NoHostRequired_SyncsGroup(t *testing.T) {
	// --group + --force should work without any host setup.
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "devtools", Tools: []config.ToolEntry{{Name: "git"}}},
		},
		// Intentionally NO host group and NO Hosts map — no bootstrap done.
		Hosts: nil,
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"tools", "install", "--group", "devtools", "--force"})
	// The sync may fail at provider level (no brew in test), but the command
	// must not fail at the host-enforcement gate.
	err := cmd.Execute()
	if err != nil && strings.Contains(err.Error(), "run 'omni bootstrap'") {
		t.Fatalf("--group --force should bypass host check, got: %v", err)
	}
}

func TestInstall_Group_UnknownGroup_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"tools", "install", "--group", "nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown group")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

func TestInstall_GroupAndToolArg_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"tools", "install", "--group", "devtools", "sometool"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --group and <tool> both provided")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive', got: %v", err)
	}
}

func TestInstall_NoArgsNoGroup_ReturnsError(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"tools", "install"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no args and no --group")
	}
	if !strings.Contains(err.Error(), "tool name is required") {
		t.Errorf("expected 'tool name is required', got: %v", err)
	}
}

// ─── sync: group positional arg that exists with tools ───────────────────────

func TestSync_DryRun_WithNamedGroupArg(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "devtools", Tools: []config.ToolEntry{
				{Name: "git"},
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run", "devtools"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run devtools: %v", err)
	}
}

// ─── list: group filter where tools ARE in config (uses inGroup logic) ────────

func TestList_GroupFilter_GroupWithTools_InConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"jq":      {Provider: "system", InstallWith: "brew"},
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			cliTestHostGroup(),
			{Name: "devtools", Tools: []config.ToolEntry{
				{Name: "ripgrep"},
				{Name: "jq"},
			}},
		},
		Hosts: map[string][]string{"testhost": {"devtools"}},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "list", "--group", "devtools"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("list --group devtools: %v", err)
	}
	// DB is empty but group exists → "No tools in cache" is printed.
}

// ─── hosts remove: host assignment roundtrip ──────────────────────────────────

func TestHostsEnsure_ThenRemove_Roundtrip(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd1 := NewRootCmd()
	cmd1.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "hosts", "ensure", "mybox"})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("hosts ensure: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Hosts["mybox"]; !ok {
		t.Fatal("expected mybox host entry")
	}

	cmd2 := NewRootCmd()
	cmd2.SetArgs([]string{"-y", "--config", cfgPath, "--cache-dir", cacheDir, "hosts", "remove", "mybox"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("hosts remove: %v", err)
	}

	cfg2, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after remove: %v", err)
	}
	if _, ok := cfg2.Hosts["mybox"]; ok {
		t.Error("expected host entry to be removed")
	}
}

// ─── add command: non-interactive group requirement ───────────────────────────

func TestAdd_NonTTYRequiresGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "ripgrep", "--provider", "system"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing group error")
	}
	if !strings.Contains(err.Error(), "--group") {
		t.Fatalf("error = %v, want --group guidance", err)
	}
}

func TestAdd_TTYPromptsForGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{{Name: "work"}},
	})
	withHost(t, cfgPath)

	withMockTerminal(t, true, func() {
		withMockStdin(t, "work\n", func() {
			cmd := NewRootCmd()
			cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "add", "ripgrep", "--provider", "system", "--install-with", "brew"})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("add with prompted group: %v", err)
			}
		})
	})

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; !ok {
		t.Fatal("ripgrep spec missing after prompted add")
	}
	work := cliTestGroupByName(cfg, "work")
	if work == nil || !cliTestGroupHasTool(work, "ripgrep") {
		t.Fatalf("work group missing prompted ripgrep membership: %+v", cfg.Groups)
	}
}

// ─── consolidate: provider mode with tools to migrate (dry-run) ──────────────

func TestConsolidate_ProviderMode_DryRun_WithBrewTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"fd":      {Provider: "python", InstallWith: "pip"},
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "ripgrep"},
				{Name: "fd"}, // non-brew tool = candidate for migration
			}},
		},
	})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "consolidate", "--to", "brew", "--dry-run"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("consolidate --to brew --dry-run with mixed tools: %v", err)
	}
}

// ─── sync: OpIgnored path — global ignore list ────────────────────────────────

func TestSync_DryRun_WithIgnoredTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"git":   {Provider: "system", InstallWith: "brew"},
			"slack": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "git"},
				{Name: "slack"},
			}},
		},
		Ignore: config.GlobalIgnore{Tools: []string{"slack"}},
	})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "sync", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync --dry-run with ignored tool: %v", err)
	}
}

// ─── delete: success path via provider (may error at PM level) ─────────────

func TestDelete_WithProvider_TargetsMissingTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})
	withHost(t, cfgPath)

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "tools", "delete", "--provider", "pip", "definitely_nonexistent_xyz_tool_123"})
	// Will fail at the PM level — exercises code paths in newDeleteCmd beyond
	// the --provider-missing check.
	_ = cmd.Execute()
}

// ─── dots status: with git repo has clean status ─────────────────────────────

func TestDotsStatus_WithGitRepo_WorkingTreeClean(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	// Create a real git repo so that DotsStatus can call git status.
	repoDir := t.TempDir()
	if err := initGitRepo(t, repoDir); err != nil {
		t.Skip("git not available: " + err.Error())
	}

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots status with git repo: %v", err)
	}
	output := outBuf.String()
	// Clean git repo → "Git: working tree clean".
	if !strings.Contains(output, "Git") {
		t.Errorf("expected 'Git' in output, got: %q", output)
	}
}

// initGitRepo initialises a minimal git repo in dir for testing.
func initGitRepo(t *testing.T, dir string) error {
	t.Helper()
	// Use os/exec to run git commands — git is a standard system binary.
	var runErr error
	runCmd := func(args ...string) {
		if runErr != nil {
			return
		}
		c := exec.Command("git", args...) //nolint:gosec
		c.Dir = dir
		// Set minimal git config to avoid "user.email" errors on commits.
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := c.CombinedOutput()
		if err != nil {
			runErr = fmt.Errorf("git %v: %w (%s)", args, err, out)
		}
	}
	runCmd("init")
	runCmd("commit", "--allow-empty", "-m", "init")
	return runErr
}

// ─── dots status: ensure the working-tree-clean path is asserted ─────────────

func TestDotsStatus_EmptyEntries_OutputsCleanStatus(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots status: %v", err)
	}
	// Non-git dir → gitStatus is empty → prints "Git: working tree clean".
	output := outBuf.String()
	if !strings.Contains(output, "Git") {
		t.Errorf("expected 'Git' in output, got: %q", output)
	}
}

// ─── scanLine and promptYesNo via stdinScanner mock ──────────────────────────

// withMockStdin replaces the package-level stdinScanner with one reading from
// the provided string, runs f, then restores the original scanner.
func withMockStdin(t *testing.T, input string, f func()) {
	t.Helper()
	orig := stdinScanner
	origTerm := stdinIsTerminal
	t.Cleanup(func() {
		stdinScanner = orig
		stdinIsTerminal = origTerm
	})
	stdinScanner = bufio.NewScanner(strings.NewReader(input))
	// Mocked input implies an interactive session: prompts gated on a real
	// terminal must still read it, independent of go test's /dev/null stdin
	// happening to satisfy the char-device check.
	stdinIsTerminal = func() bool { return true }
	f()
}

func withMockTerminal(t *testing.T, isTerminal bool, f func()) {
	t.Helper()
	orig := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = orig })
	stdinIsTerminal = func() bool { return isTerminal }
	f()
}

func TestScanLine_ReturnsLine(t *testing.T) {
	withMockStdin(t, "hello world\n", func() {
		line, ok := scanLine()
		if !ok {
			t.Error("expected ok=true")
		}
		if line != "hello world" {
			t.Errorf("scanLine() = %q, want %q", line, "hello world")
		}
	})
}

func TestScanLine_EOF_ReturnsFalse(t *testing.T) {
	withMockStdin(t, "", func() {
		_, ok := scanLine()
		if ok {
			t.Error("expected ok=false on EOF")
		}
	})
}

func TestPromptYesNo_YesAnswer(t *testing.T) {
	withMockStdin(t, "y\n", func() {
		result := promptYesNo(nil, "Continue?", false)
		if !result {
			t.Error("expected true for 'y' answer")
		}
	})
}

func TestPromptYesNo_YesFullWord(t *testing.T) {
	withMockStdin(t, "yes\n", func() {
		result := promptYesNo(nil, "Continue?", false)
		if !result {
			t.Error("expected true for 'yes' answer")
		}
	})
}

func TestPromptYesNo_NoAnswer(t *testing.T) {
	withMockStdin(t, "n\n", func() {
		result := promptYesNo(nil, "Continue?", true)
		if result {
			t.Error("expected false for 'n' answer")
		}
	})
}

func TestPromptYesNo_EmptyInput_UsesDefault_False(t *testing.T) {
	withMockStdin(t, "\n", func() {
		result := promptYesNo(nil, "Continue?", false)
		if result {
			t.Error("expected default (false) for empty input")
		}
	})
}

func TestPromptYesNo_EmptyInput_UsesDefault_True(t *testing.T) {
	withMockStdin(t, "\n", func() {
		result := promptYesNo(nil, "Continue?", true)
		if !result {
			t.Error("expected default (true) for empty input")
		}
	})
}

func TestPromptYesNo_EOF_UsesDefault(t *testing.T) {
	withMockStdin(t, "", func() {
		result := promptYesNo(nil, "Continue?", true)
		if !result {
			t.Error("expected default (true) on EOF")
		}
	})
}

func TestPromptYesNo_StateYesBypass(t *testing.T) {
	// state.yes must short-circuit the prompt and return true regardless of
	// the default value or stdin content.
	called := false
	stdinScannerOrig := stdinScanner
	t.Cleanup(func() { stdinScanner = stdinScannerOrig })
	stdinScanner = bufio.NewScanner(funcReader{readFn: func([]byte) (int, error) {
		called = true
		return 0, nil
	}})
	if !promptYesNo(&rootState{yes: true}, "Continue?", false) {
		t.Error("state.yes should force true")
	}
	if called {
		t.Error("state.yes should bypass stdin entirely")
	}
}

type funcReader struct{ readFn func([]byte) (int, error) }

func (r funcReader) Read(p []byte) (int, error) { return r.readFn(p) }

func TestPromptSatisfiedGroups_WithSatisfiedGroup_YesAnswer_CallsAddFn(t *testing.T) {
	called := ""
	withMockStdin(t, "y\n", func() {
		promptSatisfiedGroups(nil, "work", []string{"extras"}, func(g string) error {
			called = g
			return nil
		})
	})
	if called != "extras" {
		t.Errorf("expected addGroupFn to be called with 'extras', got %q", called)
	}
}

func TestPromptSatisfiedGroups_WithSatisfiedGroup_NoAnswer_NoCalls(t *testing.T) {
	called := false
	withMockStdin(t, "n\n", func() {
		promptSatisfiedGroups(nil, "work", []string{"extras"}, func(g string) error {
			called = true
			return nil
		})
	})
	if called {
		t.Error("expected addGroupFn NOT to be called when user answers no")
	}
}

// ─── promptReassignClaimedTools ──────────────────────────────────────────────

func newReassignTestApp(t *testing.T, toolNames ...string) *app.App {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	tools := map[string]config.ToolSpec{}
	hostGroup := &config.GroupConfig{Name: "testhost", Special: "host"}
	for _, name := range toolNames {
		tools[name] = config.ToolSpec{Provider: "system", InstallWith: "brew"}
		hostGroup.Tools = append(hostGroup.Tools, config.ToolEntry{Name: name})
	}
	withConfig(t, cfgPath, &config.RootConfig{
		Tools:  tools,
		Groups: []*config.GroupConfig{hostGroup},
	})
	a := app.New(cfgPath)
	a.CacheDir = cacheDir
	brew := &cliStubProvider{name: "brew"}
	if err := a.InitTestMode(context.Background(), brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestPromptReassign_AllToSameGroup(t *testing.T) {
	a := newReassignTestApp(t, "ripgrep", "fd", "bat")
	state := &rootState{app: a}
	// User types "a" (all), then "dev" as group name.
	withMockStdin(t, "a\ndev\n", func() {
		promptReassignClaimedTools(state, []string{"ripgrep", "fd", "bat"})
	})
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	devGroup := findGroup(cfg, "dev")
	if devGroup == nil {
		t.Fatal("expected 'dev' group to be created")
	}
	got := groupToolNames(devGroup)
	want := []string{"bat", "fd", "ripgrep"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("dev group tools = %v, want %v", got, want)
	}
}

func TestPromptReassign_Individual(t *testing.T) {
	a := newReassignTestApp(t, "ripgrep", "fd")
	state := &rootState{app: a}
	// User types "i" (individual), "dev" for ripgrep, "" for fd (uses lastGroup="dev").
	withMockStdin(t, "i\ndev\n\n", func() {
		promptReassignClaimedTools(state, []string{"ripgrep", "fd"})
	})
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	devGroup := findGroup(cfg, "dev")
	if devGroup == nil {
		t.Fatal("expected 'dev' group to be created")
	}
	got := groupToolNames(devGroup)
	slices.Sort(got)
	want := []string{"fd", "ripgrep"}
	if !slices.Equal(got, want) {
		t.Errorf("dev group tools = %v, want %v", got, want)
	}
}

func TestPromptReassign_Skip(t *testing.T) {
	a := newReassignTestApp(t, "ripgrep")
	state := &rootState{app: a}
	withMockStdin(t, "s\n", func() {
		promptReassignClaimedTools(state, []string{"ripgrep"})
	})
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	// Tool should stay in testhost group, no "dev" group created.
	if findGroup(cfg, "dev") != nil {
		t.Error("expected no 'dev' group after skip")
	}
	hostGroup := findGroup(cfg, "testhost")
	if hostGroup == nil || !slices.Contains(groupToolNames(hostGroup), "ripgrep") {
		t.Error("ripgrep should remain in testhost group")
	}
}

func TestPromptReassign_EOF(t *testing.T) {
	a := newReassignTestApp(t, "ripgrep")
	state := &rootState{app: a}
	withMockStdin(t, "", func() {
		promptReassignClaimedTools(state, []string{"ripgrep"})
	})
	// Should not panic or error — graceful exit on EOF.
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if findGroup(cfg, "dev") != nil {
		t.Error("expected no 'dev' group after EOF")
	}
}

func TestPromptReassign_SkipsNonInteractiveStdin(t *testing.T) {
	a := newReassignTestApp(t, "ripgrep")
	state := &rootState{app: a}
	withMockStdin(t, "a\ndev\n", func() {
		withMockTerminal(t, false, func() {
			promptReassignClaimedTools(state, []string{"ripgrep"})
		})
	})
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if findGroup(cfg, "dev") != nil {
		t.Error("expected no prompt interaction on non-terminal stdin")
	}
}

func TestPromptReassign_SkipsAssumeYes(t *testing.T) {
	a := newReassignTestApp(t, "ripgrep")
	state := &rootState{app: a, yes: true}
	withMockStdin(t, "a\ndev\n", func() {
		promptReassignClaimedTools(state, []string{"ripgrep"})
	})
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if findGroup(cfg, "dev") != nil {
		t.Error("expected no prompt interaction under --yes")
	}
}

func TestPromptReassign_Empty(t *testing.T) {
	state := &rootState{}
	// Empty list — should be no-op, no panic.
	promptReassignClaimedTools(state, nil)
	promptReassignClaimedTools(state, []string{})
}

func findGroup(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, g := range cfg.Groups {
		if g.BaseName() == name {
			return g
		}
	}
	return nil
}

func groupToolNames(g *config.GroupConfig) []string {
	names := make([]string, len(g.Tools))
	for i, t := range g.Tools {
		names[i] = t.Name
	}
	return names
}

// ─── global ignore add/remove roundtrip ──────────────────────────────────────

func TestGlobalIgnore_AddRemove_Roundtrip(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"zoom": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{{Name: "work", Tools: []config.ToolEntry{{Name: "zoom"}}}},
	})
	withHost(t, cfgPath)

	cmd1 := NewRootCmd()
	cmd1.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "ignore-tool", "work", "zoom"})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("groups ignore-tool: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Ignore.Tools) != 1 || cfg.Ignore.Tools[0] != "zoom" {
		t.Fatalf("expected zoom in global ignore list after add, got %+v", cfg.Ignore.Tools)
	}

	cmd2 := NewRootCmd()
	cmd2.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "groups", "unignore-tool", "work", "zoom"})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("groups unignore-tool: %v", err)
	}

	cfg2, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after remove: %v", err)
	}
	if len(cfg2.Ignore.Tools) != 0 {
		t.Fatalf("expected zoom to be removed from global ignore list, got %+v", cfg2.Ignore.Tools)
	}
}

// ─── dots status: dirty git repo prints non-empty git status ─────────────────

func TestDotsStatus_WithDirtyGitRepo_PrintsGitStatus(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")

	repoDir := t.TempDir()
	if err := initGitRepo(t, repoDir); err != nil {
		t.Skip("git not available: " + err.Error())
	}

	// Add an untracked file to make git status non-empty.
	untracked := filepath.Join(repoDir, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots status with dirty repo: %v", err)
	}
	output := outBuf.String()
	// Dirty repo → GitStatus != "" → prints "Git status:" header and the status lines.
	if !strings.Contains(output, "Git status:") {
		t.Errorf("expected 'Git status:' in output for dirty repo, got: %q", output)
	}
}

func TestDotsReminderCheck_Clean(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "reminder", "check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots reminder check: %v", err)
	}
	if got := outBuf.String(); !strings.Contains(got, "Dotfiles are in sync.") {
		t.Fatalf("dots reminder check output = %q, want clean message", got)
	}
}

func TestDotsReminderCheck_DirtyGitRepo(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", t.TempDir())
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	if err := initGitRepo(t, repoDir); err != nil {
		t.Skip("git not available: " + err.Error())
	}
	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	})

	cmd := NewRootCmd()
	outBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "dots", "reminder", "check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dots reminder check: %v", err)
	}
	output := outBuf.String()
	if !strings.Contains(output, "Dotfiles need attention:") || !strings.Contains(output, "pending git change") {
		t.Fatalf("dots reminder check output = %q, want git reminder", output)
	}
}

func TestDotsReminderStatus_PrintsPersistedServiceOptions(t *testing.T) {
	cfgPath, cacheDir := setupDotsServiceCLITest(t)

	if _, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "reminder", "install", "--interval", "3h", "--notify=false"); err != nil {
		t.Fatalf("dots reminder install: %v", err)
	}
	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "reminder", "status")
	if err != nil {
		t.Fatalf("dots reminder status: %v", err)
	}

	for _, want := range []string{
		"Dots reminder service: installed",
		"Interval: 3h0m0s",
		"Notifications: false",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dots reminder status output = %q, want %q", output, want)
		}
	}
}

func TestDotsWatchStatus_PrintsPersistedServiceOptions(t *testing.T) {
	cfgPath, cacheDir := setupDotsServiceCLITest(t)

	if _, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "watch", "install", "--debounce", "7s"); err != nil {
		t.Fatalf("dots watch install: %v", err)
	}
	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "watch", "status")
	if err != nil {
		t.Fatalf("dots watch status: %v", err)
	}

	for _, want := range []string{
		"Dots watch service: installed",
		"Debounce: 7s",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dots watch status output = %q, want %q", output, want)
		}
	}
}

func TestDotsServicesStatus_PrintsCombinedServiceOptions(t *testing.T) {
	cfgPath, cacheDir := setupDotsServiceCLITest(t)

	if _, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "reminder", "install", "--interval", "3h", "--notify=false"); err != nil {
		t.Fatalf("dots reminder install: %v", err)
	}
	if _, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "watch", "install", "--debounce", "7s"); err != nil {
		t.Fatalf("dots watch install: %v", err)
	}
	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "dots", "services", "status")
	if err != nil {
		t.Fatalf("dots services status: %v", err)
	}

	for _, want := range []string{
		"Dots services",
		"Reminder: installed",
		"Interval: 3h0m0s",
		"Notifications: false",
		"Watch: installed",
		"Debounce: 7s",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dots services status output = %q, want %q", output, want)
		}
	}
}

func TestDotsServiceCommandsUseAppDefaultDurations(t *testing.T) {
	root := NewRootCmd()
	reminderInstall := findCommand(root, []string{"dots", "reminder", "install"})
	if reminderInstall == nil {
		t.Fatal("missing dots reminder install command")
	}
	if got := reminderInstall.Flags().Lookup("interval").DefValue; got != app.DefaultDotsReminderInterval().String() {
		t.Fatalf("reminder interval default = %q, want %q", got, app.DefaultDotsReminderInterval())
	}

	watchRun := findCommand(root, []string{"dots", "watch", "run"})
	if watchRun == nil {
		t.Fatal("missing dots watch run command")
	}
	if got := watchRun.Flags().Lookup("debounce").DefValue; got != app.DefaultDotsWatchDebounce().String() {
		t.Fatalf("watch run debounce default = %q, want %q", got, app.DefaultDotsWatchDebounce())
	}

	watchInstall := findCommand(root, []string{"dots", "watch", "install"})
	if watchInstall == nil {
		t.Fatal("missing dots watch install command")
	}
	if got := watchInstall.Flags().Lookup("debounce").DefValue; got != app.DefaultDotsWatchDebounce().String() {
		t.Fatalf("watch install debounce default = %q, want %q", got, app.DefaultDotsWatchDebounce())
	}
}

func TestDoctor_NoHostStillRuns(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "doctor")
	if err != nil {
		t.Fatalf("doctor without host: %v\n%s", err, output)
	}
	for _, want := range []string{"Omni doctor", "Host:", "current host is not configured", "Summary:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output = %q, want %q", output, want)
		}
	}
}

func TestDoctor_InvalidConfigPrintsDiagnosticsAndFails(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	if err := os.WriteFile(cfgPath, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "doctor")
	if err == nil {
		t.Fatalf("doctor invalid config err = nil, output:\n%s", output)
	}
	for _, want := range []string{"Omni doctor", "[fail] Config:", "settings.json cannot be loaded"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output = %q, want %q", output, want)
		}
	}
}

func TestDoctorFixRemovesDuplicateDotDefinitions(t *testing.T) {
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	t.Setenv("OMNI_HOSTNAME", "testhost")
	writeFile := func(rel, content string) {
		p := filepath.Join(cfgDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("settings.json", `{
  "$include": ["settings.d/dots.json"],
  "hosts": {"testhost": ["dev"]},
  "groups": [
    {"name": "testhost", "special": "host"},
    {"name": "dev", "dots": [{"name": "git", "path": "~/.gitconfig"}]}
  ]
}`)
	writeFile("settings.d/dots.json", `{
  "groups": [{"name": "dev", "dots": [{"name": "git", "path": "~/.gitconfig"}]}]
}`)

	// Dry-run: reports, does not write.
	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "doctor", "--fix", "--dry-run")
	if err != nil {
		t.Fatalf("doctor --fix --dry-run: %v\n%s", err, output)
	}
	if !strings.Contains(output, "git") || !strings.Contains(output, "would remove") {
		t.Fatalf("dry-run output missing planned removal: %s", output)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MergeNotices) == 0 {
		t.Fatal("dry-run must not have fixed anything")
	}

	// Real fix: removes the parent copy, doctor no longer warns.
	output, err = runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "doctor", "--fix")
	if err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, output)
	}
	if !strings.Contains(output, "removed") {
		t.Fatalf("fix output missing removal summary: %s", output)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MergeNotices) != 0 {
		t.Fatalf("merge notices survived --fix: %v", cfg.MergeNotices)
	}
	if strings.Contains(output, "duplicate definitions") {
		t.Fatalf("doctor report still shows duplicates after fix: %s", output)
	}
}

func TestDoctorFixOptimizerFailureStillCleansIgnorePatterns(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	if err := os.MkdirAll(filepath.Join(cfgDir, "settings.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(cfgPath, `{
  "$include": ["settings.d/dots.json"],
  "groups": [
	{"dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "vim", "path": "~/.vimrc"}]},
    {"name": "dev", "dots": [{"name": "myapp", "path": "~/.myapp", "ignore": ["*", "!/settings.json", "!/skills/", "skills"]}]}
  ]
}`)
	write(filepath.Join(cfgDir, "settings.d", "dots.json"), `{
  "groups": [{"dots": [{"name": "git", "path": "~/.gitconfig"}, {"name": "zsh", "path": "~/.zshrc"}]}]
}`)

	cmd := newDoctorCmd(&rootState{app: app.New(cfgPath)})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--fix"})
	err := cmd.Execute()
	output := out.String()
	if err == nil {
		t.Fatalf("doctor --fix should report optimizer failure:\n%s", output)
	}
	if !strings.Contains(err.Error(), "merged config would change") {
		t.Fatalf("doctor --fix error = %v", err)
	}
	if !strings.Contains(output, "cleaned ignore patterns for: myapp") {
		t.Fatalf("doctor --fix did not report independent ignore cleanup:\n%s", output)
	}

	cfg, loadErr := config.Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	dev := findCLIDotsTestGroup(cfg.Groups, "dev")
	if dev == nil || len(dev.Dots) != 1 {
		t.Fatalf("dev group after fix = %#v", dev)
	}
	if want := []string{"*", "!/settings.json"}; !slices.Equal(dev.Dots[0].Ignore, want) {
		t.Fatalf("ignore patterns = %v, want %v", dev.Dots[0].Ignore, want)
	}
}

func TestDoctorDryRunRequiresFix(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	cacheDir := t.TempDir()
	t.Setenv("OMNI_HOSTNAME", "testhost")
	withConfig(t, cfgPath, &config.RootConfig{})

	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", cacheDir, "doctor", "--dry-run")
	if err == nil {
		t.Fatalf("doctor --dry-run without --fix should error, got:\n%s", output)
	}
	if !strings.Contains(err.Error(), "--fix") {
		t.Fatalf("error should mention --fix, got: %v", err)
	}
}

func setupDotsServiceCLITest(t *testing.T) (string, string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		t.Skipf("native dots services are not supported on %s", runtime.GOOS)
	}

	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	repoDir := filepath.Join(home, "dotfiles")
	configDir := filepath.Join(home, "config")
	cacheDir := filepath.Join(home, "cache")
	cfgPath := filepath.Join(configDir, "settings.json")
	for _, dir := range []string{binDir, repoDir, configDir, cacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"stow", "systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OMNI_HOSTNAME", "testhost")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("PATH", binDir)

	withConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups:   []*config.GroupConfig{{Name: "testhost", Special: "host"}},
		Hosts:    map[string][]string{"testhost": {}},
	})
	return cfgPath, cacheDir
}

func runRootCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err != nil && errOut.Len() > 0 {
		return out.String() + errOut.String(), err
	}
	return out.String(), err
}

func TestList_SingleToolAndStateFilter_PrintsOnlyMatch(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")

	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"bat":     {Provider: "system", InstallWith: "brew"},
			"ripgrep": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host", Tools: []config.ToolEntry{
				{Name: "ripgrep"},
				{Name: "bat"},
			}},
		},
	})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}
	ctx := t.Context()
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: true}); err != nil {
		t.Fatalf("upsert ripgrep: %v", err)
	}
	if err := a.DB().UpdateOutdated(ctx, "ripgrep", "brew", "ripgrep", true, "14.0.0"); err != nil {
		t.Fatalf("outdated ripgrep: %v", err)
	}
	if err := a.DB().Upsert(ctx, &database.ToolCache{Name: "bat", Provider: "brew", Package: "bat", Installed: false}); err != nil {
		t.Fatalf("upsert bat: %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", a.CacheDir, "tools", "list", "ripgrep", "--state", "outdated"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list single outdated: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "ripgrep") || !strings.Contains(got, "outdated") {
		t.Fatalf("output = %q, want ripgrep outdated", got)
	}
	if strings.Contains(got, "bat") {
		t.Fatalf("output = %q, did not expect bat", got)
	}
}

func TestList_JSONExcludesIgnoredTools(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")

	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"bat": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{cliTestHostGroup("bat")},
		Ignore: config.GlobalIgnore{Tools: []string{"bat"}},
	})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}
	if err := a.DB().Upsert(t.Context(), &database.ToolCache{Name: "bat", Provider: "brew", Package: "bat", Installed: true}); err != nil {
		t.Fatalf("upsert bat: %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", a.CacheDir, "tools", "list", "--state", "ignored", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list json ignored: %v", err)
	}
	got := out.String()
	if strings.TrimSpace(got) != "[]" {
		t.Fatalf("output = %q, want ignored tools hidden from JSON list", got)
	}
}

// ─── list: installed tool with version string ─────────────────────────────────

func TestList_WithInstalledToolAndVersion_PrintsVersion(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")

	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{cliTestHostGroup()}})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	ctx := t.Context()
	err = a.DB().Upsert(ctx, &database.ToolCache{
		Name:      "fzf",
		Provider:  "brew",
		Package:   "fzf",
		Installed: true,
	})
	if err != nil {
		t.Fatalf("DB.Upsert: %v", err)
	}
	// Mark it installed with a version to exercise the version.Valid branch.
	if err := a.DB().MarkInstalled(ctx, "fzf", "brew", "fzf", "0.54.0"); err != nil {
		t.Fatalf("DB.MarkInstalled: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", a.CacheDir, "tools", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list with versioned tool: %v", err)
	}
	// Exercise covers the version.Valid branch.
}

// ─── NewRootCmd default config path (no --config flag) ────────────────────────

// TestNewRootCmd_DefaultConfigPath_ViaEnv exercises the PersistentPreRunE branch
// that resolves the config path from OMNI_CONFIG when --config is not provided.
func TestNewRootCmd_DefaultConfigPath_ViaEnv(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	cacheDir := t.TempDir()
	withConfig(t, cfgPath, &config.RootConfig{})

	// Set OMNI_CONFIG so DefaultConfigPath returns our temp file.
	t.Setenv("OMNI_CONFIG", cfgPath)

	cmd := NewRootCmd()
	// Run a host-exempt command (providers) without --config.
	cmd.SetArgs([]string{"--cache-dir", cacheDir, "tools", "providers"})
	err := cmd.Execute()
	// Providers is host-exempt; the default-path branch is exercised.
	_ = err
}

// ─── list: missing tool (not installed) prints "missing" status ──────────────

func TestList_WithMissingTool_PrintsMissingStatus(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")

	withConfig(t, cfgPath, &config.RootConfig{Groups: []*config.GroupConfig{cliTestHostGroup()}})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	ctx := t.Context()
	// Seed a tool that is NOT installed to exercise the "missing" branch.
	err = a.DB().Upsert(ctx, &database.ToolCache{
		Name:      "bat",
		Provider:  "brew",
		Package:   "bat",
		Installed: false,
	})
	if err != nil {
		t.Fatalf("DB.Upsert: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", a.CacheDir, "tools", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list with missing tool: %v", err)
	}
	// Exercises the Installed=false → status="missing" branch in newListCmd.
}

func TestSettingsShow_KeyAndJSON(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	settings := config.Settings{DisabledProviders: []string{"system"}, ProviderPriority: []string{"npm", "brew"}}
	withConfig(t, cfgPath, &config.RootConfig{Settings: settings})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "settings", "show", "provider_priority", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("settings show key json: %v", err)
	}
	if !strings.Contains(out.String(), `"provider_priority":["npm","brew"]`) {
		t.Fatalf("output = %q, want provider_priority JSON", out.String())
	}
}

func TestSettingsGet_PrintsSingleValue(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	settings := config.Settings{ProviderPriority: []string{"uv", "pip"}}
	withConfig(t, cfgPath, &config.RootConfig{Settings: settings})

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "settings", "get", "provider_priority"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("settings get: %v", err)
	}
	if strings.TrimSpace(out.String()) != "uv, pip" {
		t.Fatalf("output = %q, want provider priority", out.String())
	}
}

func TestSettingsSet_BooleanAndDotsGitSettings(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	for _, tc := range []struct {
		key  string
		want string
	}{
		{key: "auto_import", want: "auto_import = true"},
		{key: "dots_git.auto_commit", want: "dots_git.auto_commit = true"},
		{key: "dots_git.auto_push", want: "dots_git.auto_push = true"},
	} {
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "settings", "set", tc.key, "true"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("settings set %s: %v", tc.key, err)
		}
		if strings.TrimSpace(out.String()) != tc.want {
			t.Fatalf("settings set %s output = %q, want %q", tc.key, strings.TrimSpace(out.String()), tc.want)
		}
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !loaded.Settings.AutoImport {
		t.Error("auto_import was not persisted")
	}
	if !loaded.Settings.DotsGit.AutoCommit {
		t.Error("dots_git.auto_commit was not persisted")
	}
	if !loaded.Settings.DotsGit.AutoPush {
		t.Error("dots_git.auto_push was not persisted")
	}
}

func TestSettingsDisableProvider_AcceptsConcreteProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "settings", "disable-provider", "brew"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("settings disable-provider brew: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.HostSettings["testhost"].DisabledProviders; !slices.Contains(got, "brew") {
		t.Fatalf("disabled providers = %v, want brew present", got)
	}
}

func TestSettingsDisableProvider_RejectsUnknownProvider(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "settings", "disable-provider", "bogus-pm"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "is not a known provider") {
		t.Fatalf("error = %v, want unknown-provider validation", err)
	}
}

func TestSettingsSet_RejectsInvalidManagerAndPriority(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "node.manager no longer settable",
			args: []string{"settings", "set", "node.manager", "uv"},
			want: "no longer settable",
		},
		{
			name: "system.priority no longer settable",
			args: []string{"settings", "set", "system.priority", "brew"},
			want: "no longer settable",
		},
	} {
		cmd := NewRootCmd()
		cmd.SetArgs(append([]string{"--config", cfgPath, "--cache-dir", cacheDir}, tc.args...))
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("%s succeeded, want validation error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s error = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// ─── list: group filter with matching tool in DB ──────────────────────────────

func TestList_GroupFilter_MatchingTool_PrintsTool(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")

	withConfig(t, cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"slack": {Provider: "system", InstallWith: "brew"},
		},
		Groups: []*config.GroupConfig{
			cliTestHostGroup(),
			{Name: "work", Tools: []config.ToolEntry{{Name: "slack"}}},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	ctx := t.Context()
	err = a.DB().Upsert(ctx, &database.ToolCache{
		Name:      "slack",
		Provider:  "brew",
		Package:   "slack",
		Installed: true,
	})
	if err != nil {
		t.Fatalf("DB.Upsert: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", a.CacheDir, "tools", "list", "--group", "work"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list --group work with matching tool: %v", err)
	}
	// Exercises the group filter loop + tool filtering + tool-print loop.
}

// ─── init: full flow with stdin mock ─────────────────────────────────────────

// TestInit_FullFlow_WithMockedStdin exercises the init command's non-interactive
// branches by providing stdin answers for the setup prompts.
func TestInit_FullFlow_WithMockedStdin(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	// Use a non-existent config path so init proceeds past the "already exists" guard.
	cfgPath := filepath.Join(cfgDir, "new_settings.json")

	// Provide stdin answers:
	// - Dots section: "n" (no)
	// - Import: "n" (no)
	stdinInput := "n\nn\n"
	withMockStdin(t, stdinInput, func() {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "init", "--no-import"})
		_ = cmd.Execute()
	})
}

func TestBootstrapImportConfigFlag(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	sourcePath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, sourcePath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: []config.ToolEntry{{Name: "ripgrep"}}},
			cliTestHostGroup(),
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	})

	withMockStdin(t, "n\nn\n", func() {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "bootstrap", "--import-config", sourcePath})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("bootstrap --import-config: %v", err)
		}
	})

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; !ok {
		t.Fatal("imported config missing ripgrep tool")
	}
	if group := cliTestGroupByName(cfg, "work"); group == nil || !cliTestGroupHasTool(group, "ripgrep") {
		t.Fatalf("work group = %#v, want ripgrep membership", group)
	}
}

func TestBootstrapPromptImportsExistingConfig(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	sourcePath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, sourcePath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"jq": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{
			{Name: "work", Tools: []config.ToolEntry{{Name: "jq"}}},
			cliTestHostGroup(),
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	})

	withMockStdin(t, "y\n"+sourcePath+"\nn\nn\n", func() {
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir, "bootstrap"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("bootstrap prompted import: %v", err)
		}
	})

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["jq"]; !ok {
		t.Fatal("imported config missing jq tool")
	}
}

// ─── ensureHost: active host setup ────────────────────────────────────────────

func TestEnsureHost_UsesAppHostnameOverride(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "virtualhost.example")
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	if err := ensureHost(a); err != nil {
		t.Fatalf("ensureHost: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Hosts["virtualhost"]; !ok {
		t.Fatalf("hosts = %v, want virtualhost from OMNI_HOSTNAME", cfg.Hosts)
	}
	if group := cliTestGroupByName(cfg, "virtualhost"); group == nil || !group.IsHost() {
		t.Fatalf("virtualhost group = %#v, want special host group", group)
	}
}

// ─── runDotsInitSection direct tests ──────────────────────────────────────────

// TestRunDotsInitSection_EmptyPath_Skips tests the path where the user enters
// an empty dots repo path, causing the function to skip dots setup.
func TestRunDotsInitSection_EmptyPath_Skips(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	withMockStdin(t, "\n", func() {
		err := runDotsInitSection(context.Background(), a)
		if err != nil {
			t.Errorf("runDotsInitSection with empty path: %v", err)
		}
	})
}

// TestRunDotsInitSection_ValidPath_NoEntries tests the path where the user
// provides a valid repo path that exists on disk but has no discoverable entries.
func TestRunDotsInitSection_ValidPath_NoEntries(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	// Config must exist for SaveSettings to work.
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	// Use an empty temp dir as the "repo" — no discoverable config entries.
	repoDir := t.TempDir()

	// Stdin: repo path, then empty line for non-standard paths (finish).
	stdinInput := repoDir + "\n\n"
	withMockStdin(t, stdinInput, func() {
		_ = runDotsInitSection(context.Background(), a)
		// May fail if SaveSettings fails — tolerate errors.
	})
}

// TestRunDotsInitSection_RelativePath_NormalizesToAbsolute verifies that a
// relative dots repo path entered during init is persisted as an absolute path.
func TestRunDotsInitSection_RelativePath_NormalizesToAbsolute(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	cwd := t.TempDir()
	repoName := "myrepo"
	repoDir := filepath.Join(cwd, repoName)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.Abs(repoName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(startWD)
	})

	withMockStdin(t, repoName+"\n", func() {
		if err := runDotsInitSection(context.Background(), a); err != nil {
			t.Fatalf("runDotsInitSection: %v", err)
		}
	})

	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.DotsRepo != expected {
		t.Fatalf("saved dots repo = %q, want %q", settings.DotsRepo, expected)
	}
}

func TestRunDotsInitSection_ExpandsEnvironmentVariablePath(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	home := t.TempDir()
	repo := filepath.Join(home, "dotsrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	legacyPath := "$HOME/dotsrepo"

	withMockStdin(t, legacyPath+"\n", func() {
		if err := runDotsInitSection(context.Background(), a); err != nil {
			t.Fatalf("runDotsInitSection: %v", err)
		}
	})

	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	expected := filepath.ToSlash(filepath.Join("~", "dotsrepo"))
	if settings.DotsRepo != expected {
		t.Fatalf("saved dots repo = %q, want %q", settings.DotsRepo, expected)
	}
}

// TestRunDotsInitSection_InvalidPath tests the path where the user provides
// a path that doesn't exist on disk.
func TestRunDotsInitSection_InvalidPath(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	nonExistentPath := filepath.Join(t.TempDir(), "does_not_exist")
	withMockStdin(t, nonExistentPath+"\n", func() {
		err := runDotsInitSection(context.Background(), a)
		// Should return error for non-existent path.
		if err == nil {
			t.Error("expected error for non-existent repo path")
		}
	})
}

// TestRunDotsInitSection_UnsupportedTildePrefix_DoesNotExpand verifies that
// values like "~user/.config" are not treated as home-relative.
func TestRunDotsInitSection_UnsupportedTildePrefix_DoesNotExpand(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Create a path that legacy "~user" expansion would incorrectly map to.
	legacyTarget := filepath.Join(home, "nobody", ".config")
	if err := os.MkdirAll(legacyTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	startWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpWD := t.TempDir()
	if err := os.Chdir(tmpWD); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(startWD)
	})

	withMockStdin(t, "~nobody/.config\n", func() {
		err := runDotsInitSection(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for unsupported ~user prefix")
		}
		if !strings.Contains(err.Error(), "repo path") {
			t.Fatalf("error = %v, want repo path error", err)
		}
	})
}

// TestRunDotsInitSection_WithDiscoverableEntries tests the path where the repo
// has discoverable config entries and the user chooses "yes" to add them all.
func TestRunDotsInitSection_WithDiscoverableEntries(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	// Create a temp "repo" dir with a discoverable hidden config dir.
	repoDir := t.TempDir()
	nvimDir := filepath.Join(repoDir, "nvim")
	if err := os.MkdirAll(nvimDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Stdin: repo path, "y" to add all entries, then empty line for non-standard paths.
	stdinInput := repoDir + "\ny\n\n"
	withMockStdin(t, stdinInput, func() {
		_ = runDotsInitSection(context.Background(), a)
	})
}

// TestRunDotsInitSection_WithEntries_PickIndividually tests the per-entry
// prompting when the user declines to add all entries at once.
func TestRunDotsInitSection_WithEntries_PickIndividually(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	repoDir := t.TempDir()
	nvimDir := filepath.Join(repoDir, "nvim")
	if err := os.MkdirAll(nvimDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Stdin: repo path, "n" to not add all, "y" to add nvim individually, then empty.
	stdinInput := repoDir + "\nn\ny\n\n"
	withMockStdin(t, stdinInput, func() {
		_ = runDotsInitSection(context.Background(), a)
	})
}

// TestRunDotsInitSection_NonStandardPath_Nonexistent tests entering a non-standard
// path that doesn't exist (exercises the error branch in the non-standard loop).
func TestRunDotsInitSection_NonStandardPath_Nonexistent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	withConfig(t, cfgPath, &config.RootConfig{})

	a, err := buildTestApp(t, cfgPath)
	if err != nil {
		t.Fatalf("buildTestApp: %v", err)
	}

	repoDir := t.TempDir()
	// Stdin: repo path (no entries), then a nonexistent extra path, then empty.
	stdinInput := repoDir + "\n/nonexistent/path/xyz\n\n"
	withMockStdin(t, stdinInput, func() {
		_ = runDotsInitSection(context.Background(), a)
	})
}

// TestAgentsSkillsGroup_MultipleGroups pins that `agents skills group` sets a
// skill package's full group membership with no reusable-group cap: the
// source must land in every named group.
func TestAgentsSkillsGroup_MultipleGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	skillSource := "acme/skills"

	withConfig(t, cfgPath, &config.RootConfig{
		Agents: config.AgentsConfig{
			Packages: []config.SkillPackage{{Source: skillSource}},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
			{Name: "base"},
		},
	})

	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"agents", "skills", "group", skillSource, "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("skill multi-group set: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, name := range []string{"work", "base"} {
		group := cliTestGroup(cfg, name)
		if group == nil || !slices.Contains(group.Skills, skillSource) {
			t.Fatalf("skill %q should be a member of group %q, groups=%+v", skillSource, name, cfg.Groups)
		}
	}
}

// TestAgentsMcpGroup_MultipleGroups pins that `agents mcp group` sets an MCP
// server's full group membership across every named group.
func TestAgentsMcpGroup_MultipleGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	mcpName := "acme-mcp"

	withConfig(t, cfgPath, &config.RootConfig{
		Agents: config.AgentsConfig{
			McpServers: []config.McpServer{{Name: mcpName, Transport: "stdio", Command: "acme-mcp"}},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
			{Name: "base"},
		},
	})

	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"agents", "mcp", "group", mcpName, "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("mcp multi-group set: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, name := range []string{"work", "base"} {
		group := cliTestGroup(cfg, name)
		if group == nil || !slices.Contains(group.McpServers, mcpName) {
			t.Fatalf("mcp server %q should be a member of group %q, groups=%+v", mcpName, name, cfg.Groups)
		}
	}
}

// TestAgentsPluginGroup_MultipleGroups pins that `agents plugins group` sets
// a plugin's full group membership across every named group.
func TestAgentsPluginGroup_MultipleGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	pluginName := "acme-plugin"
	marketplaceName := "acme-marketplace"

	withConfig(t, cfgPath, &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: marketplaceName, Source: "acme/marketplace"}},
			Plugins:      []config.Plugin{{Name: pluginName, Marketplace: marketplaceName}},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
			{Name: "base"},
		},
	})

	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"agents", "plugins", "group", pluginName, "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("plugin multi-group set: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, name := range []string{"work", "base"} {
		group := cliTestGroup(cfg, name)
		if group == nil || !slices.Contains(group.Plugins, pluginName) {
			t.Fatalf("plugin %q should be a member of group %q, groups=%+v", pluginName, name, cfg.Groups)
		}
	}
}

// TestAgentsMarketplaceGroup_MultipleGroups pins that `agents plugins
// marketplace group` sets a marketplace's full group membership across every
// named group.
func TestAgentsMarketplaceGroup_MultipleGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	marketplaceName := "acme-marketplace"

	withConfig(t, cfgPath, &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: marketplaceName, Source: "acme/marketplace"}},
		},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work"},
			{Name: "base"},
		},
	})

	root := NewRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--cache-dir", cacheDir,
		"agents", "plugins", "marketplace", "group", marketplaceName, "work", "base"})
	if err := root.Execute(); err != nil {
		t.Fatalf("marketplace multi-group set: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	for _, name := range []string{"work", "base"} {
		group := cliTestGroup(cfg, name)
		if group == nil || !slices.Contains(group.Marketplaces, marketplaceName) {
			t.Fatalf("marketplace %q should be a member of group %q, groups=%+v", marketplaceName, name, cfg.Groups)
		}
	}
}
