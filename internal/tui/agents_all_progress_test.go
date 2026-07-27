package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// Backed by a real app.App so the sub-step Cmds actually execute, unlike baseModel(nil)'s nil m.app.
func agentsAllProgressModel(t *testing.T, cfg *config.RootConfig, skillsRows []app.SkillPackageRow, pluginRows []app.PluginRow, opts ...func(*app.App)) Model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if cfg == nil {
		cfg = &config.RootConfig{}
	}
	if err := saveTUIConfig(t, cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath, opts...)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("App.InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := modelForCmds(a)
	m.pluginFormName = textinput.New()
	m.pluginFormMarketplace = textinput.New()
	m.pluginFormAgents = textinput.New()
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.skillTypeIdx = agentsChipAll
	m.width = 120
	m.cursorHidden = false
	m.skillsLoaded = true
	m.mcpLoaded = true
	m.pluginLoaded = true
	m.skillsRows = skillsRows
	m.pluginRows = pluginRows
	m.enabledAgents = []string{"claude"}
	return m
}

// Runs batch commands concurrently like Bubble Tea and feeds each result back into Update, since this flow needs several waitForProgress/progressMsg round-trips rather than one flat batch.
func drainProgressCmds(t *testing.T, m Model, msg tea.Msg, captured *[]string) Model {
	t.Helper()
	tm, cmd := m.Update(msg)
	m = tm.(Model)
	*captured = append(*captured, m.progressText)
	if cmd == nil {
		return m
	}
	resolved := cmd()
	if resolved == nil {
		return m
	}
	if batch, ok := resolved.(tea.BatchMsg); ok {
		results := make(chan tea.Msg, len(batch))
		pending := 0
		for _, c := range batch {
			if c == nil {
				continue
			}
			pending++
			go func(cmd tea.Cmd) { results <- cmd() }(c)
		}
		var terminal []tea.Msg
		for range pending {
			sub := <-results
			if sub == nil {
				continue
			}
			switch sub.(type) {
			case progressMsg, progressStreamClosedMsg:
				m = drainProgressCmds(t, m, sub, captured)
			default:
				terminal = append(terminal, sub)
			}
		}
		for _, sub := range terminal {
			m = drainProgressCmds(t, m, sub, captured)
		}
		return m
	}
	return drainProgressCmds(t, m, resolved, captured)
}

func containsFold(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(strings.ToLower(s), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

func indexOfContainsFold(list []string, substr string) int {
	for i, s := range list {
		if strings.Contains(strings.ToLower(s), strings.ToLower(substr)) {
			return i
		}
	}
	return -1
}

// Reports the plugin's update as discoverable only after UpdateMarketplaces ran: LatestVersion comes from the marketplace clone, so a stale clone shows nothing outdated.
type progressStubPluginAdapter struct {
	mu        sync.Mutex
	id        string
	refreshed bool
	events    []string
}

func (s *progressStubPluginAdapter) ID() string      { return s.id }
func (s *progressStubPluginAdapter) Available() bool { return true }
func (s *progressStubPluginAdapter) ListPlugins(context.Context) ([]app.InstalledPlugin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "list-plugins")
	latest := "1.0.0"
	if s.refreshed {
		latest = "2.0.0"
	}
	return []app.InstalledPlugin{{Name: "outdated-plugin", Marketplace: "acme-market", Version: "1.0.0", LatestVersion: latest}}, nil
}
func (s *progressStubPluginAdapter) InstallPlugin(context.Context, config.Plugin) error { return nil }
func (s *progressStubPluginAdapter) RemovePlugin(context.Context, config.Plugin) error  { return nil }
func (s *progressStubPluginAdapter) UpdatePlugin(_ context.Context, name, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "update-plugin:"+name)
	return nil
}
func (s *progressStubPluginAdapter) ListMarketplaces(context.Context) ([]app.InstalledMarketplace, error) {
	return nil, nil
}
func (s *progressStubPluginAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *progressStubPluginAdapter) UpdateMarketplaces(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshed = true
	s.events = append(s.events, "update-marketplaces")
	return nil
}

