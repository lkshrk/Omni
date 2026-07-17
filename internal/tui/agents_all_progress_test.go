package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// agentsAllProgressModel builds a Model backed by a real app.App (so the
// doAgentsUpdateAll/doAgentsSyncAll sub-step Cmds actually execute), wired
// like agentsAllModel but sourced from newCmdTestApp-style app construction
// (see commands_test.go) instead of baseModel(nil)'s nil m.app.
func agentsAllProgressModel(t *testing.T, cfg *config.RootConfig, skillsRows []app.SkillPackageRow, pluginRows []app.PluginRow) Model {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if cfg == nil {
		cfg = &config.RootConfig{}
	}
	if err := saveTUIConfig(t, cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
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

// drainProgressCmds feeds msg into m.Update, then recursively resolves any
// tea.Cmd / tea.BatchMsg produced (calling each Cmd to get its Msg and
// feeding that Msg back into Update), capturing m.progressText after every
// step along the way. This mirrors the inline "drain the Cmd batch" pattern
// used by other tests in this package (see TestAgentsBoot_
// InitDoesNotFireSectionLoadsBeforeSnapshot in agents_all_test.go), adapted
// to a generic recursive loop since this flow needs multiple rounds of
// waitForProgress/progressMsg round-trips rather than a single flat batch.
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
		var terminal []tea.Msg
		for _, c := range batch {
			if c == nil {
				continue
			}
			sub := c()
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

// TestAgentsAll_UpdateAll_StreamsProgressText is a red test for the planned
// doAgentsUpdateAll change: it should stream per-step progress text (via
// beginProgressStream/sendProgress) while running skills update and each
// outdated plugin update, mirroring tools' upgrade-all. Today
// doAgentsUpdateAll never calls sendProgress, so no captured progressText
// will mention "updating skills" or the outdated plugin's name — this test
// is expected to FAIL until that production change lands.
func TestAgentsAll_UpdateAll_StreamsProgressText(t *testing.T) {
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
			Version: "1.0.0", LatestVersion: "2.0.0",
			PerAgentStatus: map[string]app.PluginStatus{"claude": app.PluginStatusInstalled},
		}},
	)

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'U', Text: "U"}, &captured)

	if !containsFold(captured, "updating skills") {
		t.Errorf("expected some captured progressText to contain 'updating skills' (case-insensitive), got %v", captured)
	}
	if !containsFold(captured, "outdated-plugin") {
		t.Errorf("expected some captured progressText to mention plugin name 'outdated-plugin', got %v", captured)
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
}

// TestAgentsAll_SyncAll_StreamsProgressTextInOrder is a red test for the
// planned doAgentsSyncAll change: it should stream progress text for
// "restoring skills" then "restoring mcp" then "restoring plugins" in that
// exact order. Today doAgentsSyncAll never calls sendProgress, so this test
// is expected to FAIL until that production change lands.
func TestAgentsAll_SyncAll_StreamsProgressTextInOrder(t *testing.T) {
	m := agentsAllProgressModel(t, nil,
		[]app.SkillPackageRow{{Name: "caveman", Source: "github.com/foo/caveman", Installed: true}},
		nil,
	)

	var captured []string
	got := drainProgressCmds(t, m, tea.KeyPressMsg{Code: 'S', Text: "S"}, &captured)

	skillsIdx := indexOfContainsFold(captured, "restoring skills")
	mcpIdx := indexOfContainsFold(captured, "restoring mcp")
	pluginsIdx := indexOfContainsFold(captured, "restoring plugins")

	if skillsIdx < 0 || mcpIdx < 0 || pluginsIdx < 0 {
		t.Fatalf("expected captured progressText to include 'restoring skills', 'restoring mcp', and 'restoring plugins', got %v", captured)
	}
	if !(skillsIdx < mcpIdx && mcpIdx < pluginsIdx) {
		t.Fatalf("expected order restoring skills < restoring mcp < restoring plugins by capture index, got skillsIdx=%d mcpIdx=%d pluginsIdx=%d in %v", skillsIdx, mcpIdx, pluginsIdx, captured)
	}

	if got.progressText != "" {
		t.Errorf("progressText after full drain = %q, want empty", got.progressText)
	}
	if got.skillsRunning || got.mcpRunning || got.pluginRunning {
		t.Errorf("expected all running flags false after full drain, got skillsRunning=%v mcpRunning=%v pluginRunning=%v", got.skillsRunning, got.mcpRunning, got.pluginRunning)
	}
}

// TestAgentsAll_UpdateAll_SkillsErrorDoesNotStickRunning rigs the skills
// update sub-step to fail (via app-layer SkillsDisabled=true, the same
// requireSkillsEnabled guard exercised by internal/app's
// TestUpdatePlugin_GuardedByPluginsEnabled-style tests) while the Model's own
// skillsSectionEnabled() gating stays true so doAgentsUpdateAll still appends
// the skills Cmd. After a full drain, m.skillsErr must be set and no running
// flag must be left stuck true, even though the sequence errors mid-way.
func TestAgentsAll_UpdateAll_SkillsErrorDoesNotStickRunning(t *testing.T) {
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

// combineSkillErrors — folding RestoreSkillsResult.Failed into the returned
// error, mirroring combineMcpErrors/combinePluginErrors (see
// internal/tui/agents_all.go's doc comment on combineSkillErrors).

func TestCombineSkillErrors_NilErrNoFailed_ReturnsNil(t *testing.T) {
	got := combineSkillErrors(nil, app.RestoreSkillsResult{})
	if got != nil {
		t.Fatalf("combineSkillErrors(nil, no Failed) = %v, want nil", got)
	}
}

func TestCombineSkillErrors_NilErrWithFailed_ReturnsErrorPerPackage(t *testing.T) {
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

// agentsProgressDoneMsg — skills-specific handling: a non-nil skillsErr
// (whether from a top-level RestoreSkills error or from combineSkillErrors
// folding res.Failed) must clear skillsRunning, set skillsErr, surface the
// error via setStatus (see firstAgentsProgressError), and must NOT dispatch a
// manifest reload (loadSkillsManifestCmd is only called on the msg.skillsErr
// == nil branch in update.go's agentsProgressDoneMsg case).

func TestAgentsProgressDoneMsg_SkillsErrorFromFailedPackages_StopsRunningNoReload(t *testing.T) {
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
