package tui

// Tests for the do* Cmd closures and supporting helpers.
// These test the actual tea.Cmd closure body by invoking cmd() directly,
// which is the part missed by the integration-level drive() tests.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
	gosync "github.com/lkshrk/omni/internal/sync"
)

// ── stub providers ─────────────────────────────────────────────────────────────

// okProvider is a Provider whose Install/Uninstall/Upgrade all succeed.
type okProvider struct{ name string }

func (p *okProvider) Name() string                                       { return p.name }
func (p *okProvider) Description() string                                { return p.name + " stub" }
func (p *okProvider) Available(_ context.Context) (bool, error)          { return true, nil }
func (p *okProvider) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (p *okProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (p *okProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (p *okProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return true, "1.0.0", nil
}
func (p *okProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}
func (p *okProvider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	pkg := tool.EffectivePackage()
	switch p.name {
	case "brew":
		verb := "uninstall"
		switch action {
		case provider.PrivilegeActionInstall:
			verb = "install"
		case provider.PrivilegeActionUpgrade:
			verb = "upgrade"
		}
		args := []string{verb, "--cask"}
		if action == provider.PrivilegeActionInstall || action == provider.PrivilegeActionUpgrade {
			args = append(args, "--no-ask")
		}
		args = append(args, pkg)
		return "brew", args, true
	case "apt":
		switch action {
		case provider.PrivilegeActionInstall:
			return "apt-get", []string{"install", "-y", pkg}, true
		case provider.PrivilegeActionUpgrade:
			return "apt-get", []string{"install", "--only-upgrade", "-y", pkg}, true
		default:
			return "apt-get", []string{"remove", "-y", pkg}, true
		}
	default:
		return "", nil, false
	}
}

type searchOKProvider struct {
	okProvider
	results []provider.SearchResult
}

func (p *searchOKProvider) Search(_ context.Context, _ string) ([]provider.SearchResult, error) {
	return p.results, nil
}

type installRecordingProvider struct {
	okProvider
	installed []provider.Tool
}

func (p *installRecordingProvider) Install(_ context.Context, tool provider.Tool) error {
	p.installed = append(p.installed, tool)
	return nil
}

type privilegedOKProvider struct {
	okProvider
	plan provider.PrivilegePlan
}

func (p *privilegedOKProvider) PrivilegePlan(_ context.Context, _ provider.PrivilegeAction, _ provider.Tool) (provider.PrivilegePlan, error) {
	return p.plan, nil
}

func (p *privilegedOKProvider) PrivilegeCommand(action provider.PrivilegeAction, tool provider.Tool) (string, []string, bool) {
	return p.okProvider.PrivilegeCommand(action, tool)
}

type privilegedMissingProvider struct {
	privilegedOKProvider
}

func (p *privilegedMissingProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}

// errProvider is a Provider whose Install/Upgrade always fail.
type errProvider struct{ name string }

func (p *errProvider) Name() string                              { return p.name }
func (p *errProvider) Description() string                       { return p.name + " failing stub" }
func (p *errProvider) Available(_ context.Context) (bool, error) { return true, nil }
func (p *errProvider) Install(_ context.Context, _ provider.Tool) error {
	return errors.New("install failed")
}
func (p *errProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (p *errProvider) Upgrade(_ context.Context, _ provider.Tool) error {
	return errors.New("upgrade failed")
}
func (p *errProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil
}
func (p *errProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

// ── test App helpers ───────────────────────────────────────────────────────────

type tuiFixtureTool struct {
	Name string
	Spec config.ToolSpec
}

func tuiTool(name, providerName string) tuiFixtureTool {
	return tuiFixtureTool{Name: name, Spec: tuiToolSpec(providerName)}
}

func tuiToolSpec(providerName string) config.ToolSpec {
	return config.ToolSpec{
		Providers: []config.ToolInstallSpec{{Provider: tuiFixtureProvider(providerName)}},
	}
}

func tuiTestHostGroup(names ...string) *config.GroupConfig {
	group := &config.GroupConfig{Name: shortHostname(), Special: "host"}
	for _, name := range names {
		group.Tools = append(group.Tools, config.ToolEntry{Name: name})
	}
	return group
}

func tuiNamedHostGroup(name string, tools ...string) *config.GroupConfig {
	group := &config.GroupConfig{Name: name, Special: "host"}
	for _, tool := range tools {
		group.Tools = append(group.Tools, config.ToolEntry{Name: tool})
	}
	return group
}

// newCmdApp creates an App backed by a stub provider and a pre-existing settings.json.
func newCmdApp(t *testing.T, prov provider.Provider, tools []tuiFixtureTool) (*app.App, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")

	cfg := &config.RootConfig{
		Groups: []*config.GroupConfig{tuiTestHostGroup()},
	}
	if len(tools) > 0 {
		cfg.Tools = make(map[string]config.ToolSpec, len(tools))
		groupTools := make([]config.ToolEntry, 0, len(tools))
		for _, tool := range tools {
			cfg.Tools[tool.Name] = tool.Spec
			groupTools = append(groupTools, config.ToolEntry{Name: tool.Name})
		}
		cfg.Groups[0].Tools = groupTools
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Tools:  cfg.Tools,
		Groups: cfg.Groups,
	}); err != nil {
		t.Fatal(err)
	}

	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, cfgPath
}

func saveTUIConfig(t testing.TB, path string, cfg *config.RootConfig) error {
	t.Helper()
	normalizeTUITestRootConfig(cfg)
	return config.Save(path, cfg)
}

func normalizeTUITestRootConfig(cfg *config.RootConfig) {
	if cfg == nil {
		return
	}
	host := shortHostname()
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, ignored := range group.Ignore {
			if ignored != "" && !slices.Contains(cfg.Ignore.Tools, ignored) {
				cfg.Ignore.Tools = append(cfg.Ignore.Tools, ignored)
			}
		}
		if group.Name == host {
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

func tuiFixtureEcosystem(providerName string) string {
	switch providerName {
	case "brew", "apt", "apk", "dnf", "pacman", "zypper":
		return "system"
	case "pip", "pip3", "uv":
		return "python"
	case "npm", "pnpm", "bun":
		return "node"
	default:
		return ""
	}
}

func tuiFixtureProvider(providerName string) string {
	switch providerName {
	case "system":
		return "brew"
	case "node":
		return "npm"
	case "python", "pip3":
		return "pip"
	default:
		return providerName
	}
}

func findTestGroup(cfg *config.RootConfig, name string) *config.GroupConfig {
	for _, group := range cfg.Groups {
		if group.BaseName() == name {
			return group
		}
	}
	return nil
}

func containsToolMembership(tools []config.ToolEntry, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func containsDotMembership(dots []config.DotEntry, name string) bool {
	for _, dot := range dots {
		if dot.Name == name {
			return true
		}
	}
	return false
}

// newCmdAppNoConfig creates an App with no settings.json (setup wizard scenario).
func newCmdAppNoConfig(t *testing.T, prov provider.Provider) *app.App {
	t.Helper()
	dir := t.TempDir()
	a := app.New(filepath.Join(dir, "settings.json"))
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// modelWithApp builds a minimal Model wired to the given App.
func modelForCmds(a *app.App) Model {
	fi := textinput.New()
	return Model{
		app:           a,
		ctx:           context.Background(),
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        fi,
		mode:          viewList,
		upgradingKeys: make(map[string]bool),
	}
}

func cmdTestToolInConfig(cfg *config.RootConfig, name, providerName string) bool {
	for _, g := range cfg.Groups {
		for _, tool := range g.Tools {
			if tool.Name != name {
				continue
			}
			spec, ok := cfg.Tools[name]
			if !ok {
				return tool.Provider == providerName
			}
			if spec.Provider == providerName {
				return true
			}
		}
	}
	return false
}

// ── doInstall ─────────────────────────────────────────────────────────────────

func TestDoInstall_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	msg := m.doInstall("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.message == "" {
		t.Error("expected non-empty success message")
	}
}

func TestDoInstall_ConfiguredEmptyToolAddsHighConfidenceProviderMatch(t *testing.T) {
	prov := &searchOKProvider{
		okProvider: okProvider{name: "brew"},
		results: []provider.SearchResult{{
			Name:     "prettier",
			Provider: "brew",
		}},
	}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{tuiTestHostGroup("prettier")},
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}

	m := modelForCmds(a)
	msg := m.doInstall("prettier", "")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	providers := cfg.Tools["prettier"].Providers
	if len(providers) != 1 || providers[0].Provider != "brew" {
		t.Fatalf("providers = %+v, want high-confidence brew match saved", providers)
	}
	if len(got.tools) != 1 || got.tools[0].Provider != "brew" || !got.tools[0].Installed {
		t.Fatalf("tools = %+v, want installed brew row", got.tools)
	}
}

func TestDoInstall_ConfiguredEmptyToolDoesNotInstallWeakProviderMatch(t *testing.T) {
	prov := &searchOKProvider{
		okProvider: okProvider{name: "brew"},
		results: []provider.SearchResult{{
			Name:     "prettier-plugin-tailwindcss",
			Provider: "brew",
		}},
	}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{ProviderPriority: []string{"brew"}},
		Tools: map[string]config.ToolSpec{
			"prettier": {},
		},
		Groups: []*config.GroupConfig{tuiTestHostGroup("prettier")},
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}

	m := modelForCmds(a)
	msg := m.doInstall("prettier", "")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "no high-confidence provider match") {
		t.Fatalf("err = %v, want no high-confidence provider match", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if providers := cfg.Tools["prettier"].Providers; len(providers) != 0 {
		t.Fatalf("providers = %+v, want no weak match saved by TUI install", providers)
	}
	if len(got.tools) != 0 {
		t.Fatalf("tools = %+v, want no installed rows", got.tools)
	}
}

func TestHandleListActionInstallUsesSelectedProviderCandidate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Tools: map[string]config.ToolSpec{
			"prettier": {
				Providers: []config.ToolInstallSpec{
					{Provider: "npm", Package: "prettier"},
					{Provider: "brew", Package: "prettier"},
				},
			},
		},
		Groups: []*config.GroupConfig{tuiTestHostGroup("prettier")},
	}); err != nil {
		t.Fatalf("write config: %v", err)
	}
	npm := &installRecordingProvider{okProvider: okProvider{name: "npm"}}
	brew := &installRecordingProvider{okProvider: okProvider{name: "brew"}}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), npm, brew); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	m.allTools = []*database.ToolCache{{Name: "prettier", Provider: "", Installed: false, Tracked: true}}
	m.toolProviderCandidates = map[string][]config.ToolInstallSpec{
		"prettier": {
			{Provider: "npm", Package: "prettier"},
			{Provider: "brew", Package: "prettier"},
		},
	}
	m.providerCandidateCursor = 1
	m.applyFilter()

	cmds := m.handleListActionKeyMsg(pressRune('i').(tea.KeyPressMsg))
	if len(cmds) == 0 {
		t.Fatal("install action returned no commands")
	}
	done, ok := opCompleteFromCmd(cmds[len(cmds)-1])
	if !ok {
		t.Fatalf("expected opCompleteMsg from install command")
	}
	if done.err != nil {
		t.Fatalf("unexpected install error: %v", done.err)
	}
	if len(npm.installed) != 0 {
		t.Fatalf("npm installs = %+v, want none", npm.installed)
	}
	if len(brew.installed) != 1 || brew.installed[0].Name != "prettier" {
		t.Fatalf("brew installs = %+v, want prettier", brew.installed)
	}
}

func TestDoInstall_Error(t *testing.T) {
	prov := &errProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doInstall("curl", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error from failing provider")
	}
}