// The cached rows and the adapter show nothing outdated until UpdateMarketplaces runs, so the plugin updates only if doAgentsUpdateAll recomputes outdated rows post-refresh.
func TestAgentsAll_UpdateAll_StreamsProgressText(t *testing.T) {
	t.Parallel()
	fake := &progressStubPluginAdapter{id: "claude-code"}
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "acme-market", Source: "acme/market"}},
			Plugins:      []config.Plugin{{Name: "outdated-plugin", Marketplace: "acme-market"}},
		},
	}
	m := agentsAllProgressModel(t, cfg,
		[]app.SkillPackageRow{{Name: "caveman", Source: "github.com/foo/caveman", Installed: true}},
		[]app.PluginRow{{
			Name: "outdated-plugin", Marketplace: "acme-market",
			Version: "1.0.0", LatestVersion: "1.0.0",
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		}},
		app.WithPluginAdapters([]app.PluginAdapter{fake}),
	)

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'U', Text: "U"}, &captured)

	if !containsFold(captured, "updating skills") {
		t.Errorf("expected some captured progressText to contain 'updating skills' (case-insensitive), got %v", captured)
	}
	if !containsFold(captured, "outdated-plugin") {
		t.Errorf("expected some captured progressText to mention plugin name 'outdated-plugin', got %v", captured)
	}

	refreshIdx := indexOfContainsFold(fake.events, "update-marketplaces")
	updateIdx := indexOfContainsFold(fake.events, "update-plugin:outdated-plugin")
	if refreshIdx < 0 {
		t.Fatalf("expected UpdateMarketplaces to run on the adapter, events = %v", fake.events)
	}
	if updateIdx < 0 {
		t.Fatalf("expected the post-refresh-outdated plugin to be updated in the same 'U' press, events = %v", fake.events)
	}
	if refreshIdx > updateIdx {
		t.Errorf("marketplace refresh must precede the plugin update, events = %v", fake.events)
	}
	refreshes := 0
	for _, e := range fake.events {
		if e == "update-marketplaces" {
			refreshes++
		}
	}
	if refreshes != 1 {
		t.Errorf("marketplace refresh should run exactly once (plugin update uses UpdatePluginsPreRefreshed), got %d in %v", refreshes, fake.events)
	}

	if got.progressText != "" {
		t.Errorf("progressText after full drain = %q, want empty", got.progressText)
	}
	if got.skillsRunning {
		t.Error("skillsRunning should be false after full drain")
	}
	if got.pluginRunning {
		t.Error("pluginRunning should be false after full drain")
	}
	if got.marketplaceRunning {
		t.Error("marketplaceRunning should be false after full drain")
	}
}

// Reports nothing installed so the install-missing sub-step has something to install.
type missingPluginStubAdapter struct {
	id        string
	installed []string
}

func (s *missingPluginStubAdapter) ID() string      { return s.id }
func (s *missingPluginStubAdapter) Available() bool { return true }
func (s *missingPluginStubAdapter) ListPlugins(context.Context) ([]app.InstalledPlugin, error) {
	return nil, nil
}
func (s *missingPluginStubAdapter) InstallPlugin(_ context.Context, p config.Plugin) error {
	s.installed = append(s.installed, p.Name)
	return nil
}
func (s *missingPluginStubAdapter) RemovePlugin(context.Context, config.Plugin) error { return nil }
func (s *missingPluginStubAdapter) UpdatePlugin(context.Context, string, string) error {
	return nil
}
func (s *missingPluginStubAdapter) ListMarketplaces(context.Context) ([]app.InstalledMarketplace, error) {
	return nil, nil
}
func (s *missingPluginStubAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *missingPluginStubAdapter) UpdateMarketplaces(context.Context) error { return nil }

// Reports nothing installed so RestoreMcpServers has something to add.
type progressStubMcpAdapter struct {
	id    string
	added []string
}