// ── doDelete ───────────────────────────────────────────────────────────────

func TestDoDelete_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doDelete("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if !slices.Contains(got.removeDiscoveredKeys, toolKey("ripgrep", "brew")) {
		t.Fatalf("removeDiscoveredKeys = %v, want ripgrep/brew", got.removeDiscoveredKeys)
	}
}

func TestDoDelete_DeletesConfigEntry(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doDelete("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cmdTestToolInConfig(cfg, "ripgrep", "brew") {
		t.Fatalf("ripgrep still present in config after TUI delete: %+v", cfg.Groups)
	}
}

func TestDoSaveFallbackEditor_PersistsStructuredRecipe(t *testing.T) {
	prov := &okProvider{name: "apt"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("rg", "apt")})
	m := modelForCmds(a)
	m.fallbackEditor = fallbackEditorState{
		fields: map[fallbackEditorFieldID]string{
			fallbackFieldRepo:           "git@github.com:BurntSushi/ripgrep.git",
			fallbackFieldBinary:         "rg",
			fallbackFieldBinDir:         "~/.local/share/omni/fallback/bin",
			fallbackFieldAssetPattern:   "ripgrep-{version}-{os}-{arch}.tar.gz",
			fallbackFieldInstallCommand: "install rg",
			fallbackFieldCheckCommand:   "test -x {{bin_dir}}/{{binary}}",
			fallbackFieldUninstall:      "rm -f {{bin_dir}}/{{binary}}",
			fallbackFieldUpgrade:        "upgrade rg",
			fallbackFieldVersion:        "{{binary}} --version",
			fallbackFieldReleaseChannel: "stable",
		},
	}

	msg := m.doSaveFallbackEditor("rg")()
	got, ok := msg.(fallbackSavedMsg)
	if !ok {
		t.Fatalf("expected fallbackSavedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doSaveFallbackEditor: %v", got.err)
	}
	if fallback := got.toolFallbacks["rg"]; fallback.Source.Owner != "BurntSushi" || fallback.Source.Repo != "ripgrep" {
		t.Fatalf("msg.toolFallbacks = %+v, want rg BurntSushi/ripgrep", got.toolFallbacks)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fallback := cfg.Tools["rg"].Fallback
	if fallback == nil {
		t.Fatal("fallback missing from config")
	}
	if fallback.Source.Owner != "BurntSushi" || fallback.Source.Repo != "ripgrep" || fallback.Source.URL != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("source = %+v, want normalized GitHub source", fallback.Source)
	}
	if fallback.Status != config.FallbackStatusUnverified {
		t.Fatalf("status = %q, want unverified", fallback.Status)
	}
	if fallback.Binary != "rg" || fallback.BinDir != "~/.local/share/omni/fallback/bin" || fallback.ReleaseChannel != "stable" {
		t.Fatalf("fallback identity = %+v, want binary/bin dir/release channel", fallback)
	}
	if fallback.Recipe.Type != config.FallbackRecipeGitHubReleaseAsset || fallback.Recipe.AssetPattern != "ripgrep-{version}-{os}-{arch}.tar.gz" {
		t.Fatalf("recipe = %+v, want github release asset pattern", fallback.Recipe)
	}
	if fallback.Commands.Install != "install rg" ||
		fallback.Commands.Check != "test -x {{bin_dir}}/{{binary}}" ||
		fallback.Commands.Uninstall != "rm -f {{bin_dir}}/{{binary}}" ||
		fallback.Commands.Upgrade != "upgrade rg" ||
		fallback.Commands.Version != "{{binary}} --version" {
		t.Fatalf("commands = %+v, want structured editor commands", fallback.Commands)
	}
}

func TestDoSaveFallbackEditor_InvalidRepoReturnsError(t *testing.T) {
	prov := &okProvider{name: "apt"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("rg", "apt")})
	m := modelForCmds(a)
	m.fallbackEditor = fallbackEditorState{
		fields: map[fallbackEditorFieldID]string{
			fallbackFieldRepo:           "not-a-repo",
			fallbackFieldBinary:         "rg",
			fallbackFieldInstallCommand: "install rg",
			fallbackFieldCheckCommand:   "command -v rg",
		},
	}

	msg := m.doSaveFallbackEditor("rg")()
	got, ok := msg.(fallbackSavedMsg)
	if !ok {
		t.Fatalf("expected fallbackSavedMsg, got %T", msg)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "github repo must be owner/repo") {
		t.Fatalf("doSaveFallbackEditor err = %v, want invalid repo", got.err)
	}
	if len(got.toolFallbacks) != 0 {
		t.Fatalf("toolFallbacks = %+v, want none on invalid repo", got.toolFallbacks)
	}
}

func TestDoSaveFallbackEditor_InvalidTemplateReturnsError(t *testing.T) {
	prov := &okProvider{name: "apt"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("rg", "apt")})
	m := modelForCmds(a)
	m.fallbackEditor = fallbackEditorState{
		fields: map[fallbackEditorFieldID]string{
			fallbackFieldRepo:           "BurntSushi/ripgrep",
			fallbackFieldBinary:         "rg",
			fallbackFieldInstallCommand: "install {{missing}}",
			fallbackFieldCheckCommand:   "command -v {{binary}}",
		},
	}

	msg := m.doSaveFallbackEditor("rg")()
	got, ok := msg.(fallbackSavedMsg)
	if !ok {
		t.Fatalf("expected fallbackSavedMsg, got %T", msg)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), `unknown fallback template variable "missing"`) {
		t.Fatalf("doSaveFallbackEditor err = %v, want unknown template variable", got.err)
	}
	if len(got.toolFallbacks) != 0 {
		t.Fatalf("toolFallbacks = %+v, want none on invalid template", got.toolFallbacks)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if fallback := cfg.Tools["rg"].Fallback; fallback != nil {
		t.Fatalf("fallback = %+v, want no invalid template saved", fallback)
	}
}

func TestDoDelete_RefreshesToolMembershipState(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	key := toolKey("ripgrep", "brew")
	m.toolGroups = map[string]string{key: "base"}
	m.toolMemberships = map[string][]string{key: {"base"}}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	msg := m.doDelete("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if _, ok := got.toolGroups[key]; ok {
		t.Fatalf("deleted tool still present in refreshed toolGroups: %v", got.toolGroups)
	}
	if _, ok := got.toolMemberships[key]; ok {
		t.Fatalf("deleted tool still present in refreshed toolMemberships: %v", got.toolMemberships)
	}
	if len(got.tools) != 0 {
		t.Fatalf("deleted tool still present in refreshed tools: %+v", got.tools)
	}

	m.handleOpCompleteMsg(got)
	if len(m.visibleTools) != 0 {
		t.Fatalf("deleted tool should disappear from visible tools, got %+v", m.visibleTools)
	}
	if _, ok := m.toolGroups[key]; ok {
		t.Fatalf("stale tool group survived opComplete handling: %v", m.toolGroups)
	}
	if _, ok := m.toolMemberships[key]; ok {
		t.Fatalf("stale tool membership survived opComplete handling: %v", m.toolMemberships)
	}
}

func TestHandleOpCompleteMsg_DeleteRemovesDiscoveredOrphan(t *testing.T) {
	m := modelForCmds(nil)
	key := toolKey("swiftlint", "system")
	swiftformat := &database.ToolCache{Name: "swiftformat", Provider: "system", Package: "swiftformat", Installed: true, Tracked: true}
	orphan := &database.ToolCache{Name: "swiftlint", Provider: "system", Package: "swiftlint", Installed: true, Tracked: false}
	m.allTools = []*database.ToolCache{swiftformat}
	m.discoveredTools = []*database.ToolCache{orphan}
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	if got := m.countSection(sectionOutOfSync); got != 1 {
		t.Fatalf("precondition out-of-sync count = %d, want 1", got)
	}

	m.handleOpCompleteMsg(opCompleteMsg{
		message:              "deleted swiftlint",
		tools:                []*database.ToolCache{swiftformat},
		removeDiscoveredKeys: []string{key},
	})
	if len(m.discoveredTools) != 0 {
		t.Fatalf("deleted orphan should leave discovered list: %+v", m.discoveredTools)
	}
	if got := m.countSection(sectionOutOfSync); got != 0 {
		t.Fatalf("out-of-sync count after delete = %d, want 0", got)
	}
	if len(m.visibleTools) != 1 || m.visibleTools[0].Name != "swiftformat" {
		t.Fatalf("visible tools after delete = %+v, want only swiftformat", m.visibleTools)
	}
}

func TestDoDeleteFromConfig_DeletesMissingConfigEntry(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Installed: false,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	msg := m.doDeleteFromConfig("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cmdTestToolInConfig(cfg, "ripgrep", "brew") {
		t.Fatalf("ripgrep still present in config after delete-from-config: %+v", cfg.Groups)
	}
}

func TestDoDeleteFromConfig_RefreshesToolMembershipState(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	key := toolKey("ripgrep", "brew")
	m.toolGroups = map[string]string{key: "base"}
	m.toolMemberships = map[string][]string{key: {"base"}}
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Installed: false,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	msg := m.doDeleteFromConfig("ripgrep", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	m.handleOpCompleteMsg(got)
	if _, ok := m.toolGroups[key]; ok {
		t.Fatalf("stale tool group survived config delete: %v", m.toolGroups)
	}
	if _, ok := m.toolMemberships[key]; ok {
		t.Fatalf("stale tool membership survived config delete: %v", m.toolMemberships)
	}
}

// ── doUpgrade ─────────────────────────────────────────────────────────────────

func TestDoUpgrade_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doUpgrade("git", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

func TestDoUpgrade_Error(t *testing.T) {
	prov := &errProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doUpgrade("git", "brew")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error from failing provider")
	}
}

// ── doSyncWithProgress ────────────────────────────────────────────────────────

func TestDoSyncWithProgress_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	msg := m.doSyncWithProgress(ch, 1)()
	_, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
}

func TestDoSyncAllWithProgress_ClaimsDiscoveredToHostnameGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	discovered := []*database.ToolCache{
		{Name: "fzf", Provider: "brew", Installed: true, Tracked: false},
	}
	ch := make(chan progressUpdate, 16)
	msg := m.doSyncAllWithProgress(ch, 1, discovered)()
	got, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if len(got.claimedNames) != 1 || got.claimedNames[0] != "fzf" {
		t.Fatalf("claimedNames = %v, want [fzf]", got.claimedNames)
	}
	if got.message != "sync complete — 0 installed, 1 added to config" {
		t.Errorf("message = %q", got.message)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	var found bool
	for _, g := range cfg.Groups {
		if g.Name == "testhost" && len(g.Tools) == 1 && g.Tools[0].Name == "fzf" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected fzf claimed into testhost group, groups=%+v", cfg.Groups)
	}
}