func (s *progressStubMcpAdapter) ID() string      { return s.id }
func (s *progressStubMcpAdapter) Available() bool { return true }
func (s *progressStubMcpAdapter) List(context.Context) ([]app.InstalledMcpServer, error) {
	return nil, nil
}
func (s *progressStubMcpAdapter) Add(_ context.Context, srv config.McpServer) error {
	s.added = append(s.added, srv.Name)
	return nil
}
func (s *progressStubMcpAdapter) Remove(context.Context, string) error { return nil }

func TestAgentsAll_UpdateAll_InstallsMissingPluginsAndMcpServers(t *testing.T) {
	t.Parallel()
	pluginStub := &missingPluginStubAdapter{id: "claude-code"}
	mcpStub := &progressStubMcpAdapter{id: "claude-code"}
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "acme-market", Source: "acme/market"}},
			Plugins:      []config.Plugin{{Name: "missing-plugin", Marketplace: "acme-market"}},
			McpServers:   []config.McpServer{{Name: "missing-mcp", Transport: "stdio", Command: "npx foo"}},
		},
	}
	m := agentsAllProgressModel(t, cfg,
		nil,
		[]app.PluginRow{{
			Name: "missing-plugin", Marketplace: "acme-market",
			PerAgentStatus: map[string]app.PluginStatus{"claude-code": app.PluginStatusMissing},
		}},
		app.WithPluginAdapters([]app.PluginAdapter{pluginStub}),
		app.WithMcpAdapters([]app.McpAdapter{mcpStub}),
	)

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'U', Text: "U"}, &captured)

	if !containsFold(captured, "installing missing plugins") {
		t.Errorf("expected progress text to include 'installing missing plugins', got %v", captured)
	}
	if !containsFold(captured, "installing missing mcp servers") {
		t.Errorf("expected progress text to include 'installing missing mcp servers', got %v", captured)
	}
	if len(pluginStub.installed) != 1 || pluginStub.installed[0] != "missing-plugin" {
		t.Fatalf("expected missing-plugin to be installed via update-all, got %v", pluginStub.installed)
	}
	if len(mcpStub.added) != 1 || mcpStub.added[0] != "missing-mcp" {
		t.Fatalf("expected missing-mcp to be added via update-all, got %v", mcpStub.added)
	}
	if got.pluginErr != nil {
		t.Errorf("pluginErr = %v, want nil", got.pluginErr)
	}
	if got.mcpErr != nil {
		t.Errorf("mcpErr = %v, want nil", got.mcpErr)
	}
}

// One installed-but-outdated plugin plus a second manifest plugin left unreported, so a single U run exercises both the outdated-update and install-missing sub-steps.
type combinedPluginStubAdapter struct {
	id           string
	refreshes    int
	afterRefresh bool
	installed    []string
	updated      []string
}

func (s *combinedPluginStubAdapter) ID() string      { return s.id }
func (s *combinedPluginStubAdapter) Available() bool { return true }
func (s *combinedPluginStubAdapter) ListPlugins(context.Context) ([]app.InstalledPlugin, error) {
	latest := "1.0.0"
	if s.afterRefresh {
		latest = "2.0.0"
	}
	return []app.InstalledPlugin{
		{Name: "outdated-plugin", Marketplace: "acme-market", Version: "1.0.0", LatestVersion: latest},
	}, nil
}
func (s *combinedPluginStubAdapter) InstallPlugin(_ context.Context, p config.Plugin) error {
	s.installed = append(s.installed, p.Name)
	return nil
}
func (s *combinedPluginStubAdapter) RemovePlugin(context.Context, config.Plugin) error { return nil }
func (s *combinedPluginStubAdapter) UpdatePlugin(_ context.Context, name, _ string) error {
	s.updated = append(s.updated, name)
	return nil
}
func (s *combinedPluginStubAdapter) ListMarketplaces(context.Context) ([]app.InstalledMarketplace, error) {
	return nil, nil
}
func (s *combinedPluginStubAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *combinedPluginStubAdapter) UpdateMarketplaces(context.Context) error {
	s.refreshes++
	s.afterRefresh = true
	return nil
}

// Drives a manifest with both an outdated and a missing plugin so both sub-steps run and the marketplace refresh can be shown to happen once for the whole run.
func TestAgentsAll_UpdateAll_RefreshesMarketplacesOnceAcrossUpdateAndInstall(t *testing.T) {
	t.Parallel()
	fake := &combinedPluginStubAdapter{id: "claude-code"}
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "acme-market", Source: "acme/market"}},
			Plugins: []config.Plugin{
				{Name: "outdated-plugin", Marketplace: "acme-market"},
				{Name: "missing-plugin", Marketplace: "acme-market"},
			},
		},
	}
	m := agentsAllProgressModel(t, cfg,
		nil,
		[]app.PluginRow{
			{
				Name: "outdated-plugin", Marketplace: "acme-market",
				Version: "1.0.0", LatestVersion: "1.0.0",
				PerAgentStatus: map[string]app.PluginStatus{"claude-code": app.PluginStatusInstalled},
			},
			{
				Name: "missing-plugin", Marketplace: "acme-market",
				PerAgentStatus: map[string]app.PluginStatus{"claude-code": app.PluginStatusMissing},
			},
		},
		app.WithPluginAdapters([]app.PluginAdapter{fake}),
	)

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'U', Text: "U"}, &captured)

	if fake.refreshes != 1 {
		t.Errorf("expected exactly 1 marketplace refresh across the whole 'U' run (update + install-missing), got %d", fake.refreshes)
	}
	if len(fake.updated) != 1 || fake.updated[0] != "outdated-plugin" {
		t.Errorf("expected outdated-plugin to be updated, got %v", fake.updated)
	}
	if len(fake.installed) != 1 || fake.installed[0] != "missing-plugin" {
		t.Errorf("expected missing-plugin to be installed, got %v", fake.installed)
	}
	if got.pluginErr != nil {
		t.Errorf("pluginErr = %v, want nil", got.pluginErr)
	}
}

// Nothing installed and every install fails, so the install-missing sub-step has a real failure to propagate.
type erroringPluginStubAdapter struct{ id string }

func (s *erroringPluginStubAdapter) ID() string      { return s.id }
func (s *erroringPluginStubAdapter) Available() bool { return true }
func (s *erroringPluginStubAdapter) ListPlugins(context.Context) ([]app.InstalledPlugin, error) {
	return nil, nil
}
func (s *erroringPluginStubAdapter) InstallPlugin(context.Context, config.Plugin) error {
	return errors.New("install exploded")
}
func (s *erroringPluginStubAdapter) RemovePlugin(context.Context, config.Plugin) error { return nil }
func (s *erroringPluginStubAdapter) UpdatePlugin(context.Context, string, string) error {
	return nil
}
func (s *erroringPluginStubAdapter) ListMarketplaces(context.Context) ([]app.InstalledMarketplace, error) {
	return nil, nil
}
func (s *erroringPluginStubAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *erroringPluginStubAdapter) UpdateMarketplaces(context.Context) error { return nil }

// Nothing installed and every add fails, so the install-missing sub-step has a real failure to propagate.
type erroringMcpStubAdapter struct{ id string }

func (s *erroringMcpStubAdapter) ID() string      { return s.id }
func (s *erroringMcpStubAdapter) Available() bool { return true }
func (s *erroringMcpStubAdapter) List(context.Context) ([]app.InstalledMcpServer, error) {
	return nil, nil
}
func (s *erroringMcpStubAdapter) Add(context.Context, config.McpServer) error {
	return errors.New("add exploded")
}
func (s *erroringMcpStubAdapter) Remove(context.Context, string) error { return nil }