func TestDoSyncAllWithProgress_PrivilegedInstallOpensAdminTerminal(t *testing.T) {
	prov := &privilegedMissingProvider{
		privilegedOKProvider: privilegedOKProvider{
			okProvider: okProvider{name: "apt"},
			plan:       provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "apt install vim"},
		},
	}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("vim", "apt")})
	m := modelForCmds(a)
	m.mode = viewList
	m.allTools = []*database.ToolCache{
		{Name: "vim", Provider: "apt", Package: "vim", Tracked: true},
	}
	m.applyFilter()
	ch, gen := m.beginProgressStream()

	msg := m.doSyncAllWithProgress(ch, gen, nil)()
	done, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("unexpected error: %v", done.err)
	}
	if done.promptPrivilegedActions[toolKey("vim", "apt")] != provider.PrivilegeActionInstall {
		t.Fatalf("promptPrivilegedActions = %#v, want vim install prompt", done.promptPrivilegedActions)
	}

	got := drive(m, done)
	if got.mode != viewAdminTerminal || got.adminTerminal == nil {
		t.Fatalf("mode=%v adminTerminal=%v, want admin terminal prompt", got.mode, got.adminTerminal != nil)
	}
	if got.adminTerminal.name != "vim" || got.adminTerminal.providerName != "apt" {
		t.Fatalf("admin terminal target = %s/%s, want vim/apt", got.adminTerminal.providerName, got.adminTerminal.name)
	}
	if got.adminTerminal.display != expectedInteractiveAdminDisplay("apt-get install -y vim") {
		t.Fatalf("display command = %q", got.adminTerminal.display)
	}
	if got.statusMsg != "" || got.statusIsErr {
		t.Fatalf("status=%q err=%v, want prompt to own status", got.statusMsg, got.statusIsErr)
	}
	if got.rowErrors[toolKey("vim", "apt")] != "admin approval required to install" {
		t.Fatalf("rowErrors = %#v, want admin approval row error retained", got.rowErrors)
	}
}

func TestQueuedPrivilegedAdminTerminalContinuesAfterSuccess(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "bat", Provider: "apt", Package: "bat"},
	})
	m.mode = viewList
	m.adminTerminalQueue = []adminTerminalState{
		{
			action:                 provider.PrivilegeActionInstall,
			name:                   "vim",
			providerName:           "apt",
			pkg:                    "vim",
			command:                "sudo",
			args:                   []string{"apt-get", "install", "-y", "vim"},
			display:                "sudo apt-get install -y vim",
			returnMode:             viewList,
			rowKey:                 toolKey("vim", "apt"),
			preserveOtherRowErrors: true,
		},
	}
	m.rowErrors = map[string]string{
		toolKey("bat", "apt"): "requires sudo: apt install bat",
		toolKey("vim", "apt"): "requires sudo: apt install vim",
	}

	got := drive(m, opCompleteMsg{
		key:                    toolKey("bat", "apt"),
		message:                "installed bat",
		preserveOtherRowErrors: true,
	})
	if got.mode != viewAdminTerminal || got.adminTerminal == nil {
		t.Fatalf("mode=%v adminTerminal=%v, want next admin terminal prompt", got.mode, got.adminTerminal != nil)
	}
	if got.adminTerminal.name != "vim" {
		t.Fatalf("next admin terminal target = %q, want vim", got.adminTerminal.name)
	}
	if _, ok := got.rowErrors[toolKey("bat", "apt")]; ok {
		t.Fatalf("bat row error should be cleared after success, rowErrors=%#v", got.rowErrors)
	}
	if got.rowErrors[toolKey("vim", "apt")] == "" {
		t.Fatalf("vim row error should remain until its prompt finishes, rowErrors=%#v", got.rowErrors)
	}
}

// ── doUpgradeAll ──────────────────────────────────────────────────────────────

func TestDoUpgradeAll_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	msg := m.doUpgradeAll(ch, 1)()
	got, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

func TestDoUpgradeAll_ProgressShowsCurrentTool(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	tools := []*database.ToolCache{
		{Name: "bat", Provider: "brew", Package: "bat", Installed: true, Outdated: true, Tracked: true},
		{Name: "ripgrep", Provider: "brew", Package: "ripgrep", Installed: true, Outdated: true, Tracked: true},
	}
	for _, tool := range tools {
		if err := a.DB().Upsert(context.Background(), tool); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
	}
	m := modelForCmds(a)
	m.allTools = tools
	ch := make(chan progressUpdate, 16)

	msg := m.doUpgradeAll(ch, 1)()
	if _, ok := msg.(progressDoneMsg); !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	var updates []progressUpdate
	var texts []string
	for update := range ch {
		updates = append(updates, update)
		texts = append(texts, update.text)
	}
	if !slices.Contains(texts, "Upgrading tools 1/2: bat…") {
		t.Fatalf("progress texts = %v, want current bat status", texts)
	}
	if !slices.Contains(texts, "Upgrading tools 2/2: ripgrep…") {
		t.Fatalf("progress texts = %v, want current ripgrep status", texts)
	}
	if !slices.Contains(texts, "Upgrading tools 1/2: bat upgraded") {
		t.Fatalf("progress texts = %v, want bat done status", texts)
	}
	for _, update := range updates {
		if update.rowStatus == "Upgrading tools 1/2: bat…" {
			t.Fatalf("row status should stay row-local, got %q", update.rowStatus)
		}
	}
}

func TestDoUpgradeAll_PrivilegedUpgradesOpenAdminTerminalQueue(t *testing.T) {
	prov := &privilegedOKProvider{
		okProvider: okProvider{name: "apt"},
		plan:       provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "apt upgrade package"},
	}
	a, _ := newCmdApp(t, prov, nil)
	tools := []*database.ToolCache{
		{Name: "bat", Provider: "apt", Package: "bat", Installed: true, Outdated: true, Tracked: true},
		{Name: "vim", Provider: "apt", Package: "vim", Installed: true, Outdated: true, Tracked: true},
	}
	for _, tool := range tools {
		if err := a.DB().Upsert(context.Background(), tool); err != nil {
			t.Fatalf("seed cache: %v", err)
		}
	}
	m := modelForCmds(a)
	m.mode = viewList
	m.allTools = tools
	m.applyFilter()
	ch, gen := m.beginProgressStream()

	msg := m.doUpgradeAll(ch, gen)()
	done, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("unexpected error: %v", done.err)
	}
	for _, tool := range tools {
		key := toolKey(tool.Name, tool.Provider)
		if done.promptPrivilegedActions[key] != provider.PrivilegeActionUpgrade {
			t.Fatalf("promptPrivilegedActions[%q] = %q, want upgrade; all actions=%#v", key, done.promptPrivilegedActions[key], done.promptPrivilegedActions)
		}
	}

	got := drive(m, done)
	if got.mode != viewAdminTerminal || got.adminTerminal == nil {
		t.Fatalf("mode=%v adminTerminal=%v, want admin terminal prompt", got.mode, got.adminTerminal != nil)
	}
	if got.adminTerminal.action != provider.PrivilegeActionUpgrade {
		t.Fatalf("admin action = %q, want upgrade", got.adminTerminal.action)
	}
	if got.adminTerminal.queueTotal != 2 || len(got.adminTerminalQueue) != 1 {
		t.Fatalf("queue total=%d remaining=%d, want first prompt plus one queued", got.adminTerminal.queueTotal, len(got.adminTerminalQueue))
	}
	queuedNames := map[string]bool{got.adminTerminal.name: true, got.adminTerminalQueue[0].name: true}
	if !queuedNames["bat"] || !queuedNames["vim"] {
		t.Fatalf("queued admin targets = %#v, want bat and vim", queuedNames)
	}
	for _, tool := range tools {
		key := toolKey(tool.Name, tool.Provider)
		if got.rowErrors[key] != "admin approval required to upgrade" {
			t.Fatalf("rowErrors[%q] = %q, want admin approval to upgrade; all errors=%#v", key, got.rowErrors[key], got.rowErrors)
		}
	}
}

func TestUpgradeAllProgressText_DeduplicatesBulkVerb(t *testing.T) {
	tool := provider.Tool{Name: "bat", Provider: "brew"}
	tests := []struct {
		name    string
		message string
		err     error
		want    string
	}{
		{name: "started", message: "Upgrading bat…", want: "Upgrading tools 1/2: bat…"},
		{name: "done", message: "Upgraded bat", want: "Upgrading tools 1/2: bat upgraded"},
		{name: "skipped", message: "Skipped upgrading bat", want: "Upgrading tools 1/2: bat skipped"},
		{name: "admin needed", message: "Admin approval needed for bat", err: errors.New("requires sudo: apt upgrade bat"), want: "Upgrading tools 1/2: bat needs admin approval"},
		{name: "failed", message: "Failed upgrading bat", want: "Upgrading tools 1/2: bat failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := app.UpgradeAllProgressText(gosync.ProgressEvent{Tool: tool, Message: tt.message, Err: tt.err}, 1, 2)
			if got != tt.want {
				t.Fatalf("upgradeAllProgressText = %q, want %q", got, tt.want)
			}
			if strings.Contains(strings.ToLower(got), "upgrading tools 1/2: upgrading") {
				t.Fatalf("progress text repeats the bulk verb: %q", got)
			}
		})
	}
}

func TestDoUpgradeAll_FailureReturnsRowErrorAndContinues(t *testing.T) {
	prov := &errProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	if err := a.DB().Upsert(context.Background(), &database.ToolCache{
		Name:      "ripgrep",
		Provider:  "brew",
		Package:   "ripgrep",
		Installed: true,
		Outdated:  true,
		Tracked:   true,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	m := modelForCmds(a)
	ch := make(chan progressUpdate, 16)
	msg := m.doUpgradeAll(ch, 1)()
	got, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("bulk row failure should not fail entire TUI operation, got %v", got.err)
	}
	if got.rowErrors[toolKey("ripgrep", "brew")] != "upgrade failed" {
		t.Fatalf("rowErrors = %#v, want ripgrep upgrade failure", got.rowErrors)
	}
}

// ── doCreateConfig ────────────────────────────────────────────────────────────

func TestDoCreateConfig_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a := newCmdAppNoConfig(t, prov)
	m := modelForCmds(a)
	msg := m.doCreateConfig()()
	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("expected toolsLoadedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	// Regression: setupProviders must be populated so the setup wizard step 1
	// (provider picker) has rows to display. Previously doCreateConfig returned
	// without calling AllAvailableManagers/ResolvedEcosystemProviders, leaving
	// setupProviders nil and showing 0 providers in the wizard.
	if len(got.setupProviders) == 0 {
		t.Error("setupProviders must not be empty after doCreateConfig — step 1 needs provider rows")
	}
}

func TestDoCreateConfig_ExistingConfig_Noop(t *testing.T) {
	prov := &okProvider{name: "brew"}
	// Config already exists → CreateEmptyConfig is a no-op, still succeeds.
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doCreateConfig()()
	_, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("expected toolsLoadedMsg, got %T", msg)
	}
}

func TestDoImportConfigFile_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a := newCmdAppNoConfig(t, prov)
	m := modelForCmds(a)
	sourcePath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(sourcePath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Provider: "system"},
		},
		Groups: []*config.GroupConfig{{
			Name:  "work",
			Tools: []config.ToolEntry{{Name: "ripgrep"}},
		}},
	}); err != nil {
		t.Fatalf("save source config: %v", err)
	}

	msg := m.doImportConfigFile(sourcePath)()
	got, ok := msg.(setupConfigImportDoneMsg)
	if !ok {
		t.Fatalf("expected setupConfigImportDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("doImportConfigFile: %v", got.err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, ok := cfg.Tools["ripgrep"]; !ok {
		t.Fatal("imported config missing ripgrep")
	}
}