// A failure in either install-missing sub-step must reach the model as pluginErr/mcpErr rather than being swallowed, and its running flag must still clear.
func TestAgentsAll_UpdateAll_InstallMissingFailures_PropagateAsPluginAndMcpErr(t *testing.T) {
	t.Parallel()
	pluginStub := &erroringPluginStubAdapter{id: "claude-code"}
	mcpStub := &erroringMcpStubAdapter{id: "claude-code"}
	cfg := &config.RootConfig{
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "acme-market", Source: "acme/market"}},
			Plugins:      []config.Plugin{{Name: "missing-plugin", Marketplace: "acme-market"}},
			McpServers:   []config.McpServer{{Name: "missing-mcp", Transport: "stdio", Command: "npx foo"}},
		},
	}
	m := agentsAllProgressModel(t, cfg,
		nil,
		[]app.PluginRow{{
			Name: "missing-plugin", Marketplace: "acme-market",
			PerAgentStatus: map[string]app.PluginStatus{"claude-code": app.PluginStatusMissing},
		}},
		app.WithPluginAdapters([]app.PluginAdapter{pluginStub}),
		app.WithMcpAdapters([]app.McpAdapter{mcpStub}),
	)

	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'U', Text: "U"}, &[]string{})

	if got.pluginErr == nil || !strings.Contains(got.pluginErr.Error(), "install exploded") {
		t.Errorf("pluginErr = %v, want it to contain 'install exploded'", got.pluginErr)
	}
	if got.mcpErr == nil || !strings.Contains(got.mcpErr.Error(), "add exploded") {
		t.Errorf("mcpErr = %v, want it to contain 'add exploded'", got.mcpErr)
	}
	if got.pluginRunning {
		t.Error("pluginRunning should be false after full drain even when install-missing errored")
	}
	if got.mcpRunning {
		t.Error("mcpRunning should be false after full drain even when install-missing errored")
	}
}

// Pins the exact S order: the claim step first, then skills, mcp and plugins restore.
func TestAgentsAll_SyncAll_StreamsProgressTextInOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := agentsAllProgressModel(t, nil,
		[]app.SkillPackageRow{{Name: "caveman", Source: "github.com/foo/caveman", Installed: true}},
		nil,
	)

	armed, _ := m.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	m = armed.(Model)
	if !m.agentsSyncAllConfirm {
		t.Fatal("first S must arm the sync-all confirm, not start the run")
	}

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'S', Text: "S"}, &captured)

	importIdx := indexOfContainsFold(captured, "importing unmanaged skills")
	skillsIdx := indexOfContainsFold(captured, "restoring skills")
	mcpIdx := indexOfContainsFold(captured, "restoring mcp")
	pluginsIdx := indexOfContainsFold(captured, "restoring plugins")

	if importIdx < 0 || skillsIdx < 0 || mcpIdx < 0 || pluginsIdx < 0 {
		t.Fatalf("expected captured progressText to include 'importing unmanaged skills', 'restoring skills', 'restoring mcp', and 'restoring plugins', got %v", captured)
	}
	if !(importIdx < skillsIdx && skillsIdx < mcpIdx && mcpIdx < pluginsIdx) {
		t.Fatalf("expected order importing unmanaged skills < restoring skills < restoring mcp < restoring plugins by capture index, got importIdx=%d skillsIdx=%d mcpIdx=%d pluginsIdx=%d in %v", importIdx, skillsIdx, mcpIdx, pluginsIdx, captured)
	}

	if got.progressText != "" {
		t.Errorf("progressText after full drain = %q, want empty", got.progressText)
	}
	if got.skillsRunning || got.mcpRunning || got.pluginRunning {
		t.Errorf("expected all running flags false after full drain, got skillsRunning=%v mcpRunning=%v pluginRunning=%v", got.skillsRunning, got.mcpRunning, got.pluginRunning)
	}
}

// Rigs the skills sub-step to fail via app-layer SkillsDisabled while the Model's own gating stays true, so skillsErr is set and no running flag sticks.
func TestAgentsAll_UpdateAll_SkillsErrorDoesNotStickRunning(t *testing.T) {
	t.Parallel()
	cfg := &config.RootConfig{
		Settings: config.Settings{SkillsDisabled: config.BoolPtr(true)},
	}
	m := agentsAllProgressModel(t, cfg,
		[]app.SkillPackageRow{{Name: "caveman", Source: "github.com/foo/caveman", Installed: true}},
		nil,
	)

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'U', Text: "U"}, &captured)

	if got.skillsErr == nil {
		t.Fatal("expected skillsErr to be set after a failed skills update sub-step")
	}
	if !containsFold(captured, "updating skills") {
		t.Errorf("expected progress streaming to have announced 'updating skills' before the sub-step failed, got %v", captured)
	}
	if got.progressText != "" {
		t.Errorf("progressText after full drain (with error mid-sequence) = %q, want empty", got.progressText)
	}
	if got.skillsRunning {
		t.Error("skillsRunning should be false after full drain even when the sequence errored")
	}
	if got.pluginRunning {
		t.Error("pluginRunning should be false after full drain even when the sequence errored")
	}
	if got.mcpRunning {
		t.Error("mcpRunning should be false after full drain even when the sequence errored")
	}
}