func TestHandleSetupConfigImportDoneMsg_SuccessReloadsTools(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	cmds := m.handleSetupConfigImportDoneMsg(setupConfigImportDoneMsg{path: "settings.json"})

	if m.statusMsg != "✓ imported settings" || m.statusIsErr {
		t.Fatalf("status=%q err=%v, want imported settings success", m.statusMsg, m.statusIsErr)
	}
	if !m.loading {
		t.Fatal("loading = false, want true while imported config reloads tools")
	}
	if len(cmds) != 3 {
		t.Fatalf("cmd count = %d, want status, spinner, and loadTools", len(cmds))
	}
	msg := cmds[len(cmds)-1]()
	got, ok := msg.(toolsLoadedMsg)
	if !ok {
		t.Fatalf("reload command returned %T, want toolsLoadedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("reload command err = %v", got.err)
	}
}

func TestHandleSetupConfigImportDoneMsg_ErrorStopsLoading(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a := newCmdAppNoConfig(t, prov)
	m := modelForCmds(a)
	m.loading = true

	cmds := m.handleSetupConfigImportDoneMsg(setupConfigImportDoneMsg{err: errors.New("bad import")})

	if m.loading {
		t.Fatal("loading = true, want false after import error")
	}
	if m.statusMsg != "✗ bad import" || !m.statusIsErr {
		t.Fatalf("status=%q err=%v, want import error status", m.statusMsg, m.statusIsErr)
	}
	if len(cmds) != 1 {
		t.Fatalf("cmd count = %d, want status clear command only", len(cmds))
	}
}

// ── doSetupImport ─────────────────────────────────────────────────────────────

func TestDoSetupImport_Success(t *testing.T) {
	// Use a provider that returns real tools from ListInstalled so that the
	// import step actually processes entries and got.added > 0.
	prov := &listableProvider{
		name: "brew",
		tools: []provider.InstalledTool{
			{Tool: provider.Tool{Name: "ripgrep"}, Version: "14.1.1"},
			{Tool: provider.Tool{Name: "jq"}, Version: "1.7"},
		},
	}
	a, cfgPath := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doSetupImport(nil)()
	got, ok := msg.(setupImportDoneMsg)
	if !ok {
		t.Fatalf("expected setupImportDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.added == 0 {
		t.Errorf("expected added > 0 when provider has installed tools, got 0")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findTestGroup(cfg, shortHostname())
	if group == nil || !group.IsHost() {
		t.Fatalf("setup import group = %#v, want protected host group", group)
	}
	hostMsg := m.doSetupHost(shortHostname())()
	hostDone, ok := hostMsg.(setupHostDoneMsg)
	if !ok {
		t.Fatalf("expected setupHostDoneMsg, got %T", hostMsg)
	}
	if hostDone.err != nil {
		t.Fatalf("doSetupHost after import: %v", hostDone.err)
	}
}

// ── doSetToolGroupMembership ─────────────────────────────────────────────────

func TestDoSetToolGroupMembership_AddSuccess(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	msg := m.doSetToolGroupMembership("ripgrep", "work", true)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.detail == "" {
		t.Error("expected non-empty success message")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := findTestGroup(cfg, "work")
	if work == nil || !containsToolMembership(work.Tools, "ripgrep") {
		t.Fatalf("work group missing ripgrep membership: %+v", work)
	}
	host := findTestGroup(cfg, shortHostname())
	if host != nil && containsToolMembership(host.Tools, "ripgrep") {
		t.Fatalf("host group membership should be moved out: %+v", host)
	}
}

func TestDoSetToolGroupMembership_RemoveSuccess(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	msg := m.doSetToolGroupMembership("ripgrep", shortHostname(), false)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	host := findTestGroup(cfg, shortHostname())
	if host != nil && containsToolMembership(host.Tools, "ripgrep") {
		t.Fatalf("host group still has ripgrep membership: %+v", host.Tools)
	}
	if _, ok := cfg.Tools["ripgrep"]; !ok {
		t.Fatal("removing membership deleted logical tool spec")
	}
}

func TestDoSetToolGroupMembership_Error(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doSetToolGroupMembership("notexist", "work", true)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for missing logical tool")
	}
}

func TestDoSetToolGroupMemberships_CreatedGroupJoinsHost(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	host := shortHostname()
	msg := m.doSetToolGroupMemberships("ripgrep", []string{host}, []string{"work"}, []string{"work"}, host)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	work := findTestGroup(cfg, "work")
	if work == nil || !containsToolMembership(work.Tools, "ripgrep") {
		t.Fatalf("work group missing ripgrep membership: %+v", work)
	}
	groups, ok := cfg.Hosts[host]
	if !ok {
		t.Fatalf("host %s was not created: %+v", host, cfg.Hosts)
	}
	if !slices.Contains(groups, "work") {
		t.Fatalf("host %s groups = %v, want work", host, groups)
	}
}

func TestDoSetToolGroupMemberships_ExistingGroupJoinsHost(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	if err := a.CreateGroup("work"); err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	m := modelForCmds(a)
	host := shortHostname()
	msg := m.doSetToolGroupMemberships("ripgrep", []string{host}, []string{"work"}, nil, host)()
	got, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("expected groupChangedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	key := toolKey("ripgrep", "brew")
	if got.toolGroups[key] != "work" {
		t.Fatalf("toolGroups[%q] = %q, want work", key, got.toolGroups[key])
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if groups := cfg.Hosts[host]; !slices.Contains(groups, "work") {
		t.Fatalf("host %s groups = %v, want work", host, groups)
	}

	rowModel := baseModel([]*database.ToolCache{{
		Name:     "ripgrep",
		Provider: "brew",
		Tracked:  true,
	}})
	rowModel.toolGroups = got.toolGroups
	rowModel.groupNames = []string{"work"}
	rowModel.hostInfo = &app.HostInfo{
		Active: host,
		Hosts:  map[string]config.HostAssignment{host: {Groups: []string{"work"}}},
	}
	rowModel.applyFilter()
	out := renderList(rowModel)
	if !strings.Contains(out, "[work]") {
		t.Fatalf("missing tool row should render group badge:\n%s", out)
	}
}

func TestDoSetDotGroupMemberships_ExistingGroupJoinsHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop.local")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	sourceDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "init.lua"), []byte("-- config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(homeDir, ".config", "nvim")
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sourceDir, targetDir); err != nil {
		t.Fatal(err)
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{
				Name:    "laptop",
				Special: "host",
				Dots: []config.DotEntry{
					{Name: "nvim", Path: "~/.config/nvim"},
				},
			},
			{Name: "work"},
		},
		Hosts: map[string][]string{"laptop": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	m.settings = config.Settings{DotsRepo: repoDir}
	m.hostInfo, _ = a.HostStatus()
	m.beginDotsOperation("Updating groups...")
	msg := m.doSetDotGroupMemberships("nvim", []string{"laptop"}, []string{"work"}, nil, "laptop")()
	got, ok := msg.(dotsLoadedMsg)
	if !ok {
		t.Fatalf("expected dotsLoadedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	hostGroup := findTestGroup(cfg, "laptop")
	if hostGroup == nil {
		t.Fatalf("host group missing: %#v", cfg.Groups)
	}
	if containsDotMembership(hostGroup.Dots, "nvim") {
		t.Fatalf("host group still has nvim membership: %#v", hostGroup.Dots)
	}
	work := findTestGroup(cfg, "work")
	if work == nil || !containsDotMembership(work.Dots, "nvim") {
		t.Fatalf("work group missing nvim membership: %+v", work)
	}
	if groups := cfg.Hosts["laptop"]; !slices.Contains(groups, "work") {
		t.Fatalf("host laptop groups = %v, want work", groups)
	}
	if memberships := got.dotMemberships["nvim"]; !slices.Equal(memberships, []string{"work"}) {
		t.Fatalf("dot memberships = %v, want [work]", memberships)
	}
	if len(got.entries) != 1 {
		t.Fatalf("refreshed entries = %#v, want exactly nvim", got.entries)
	}
	if got.entries[0].Name != "nvim" || got.entries[0].Group != "work" || got.entries[0].State != app.DotStateSynced {
		t.Fatalf("refreshed entry = %#v, want synced nvim in work", got.entries[0])
	}
}

// ── doSaveSettingsChange ──────────────────────────────────────────────────────

func TestDoSaveSettingsChange_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doSaveSettingsChange(app.ToggleSettingBool("auto_import"))()
	got, ok := msg.(settingsSavedMsg)
	if !ok {
		t.Fatalf("expected settingsSavedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Settings.AutoImport {
		t.Fatal("auto_import should be toggled on")
	}
}

func TestDoSaveSettingsChange_QueuesPendingChanges(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "savequeuetest")
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		Groups:   []*config.GroupConfig{tuiTestHostGroup()},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	m := modelForCmds(a)

	first := m.doSaveSettingsChange(app.ToggleSettingBool("dots_git.auto_push"))
	if first == nil {
		t.Fatal("first save should start immediately")
	}
	second := m.doSaveSettingsChange(app.SetSettingValue("provider_priority", "pnpm,npm,uv,pip,brew"))
	if second != nil {
		t.Fatal("second save should queue while first save is running")
	}
	if !m.settingsSaveQueued {
		t.Fatal("pending settings change should be queued")
	}

	firstMsg, ok := first().(settingsSavedMsg)
	if !ok {
		t.Fatalf("first save returned %T, want settingsSavedMsg", firstMsg)
	}
	cmds := m.handleSettingsSavedMsg(firstMsg)
	if m.statusMsg != "" {
		t.Fatalf("intermediate save should not publish stale status, got %q", m.statusMsg)
	}
	if len(cmds) != 1 {
		t.Fatalf("first completion produced %d commands, want queued save", len(cmds))
	}

	queuedMsg, ok := cmds[0]().(settingsSavedMsg)
	if !ok {
		t.Fatalf("queued save returned %T, want settingsSavedMsg", queuedMsg)
	}
	m.handleSettingsSavedMsg(queuedMsg)
	if m.settingsSaveRunning || m.settingsSaveQueued {
		t.Fatalf("save queue still active: running=%v queued=%v", m.settingsSaveRunning, m.settingsSaveQueued)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Settings.AutoImport {
		t.Fatal("auto_import should be preserved from current config")
	}
	if !cfg.Settings.DotsGit.AutoPush {
		t.Fatal("dots_git.auto_push should be persisted by first change")
	}
	settings, err := a.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if got := app.EffectiveEcosystemManager(settings, provider.EcosystemNode); got != "pnpm" {
		t.Fatalf("node manager = %q, want queued change pnpm", got)
	}
}

func TestBlockPrivilegedToolAction_GenericPrivilegeOpensAdminTerminal(t *testing.T) {
	prov := &privilegedOKProvider{
		okProvider: okProvider{name: "apt"},
		plan:       provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "apt install vim"},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	tool := &database.ToolCache{Name: "vim", Provider: "apt", Package: "vim"}

	if !m.blockPrivilegedToolAction(tool, provider.PrivilegeActionInstall) {
		t.Fatal("generic privileged action should open the admin terminal")
	}
	if m.statusMsg != "" || m.statusIsErr || len(m.rowErrors) != 0 {
		t.Fatalf("status=%q err=%v rowErrors=%v, want clean prompt state", m.statusMsg, m.statusIsErr, m.rowErrors)
	}
	if m.mode != viewAdminTerminal || m.adminTerminal == nil {
		t.Fatalf("mode=%v adminTerminal=%v, want admin terminal prompt", m.mode, m.adminTerminal != nil)
	}
	if got := m.adminTerminal.display; got != expectedInteractiveAdminDisplay("apt-get install -y vim") {
		t.Fatalf("display command = %q", got)
	}
	cached, err := a.DB().Get(context.Background(), "vim", "apt", "vim")
	if err != nil {
		t.Fatalf("Get cached row: %v", err)
	}
	if cached.Privilege != string(provider.PrivilegeRequired) {
		t.Fatalf("Privilege = %q, want %q", cached.Privilege, provider.PrivilegeRequired)
	}
}

func TestBlockPrivilegedToolAction_RejectsProviderToolUninstall(t *testing.T) {
	prov := &privilegedOKProvider{
		okProvider: okProvider{name: "node"},
		plan:       provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "npm uninstall npm"},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	tool := &database.ToolCache{
		Name:          "npm",
		Provider:      "node",
		Package:       "npm",
		Installed:     true,
		InstalledWith: "npm",
	}

	if !m.blockPrivilegedToolAction(tool, provider.PrivilegeActionUninstall) {
		t.Fatal("protected provider uninstall should pause the normal TUI operation")
	}
	if m.mode == viewAdminTerminal || m.adminTerminal != nil {
		t.Fatalf("admin terminal opened for protected provider uninstall: mode=%v terminal=%v", m.mode, m.adminTerminal != nil)
	}
	if got := m.rowErrors[toolKey("npm", "node")]; !strings.Contains(got, "package manager/provider") {
		t.Fatalf("row error = %q, want protected provider tool error", got)
	}
}

func expectedInteractiveAdminDisplay(direct string) string {
	if os.Geteuid() == 0 {
		return direct
	}
	return "sudo " + direct
}

func TestBlockPrivilegedToolAction_OpensAdminTerminalForInteractiveBrewCask(t *testing.T) {
	prov := &privilegedOKProvider{
		okProvider: okProvider{name: "brew"},
		plan: provider.PrivilegePlan{
			Requirement: provider.PrivilegeMaybe,
			Reason:      "brew cask parsec uses pkgutil uninstall",
		},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	tool := &database.ToolCache{
		Name:          "parsec",
		Provider:      "system",
		Package:       "parsec",
		Installed:     true,
		InstalledWith: "brew",
	}

	if !m.blockPrivilegedToolAction(tool, provider.PrivilegeActionUninstall) {
		t.Fatal("interactive brew cask action should pause the normal TUI operation")
	}
	if m.mode != viewAdminTerminal {
		t.Fatalf("mode = %v, want viewAdminTerminal", m.mode)
	}
	if m.adminTerminal == nil {
		t.Fatal("admin terminal prompt was not opened")
	}
	if got := m.adminTerminal.display; got != "brew uninstall --cask parsec" {
		t.Fatalf("display command = %q, want brew uninstall --cask parsec", got)
	}
	if m.adminTerminal.providerName != "system" || m.adminTerminal.installedWith != "brew" {
		t.Fatalf("admin provider state = %q/%q, want system/brew", m.adminTerminal.providerName, m.adminTerminal.installedWith)
	}
	if m.statusMsg != "" || len(m.rowErrors) != 0 {
		t.Fatalf("status=%q rowErrors=%v, want clean prompt state", m.statusMsg, m.rowErrors)
	}
}

func TestBlockPrivilegedToolAction_RefreshesGenericCachedBrewPrivilegeIntoAdminPrompt(t *testing.T) {
	prov := &privilegedOKProvider{
		okProvider: okProvider{name: "brew"},
		plan: provider.PrivilegePlan{
			Requirement: provider.PrivilegeMaybe,
			Reason:      "brew cask parsec uses pkgutil uninstall",
		},
	}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	tool := &database.ToolCache{
		Name:            "parsec",
		Provider:        "system",
		Package:         "parsec",
		Installed:       true,
		InstalledWith:   "brew",
		Privilege:       string(provider.PrivilegeRequired),
		PrivilegeReason: sql.NullString{String: "package manager needs sudo/root access", Valid: true},
	}

	if !m.blockPrivilegedToolAction(tool, provider.PrivilegeActionUninstall) {
		t.Fatal("generic cached brew privilege should be refreshed into an admin terminal prompt")
	}
	if m.adminTerminal == nil {
		t.Fatal("admin terminal prompt was not opened")
	}
	if !strings.Contains(m.adminTerminal.reason, "pkgutil uninstall") {
		t.Fatalf("admin reason = %q, want refreshed cask-specific reason", m.adminTerminal.reason)
	}
	if got := m.adminTerminal.display; got != "brew uninstall --cask parsec" {
		t.Fatalf("display command = %q, want brew uninstall --cask parsec", got)
	}
}

func TestSettingsRowActionsPersistExpectedConfigFields(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "settingsrowtest")
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	for _, row := range []int{
		settingsRowAutoImport,
		settingsRowDotsCommit,
		settingsRowDotsPush,
	} {
		m.settingsCursor = row
		var cmds []tea.Cmd
		m.handleSettingsRowAction(&cmds)
		if len(cmds) != 1 {
			t.Fatalf("row %d produced %d save commands, want 1", row, len(cmds))
		}
		msg, ok := cmds[0]().(settingsSavedMsg)
		if !ok {
			t.Fatalf("row %d save command returned %T, want settingsSavedMsg", row, msg)
		}
		if msg.err != nil {
			t.Fatalf("row %d save command failed: %v", row, msg.err)
		}
		m.handleSettingsSavedMsg(msg)
	}

	m.settingsCursor = settingsRowProviderPriority
	m.startSettingsPriorityEdit()
	m.priorityDraft = []string{"brew"}
	cmds := m.handleSettingsPriorityKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(cmds) != 1 {
		t.Fatalf("row %d priority save produced %d save commands, want 1", settingsRowProviderPriority, len(cmds))
	}
	msg, ok := cmds[0]().(settingsSavedMsg)
	if !ok {
		t.Fatalf("row %d priority save command returned %T, want settingsSavedMsg", settingsRowProviderPriority, msg)
	}
	if msg.err != nil {
		t.Fatalf("row %d priority save command failed: %v", settingsRowProviderPriority, msg.err)
	}
	m.handleSettingsSavedMsg(msg)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Settings.AutoImport {
		t.Fatal("row 0 should persist settings.auto_import=true")
	}
	if !cfg.Settings.DotsGit.AutoCommit {
		t.Fatalf("row %d should persist settings.dots_git.auto_commit=true", settingsRowDotsCommit)
	}
	if !cfg.Settings.DotsGit.AutoPush {
		t.Fatalf("row %d should persist settings.dots_git.auto_push=true", settingsRowDotsPush)
	}

	// Priority editor save: SetProviderLayout(["brew"], []) should persist
	// provider_priority = ["brew"] in the host settings.
	host := cfg.HostSettings["settingsrowtest"]
	if got := host.ProviderPriority; len(got) != 1 || got[0] != "brew" {
		t.Fatalf("priority save should persist host_settings.settingsrowtest.provider_priority = [brew], got %v", got)
	}
}

func TestSettingsRowActionDoesNotOverwriteConfigWithStaleModel(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "stalesettingstest")
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		Groups:   []*config.GroupConfig{tuiTestHostGroup()},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	m := modelForCmds(a)
	m.settings = config.Settings{} // stale — AutoImport is false in model but true on disk
	m.settingsCursor = settingsRowAutoImport

	var cmds []tea.Cmd
	m.handleSettingsRowAction(&cmds)
	if len(cmds) != 1 {
		t.Fatalf("auto-import row produced %d save commands, want 1", len(cmds))
	}
	msg, ok := cmds[0]().(settingsSavedMsg)
	if !ok {
		t.Fatalf("save command returned %T, want settingsSavedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("save command failed: %v", msg.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	// The toggle read from disk (AutoImport=true) and flipped to false — the
	// live config value must have been used, not the stale in-model zero value.
	if cfg.Settings.AutoImport {
		t.Fatal("settings.auto_import should have been toggled off from live config value")
	}
}

// ── doConsolidate ─────────────────────────────────────────────────────────────

func TestDoConsolidate_Success(t *testing.T) {
	prov := &okProvider{name: "node"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	// "node"/"bun" is a valid (ecosystem, manager) pair.
	msg := m.doConsolidate("node", "bun")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

func TestDoConsolidate_UnknownEcosystem(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doConsolidate("winget", "choco")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for unknown ecosystem")
	}
}

// ── waitForProgress ───────────────────────────────────────────────────────────

func TestWaitForProgress_ClosedChannel(t *testing.T) {
	ch := make(chan progressUpdate)
	close(ch)
	cmd := waitForProgress(ch, 7)
	msg := cmd()
	if got, ok := msg.(progressStreamClosedMsg); !ok {
		t.Errorf("waitForProgress on closed channel = %v (%T), want progressStreamClosedMsg", msg, msg)
	} else if got.gen != 7 {
		t.Errorf("progressStreamClosed gen = %d, want 7", got.gen)
	}
}

func TestWaitForProgress_ReceivesText(t *testing.T) {
	ch := make(chan progressUpdate, 1)
	ch <- progressUpdate{gen: 7, text: "installing…"}
	cmd := waitForProgress(ch, 7)
	msg := cmd()
	got, ok := msg.(progressMsg)
	if !ok {
		t.Fatalf("expected progressMsg, got %T", msg)
	}
	if got.text != "installing…" {
		t.Errorf("text = %q, want 'installing…'", got.text)
	}
}

func TestToolProgressUpdate_ContextCanceledCompletesWithoutRowError(t *testing.T) {
	got := toolProgressUpdate(3, gosync.ProgressEvent{
		Tool: provider.Tool{
			Name:     "ripgrep",
			Provider: "brew",
		},
		Message: "Cancelled installing ripgrep",
		Err:     context.Canceled,
		Done:    true,
	})

	if got.rowKey != toolKey("ripgrep", "brew") {
		t.Fatalf("rowKey = %q, want ripgrep/brew key", got.rowKey)
	}
	if !got.rowDone {
		t.Fatal("rowDone = false, want true")
	}
	if got.rowErr != "" {
		t.Fatalf("rowErr = %q, want empty for cancellation", got.rowErr)
	}
}

func TestSyncAllProgressText_AddAndInstallOnly(t *testing.T) {
	addText := app.SyncAllToolProgressText(gosync.ProgressEvent{
		Tool:    provider.Tool{Name: "fzf", Provider: "brew"},
		Message: "Adding fzf to config…",
	}, 1, 2)
	if addText != "Syncing tools 1/2: adding discovered fzf to config…" {
		t.Fatalf("add progress text = %q", addText)
	}

	installText := app.SyncAllToolProgressText(gosync.ProgressEvent{
		Tool:    provider.Tool{Name: "bat", Provider: "brew"},
		Message: "Installing bat…",
	}, 2, 2)
	if installText != "Syncing tools 2/2: installing missing bat…" {
		t.Fatalf("install progress text = %q", installText)
	}
	combined := strings.ToLower(addText + " " + installText)
	if strings.Contains(combined, "upgrad") {
		t.Fatalf("sync-all progress should not imply upgrades, got %q", combined)
	}
}

// ── displaySection ────────────────────────────────────────────────────────────

func TestDisplaySection_IgnoredTool(t *testing.T) {
	tc := &database.ToolCache{Name: "curl", Provider: "brew", Installed: true}
	m := baseModel([]*database.ToolCache{tc})
	m.ignoreSet = map[string]bool{"curl": true}
	if s := m.displaySection(tc); s != sectionIgnored {
		t.Errorf("displaySection (ignored) = %v, want sectionIgnored", s)
	}
}

func TestDisplaySection_NormalTool(t *testing.T) {
	tc := &database.ToolCache{Name: "curl", Provider: "brew", Installed: true}
	m := baseModel([]*database.ToolCache{tc})
	m.ignoreSet = map[string]bool{} // not in ignore list
	if s := m.displaySection(tc); s == sectionIgnored {
		t.Errorf("displaySection (not ignored) = sectionIgnored, want sectionInstalled")
	}
}

// ── additional error / branch coverage ────────────────────────────────────────

// installableProvider reports tools as not-yet-installed but installs them successfully.
// Used to trigger the "sync complete — N installed" branch in doSyncWithProgress.
type installableProvider struct{ name string }

func (p *installableProvider) Name() string                                       { return p.name }
func (p *installableProvider) Description() string                                { return p.name + " installable stub" }
func (p *installableProvider) Available(_ context.Context) (bool, error)          { return true, nil }
func (p *installableProvider) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (p *installableProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (p *installableProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (p *installableProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return false, "", nil // not installed → sync will Install
}
func (p *installableProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

// listableProvider is a Provider whose ListInstalled returns a fixed list of tools.
// Used to verify that doSetupImport imports tools when a real provider is present.
type listableProvider struct {
	name  string
	tools []provider.InstalledTool
}

func (p *listableProvider) Name() string                                       { return p.name }
func (p *listableProvider) Description() string                                { return p.name + " listable stub" }
func (p *listableProvider) Available(_ context.Context) (bool, error)          { return true, nil }
func (p *listableProvider) Install(_ context.Context, _ provider.Tool) error   { return nil }
func (p *listableProvider) Uninstall(_ context.Context, _ provider.Tool) error { return nil }
func (p *listableProvider) Upgrade(_ context.Context, _ provider.Tool) error   { return nil }
func (p *listableProvider) IsInstalled(_ context.Context, _ provider.Tool) (bool, string, error) {
	return true, "1.0.0", nil
}
func (p *listableProvider) ListInstalled(_ context.Context) ([]provider.InstalledTool, error) {
	return p.tools, nil
}

func TestDoDelete_Error(t *testing.T) {
	// Use a provider name that is not registered → Uninstall returns an error.
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doDelete("curl", "unknown-provider")()
	got, ok := msg.(opCompleteMsg)
	if !ok {
		t.Fatalf("expected opCompleteMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for unregistered provider")
	}
}

func TestDoSyncWithProgress_Installed(t *testing.T) {
	// installableProvider reports IsInstalled=false so sync actually installs tools.
	prov := &installableProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)
	ch := make(chan progressUpdate, 16)
	m.progressCh = ch
	msg := m.doSyncWithProgress(ch, 1)()
	got, ok := msg.(progressDoneMsg)
	if !ok {
		t.Fatalf("expected progressDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.message == "" {
		t.Error("expected non-empty message from doSyncWithProgress")
	}
}

func TestBuildAllGroupNames_PutsHostBeforeNamedGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	got := buildAllGroupNames([]string{"work", "apps", "personal"})
	want := []string{"host", "apps", "personal", "work"}
	if len(got) != len(want) {
		t.Fatalf("buildAllGroupNames len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildAllGroupNames[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// ── dots helpers ──────────────────────────────────────────────────────────────

// newDotsModelForCmds creates an App with settings.json pointing at a temp dir
// as the dots repo. Returns a Model wired to that App plus the repo dir path.
func newDotsModelForCmds(t *testing.T) (Model, string) {
	t.Helper()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	m.setSettings(config.Settings{DotsRepo: repoDir})
	return m, repoDir
}

// ── doLoadDots ────────────────────────────────────────────────────────────────

func TestDoLoadDots_NoRepo(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil) // no dots_repo configured in settings.json
	m := modelForCmds(a)
	msg := m.doLoadDots()()
	got, ok := msg.(dotsLoadedMsg)
	if !ok {
		t.Fatalf("expected dotsLoadedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDoLoadDots_Success(t *testing.T) {
	m, _ := newDotsModelForCmds(t)
	msg := m.doLoadDots()()
	got, ok := msg.(dotsLoadedMsg)
	if !ok {
		t.Fatalf("expected dotsLoadedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

func TestDoRefreshDotsHistory_ReadsRecentAppHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "historyhost")
	ctx := context.Background()
	cfgDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := filepath.Join(homeDir, "dotfiles")
	sourceFile := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("-- seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	runGit("config", "commit.gpgsign", "false")
	runGit("config", "tag.gpgsign", "false")
	runGit("add", "dotfiles")
	runGit("commit", "-m", "seed")
	if err := os.WriteFile(sourceFile, []byte("-- changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    shortHostname(),
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
		Hosts: map[string][]string{shortHostname(): {}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(ctx); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err := a.DotsCommit(ctx, "dots: tui history"); err != nil {
		t.Fatalf("DotsCommit: %v", err)
	}

	m := modelForCmds(a)
	m.width = 100
	m.height = 24
	m.setSettings(config.Settings{DotsRepo: repoDir})
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK, State: app.DotStateSynced}}
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	msg := m.doRefreshDotsHistory()()
	got, ok := msg.(dotsHistoryLoadedMsg)
	if !ok {
		t.Fatalf("expected dotsHistoryLoadedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected history error: %v", got.err)
	}
	if len(got.entries) != 1 || got.entries[0].Operation != "commit" || got.entries[0].Status != "success" {
		t.Fatalf("history entries = %+v, want successful commit entry", got.entries)
	}

	updated := drive(m, got)
	out := renderDots(updated)
	for _, want := range []string{"History", "commit: success", "commit completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered dots history missing %q:\n%s", want, out)
		}
	}
}

// ── doDotsSyncOnly ────────────────────────────────────────────────────────────

func TestDoDotsSyncOnly_NoRepo(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doDotsSyncOnly()()
	got, ok := msg.(dotsSyncedMsg)
	if !ok {
		t.Fatalf("expected dotsSyncedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDoDotsSyncOnly_Success(t *testing.T) {
	m, _ := newDotsModelForCmds(t)
	msg := m.doDotsSyncOnly()()
	got, ok := msg.(dotsSyncedMsg)
	if !ok {
		t.Fatalf("expected dotsSyncedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
}

func TestDoSaveDotsRepoAndSync_SavesRepoBeforeSync(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "dotspickertest")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	oldRepo := t.TempDir()
	newRepo := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	targetDir := filepath.Join(homeDir, ".config", "picked")
	target := filepath.Join(targetDir, "settings.json")
	source := filepath.Join(newRepo, "dotfiles", "picked", ".config", "picked", "settings.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("selected repo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{AutoImport: true},
		HostSettings: map[string]config.Settings{
			"dotspickertest": {DotsRepo: oldRepo},
		},
		Groups: []*config.GroupConfig{{Name: shortHostname(), Special: "host", Dots: []config.DotEntry{{Name: "picked", Path: "~/.config/picked"}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	m := modelForCmds(a)
	m.settings = config.Settings{DotsRepo: newRepo, DotsDisabled: config.BoolPtr(false)}
	m.beginDotsOperation("Syncing dots…")

	msg := m.doSaveDotsRepoAndSync(newRepo)()
	got, ok := msg.(dotsSyncedMsg)
	if !ok {
		t.Fatalf("expected dotsSyncedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("sync after save failed: %v", got.err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target was not populated from selected repo: %v", err)
	}
	if string(content) != "selected repo" {
		t.Fatalf("target content = %q, want selected repo content", string(content))
	}
	info, err := os.Lstat(targetDir)
	if err != nil {
		t.Fatalf("Lstat(targetDir): %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("target dir mode = %v, want real directory", info.Mode())
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(target): %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatalf("EvalSymlinks(source): %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("target file resolves to %q, want selected repo source file %q", resolved, wantResolved)
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

func TestDoDotsAdd_UsesMachineGroupWhenUnfiltered(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "laptop.local")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	if err := os.MkdirAll(filepath.Join(homeDir, ".config", "zed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".config", "zed", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups:   []*config.GroupConfig{tuiNamedHostGroup("laptop")},
		Hosts:    map[string][]string{"laptop": {}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	m.settings = config.Settings{DotsRepo: repoDir}
	m.hostInfo, _ = a.HostStatus()
	m.beginDotsOperation("Adding zed...")
	path := filepath.Join(homeDir, ".config", "zed")
	msg := m.doDotsAdd(path, "~/.config/zed", "laptop")()
	got, ok := msg.(dotsAddedMsg)
	if !ok {
		t.Fatalf("doDotsAdd returned %T, want dotsAddedMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("doDotsAdd error: %v", got.err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	group := findTUITestGroup(cfg.Groups, "laptop")
	if group == nil {
		t.Fatalf("machine group laptop not created: %#v", cfg.Groups)
	}
	if len(group.Dots) != 1 || group.Dots[0].Name != "zed" {
		t.Fatalf("machine group dots = %#v, want zed", group.Dots)
	}
	if len(got.entries) == 0 || got.entries[0].Group != "laptop" {
		t.Fatalf("refreshed entries = %#v, want laptop entry visible", got.entries)
	}
}

func TestDoDotsVariantChange_AddsAndRemovesCurrentHostVariant(t *testing.T) {
	if _, err := exec.LookPath("stow"); err != nil {
		t.Skip("stow not installed")
	}
	t.Setenv("OMNI_HOSTNAME", "laptop.local")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	defaultDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "init.lua"), []byte("default"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homeDir, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(homeDir, ".config", "nvim")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    "laptop",
			Special: "host",
			Dots: []config.DotEntry{{
				Name: "nvim",
				Path: targetDir,
			}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doDotsVariantChange(dotsVariantRequest{name: "nvim"})()
	added, ok := msg.(dotsVariantChangedMsg)
	if !ok {
		t.Fatalf("doDotsVariantChange(add) returned %T, want dotsVariantChangedMsg", msg)
	}
	if added.err != nil {
		t.Fatalf("add variant failed: %v", added.err)
	}
	if added.info.Package != "nvim@laptop" {
		t.Fatalf("added package = %q, want nvim@laptop", added.info.Package)
	}
	variantDir := filepath.Join(repoDir, "dotfiles", "nvim@laptop", ".config", "nvim")
	if data, err := os.ReadFile(filepath.Join(variantDir, "init.lua")); err != nil || string(data) != "default" {
		t.Fatalf("variant seed file = %q, %v; want default content", string(data), err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after add: %v", err)
	}
	dot := cfg.Groups[0].Dots[0]
	if dot.Hosts["laptop"].Package != "nvim@laptop" {
		t.Fatalf("dot host variant = %#v, want nvim@laptop", dot.Hosts)
	}
	targetFile := filepath.Join(targetDir, "init.lua")
	resolved, err := filepath.EvalSymlinks(targetFile)
	if err != nil {
		t.Fatalf("EvalSymlinks target file after add: %v", err)
	}
	wantVariant, err := filepath.EvalSymlinks(filepath.Join(variantDir, "init.lua"))
	if err != nil {
		t.Fatalf("EvalSymlinks variant source file: %v", err)
	}
	if resolved != wantVariant {
		t.Fatalf("target file resolves to %q, want active variant %q", resolved, wantVariant)
	}

	msg = m.doDotsVariantChange(dotsVariantRequest{name: "nvim", remove: true})()
	removed, ok := msg.(dotsVariantChangedMsg)
	if !ok {
		t.Fatalf("doDotsVariantChange(remove) returned %T, want dotsVariantChangedMsg", msg)
	}
	if removed.err != nil {
		t.Fatalf("remove variant failed: %v", removed.err)
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load after remove: %v", err)
	}
	if hosts := cfg.Groups[0].Dots[0].Hosts; len(hosts) != 0 {
		t.Fatalf("dot host variants after remove = %#v, want none", hosts)
	}
	resolved, err = filepath.EvalSymlinks(targetFile)
	if err != nil {
		t.Fatalf("EvalSymlinks target file after remove: %v", err)
	}
	wantDefault, err := filepath.EvalSymlinks(filepath.Join(defaultDir, "init.lua"))
	if err != nil {
		t.Fatalf("EvalSymlinks default source file: %v", err)
	}
	if resolved != wantDefault {
		t.Fatalf("target file resolves to %q, want default package %q", resolved, wantDefault)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "dotfiles", "nvim@laptop")); !os.IsNotExist(err) {
		t.Fatalf("variant package after remove error = %v, want missing", err)
	}
}

func TestHandleDotsVariantKeyMsg_UsesAppActiveHostVariant(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop.local")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()

	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    "laptop",
			Special: "host",
			Dots: []config.DotEntry{
				{
					Name: "nvim",
					Path: "~/.config/nvim",
					Hosts: map[string]config.DotVariant{
						"laptop": {Package: "nvim@laptop"},
					},
				},
				{Name: "tmux", Path: "~/.tmux.conf"},
			},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	visible := []dotsVisibleRow{
		{entry: app.DotStatus{Name: "nvim", State: app.DotStateSynced}},
		{entry: app.DotStatus{Name: "tmux", State: app.DotStateSynced}},
	}

	m.dotsCursor = 0
	cmds := m.handleDotsVariantKeyMsg(visible)
	if len(cmds) == 0 || m.dotsVariantIdx != 0 || m.dotsVariantMode != dotsVariantRemove {
		t.Fatalf("active host variant mode = idx %d mode %d cmds %d, want remove prompt at idx 0", m.dotsVariantIdx, m.dotsVariantMode, len(cmds))
	}

	m.dotsCursor = 1
	cmds = m.handleDotsVariantKeyMsg(visible)
	if len(cmds) == 0 || m.dotsVariantIdx != 1 || m.dotsVariantMode != dotsVariantCreate {
		t.Fatalf("default variant mode = idx %d mode %d cmds %d, want create prompt at idx 1", m.dotsVariantIdx, m.dotsVariantMode, len(cmds))
	}
}

func TestHandleToolsLoadedMsg_DotsRepoStartsSyncAll(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	cmds := m.handleToolsLoadedMsg(toolsLoadedMsg{settings: config.Settings{DotsRepo: repoDir}, stowInstalled: true})
	if !m.dotsPreparing {
		t.Fatal("dotsPreparing should start after initial tools load when dots repo is configured")
	}
	if !m.dotsLoading {
		t.Fatal("dotsLoading should start after initial tools load when dots repo is configured")
	}
	var sawSnapshot, sawSync bool
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		switch cmd().(type) {
		case dotsPreparedMsg:
			sawSnapshot = true
		case dotsSyncedMsg:
			sawSync = true
		}
	}
	if !sawSnapshot {
		t.Fatalf("startup should dispatch dots snapshot command, got %d commands without dotsPreparedMsg", len(cmds))
	}
	if !sawSync {
		t.Fatalf("startup should dispatch dots sync-all command, got %d commands without dotsSyncedMsg", len(cmds))
	}
}

func TestHandleToolsLoadedMsg_UsesCachedDotsSyncConfiguredForLaunchSync(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	if err := saveTUIConfig(t, m.app.ConfigPath, &config.RootConfig{
		Settings: config.Settings{
			DotsRepo:     repoDir,
			DotsDisabled: config.BoolPtr(true),
		},
	}); err != nil {
		t.Fatalf("config.Save disabled dots: %v", err)
	}

	m.handleToolsLoadedMsg(toolsLoadedMsg{
		settings:            config.Settings{DotsRepo: repoDir},
		stowInstalled:       true,
		dotsConfigured:      true,
		dotsConfiguredKnown: true,
		dotsSyncAvail:       app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: repoDir},
		dotsSyncAvailKnown:  true,
	})
	if m.dotsLoading {
		t.Fatal("launch dots sync should use cached dots availability instead of stale message settings")
	}
}

func TestHandleSetupKeyMsg_UsesCachedDotsSyncConfiguredForBootstrapDots(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	if err := saveTUIConfig(t, m.app.ConfigPath, &config.RootConfig{
		Settings: config.Settings{
			DotsRepo:     repoDir,
			DotsDisabled: config.BoolPtr(true),
		},
	}); err != nil {
		t.Fatalf("config.Save disabled dots: %v", err)
	}
	m.mode = viewSetup
	m.setupStep = 10
	m.setupActivationIdx = 2
	m.settings = config.Settings{DotsRepo: repoDir}
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: repoDir})
	m.stowInstalled = true

	_, quit := m.handleSetupKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	if quit {
		t.Fatal("bootstrap dots activation should not quit")
	}
	if !m.statusIsErr || !strings.Contains(m.statusMsg, "dotfile sync is not configured") {
		t.Fatalf("status = %q err=%v, want cached dots disabled error", m.statusMsg, m.statusIsErr)
	}
	if m.loading {
		t.Fatal("bootstrap dots sync should not start when cached dots availability reports disabled")
	}
}

func TestStartDashboardRefresh_UsesCachedDotsSyncConfigured(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	if err := saveTUIConfig(t, m.app.ConfigPath, &config.RootConfig{
		Settings: config.Settings{
			DotsRepo:     repoDir,
			DotsDisabled: config.BoolPtr(true),
		},
	}); err != nil {
		t.Fatalf("config.Save disabled dots: %v", err)
	}
	m.settings = config.Settings{DotsRepo: repoDir}
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: repoDir})

	var cmds []tea.Cmd
	m.startDashboardRefresh(&cmds)
	if m.dotsLoading || m.dotsPreparing {
		t.Fatalf("dashboard refresh should use cached dots availability, dotsLoading=%v dotsPreparing=%v", m.dotsLoading, m.dotsPreparing)
	}
}

func TestPrepareDotsSnapshotOnLaunch_UsesCachedDotsConfigured(t *testing.T) {
	m, _ := newDotsModelForCmds(t)
	m.settings = config.Settings{}

	var cmds []tea.Cmd
	m.prepareDotsSnapshotOnLaunch(&cmds)
	if !m.dotsPreparing || len(cmds) == 0 {
		t.Fatalf("launch dots snapshot should use cached dots configured state, preparing=%v cmds=%d", m.dotsPreparing, len(cmds))
	}
}

func TestPrepareDotsSnapshotOnLaunch_SkipsWhenCachedDotsUnconfigured(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	m.settings = config.Settings{DotsRepo: "/stale/dotfiles"}

	var cmds []tea.Cmd
	m.prepareDotsSnapshotOnLaunch(&cmds)
	if m.dotsPreparing || len(cmds) != 0 {
		t.Fatalf("launch dots snapshot should use cached dots configured state, preparing=%v cmds=%d", m.dotsPreparing, len(cmds))
	}
}

func TestStartDashboardDotsSync_UsesCachedDotsSyncAvailability(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	m.settings = config.Settings{
		DotsRepo:     repoDir,
		DotsDisabled: config.BoolPtr(true),
	}

	var cmds []tea.Cmd
	m.startDashboardDotsSync(&cmds)
	if !m.dotsLoading {
		t.Fatal("dashboard dots sync should use cached availability instead of stale local disabled setting")
	}
}

func TestStartDashboardDotsSync_BlocksWhenCachedDotsSyncDisabled(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	if err := saveTUIConfig(t, m.app.ConfigPath, &config.RootConfig{
		Settings: config.Settings{
			DotsRepo:     repoDir,
			DotsDisabled: config.BoolPtr(true),
		},
	}); err != nil {
		t.Fatalf("config.Save disabled dots: %v", err)
	}
	m.settings = config.Settings{DotsRepo: repoDir}
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: repoDir})

	var cmds []tea.Cmd
	m.startDashboardDotsSync(&cmds)
	if m.dotsLoading {
		t.Fatal("dashboard dots sync should not start when cached availability reports disabled")
	}
	if !m.statusIsErr || !strings.Contains(m.statusMsg, "disabled") {
		t.Fatalf("status = %q err=%v, want disabled error", m.statusMsg, m.statusIsErr)
	}
}

func TestStartDashboardDotsCommit_UsesCachedDotsSyncAvailability(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	m.settings = config.Settings{
		DotsRepo:     repoDir,
		DotsDisabled: config.BoolPtr(true),
	}
	m.dotsGitStatus = "M file"

	var cmds []tea.Cmd
	m.startDashboardDotsCommit(&cmds)
	if !m.dotsLoading {
		t.Fatal("dashboard dots commit should use cached availability instead of stale local disabled setting")
	}
}

func TestStartDashboardDotsCommit_BlocksWhenCachedDotsSyncDisabled(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	if err := saveTUIConfig(t, m.app.ConfigPath, &config.RootConfig{
		Settings: config.Settings{
			DotsRepo:     repoDir,
			DotsDisabled: config.BoolPtr(true),
		},
	}); err != nil {
		t.Fatalf("config.Save disabled dots: %v", err)
	}
	m.settings = config.Settings{DotsRepo: repoDir}
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: repoDir})
	m.dotsGitStatus = "M file"

	var cmds []tea.Cmd
	m.startDashboardDotsCommit(&cmds)
	if m.dotsLoading {
		t.Fatal("dashboard dots commit should not start when cached availability reports disabled")
	}
	if !m.statusIsErr || !strings.Contains(m.statusMsg, "disabled") {
		t.Fatalf("status = %q err=%v, want disabled error", m.statusMsg, m.statusIsErr)
	}
}

func TestHandleToolsLoadedMsg_LaunchDotsSyncDoesNotRecordUnchangedHistory(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	cmds := m.handleToolsLoadedMsg(toolsLoadedMsg{settings: config.Settings{DotsRepo: repoDir}, stowInstalled: true})
	sawSync := false
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		msg, ok := cmd().(dotsSyncedMsg)
		if !ok {
			continue
		}
		sawSync = true
		if msg.err != nil {
			t.Fatalf("launch dots sync err = %v", msg.err)
		}
	}
	if !sawSync {
		t.Fatalf("startup should dispatch dots sync-all command, got %d commands without dotsSyncedMsg", len(cmds))
	}
	history, err := m.app.RecentDotsHistory(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentDotsHistory: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("unchanged launch sync should not record dots history, got %+v", history)
	}
}

func TestHandleToolsLoadedMsg_SkipsLaunchDotsSyncAfterBootstrapDotsSync(t *testing.T) {
	m, repoDir := newDotsModelForCmds(t)
	cmds := m.handleSetupBootstrapDoneMsg(setupBootstrapDoneMsg{action: "sync-dots", message: "dotfiles applied"})
	if !m.skipLaunchDotsSyncOnce {
		t.Fatal("sync-dots bootstrap should skip the next launch dots sync")
	}
	if len(cmds) == 0 {
		t.Fatal("sync-dots bootstrap should start the post-bootstrap reload")
	}

	cmds = m.handleToolsLoadedMsg(toolsLoadedMsg{settings: config.Settings{DotsRepo: repoDir}, stowInstalled: true})
	if m.skipLaunchDotsSyncOnce {
		t.Fatal("launch dots sync skip should be one-shot")
	}
	if !m.dotsPreparing {
		t.Fatal("dotsPreparing should still start so status is hydrated")
	}
	if m.dotsLoading {
		t.Fatal("dotsLoading should stay false because bootstrap already synced dots")
	}
	var sawSnapshot, sawSync bool
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		switch cmd().(type) {
		case dotsPreparedMsg:
			sawSnapshot = true
		case dotsSyncedMsg:
			sawSync = true
		}
	}
	if !sawSnapshot {
		t.Fatalf("post-bootstrap reload should still dispatch dots snapshot, got %d commands without dotsPreparedMsg", len(cmds))
	}
	if sawSync {
		t.Fatalf("post-bootstrap reload should not repeat dots sync after sync-dots bootstrap")
	}
}

func TestHandleToolsLoadedMsg_DotsRepoPromptsForStowBeforeScans(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m, repoDir := newDotsModelForCmds(t)
	cmds := m.handleToolsLoadedMsg(toolsLoadedMsg{
		settings: config.Settings{DotsRepo: repoDir},
	})
	if !m.stowInstallPrompt {
		t.Fatal("stowInstallPrompt should open when dots sync is enabled and stow is missing")
	}
	if m.dotsLoading {
		t.Fatal("dots sync should wait for stow install prompt")
	}
	if len(m.scanningProviders) != 0 {
		t.Fatalf("provider scans should wait until stow prompt resolves, got %v", m.scanningProviders)
	}
	if !m.dotsPreparing {
		t.Fatal("dots snapshot should still prepare while stow prompt is open")
	}
	var sawSnapshot bool
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		if _, ok := cmd().(dotsPreparedMsg); ok {
			sawSnapshot = true
		}
	}
	if !sawSnapshot {
		t.Fatalf("startup should dispatch only dots snapshot before stow prompt resolves, got %d commands without dotsPreparedMsg", len(cmds))
	}
}

// ── doDotsPull ────────────────────────────────────────────────────────────────

func TestDoDotsPull_NoRepo(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doDotsPull()()
	got, ok := msg.(dotsPulledMsg)
	if !ok {
		t.Fatalf("expected dotsPulledMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDoDotsPull_NonGitDir(t *testing.T) {
	// repoDir is a plain temp dir (not a git repo) → git pull fails.
	m, _ := newDotsModelForCmds(t)
	msg := m.doDotsPull()()
	got, ok := msg.(dotsPulledMsg)
	if !ok {
		t.Fatalf("expected dotsPulledMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error pulling from non-git directory")
	}
}

// ── doDotsPush ────────────────────────────────────────────────────────────────

func TestDoDotsPush_NoRepo(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doDotsPush()()
	got, ok := msg.(dotsPushedMsg)
	if !ok {
		t.Fatalf("expected dotsPushedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error when dots_repo not configured")
	}
}

func TestDoDotsPush_NonGitDir(t *testing.T) {
	// repoDir is a plain temp dir → git commit fails.
	m, _ := newDotsModelForCmds(t)
	msg := m.doDotsPush()()
	got, ok := msg.(dotsPushedMsg)
	if !ok {
		t.Fatalf("expected dotsPushedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error pushing from non-git directory")
	}
}

// ── doDotsDelete ──────────────────────────────────────────────────────────────

func TestDoDotsDelete_NotFound(t *testing.T) {
	m, _ := newDotsModelForCmds(t)
	msg := m.doDotsDelete("nonexistent", true)()
	got, ok := msg.(dotsDeletedMsg)
	if !ok {
		t.Fatalf("expected dotsDeletedMsg, got %T", msg)
	}
	if got.err == nil {
		t.Error("expected error for unknown entry name")
	}
}

func TestDoDotsDelete_Success(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Stow packages live under the repo's dotfiles/ subtree.
	nvimPkgDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(nvimPkgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Name: shortHostname(), Special: "host", Dots: []config.DotEntry{
				{Name: "nvim", Path: filepath.Join(homeDir, ".config", "nvim")},
			}},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doDotsDelete("nvim", true)()
	got, ok := msg.(dotsDeletedMsg)
	if !ok {
		t.Fatalf("expected dotsDeletedMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.name != "nvim" {
		t.Errorf("name = %q, want nvim", got.name)
	}
}

// ── doResetSettings ───────────────────────────────────────────────────────────

func TestDoResetSettings_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doResetSettings()()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.action != "reset-settings" {
		t.Errorf("action = %q, want reset-settings", got.action)
	}
	if !got.reload {
		t.Error("reload should be true after reset-settings")
	}
}

// ── doResetCache ──────────────────────────────────────────────────────────────

func TestDoResetCache_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doResetCache()()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.action != "reset-cache" {
		t.Errorf("action = %q, want reset-cache", got.action)
	}
	if !got.reload {
		t.Error("reload should be true after reset-cache")
	}
}

// ── doDisableDots ─────────────────────────────────────────────────────────────

func TestDoDisableDots_NoDotsRepo(t *testing.T) {
	// When dots is not configured, doDisableDots skips physical unlink work
	// but still persists the disabled flag and triggers a reload.
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)
	msg := m.doDisableDots(true)()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.action != "disable-dots" {
		t.Errorf("action = %q, want disable-dots", got.action)
	}
	if got.detail != "dots disabled" {
		t.Errorf("detail = %q, want %q", got.detail, "dots disabled")
	}
	if !got.reload {
		t.Error("reload should be true")
	}
}

func TestDoDisableDots_Success(t *testing.T) {
	prov := &okProvider{name: "brew"}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Create source file and managed symlink at stow-derived path.
	// Entry Path=homeDir/.zshrc → SourcePath=repoDir/dotfiles/zsh/.zshrc
	srcFile := filepath.Join(repoDir, "dotfiles", "zsh", ".zshrc")
	dstFile := filepath.Join(homeDir, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("# zsh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcFile, dstFile); err != nil {
		t.Fatal(err)
	}

	// Write settings.json with dots_repo and dots entry.
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    shortHostname(),
			Special: "host",
			Dots: []config.DotEntry{
				{Name: "zsh", Path: dstFile},
			},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doDisableDots(true)()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Errorf("unexpected error: %v", got.err)
	}
	if got.action != "disable-dots" {
		t.Errorf("action = %q, want disable-dots", got.action)
	}
	if !got.reload {
		t.Error("reload should be true after successful disable")
	}
	// dstFile should now be a real file.
	fi, err := os.Lstat(dstFile)
	if err != nil {
		t.Fatalf("Lstat %q: %v", dstFile, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("dstFile is still a symlink after doDisableDots")
	}
}

func TestDoDisableDots_RemoveLocal(t *testing.T) {
	prov := &okProvider{name: "brew"}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	srcFile := filepath.Join(repoDir, "dotfiles", "zsh", ".zshrc")
	dstFile := filepath.Join(homeDir, ".zshrc")
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcFile, []byte("# zsh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(srcFile, dstFile); err != nil {
		t.Fatal(err)
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups:   []*config.GroupConfig{{Name: shortHostname(), Special: "host", Dots: []config.DotEntry{{Name: "zsh", Path: dstFile}}}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background(), prov); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	msg := m.doDisableDots(false)()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if _, err := os.Lstat(dstFile); !os.IsNotExist(err) {
		t.Fatalf("local target exists after doDisableDots(false): %v", err)
	}
}

// ── doEnableDots ──────────────────────────────────────────────────────────────

func TestDoEnableDots_ClearsDisabledFlag(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, cfgPath := newCmdApp(t, prov, nil)
	if err := a.SaveDotsDisabled(context.Background(), true); err != nil {
		t.Fatalf("SaveDotsDisabled(true): %v", err)
	}

	m := modelForCmds(a)
	m.beginDotsOperation("Enabling dots...")
	msg := m.doEnableDots()()
	got, ok := msg.(dangerOpDoneMsg)
	if !ok {
		t.Fatalf("expected dangerOpDoneMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.action != "enable-dots" || got.detail != "dots enabled" || !got.reload || got.mode != viewDots {
		t.Fatalf("message = %+v, want enable-dots reload to dots", got)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if config.BoolVal(cfg.HostSettings[shortHostname()].DotsDisabled) {
		t.Fatal("dots_disabled should be false after doEnableDots")
	}
}

// ── doScanProvider ────────────────────────────────────────────────────────────

// TestDoScanProvider_ReturnsMsg verifies that doScanProvider returns a
// providerScannedMsg with the correct provider name. Tools are not fetched
// here — doFetchFinalTools handles that after all providers finish.
func TestDoScanProvider_ReturnsMsg(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doScanProvider("brew", 7, nil, 0)()
	got, ok := msg.(providerScannedMsg)
	if !ok {
		t.Fatalf("expected providerScannedMsg, got %T", msg)
	}
	if got.gen != 7 {
		t.Errorf("gen = %d, want 7", got.gen)
	}
	if got.provider != "brew" {
		t.Errorf("provider = %q, want %q", got.provider, "brew")
	}
}

// TestDoFetchFinalTools_ReturnsMsg verifies that doFetchFinalTools returns an
// allProvidersDoneMsg (DB may be empty so tools can be nil/empty).
func TestDoFetchFinalTools_ReturnsMsg(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doFetchFinalTools(8)()
	got, ok := msg.(allProvidersDoneMsg)
	if !ok {
		t.Fatalf("expected allProvidersDoneMsg, got %T", msg)
	}
	if got.gen != 8 {
		t.Errorf("gen = %d, want 8", got.gen)
	}
}

func TestDoCheckProviderOutdated_ReturnsMsg(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, []tuiFixtureTool{tuiTool("ripgrep", "brew")})
	m := modelForCmds(a)

	msg := m.doCheckProviderOutdated("brew", 9)()
	got, ok := msg.(providerOutdatedCheckedMsg)
	if !ok {
		t.Fatalf("expected providerOutdatedCheckedMsg, got %T", msg)
	}
	if got.gen != 9 {
		t.Errorf("gen = %d, want 9", got.gen)
	}
	if got.provider != "brew" {
		t.Errorf("provider = %q, want brew", got.provider)
	}
}

// ── doRefreshDiscovered ───────────────────────────────────────────────────────

// TestDoRefreshDiscovered_ReturnsMsg verifies that doRefreshDiscovered always
// returns a discoveredRefreshedMsg (never nil, never an error type).
func TestDoRefreshDiscovered_ReturnsMsg(t *testing.T) {
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := modelForCmds(a)

	msg := m.doRefreshDiscovered(9, nil, 0)()
	got, ok := msg.(discoveredRefreshedMsg)
	if !ok {
		t.Fatalf("expected discoveredRefreshedMsg, got %T", msg)
	}
	if got.gen != 9 {
		t.Errorf("gen = %d, want 9", got.gen)
	}
}

// TestRenderDots_EmptyState_LoadingWhileStartupSnapshotPending pins the dots
// tab's zero-entry state: while the startup snapshot hasn't landed yet
// (m.loading) it must read as loading, not "No dotfiles tracked yet".
func TestRenderDots_EmptyState_LoadingWhileStartupSnapshotPending(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsSyncAvailCached = app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityReady}
	m.loading = true

	out := stripANSIEscapeSequences(renderDots(m))
	if !strings.Contains(out, "Loading dotfiles…") {
		t.Fatalf("expected loading line while startup snapshot is pending, got:\n%s", out)
	}
	if strings.Contains(out, "No dotfiles tracked yet.") {
		t.Fatalf("empty state must not show before the startup snapshot lands, got:\n%s", out)
	}

	m.loading = false
	out = stripANSIEscapeSequences(renderDots(m))
	if !strings.Contains(out, "No dotfiles tracked yet.") {
		t.Fatalf("expected empty state once the snapshot landed with no entries, got:\n%s", out)
	}
}

func TestRenderDotsVariantPrompts(t *testing.T) {
	m := baseModel(nil)
	m.width = 120

	create := stripANSIEscapeSequences(renderDotsVariantCreatePrompt(m, "nvim", "  ", 120))
	if !strings.Contains(create, "create host variant for") || !strings.Contains(create, "nvim") {
		t.Errorf("create prompt = %q, want it to name the entry", create)
	}
	remove := stripANSIEscapeSequences(renderDotsVariantRemovePrompt(m, "nvim", "  ", 120))
	if !strings.Contains(remove, "remove host variant for") || !strings.Contains(remove, "nvim") {
		t.Errorf("remove prompt = %q, want it to name the entry", remove)
	}

	narrow := stripANSIEscapeSequences(renderDotsVariantCreatePrompt(m, "very-long-dot-entry-name", "  ", 10))
	if strings.Contains(narrow, "create host variant for") {
		t.Errorf("narrow prompt = %q, want the prompt text dropped when it cannot fit", narrow)
	}
}