func TestCombineSkillErrors_NilErrNoFailed_ReturnsNil(t *testing.T) {
	t.Parallel()
	got := combineSkillErrors(nil, app.RestoreSkillsResult{})
	if got != nil {
		t.Fatalf("combineSkillErrors(nil, no Failed) = %v, want nil", got)
	}
}

func TestCombineSkillErrors_NilErrWithFailed_ReturnsErrorPerPackage(t *testing.T) {
	t.Parallel()
	res := app.RestoreSkillsResult{Failed: []app.SkillFailure{
		{Name: "acme/foo", Message: "install failed"},
		{Name: "acme/bar", Message: "timeout"},
	}}
	got := combineSkillErrors(nil, res)
	if got == nil {
		t.Fatal("combineSkillErrors(nil, Failed) = nil, want non-nil error")
	}
	msg := got.Error()
	for _, want := range []string{"acme/foo", "install failed", "acme/bar", "timeout"} {
		if !strings.Contains(msg, want) {
			t.Errorf("combined error %q does not contain %q", msg, want)
		}
	}
}

func TestCombineSkillErrors_ErrAndFailed_JoinsBoth(t *testing.T) {
	t.Parallel()
	topErr := errors.New("restore skills: manifest load failed")
	res := app.RestoreSkillsResult{Failed: []app.SkillFailure{
		{Name: "acme/foo", Message: "install failed"},
	}}
	got := combineSkillErrors(topErr, res)
	if got == nil {
		t.Fatal("combineSkillErrors(err, Failed) = nil, want non-nil error")
	}
	msg := got.Error()
	for _, want := range []string{"manifest load failed", "acme/foo", "install failed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("combined error %q does not contain %q", msg, want)
		}
	}
	if !errors.Is(got, topErr) {
		t.Error("combined error should wrap/join the original top-level err (errors.Is)")
	}
}

// A non-nil skillsErr must clear skillsRunning, surface via setStatus, and must NOT dispatch a manifest reload.

func TestAgentsProgressDoneMsg_SkillsErrorFromFailedPackages_StopsRunningNoReload(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.skillsRunning = true
	m.skillsLoaded = true // sentinel: only the err==nil branch resets this to false
	err := combineSkillErrors(nil, app.RestoreSkillsResult{Failed: []app.SkillFailure{
		{Name: "acme/foo", Message: "install failed"},
	}})
	if err == nil {
		t.Fatal("precondition: combineSkillErrors should produce a non-nil error")
	}

	got := drive(m, agentsProgressDoneMsg{skills: true, skillsErr: err})

	if got.skillsRunning {
		t.Error("skillsRunning should be false after an errored skills done msg")
	}
	if got.skillsErr == nil {
		t.Fatal("skillsErr should be set on the model")
	}
	if !strings.Contains(got.skillsErr.Error(), "acme/foo") {
		t.Errorf("skillsErr = %q, want it to contain the failed package name", got.skillsErr.Error())
	}
	if !got.skillsLoaded {
		t.Error("skillsLoaded should remain true (unchanged) — a manifest reload must not be dispatched on error")
	}
	if !got.statusIsErr {
		t.Error("statusIsErr should be true")
	}
	if !strings.HasPrefix(got.statusMsg, "✗ ") {
		t.Errorf("statusMsg = %q, want it to start with the error prefix %q", got.statusMsg, "✗ ")
	}
	if !strings.Contains(got.statusMsg, "acme/foo") {
		t.Errorf("statusMsg = %q, want it to surface the failed package name via firstAgentsProgressError", got.statusMsg)
	}
}
