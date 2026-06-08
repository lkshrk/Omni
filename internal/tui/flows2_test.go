package tui

// flows2_test.go — additional flow tests UC-66 through UC-136,
// covering global messages, setup wizard steps 2-5, file picker,
// dots tab pull key, settings rows 1-9, hosts tab full navigation,
// group picker inline new-group, and all remaining message handlers.

import (
	"context"
	"errors"
	"image/color"
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
)

// ── helpers ──────────────────────────────────────────────────────────────────

// setupStep2Model builds a model at setup step 2 (no-host re-run, all
// providers enabled). Step 2 uses the same provider-selection UI as step 1.
func setupStep2Model() Model {
	m := Model{
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        textinput.New(),
		commandInput:  textinput.New(),
		settingsInput: textinput.New(),
		mode:          viewSetup,
		setupStep:     2,
		upgradingKeys: make(map[string]bool),
		setupProviders: []app.SetupProviderOption{
			{Name: "system", Label: "system", Enabled: true},
			{Name: "node", Label: "node", Enabled: true},
		},
		dangerConfirmRow: -1,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		dotsIgnoreIdx:    -1,
		width:            120,
		height:           80,
	}
	return m
}

// setupStep3Model builds a model at setup step 3 (node manager selection).
func setupStep3Model() Model {
	m := Model{
		keys:             DefaultKeyMap(),
		spinner:          spinner.New(),
		filter:           textinput.New(),
		commandInput:     textinput.New(),
		settingsInput:    textinput.New(),
		mode:             viewSetup,
		setupStep:        3,
		setupNodeMgrIdx:  0,
		upgradingKeys:    make(map[string]bool),
		dangerConfirmRow: -1,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		width:            120,
		height:           80,
	}
	return m
}

// setupStep5Model builds a model at setup step 5 (enable dotfiles?).
func setupStep5Model() Model {
	m := Model{
		keys:             DefaultKeyMap(),
		spinner:          spinner.New(),
		filter:           textinput.New(),
		commandInput:     textinput.New(),
		settingsInput:    textinput.New(),
		mode:             viewSetup,
		setupStep:        5,
		upgradingKeys:    make(map[string]bool),
		dangerConfirmRow: -1,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		width:            120,
		height:           80,
	}
	return m
}

// setupStep6Model builds a model at setup step 6 (dots repo path, no file
// picker active yet).
func setupStep6Model() Model {
	m := Model{
		keys:             DefaultKeyMap(),
		spinner:          spinner.New(),
		filter:           textinput.New(),
		commandInput:     textinput.New(),
		settingsInput:    textinput.New(),
		mode:             viewSetup,
		setupStep:        6,
		showFilePicker:   false,
		upgradingKeys:    make(map[string]bool),
		dangerConfirmRow: -1,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		width:            120,
		height:           80,
	}
	return m
}

func setupStep7Model() Model {
	m := setupStep2Model()
	m.setupStep = 7
	m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{
		"laptop": {Groups: []string{"base"}},
		"server": {},
	}}
	m.groupNames = []string{"base", "work"}
	return m
}

func setupStep8Model() Model {
	m := setupStep7Model()
	m.setupStep = 8
	return m
}

func setupStep9Model() Model {
	m := setupStep7Model()
	m.setupStep = 9
	m.hostInfo = &app.HostInfo{
		Active: "testhost",
		Hosts:  map[string]config.HostAssignment{"testhost": {Groups: []string{"base"}}},
	}
	m.setupGroupIdx = 0
	m.initSetupGroupDraft()
	return m
}

// loadingSetup builds a loading model stuck at a given setup step.
func loadingSetup(step int) Model {
	m := Model{
		keys:             DefaultKeyMap(),
		spinner:          spinner.New(),
		filter:           textinput.New(),
		commandInput:     textinput.New(),
		settingsInput:    textinput.New(),
		mode:             viewSetup,
		setupStep:        step,
		loading:          true,
		upgradingKeys:    make(map[string]bool),
		dangerConfirmRow: -1,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		width:            120,
		height:           80,
	}
	return m
}

// hostsModel builds a model in viewGroups with two hosts and hostname
// mappings.
func hostsModel() Model {
	m := baseModel(nil)
	m.mode = viewGroups
	m.upgradingKeys = make(map[string]bool)
	m.groupNames = []string{"work", "personal"}
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{
			"alpha": {Groups: []string{"work"}},
			"beta":  {Groups: []string{"personal"}},
		},
	}
	return m
}

// ── Group A — Global / Special Messages ──────────────────────────────────────

// UC-66: BackgroundColorMsg — light/dark detection.
func TestFlow2_UC66_BackgroundColorMsg(t *testing.T) {
	t.Run("light terminal sets isDark=false", func(t *testing.T) {
		m := baseModel(nil)
		got := drive(m, tea.BackgroundColorMsg{Color: color.RGBA{R: 250, G: 251, B: 252, A: 255}})
		if got.isDark {
			t.Error("isDark should be false for white terminal background")
		}
	})

	t.Run("dark terminal sets isDark=true", func(t *testing.T) {
		m := baseModel(nil)
		got := drive(m, tea.BackgroundColorMsg{Color: color.RGBA{R: 13, G: 14, B: 15, A: 255}})
		if !got.isDark {
			t.Error("isDark should be true for black terminal background")
		}
	})
}

// UC-67: FocusMsg/BlurMsg.
func TestFlow2_UC67_FocusBlur(t *testing.T) {
	t.Run("BlurMsg sets focused=false", func(t *testing.T) {
		m := baseModel(nil)
		m.focused = true
		got := drive(m, tea.BlurMsg{})
		if got.focused {
			t.Error("focused should be false after BlurMsg")
		}
	})

	t.Run("FocusMsg sets focused=true", func(t *testing.T) {
		m := baseModel(nil)
		m.focused = false
		got := drive(m, tea.FocusMsg{})
		if !got.focused {
			t.Error("focused should be true after FocusMsg")
		}
	})
}

// UC-68: PasteMsg in viewSearch appends to filter.
func TestFlow2_UC68_PasteMsgSearch(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewSearch
	m.filter.Focus()
	got := drive(m, tea.PasteMsg{Content: "git"})
	if got.filter.Value() != "git" {
		t.Errorf("filter.Value() = %q, want %q", got.filter.Value(), "git")
	}
	if len(got.visibleTools) != 1 {
		t.Errorf("visibleTools = %d, want 1 after paste 'git'", len(got.visibleTools))
	}
}

// UC-69: PasteMsg in viewCommand appends to command input.
func TestFlow2_UC69_PasteMsgCommand(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewCommand
	m.commandInput.Focus()
	got := drive(m, tea.PasteMsg{Content: "sync"})
	if got.commandInput.Value() != "sync" {
		t.Errorf("commandInput.Value() = %q, want %q", got.commandInput.Value(), "sync")
	}
}

// UC-70: Second q returns tea.Quit cmd (non-nil).
func TestFlow2_UC70_SecondQQuits(t *testing.T) {
	m := baseModel(nil)
	m.confirmQuit = true
	_, cmd := m.Update(pressRune('q'))
	if cmd == nil {
		t.Error("second q should return a non-nil quit command")
	}
}

// UC-71: q while hostRequired uses footer confirmation.
func TestFlow2_UC71_QWithHostRequired(t *testing.T) {
	m := baseModel(nil)
	m.hostRequired = true
	got := drive(m, pressRune('q'))
	if !got.confirmQuit {
		t.Fatal("confirmQuit should be true")
	}
	if got.statusMsg != "" {
		t.Errorf("statusMsg = %q, want empty; quit prompt belongs in footer", got.statusMsg)
	}
	if got.quitConfirmKey != "q" {
		t.Errorf("quitConfirmKey = %q, want q", got.quitConfirmKey)
	}
}

func TestConfirmTimeoutClearsQuitConfirmation(t *testing.T) {
	m := baseModel(nil)
	model, _ := m.Update(pressRune('q'))
	got := model.(Model)
	if !got.confirmQuit {
		t.Fatal("first q should arm quit confirmation")
	}
	gen := got.confirmGen

	model, _ = got.Update(confirmTimeoutMsg{gen: gen})
	got = model.(Model)
	if got.confirmQuit {
		t.Fatal("quit confirmation should clear after timeout")
	}
	if got.quitConfirmKey != "" {
		t.Fatalf("quitConfirmKey = %q, want empty after timeout", got.quitConfirmKey)
	}
	if got.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want empty after silent timeout", got.statusMsg)
	}
}

func TestConfirmTimeoutClearsListConfirmationStatus(t *testing.T) {
	tool := &database.ToolCache{Name: "typescript", Provider: "node", Installed: true, Tracked: true, InstalledWith: "npm"}
	m := baseModel([]*database.ToolCache{tool})
	m.effectiveNodeManager = "pnpm"
	cmd := m.armListConfirmation(listConfirmReinstallDefault, tool)
	if cmd == nil {
		t.Fatal("expected confirmation timeout command")
	}
	if m.statusMsg != "" {
		t.Fatalf("row confirmation should not write footer status, got %q", m.statusMsg)
	}

	m.statusMsg = "Press r again to reinstall typescript with default (node)"
	m.handleConfirmTimeoutMsg(confirmTimeoutMsg{gen: m.confirmGen})
	if m.listConfirm.action != "" {
		t.Fatal("list confirmation should clear after timeout")
	}
	if m.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want empty after aborted confirmation timeout", m.statusMsg)
	}
}

func TestConfirmTimeoutClearsAllConfirmationState(t *testing.T) {
	m := baseModel(nil)
	m.confirmGen = 9
	m.confirmQuit = true
	m.quitConfirmKey = "q"
	m.listConfirm = listConfirmation{action: listConfirmDelete, name: "bat", provider: "brew"}
	m.hostDeleteConfirm = true
	m.groupDeleteConfirm = true
	m.dangerConfirmRow = settingsRowResetCache
	m.dotsConfirmIdx = 1
	m.dotsOverwriteIdx = 2
	m.dotsLocalIdx = 3
	m.dotsIgnoreIdx = 4

	cmds := m.handleConfirmTimeoutMsg(confirmTimeoutMsg{gen: 9})
	if len(cmds) != 0 {
		t.Fatalf("confirmation timeout commands = %d, want none", len(cmds))
	}
	if m.hasActiveConfirmation() {
		t.Fatal("confirmation state should be cleared after timeout")
	}
	if m.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want empty after silent timeout", m.statusMsg)
	}
}

func TestGlobalNavigationClearsActiveConfirmations(t *testing.T) {
	tool := &database.ToolCache{Name: "bat", Provider: "brew", Installed: true, Tracked: true}
	cases := []struct {
		name     string
		model    Model
		key      tea.Msg
		wantMode viewMode
	}{
		{
			name: "list delete confirm tab",
			model: func() Model {
				m := baseModel([]*database.ToolCache{tool})
				m.listConfirm = listConfirmation{action: listConfirmDelete, name: "bat", provider: "brew"}
				return m
			}(),
			key:      pressTab(),
			wantMode: viewDots,
		},
		{
			name: "dots ignore confirm tab",
			model: func() Model {
				m := dotsModel()
				m.dotsIgnoreIdx = 0
				return m
			}(),
			key:      pressTab(),
			wantMode: viewGroups,
		},
		{
			name: "settings danger confirm palette",
			model: func() Model {
				m := baseModel(nil)
				m.mode = viewSettings
				m.dangerConfirmRow = settingsRowResetCache
				return m
			}(),
			key:      pressRune(':'),
			wantMode: viewCommand,
		},
		{
			name: "list delete confirm palette",
			model: func() Model {
				m := baseModel([]*database.ToolCache{tool})
				m.listConfirm = listConfirmation{action: listConfirmDelete, name: "bat", provider: "brew"}
				return m
			}(),
			key:      pressRune(':'),
			wantMode: viewCommand,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.model.hasActiveConfirmation() {
				t.Fatal("test setup should start with an active confirmation")
			}
			got := drive(tc.model, tc.key)
			if got.mode != tc.wantMode {
				t.Fatalf("mode = %v, want %v", got.mode, tc.wantMode)
			}
			if got.hasActiveConfirmation() {
				t.Fatalf("confirmation carried across navigation: %+v", got)
			}
		})
	}
}

// ── Group B — Setup Wizard ────────────────────────────────────────────────────

// UC-72: Setup step 2 — all enabled (node enabled) → advance to step 3 (node manager picker).
func TestFlow2_UC72_SetupStep2AllEnabled(t *testing.T) {
	t.Run("all enabled with node Enter advances to step 3 (node manager)", func(t *testing.T) {
		got := drive(setupStep2Model(), pressEnter())
		if got.setupStep != 3 {
			t.Errorf("setupStep = %d, want 3 (node manager step)", got.setupStep)
		}
		// Node manager step does not focus settingsInput.
		if got.settingsInput.Focused() {
			t.Error("settingsInput should NOT be focused at step 3 (node manager)")
		}
	})

	t.Run("some disabled Enter sets loading", func(t *testing.T) {
		// Toggle first provider off then submit.
		got := drive(setupStep2Model(), pressRune(' '), pressEnter())
		if !got.loading {
			t.Error("loading should be true when disabled providers need saving")
		}
	})
}

func TestFlow2_SetupNoHostCopyAndGroups(t *testing.T) {
	t.Run("no-host with existing hosts starts at copy prompt", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		got := drive(m, toolsLoadedMsg{
			noHost: true,
			hostInfo: &app.HostInfo{Hosts: map[string]config.HostAssignment{
				"laptop": {Groups: []string{"base"}},
			}},
			groupNames: []string{"base"},
		})
		if got.mode != viewSetup {
			t.Fatalf("mode = %v, want setup", got.mode)
		}
		if got.setupStep != 7 {
			t.Fatalf("setupStep = %d, want copy prompt", got.setupStep)
		}
	})

	t.Run("copy prompt no continues normal onboarding", func(t *testing.T) {
		got := drive(setupStep7Model(), pressRune('n'))
		if got.setupStep != 2 {
			t.Fatalf("setupStep = %d, want provider step", got.setupStep)
		}
	})

	t.Run("copy prompt yes with one host starts copy", func(t *testing.T) {
		m := setupStep7Model()
		m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{"laptop": {}}}
		got := drive(m, pressEnter())
		if !got.loading {
			t.Fatal("loading should be true while copying the only host")
		}
		if got.setupStep != 7 {
			t.Fatalf("setupStep = %d, want to stay on copy prompt while loading", got.setupStep)
		}
	})

	t.Run("copy prompt yes with multiple hosts opens picker", func(t *testing.T) {
		got := drive(setupStep7Model(), pressEnter())
		if got.setupStep != 8 {
			t.Fatalf("setupStep = %d, want host picker", got.setupStep)
		}
	})

	t.Run("host picker navigates and confirm starts copy", func(t *testing.T) {
		got := drive(setupStep8Model(), pressRune('j'))
		if got.setupCopyHostIdx != 1 {
			t.Fatalf("setupCopyHostIdx = %d, want 1", got.setupCopyHostIdx)
		}
		got = drive(got, pressEnter())
		if !got.loading {
			t.Fatal("loading should be true after confirming host copy")
		}
	})

	t.Run("host copy success finishes onboarding and reloads", func(t *testing.T) {
		got := drive(loadingSetup(8), setupHostCopyDoneMsg{
			source: "laptop",
			target: "testhost",
			info:   &app.HostInfo{Active: "testhost", Hosts: map[string]config.HostAssignment{"testhost": {Groups: []string{"base"}}}},
		})
		if got.mode != viewStatus {
			t.Fatalf("mode = %v, want viewStatus", got.mode)
		}
		if !got.setupComplete || !got.setupReloading || !got.loading {
			t.Fatalf("setup completion flags = complete:%v reloading:%v loading:%v", got.setupComplete, got.setupReloading, got.loading)
		}
		if got.hostRequired {
			t.Fatal("hostRequired should clear after host copy")
		}
	})

	t.Run("group selection toggles and saves", func(t *testing.T) {
		got := drive(setupStep9Model(), pressRune('j'), pressRune(' '))
		if !got.setupGroupDraft["work"] {
			t.Fatalf("work group should be selected: %#v", got.setupGroupDraft)
		}
		got = drive(got, pressEnter())
		if !got.loading {
			t.Fatal("loading should be true while saving selected groups")
		}
	})

	t.Run("group save success finishes onboarding and reloads", func(t *testing.T) {
		got := drive(loadingSetup(9), setupHostGroupsDoneMsg{
			groups: []string{"base", "work"},
			info:   &app.HostInfo{Active: "testhost", Hosts: map[string]config.HostAssignment{"testhost": {Groups: []string{"base", "work"}}}},
		})
		if got.mode != viewStatus {
			t.Fatalf("mode = %v, want viewStatus", got.mode)
		}
		if !got.setupComplete || !got.setupReloading || !got.loading {
			t.Fatalf("setup completion flags = complete:%v reloading:%v loading:%v", got.setupComplete, got.setupReloading, got.loading)
		}
	})
}

// UC-76: Setup step 5 — enter opens file picker (step 6); n/esc skip.
func TestFlow2_UC76_SetupStep5(t *testing.T) {
	t.Run("enter advances to step 6 and shows file picker", func(t *testing.T) {
		got := drive(setupStep5Model(), pressEnter())
		if got.setupStep != 6 {
			t.Errorf("setupStep = %d, want 6 after enter at step 5", got.setupStep)
		}
		if !got.showFilePicker {
			t.Error("showFilePicker should be true after enter at step 5")
		}
	})

	t.Run("y remains a shortcut for dotfile setup", func(t *testing.T) {
		got := drive(setupStep5Model(), pressRune('y'))
		if got.setupStep != 6 {
			t.Errorf("setupStep = %d, want 6 after y shortcut at step 5", got.setupStep)
		}
	})

	t.Run("n sets loading=true", func(t *testing.T) {
		got := drive(setupStep5Model(), pressRune('n'))
		if !got.loading {
			t.Error("loading should be true after n at step 5")
		}
	})

	t.Run("n completes setup after disable dots finishes", func(t *testing.T) {
		m := drive(setupStep5Model(), pressRune('n'))
		got := drive(m, dangerOpDoneMsg{action: "disable-dots", detail: "dots disabled"})
		if got.mode != viewStatus {
			t.Fatalf("mode = %v, want viewStatus after setup dotfiles skip completes", got.mode)
		}
		if got.setupStep != 0 {
			t.Fatalf("setupStep = %d, want reset after setup dotfiles skip completes", got.setupStep)
		}
		if !got.setupComplete {
			t.Fatal("setupComplete should guard the follow-up reload")
		}
		if !got.setupReloading {
			t.Fatal("setupReloading should keep post-onboarding progress visible")
		}
		if got.progressText != "Loading tools…" {
			t.Fatalf("progressText = %q, want post-onboarding reload progress", got.progressText)
		}
		got = drive(got, toolsLoadedMsg{})
		if got.setupReloading {
			t.Fatal("setupReloading should clear after the reload completes")
		}
		if got.progressText != "" {
			t.Fatalf("progressText = %q, want cleared after reload completes", got.progressText)
		}
	})

	t.Run("n advances to group selection when reusable groups exist", func(t *testing.T) {
		m := setupStep5Model()
		m.groupNames = []string{"base", "work"}
		m = drive(m, pressRune('n'))
		got := drive(m, dangerOpDoneMsg{action: "disable-dots", detail: "dots disabled"})
		if got.mode != viewSetup {
			t.Fatalf("mode = %v, want setup", got.mode)
		}
		if got.setupStep != 9 {
			t.Fatalf("setupStep = %d, want reusable group selection", got.setupStep)
		}
		if got.loading {
			t.Fatal("loading should clear before group selection")
		}
	})

	t.Run("post-onboarding reload waits for provider and discovery refresh", func(t *testing.T) {
		m := setupStep5Model()
		m.app = newScanPlanTestApp(t, &scanPlanProvider{name: "brew"})
		m.setupReloading = true
		m.loading = true
		got := drive(m, toolsLoadedMsg{})
		if !got.setupReloading {
			t.Fatal("setupReloading should stay visible while provider refreshes run")
		}
		if got.loading {
			t.Fatal("loading should clear after toolsLoadedMsg; provider refresh owns the wait")
		}
		if !got.scanningProviders["brew"] {
			t.Fatalf("scanningProviders = %v, want brew", got.scanningProviders)
		}

		got = drive(got, providerScannedMsg{gen: got.scanGen, provider: "brew"})
		if !got.setupReloading || !got.providerSnapshotRefreshing || !got.discoveryRefreshing {
			t.Fatalf("setup reload should wait for provider snapshot and discovery refresh, setupReloading=%v providerSnapshotRefreshing=%v discoveryRefreshing=%v", got.setupReloading, got.providerSnapshotRefreshing, got.discoveryRefreshing)
		}
		got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen, discovered: []*database.ToolCache{{Name: "orphan"}}})
		if !got.setupReloading {
			t.Fatal("setupReloading should stay visible after discovery while provider snapshot is pending")
		}
		got = drive(got, allProvidersDoneMsg{gen: got.scanGen})
		if !got.setupReloading {
			t.Fatal("setupReloading should stay visible while discovered tools still need descriptions")
		}
		got = drive(got, descRefreshDoneMsg{gen: got.descRefreshGen})
		if got.setupReloading {
			t.Fatal("setupReloading should clear after provider/discovery/description refreshes finish")
		}
	})

	t.Run("post-onboarding reload waits for description refresh", func(t *testing.T) {
		m := setupStep5Model()
		m.setupReloading = true
		m.loading = true
		got := drive(m, toolsLoadedMsg{tools: []*database.ToolCache{{Name: "git"}}})
		if !got.setupReloading || !got.descRefreshing {
			t.Fatalf("setup reload should wait for descriptions, setupReloading=%v descRefreshing=%v", got.setupReloading, got.descRefreshing)
		}
		if got.progressText != "Refreshing tool descriptions…" {
			t.Fatalf("progressText = %q, want tool description refresh", got.progressText)
		}
		got = drive(got, descRefreshDoneMsg{gen: got.descRefreshGen})
		if got.setupReloading {
			t.Fatal("setupReloading should clear after description refresh finishes")
		}
	})

	t.Run("completed setup does not reopen on stale no-host reload", func(t *testing.T) {
		m := setupStep5Model()
		m.setupComplete = true
		got := drive(m, toolsLoadedMsg{noHost: true})
		if got.mode == viewSetup {
			t.Fatalf("completed setup should not reopen onboarding on stale no-host reload")
		}
		if got.hostRequired {
			t.Fatal("hostRequired should remain false after completed setup reload")
		}
	})

	t.Run("configured dotfiles closes setup and shows reload progress", func(t *testing.T) {
		got := drive(setupStep6Model(), dangerOpDoneMsg{action: "setup-dots", detail: "dots configured"})
		if got.mode != viewStatus {
			t.Fatalf("mode = %v, want viewStatus after setup dotfiles repo completes", got.mode)
		}
		if !got.setupReloading {
			t.Fatal("setupReloading should keep post-onboarding progress visible after dots repo setup")
		}
		if got.progressText != "Loading tools…" {
			t.Fatalf("progressText = %q, want post-onboarding reload progress", got.progressText)
		}
	})

	t.Run("configured dotfiles from dots tab stays on dots during reload", func(t *testing.T) {
		m := setupStep6Model()
		m.setupBackgroundMode = viewDots
		got := drive(m, dangerOpDoneMsg{action: "setup-dots", detail: "dots configured"})
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots after dotfiles setup from Dots tab", got.mode)
		}
		if got.setupBackgroundMode != viewDots {
			t.Fatalf("setupBackgroundMode = %v, want viewDots reload target", got.setupBackgroundMode)
		}
		if !got.setupReloading {
			t.Fatal("setupReloading should keep post-onboarding progress visible on Dots tab")
		}
		got = drive(got, toolsLoadedMsg{})
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots after dotfiles setup reload", got.mode)
		}
	})

	t.Run("configured dotfiles advances to group selection when reusable groups exist", func(t *testing.T) {
		m := setupStep6Model()
		m.groupNames = []string{"base"}
		got := drive(m, dangerOpDoneMsg{action: "setup-dots", detail: "dots configured"})
		if got.mode != viewSetup {
			t.Fatalf("mode = %v, want setup", got.mode)
		}
		if got.setupStep != 9 {
			t.Fatalf("setupStep = %d, want group selection", got.setupStep)
		}
	})

	t.Run("group selection after dotfiles setup from dots tab returns to dots", func(t *testing.T) {
		m := setupStep9Model()
		m.setupBackgroundMode = viewDots
		got := drive(m, setupHostGroupsDoneMsg{
			groups: []string{"base"},
			info:   &app.HostInfo{Active: "testhost", Hosts: map[string]config.HostAssignment{"testhost": {Groups: []string{"base"}}}},
		})
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots after group selection from Dots setup", got.mode)
		}
		if got.setupBackgroundMode != viewDots {
			t.Fatalf("setupBackgroundMode = %v, want viewDots reload target", got.setupBackgroundMode)
		}
		got = drive(got, toolsLoadedMsg{})
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots after group-selection reload", got.mode)
		}
	})

	t.Run("loading setup ignores dotfile choices until host creation finishes", func(t *testing.T) {
		m := setupStep5Model()
		m.loading = true
		got := drive(m, pressEnter())
		if got.showFilePicker {
			t.Fatal("dotfile repo picker should not open while automatic host creation is still loading")
		}
		if got.setupStep != 5 {
			t.Fatalf("setupStep = %d, want to stay on dotfile decision", got.setupStep)
		}
	})
}

// UC-77: Setup step 6 — Esc (no file picker) → loading=true.
func TestFlow2_UC77_SetupStep6Esc(t *testing.T) {
	got := drive(setupStep6Model(), pressEsc())
	if !got.loading {
		t.Error("loading should be true after Esc at step 6 with no file picker")
	}
}

// UC-78: setupImportDoneMsg (no node providers) → auto-create host.
func TestFlow2_UC78_SetupImportDoneMsg(t *testing.T) {
	// loadingSetup(1) has no setupProviders, so node is not enabled → creates host.
	m := loadingSetup(1)
	m.allTools = []*database.ToolCache{{Name: "snapshot", Provider: "brew"}}
	got := drive(m, setupImportDoneMsg{added: 2})
	if got.setupStep == 4 {
		t.Fatalf("setupStep = %d, host confirmation screen should not be shown", got.setupStep)
	}
	if got.setupStep != 5 {
		t.Fatalf("setupStep = %d, want dotfile decision while host is created", got.setupStep)
	}
	if !got.loading {
		t.Error("loading should be true while creating the host automatically")
	}
	if got.settingsInput.Focused() {
		t.Error("settingsInput should not be focused during automatic host creation")
	}
	if len(got.allTools) != 1 || got.allTools[0].Name != "snapshot" {
		t.Fatalf("allTools changed before onboarding finished: %+v", got.allTools)
	}
}

// UC-79: setupProvidersDoneMsg (no node providers) → auto-create host.
func TestFlow2_UC79_SetupProvidersDoneMsg(t *testing.T) {
	// loadingSetup(2) has no setupProviders, so node is not enabled → creates host.
	got := drive(loadingSetup(2), setupProvidersDoneMsg{})
	if got.setupStep == 4 {
		t.Fatalf("setupStep = %d, host confirmation screen should not be shown", got.setupStep)
	}
	if got.setupStep != 5 {
		t.Fatalf("setupStep = %d, want dotfile decision while host is created", got.setupStep)
	}
	if !got.loading {
		t.Error("loading should be true while creating the host automatically")
	}
	if got.settingsInput.Focused() {
		t.Error("settingsInput should not be focused during automatic host creation")
	}
}

// UC-80: setupHostDoneMsg → step 5, status ✓.
func TestFlow2_UC80_SetupHostDoneMsg(t *testing.T) {
	info := &app.HostInfo{
		Active: "myhost",
		Hosts:  map[string]config.HostAssignment{"myhost": {}},
	}
	got := drive(loadingSetup(4), setupHostDoneMsg{hostName: "myhost", info: info})
	if got.setupStep != 5 {
		t.Errorf("setupStep = %d, want 5", got.setupStep)
	}
	if got.loading {
		t.Error("loading should be false after setupHostDoneMsg")
	}
	if got.hostInfo == nil || got.hostInfo.Active != "myhost" {
		t.Fatalf("hostInfo = %#v, want refreshed myhost info", got.hostInfo)
	}
	if got.hostRequired {
		t.Fatal("hostRequired should clear after setup host succeeds")
	}
	// Status is set via setStatus (async cmd), but the statusMsg may not be
	// populated synchronously. Check that loading is cleared and step advanced.
}

func TestFlow2_SetupErrorsStayOnCurrentStep(t *testing.T) {
	setupErr := errors.New("setup failed")

	t.Run("import failure stays on import step", func(t *testing.T) {
		m := loadingSetup(1)
		m.setupProviders = []app.SetupProviderOption{{Name: "node", Enabled: true}}

		got := drive(m, setupImportDoneMsg{err: setupErr})

		if got.setupStep != 1 {
			t.Fatalf("setupStep = %d, want 1", got.setupStep)
		}
		if got.loading {
			t.Fatal("loading should clear after import failure")
		}
	})

	t.Run("provider save failure stays on provider step", func(t *testing.T) {
		got := drive(loadingSetup(2), setupProvidersDoneMsg{err: setupErr})

		if got.setupStep != 2 {
			t.Fatalf("setupStep = %d, want 2", got.setupStep)
		}
		if got.loading {
			t.Fatal("loading should clear after provider failure")
		}
	})

	t.Run("node manager failure stays on manager step", func(t *testing.T) {
		got := drive(loadingSetup(3), setupNodeMgrDoneMsg{err: setupErr})

		if got.setupStep != 3 {
			t.Fatalf("setupStep = %d, want 3", got.setupStep)
		}
		if got.loading {
			t.Fatal("loading should clear after node manager failure")
		}
	})

	t.Run("host creation failure returns to retry step", func(t *testing.T) {
		m := setupStep3Model()
		var cmds []tea.Cmd
		m.startSetupHostCreation(&cmds)
		if m.setupStep != 5 {
			t.Fatalf("setupStep = %d after starting host creation, want 5", m.setupStep)
		}

		got := drive(m, setupHostDoneMsg{err: setupErr})

		if got.setupStep != 3 {
			t.Fatalf("setupStep = %d, want 3", got.setupStep)
		}
		if got.loading {
			t.Fatal("loading should clear after host failure")
		}
	})
}

// ── Group C — File Picker ─────────────────────────────────────────────────────

// UC-81: Esc with file picker open → close picker.
func TestFlow2_UC81_FilePickerEscClosesPicker(t *testing.T) {
	m := baseModel(nil)
	m.showFilePicker = true
	got := drive(m, pressEsc())
	if got.showFilePicker {
		t.Error("showFilePicker should be false after Esc")
	}
}

// UC-82: Pick path in settings → saves DotsRepo.
func TestFlow2_UC82_FilePickerPickSettings(t *testing.T) {
	tmp := t.TempDir()
	m := baseModel(nil)
	m.mode = viewSettings
	m.openFilePicker("Dots repo path", tmp, false)
	got := drive(m, pressEnter())
	if got.showFilePicker {
		t.Error("showFilePicker should be false after picking path in settings")
	}
	if got.settings.DotsRepo != "" {
		t.Errorf("settings.DotsRepo = %q, want unchanged until app result", got.settings.DotsRepo)
	}
	if !got.dotsLoading {
		t.Error("dotsLoading should be true after saving DotsRepo")
	}
}

// UC-83: Pick path in setup → loading=true.
func TestFlow2_UC83_FilePickerPickSetup(t *testing.T) {
	tmp := t.TempDir()
	m := setupStep6Model()
	m.openFilePicker("Dots repo path", tmp, false)
	got := drive(m, pressEnter())
	if got.showFilePicker {
		t.Error("showFilePicker should be false after picking in setup")
	}
	if !got.loading {
		t.Error("loading should be true after picking dots repo in setup")
	}
}

// ── Group D — Dots Tab ────────────────────────────────────────────────────────

// UC-84: R key → dotsLoading=true.
func TestFlow2_UC84_DotsRefreshKey(t *testing.T) {
	got := drive(dotsModel(), pressRune('R'))
	if !got.dotsLoading {
		t.Error("dotsLoading should be true after R (refresh) key in dots tab")
	}
}

// UC-85: R with IsRepeat → no-op (dotsLoading stays false).
func TestFlow2_UC85_DotsRefreshKeyRepeat(t *testing.T) {
	got := drive(dotsModel(), tea.KeyPressMsg{Code: 'R', Text: "R", IsRepeat: true})
	if got.dotsLoading {
		t.Error("dotsLoading should stay false when R is a repeated key press")
	}
}

// UC-85b: D key is no longer bound in dots tab (was discover, now R=refresh).
func TestFlow2_UC85b_DotsOldDiscoverKeyNoop(t *testing.T) {
	got := drive(dotsModel(), pressRune('D'))
	if got.dotsLoading {
		t.Error("D should not trigger any dots operation (discover moved to R)")
	}
}

// ── Group E — Settings Tab ────────────────────────────────────────────────────

// UC-89: Commit Changes row toggles AutoCommit (only when AutoPush=false).
func TestFlow2_UC89_SettingsAutoCommit(t *testing.T) {
	t.Run("AutoPush=false: toggles AutoCommit", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowDotsCommit)...)
		msgs = append(msgs, pressRune(' '))
		got := drive(baseModel(nil), msgs...)
		if !got.settings.DotsGit.AutoCommit {
			t.Error("AutoCommit should be true after toggle when AutoPush=false")
		}
	})

	t.Run("AutoPush=true: no toggle", func(t *testing.T) {
		m := baseModel(nil)
		m.settings.DotsGit.AutoPush = true
		msgs := append(toSettings(), nj(settingsRowDotsCommit)...)
		msgs = append(msgs, pressRune(' '))
		got := drive(m, msgs...)
		if got.settings.DotsGit.AutoCommit {
			t.Error("AutoCommit should stay false when AutoPush=true")
		}
	})
}

// UC-90: Push Changes row toggles AutoPush.
func TestFlow2_UC90_SettingsAutoPush(t *testing.T) {
	msgs := append(toSettings(), nj(settingsRowDotsPush)...)
	msgs = append(msgs, pressRune(' '))
	got := drive(baseModel(nil), msgs...)
	if !got.settings.DotsGit.AutoPush {
		t.Error("AutoPush should be true after toggling Push Changes")
	}
}

// UC-91: Danger row 11: second Enter fires doResetSettings (loading=true).
func TestFlow2_UC91_DangerResetSettings(t *testing.T) {
	msgs := append(toSettings(), nj(settingsRowResetSettings)...)
	msgs = append(msgs, pressEnter()) // Enter arms danger confirmation
	msgs = append(msgs, pressEnter()) // second Enter: fires reset
	got := drive(baseModel(nil), msgs...)
	if !got.loading {
		t.Error("loading should be true after confirming reset settings")
	}
	if got.dangerConfirmRow != -1 {
		t.Errorf("dangerConfirmRow = %d, want -1 after confirmation", got.dangerConfirmRow)
	}
}

// UC-92: Danger row 12: second Enter fires doResetCache (loading=true).
func TestFlow2_UC92_DangerResetCache(t *testing.T) {
	msgs := append(toSettings(), nj(settingsRowResetCache)...)
	msgs = append(msgs, pressEnter()) // Enter arms danger confirmation
	msgs = append(msgs, pressEnter()) // second Enter: fires reset
	got := drive(baseModel(nil), msgs...)
	if !got.loading {
		t.Error("loading should be true after confirming reset cache")
	}
}

// UC-93: Dots sync row: keep-local choice fires.
func TestFlow2_UC93_DangerDisableDots(t *testing.T) {
	got := drive(openSettingsDotsSyncChoice(t), pressRune('y')) // fires doDisableDots keeping local files
	if !got.loading {
		t.Error("loading should be true after confirming disable dots")
	}
	if got.dangerConfirmRow != -1 {
		t.Errorf("dangerConfirmRow = %d, want -1", got.dangerConfirmRow)
	}
}

// UC-94: Provider-order editor — K swaps item up.
func TestFlow2_UC94_PriorityEditorKSwap(t *testing.T) {
	// Navigate to the provider-order row, open it, move cursor down once,
	// then K to swap up (moving item 1 to position 0).
	msgs := append(toSettings(), nj(settingsRowProviderPriority)...)
	msgs = append(msgs, pressEnter())                          // opens priority editor
	msgs = append(msgs, pressRune('j'))                        // move cursor to index 1
	msgs = append(msgs, tea.KeyPressMsg{Code: 'K', Text: "K"}) // swap up
	got := drive(baseModel(nil), msgs...)
	if !got.editingPriority {
		t.Error("editingPriority should still be true")
	}
	if got.priorityCursor != 0 {
		t.Errorf("priorityCursor = %d, want 0 after K swap", got.priorityCursor)
	}
	// The item that was at index 1 should now be at index 0.
	if len(got.priorityDraft) < 2 {
		t.Fatal("priorityDraft should have at least 2 items")
	}
	// Default draft is [brew, apt, apk, ...]. After j (cursor=1) and K (swap
	// up, cursor=0), draft[0] is the item originally at index 1 (apt).
	original := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip"}
	if got.priorityDraft[0] != original[1] {
		t.Errorf("priorityDraft[0] = %q, want %q (swapped up)", got.priorityDraft[0], original[1])
	}
}

// ── Group F — Hosts Tab ────────────────────────────────────────────────────

// UC-95: Up at top of groups → back to hosts section.
func TestFlow2_UC95_GroupSectionUpAtTop(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 0
	got := drive(m, pressRune('k'))
	if got.assignmentSection != 0 {
		t.Errorf("assignmentSection = %d, want 0 after k at top of groups", got.assignmentSection)
	}
}

// UC-97: Down at last host → advances to group section.
func TestFlow2_UC97_HostSectionDownAtLast(t *testing.T) {
	m := hostsModel()
	// 2 hosts (alpha, beta), cursor at last index = 1
	m.hostCursor = 1
	got := drive(m, pressRune('j'))
	if got.assignmentSection != 1 {
		t.Errorf("assignmentSection = %d, want 1 after j at last host", got.assignmentSection)
	}
	if got.groupCursor != 0 {
		t.Errorf("groupCursor = %d, want 0 after entering group section", got.groupCursor)
	}
}

// UC-98: Down at last group wraps to top of hosts section.
func TestFlow2_UC98_GroupSectionDownAtLast(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	allGroupNames := buildAllGroupNames(m.groupNames)
	m.groupCursor = len(allGroupNames) - 1
	got := drive(m, pressRune('j'))
	if got.assignmentSection != 0 {
		t.Errorf("assignmentSection = %d, want 0 (wrapped to hosts)", got.assignmentSection)
	}
	if got.hostCursor != 0 {
		t.Errorf("hostCursor = %d, want 0 (wrapped to top)", got.hostCursor)
	}
}

// UC-99: r key → hostRenameMode=true (only when a host is selected).
func TestFlow2_UC99_HostRenameKey(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 0
	m.hostCursor = 0
	got := drive(m, pressRune('r'))
	if !got.hostRenameMode {
		t.Error("hostRenameMode should be true after r key with a host selected")
	}
	if got.settingsInput.Value() != "alpha" {
		t.Fatalf("rename input = %q, want alpha", got.settingsInput.Value())
	}
}

func TestFlow2_HostRowSpaceActivatesHighlightedHost(t *testing.T) {
	m := hostsModel()
	key := toolKey("ripgrep", "system")
	m.hostInfo.Active = "alpha"
	m.hostInfo.Hosts["beta"] = config.HostAssignment{Groups: []string{"personal"}, Ignore: []string{"fd"}}
	m.toolMemberships = map[string][]string{key: {"work", "personal"}}
	m.toolGroups = app.ToolGroupLabelsForHost(m.toolMemberships, m.hostInfo, shortHostname())
	m.ignoreLabels = map[string]string{"old": "host"}
	m.hostCursor = 1
	m.groupFilter = "work"
	m.groupTabIdx = 1

	tm, cmd := m.Update(pressRune(' '))
	got := tm.(Model)
	if cmd == nil {
		t.Fatal("host activation should dispatch persistence command")
	}
	got = drive(got, hostCopiedMsg{host: "beta", info: &app.HostInfo{
		Active: "beta",
		Hosts: map[string]config.HostAssignment{
			"alpha": {Groups: []string{"work"}},
			"beta":  {Groups: []string{"personal"}, Ignore: []string{"fd"}},
		},
	}})

	if got.hostInfo.Active != "beta" {
		t.Fatalf("active host = %q, want beta", got.hostInfo.Active)
	}
	if got.groupFilter != "" || got.groupTabIdx != 0 {
		t.Fatalf("group filter = %q/%d, want cleared", got.groupFilter, got.groupTabIdx)
	}
	if got.toolGroups[key] != "personal" {
		t.Fatalf("toolGroups[%q] = %q, want personal", key, got.toolGroups[key])
	}
	if !got.ignoreSet["fd"] {
		t.Fatalf("ignoreSet = %v, want fd ignored", got.ignoreSet)
	}
	if _, ok := got.ignoreLabels["old"]; ok {
		t.Fatalf("old host ignore label should be removed: %v", got.ignoreLabels)
	}
}

func TestFlow2_HostRowEnterDoesNotActivate(t *testing.T) {
	m := hostsModel()
	m.hostInfo.Active = "alpha"
	m.hostCursor = 1

	got := drive(m, pressEnter())

	if got.hostInfo.Active != "alpha" {
		t.Fatalf("active host = %q, want alpha", got.hostInfo.Active)
	}
}

// UC-100: hostRenameMode Enter/Esc.
func TestFlow2_UC100_HostRenameMode(t *testing.T) {
	t.Run("Enter clears hostRenameMode", func(t *testing.T) {
		m := hostsModel()
		m.hostRenameMode = true
		m.hostRenameName = "alpha"
		m.settingsInput.SetValue("renamed")
		got := drive(m, pressEnter())
		if got.hostRenameMode {
			t.Error("hostRenameMode should be false after Enter")
		}
		if got.hostRenameName != "" {
			t.Fatalf("hostRenameName = %q, want cleared", got.hostRenameName)
		}
	})

	t.Run("Esc clears hostRenameMode", func(t *testing.T) {
		m := hostsModel()
		m.hostRenameMode = true
		m.hostRenameName = "alpha"
		got := drive(m, pressEsc())
		if got.hostRenameMode {
			t.Error("hostRenameMode should be false after Esc")
		}
		if got.hostRenameName != "" {
			t.Fatalf("hostRenameName = %q, want cleared", got.hostRenameName)
		}
	})
}

func TestFlow2_HostRenameUsesCapturedHostAfterCursorMoves(t *testing.T) {
	a := newHostsFlowApp(t)
	m := hostsModel()
	m.app = a
	m.ctx = context.Background()
	m.hostCursor = 0

	tm, _ := m.Update(pressRune('r'))
	renaming := tm.(Model)
	renaming.hostCursor = 1
	renaming.settingsInput.SetValue("renamed-alpha")

	tm, cmd := renaming.Update(pressEnter())
	got := tm.(Model)
	if got.hostRenameMode || got.hostRenameName != "" {
		t.Fatalf("rename state not cleared: mode=%v name=%q", got.hostRenameMode, got.hostRenameName)
	}
	if cmd == nil {
		t.Fatal("rename command missing")
	}
	msg := cmd()
	changed, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("rename command returned %T", msg)
	}
	if changed.err != nil {
		t.Fatalf("rename command error: %v", changed.err)
	}
	info, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if _, ok := info.Hosts["renamed-alpha"]; !ok {
		t.Fatalf("renamed-alpha missing after rename: %+v", info.Hosts)
	}
	if _, ok := info.Hosts["alpha"]; ok {
		t.Fatalf("alpha still present after rename: %+v", info.Hosts)
	}
	if _, ok := info.Hosts["beta"]; !ok {
		t.Fatalf("beta should not be renamed: %+v", info.Hosts)
	}
}

// UC-101: g key → hostEditMode=1 (edit group picker).
func TestFlow2_UC101_EditHostGroups(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 0
	m.hostCursor = 0 // "alpha" host
	got := drive(m, pressRune('g'))
	if got.hostEditMode != 1 {
		t.Errorf("hostEditMode = %d, want 1 after g key", got.hostEditMode)
	}
	if got.hostGroupPicker == nil {
		t.Error("hostGroupPicker should not be nil after g key")
	}
	if !slices.Contains(got.hostGroupDraft, "work") {
		t.Fatalf("hostGroupDraft = %v, want work checked", got.hostGroupDraft)
	}
	if got.hostEditName != "alpha" {
		t.Fatalf("hostEditName = %q, want alpha", got.hostEditName)
	}
}

func TestFlow2_EditHostGroupsUsesRenderedActiveHostOrder(t *testing.T) {
	m := hostsModel()
	m.hostInfo.Active = "beta"
	m.assignmentSection = 0
	m.hostCursor = 0 // rendered first because beta is active

	got := drive(m, pressRune('g'))

	if got.hostEditMode != 1 {
		t.Fatalf("hostEditMode = %d, want 1 after g key", got.hostEditMode)
	}
	if got.hostEditName != "beta" {
		t.Fatalf("hostEditName = %q, want beta", got.hostEditName)
	}
	if !slices.Contains(got.hostGroupDraft, "personal") {
		t.Fatalf("hostGroupDraft = %v, want personal checked", got.hostGroupDraft)
	}
}

func TestFlow2_HostGroupEditUsesCapturedHostAfterCursorMoves(t *testing.T) {
	a := newHostsFlowApp(t)
	m := hostsModel()
	m.app = a
	m.ctx = context.Background()
	m.assignmentSection = 0
	m.hostCursor = 0

	tm, _ := m.Update(pressRune('g'))
	editing := tm.(Model)
	editing.hostCursor = 1
	editing.hostGroupDraft = []string{"work", "personal"}

	tm, cmd := editing.Update(pressEnter())
	got := tm.(Model)
	if got.hostEditMode != 0 || got.hostEditName != "" {
		t.Fatalf("host edit state not cleared: mode=%d name=%q", got.hostEditMode, got.hostEditName)
	}
	if cmd == nil {
		t.Fatal("host group save command missing")
	}
	msg := cmd()
	changed, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("host group command returned %T", msg)
	}
	if changed.err != nil {
		t.Fatalf("host group command error: %v", changed.err)
	}
	info, err := a.HostStatus()
	if err != nil {
		t.Fatalf("HostStatus: %v", err)
	}
	if !slices.Contains(info.Hosts["alpha"].Groups, "personal") {
		t.Fatalf("alpha groups = %v, want personal added", info.Hosts["alpha"].Groups)
	}
	if slices.Contains(info.Hosts["beta"].Groups, "personal") {
		t.Fatalf("beta groups unexpectedly changed: %v", info.Hosts["beta"].Groups)
	}
}

// UC-102: h no longer opens the legacy host→host mapping editor.
func TestFlow2_UC102_HDoesNotOpenLegacyHostMapping(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 0
	m.hostCursor = 0 // "alpha" has myhost
	got := drive(m, pressRune('h'))
	if got.hostEditMode != 0 {
		t.Errorf("hostEditMode = %d, want 0 after h key", got.hostEditMode)
	}
	if got.hostEditName != "" {
		t.Fatalf("hostEditName = %q, want empty", got.hostEditName)
	}
}

// UC-103: host group picker j/k/Space/Enter/Esc.
func TestFlow2_UC103_HostGroupPickerNavigation(t *testing.T) {
	t.Run("j moves picker cursor", func(t *testing.T) {
		m := hostsModel()
		m.hostEditMode = 1
		m.hostGroupPicker = []string{"work", "personal"}
		m.hostGroupIdx = 0
		got := drive(m, pressRune('j'))
		if got.hostGroupIdx != 1 {
			t.Errorf("hostGroupIdx = %d, want 1 after j", got.hostGroupIdx)
		}
	})

	t.Run("k moves picker cursor up", func(t *testing.T) {
		m := hostsModel()
		m.hostEditMode = 1
		m.hostGroupPicker = []string{"work", "personal"}
		m.hostGroupIdx = 1
		got := drive(m, pressRune('k'))
		if got.hostGroupIdx != 0 {
			t.Errorf("hostGroupIdx = %d, want 0 after k", got.hostGroupIdx)
		}
	})

	t.Run("space toggles draft group", func(t *testing.T) {
		m := hostsModel()
		m.hostEditMode = 1
		m.hostGroupPicker = []string{"work", "personal"}
		m.hostGroupDraft = []string{"work"}
		m.hostGroupIdx = 1
		got := drive(m, pressRune(' '))
		if !slices.Contains(got.hostGroupDraft, "personal") {
			t.Fatalf("hostGroupDraft = %v, want personal checked", got.hostGroupDraft)
		}
	})

	t.Run("Esc clears mode and picker", func(t *testing.T) {
		m := hostsModel()
		m.hostEditMode = 1
		m.hostGroupPicker = []string{"work", "personal"}
		got := drive(m, pressEsc())
		if got.hostEditMode != 0 {
			t.Errorf("hostEditMode = %d, want 0 after Esc", got.hostEditMode)
		}
		if got.hostGroupPicker != nil {
			t.Error("hostGroupPicker should be nil after Esc")
		}
	})

	t.Run("Enter clears mode and picker", func(t *testing.T) {
		m := hostsModel()
		m.hostEditMode = 1
		m.hostGroupPicker = []string{"personal"}
		m.hostGroupDraft = []string{"personal"}
		m.hostGroupIdx = 0
		m.hostCursor = 0 // alpha
		got := drive(m, pressEnter())
		if got.hostEditMode != 0 {
			t.Errorf("hostEditMode = %d, want 0 after Enter", got.hostEditMode)
		}
		if got.hostGroupPicker != nil {
			t.Error("hostGroupPicker should be nil after Enter")
		}
	})
}

// UC-104: n in assignmentSection=1 → groupCreating=true.
func TestFlow2_UC104_NewGroupCreating(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	got := drive(m, pressRune('n'))
	if !got.groupCreating {
		t.Error("groupCreating should be true after n in section 1")
	}
	if !got.settingsInput.Focused() {
		t.Error("settingsInput should be focused after n")
	}
}

func TestFlow2_HostCreationRemovedFromGroupSection(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	got := drive(m, pressRune('p'))
	if got.groupCreating {
		t.Error("groupCreating should stay false after p in section 1")
	}
	if got.hostEditMode != 0 || got.hostRenameMode {
		t.Fatalf("p should not start a Hosts tab edit mode: hostEditMode=%d hostRename=%v", got.hostEditMode, got.hostRenameMode)
	}
}

func TestFlow2_GroupCreationAvailableFromHostSection(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 0
	got := drive(m, pressRune('n'))
	if !got.groupCreating {
		t.Error("groupCreating should be true after n in section 0")
	}
	if got.assignmentSection != 1 {
		t.Errorf("assignmentSection = %d, want 1 after starting group creation", got.assignmentSection)
	}
}

// UC-105: d on host group (index 0) is blocked.
func TestFlow2_UC105_DeleteHostGroupBlocked(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 0
	got := drive(m, pressRune('d'))
	if got.groupDeleteConfirm {
		t.Error("groupDeleteConfirm should not be set for host group (index 0)")
	}
}

// UC-106: r on host group is blocked.
func TestFlow2_UC106_RenameHostGroupBlocked(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 0
	got := drive(m, pressRune('r'))
	if got.groupRenameMode {
		t.Error("groupRenameMode should not be set for host group (index 0)")
	}
}

func TestFlow2_GroupRenameNegativeCursorIsNoop(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = -1
	got := drive(m, pressRune('r'))
	if got.groupRenameMode {
		t.Error("groupRenameMode should not be set for negative group cursor")
	}
	if got.groupRenameName != "" {
		t.Fatalf("groupRenameName = %q, want empty", got.groupRenameName)
	}
}

func TestFlow2_GroupAfterHostCanBeDeletedAndRenamed(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	m := hostsModel()
	m.groupNames = []string{"apps", "work"}
	m.assignmentSection = 1
	m.groupCursor = 1 // "apps"; host group is index 0.

	deleteGot := drive(m, pressRune('d'))
	if !deleteGot.groupDeleteConfirm {
		t.Error("groupDeleteConfirm should be set for reusable group after host")
	}

	renameGot := drive(m, pressRune('r'))
	if !renameGot.groupRenameMode {
		t.Error("groupRenameMode should be set for reusable group after host")
	}
	if renameGot.settingsInput.Value() != "apps" {
		t.Errorf("rename input = %q, want apps", renameGot.settingsInput.Value())
	}
	if renameGot.groupRenameName != "apps" {
		t.Errorf("groupRenameName = %q, want apps", renameGot.groupRenameName)
	}
}

func TestFlow2_GroupRenameUsesCapturedGroupAfterCursorMoves(t *testing.T) {
	a := newHostsFlowApp(t)
	m := hostsModel()
	m.app = a
	m.ctx = context.Background()
	m.groupNames = []string{"apps", "work"}
	m.assignmentSection = 1
	m.groupCursor = 1

	tm, _ := m.Update(pressRune('r'))
	renaming := tm.(Model)
	renaming.groupCursor = 2
	renaming.settingsInput.SetValue("renamed-apps")

	tm, cmd := renaming.Update(pressEnter())
	got := tm.(Model)
	if got.groupRenameMode || got.groupRenameName != "" {
		t.Fatalf("rename state not cleared: mode=%v name=%q", got.groupRenameMode, got.groupRenameName)
	}
	if cmd == nil {
		t.Fatal("rename command missing")
	}
	msg := cmd()
	changed, ok := msg.(groupChangedMsg)
	if !ok {
		t.Fatalf("rename command returned %T", msg)
	}
	if changed.err != nil {
		t.Fatalf("rename command error: %v", changed.err)
	}
	cfg, err := config.Load(a.ConfigPath)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if findTestGroup(cfg, "renamed-apps") == nil {
		t.Fatalf("renamed-apps group missing: %+v", cfg.Groups)
	}
	if findTestGroup(cfg, "apps") != nil {
		t.Fatalf("apps group still present: %+v", cfg.Groups)
	}
	if findTestGroup(cfg, "work") == nil {
		t.Fatalf("work should not be renamed: %+v", cfg.Groups)
	}
}

func TestFlow2_GroupDeletePopupOffersMoveOrDeleteTools(t *testing.T) {
	m := hostsModel()
	m.groupNames = []string{"work"}
	m.toolMemberships = map[string][]string{toolKey("ripgrep", "brew"): {"work"}}
	m.assignmentSection = 1
	m.groupCursor = 1 // work, after host

	got := drive(m, pressRune('d'))
	if !got.groupDeleteConfirm {
		t.Fatal("groupDeleteConfirm should be set")
	}
	if got.groupDeleteChoice != 0 {
		t.Fatalf("groupDeleteChoice = %d, want move-to-host default", got.groupDeleteChoice)
	}
	view := got.viewString()
	if !strings.Contains(view, "Move last-membership tools to this host") || !strings.Contains(view, "Delete last-membership logical tools") {
		t.Fatalf("delete popup missing choices: %q", view)
	}

	got = drive(got, pressRune('j'))
	if got.groupDeleteChoice != 1 {
		t.Fatalf("groupDeleteChoice = %d, want delete-tools choice", got.groupDeleteChoice)
	}

	tm, cmd := got.Update(pressEnter())
	got = tm.(Model)
	if got.groupDeleteConfirm {
		t.Fatal("groupDeleteConfirm should clear after confirming choice")
	}
	if cmd == nil {
		t.Fatal("confirming group delete choice should dispatch command")
	}
}

func TestFlow2_GroupDeletePopupSkipsMoveChoiceForEmptyGroup(t *testing.T) {
	m := hostsModel()
	m.groupNames = []string{"work"}
	m.assignmentSection = 1
	m.groupCursor = 1 // work, after host

	got := drive(m, pressRune('d'))
	if !got.groupDeleteConfirm {
		t.Fatal("groupDeleteConfirm should be set")
	}
	view := got.viewString()
	if strings.Contains(view, "Move last-membership tools") || strings.Contains(view, "Delete last-membership logical tools") {
		t.Fatalf("empty group delete popup should not ask where to move contents: %q", view)
	}
	if !strings.Contains(view, "No tools or dotfiles belong to this group") {
		t.Fatalf("empty group delete popup should explain no contents: %q", view)
	}

	got = drive(got, pressRune('j'))
	if got.groupDeleteChoice != 0 {
		t.Fatalf("empty group delete choice should not move, got %d", got.groupDeleteChoice)
	}
}

func newHostsFlowApp(t *testing.T) *app.App {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: "myhost", Special: "host"},
			{Name: "alpha", Special: "host"},
			{Name: "beta", Special: "host"},
			{Name: "apps"},
			{Name: "work"},
			{Name: "personal"},
		},
		Hosts: map[string][]string{"myhost": {"apps"}, "alpha": {"apps"}, "beta": {"work"}},
	}); err != nil {
		t.Fatalf("saveTUIConfig: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestFlow2_HostGroupBlockedBeforeReusableGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	m := hostsModel()
	m.groupNames = []string{"apps", "work"}
	m.assignmentSection = 1
	m.groupCursor = 0 // host group.

	deleteGot := drive(m, pressRune('d'))
	if deleteGot.groupDeleteConfirm {
		t.Error("groupDeleteConfirm should not be set for host group")
	}

	renameGot := drive(m, pressRune('r'))
	if renameGot.groupRenameMode {
		t.Error("groupRenameMode should not be set for host group")
	}
}

// UC-109: t in group section → opens group tools editor.
func TestFlow2_UC109_GroupSectionToolsKey(t *testing.T) {
	m := hostsModel()
	m.allTools = []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Tracked: true},
	}
	m.toolMemberships = map[string][]string{toolKey("ripgrep", "system"): {"work"}}
	m.assignmentSection = 1
	allGroupNames := buildAllGroupNames(m.groupNames)
	for i, name := range allGroupNames {
		if name == "work" {
			m.groupCursor = i
			break
		}
	}
	got := drive(m, pressRune('t'))
	if got.groupToolsEditor.group != "work" {
		t.Errorf("groupToolsEditor.group = %q, want %q", got.groupToolsEditor.group, "work")
	}
	if got.mode != viewGroupTools {
		t.Errorf("mode = %v, want viewGroupTools", got.mode)
	}
}

func TestFlow2_GroupToolsKeyIgnoredOnHostSection(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroups
	m.assignmentSection = 0
	m.groupCursor = 1
	m.allTools = []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Tracked: true},
	}
	got := drive(m, pressRune('t'))
	if got.mode != viewGroups {
		t.Fatalf("mode = %v, want viewGroups", got.mode)
	}
	if got.groupToolsEditor.group != "" {
		t.Fatalf("group tools editor opened for host section group %q", got.groupToolsEditor.group)
	}
}

func TestFlow2_GroupDotsEditorOpensFromGroupRow(t *testing.T) {
	m := hostsModel()
	m.dotMemberships = map[string][]string{"nvim": {"base"}, "zsh": {"work"}}
	m.assignmentSection = 1
	allGroupNames := buildAllGroupNames(m.groupNames)
	for i, name := range allGroupNames {
		if name == "work" {
			m.groupCursor = i
			break
		}
	}
	got := drive(m, pressRune('f'))
	if got.groupDotsEditor.group != "work" {
		t.Errorf("groupDotsEditor.group = %q, want %q", got.groupDotsEditor.group, "work")
	}
	if got.mode != viewGroupDots {
		t.Errorf("mode = %v, want viewGroupDots", got.mode)
	}
	if got.groupDotsEditor.membership["nvim"] {
		t.Fatal("nvim should start disabled for work")
	}
	if !got.groupDotsEditor.membership["zsh"] {
		t.Fatal("zsh should start enabled for work")
	}
}

func TestFlow2_GroupToolsEditorStagesMembershipIgnoreAndFilters(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupTools
	m.groupToolsEditor.group = "work"
	m.allTools = []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Tracked: true},
		{Name: "eslint", Provider: "node", Tracked: true},
	}
	m.toolMemberships = map[string][]string{toolKey("ripgrep", "system"): {"work"}}
	m.groupToolsEditor.membership = map[string]bool{"ripgrep": true, "eslint": false}
	m.groupToolsEditor.originalMembership = copyBoolMap(m.groupToolsEditor.membership)
	m.groupToolsIgnore = map[string]bool{"ripgrep": false, "eslint": false}
	m.groupToolsOriginalIgnore = copyBoolMap(m.groupToolsIgnore)

	got := drive(m, pressRune('x'))
	if !got.groupToolsIgnore["ripgrep"] {
		t.Fatal("x should toggle group ignore for selected tool")
	}
	for i, row := range groupToolRows(got) {
		if row.tool.Name == "ripgrep" {
			got.groupToolsEditor.cursor = i
			break
		}
	}
	got = drive(got, pressRune(' '))
	if got.groupToolsEditor.membership["ripgrep"] {
		t.Fatal("space should disable selected enabled tool")
	}
	got = drive(got, pressRune('/'), pressRune('e'), pressRune('s'))
	if !got.groupToolsEditor.searchActive || got.groupToolsEditor.search != "es" {
		t.Fatalf("search state = active:%v query:%q, want active query es", got.groupToolsEditor.searchActive, got.groupToolsEditor.search)
	}
	if rows := groupToolRows(got); len(rows) != 1 || rows[0].tool.Name != "eslint" {
		t.Fatalf("search rows = %+v, want eslint only", rows)
	}
	got = drive(got, pressEnter())
	if got.groupToolsEditor.searchActive {
		t.Fatal("enter while searching should apply/close search, not save")
	}
	got = drive(got, pressRune(']'))
	if got.groupToolsProviderIdx == 0 {
		t.Fatal("] should cycle provider filter")
	}
	got = drive(got, pressEnter())
	if got.mode != viewGroups || !got.loading {
		t.Fatalf("enter after staged changes should save and close, mode=%v loading=%v", got.mode, got.loading)
	}
}

func TestFlow2_GroupDotsEditorStagesMembershipAndSearch(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupDots
	m.groupDotsEditor.group = "work"
	m.dotMemberships = map[string][]string{"nvim": {"work"}, "zsh": {"base"}}
	m.groupDotsEditor.membership = map[string]bool{"nvim": true, "zsh": false}
	m.groupDotsEditor.originalMembership = copyBoolMap(m.groupDotsEditor.membership)

	got := drive(m, pressRune(' '))
	if got.groupDotsEditor.membership["nvim"] {
		t.Fatal("space should disable selected enabled dotfile")
	}
	got = drive(got, pressRune('/'), pressRune('z'), pressRune('s'))
	if !got.groupDotsEditor.searchActive || got.groupDotsEditor.search != "zs" {
		t.Fatalf("search state = active:%v query:%q, want active query zs", got.groupDotsEditor.searchActive, got.groupDotsEditor.search)
	}
	if rows := groupDotRows(got); len(rows) != 1 || rows[0].name != "zsh" {
		t.Fatalf("search rows = %+v, want zsh only", rows)
	}
	got = drive(got, pressEnter())
	if got.groupDotsEditor.searchActive {
		t.Fatal("enter while searching should apply/close search, not save")
	}
	got = drive(got, pressEnter())
	if got.mode != viewGroups || !got.loading {
		t.Fatalf("enter after staged changes should save and close, mode=%v loading=%v", got.mode, got.loading)
	}
}

// UC-112: hostRequired blocks Esc from leaving hosts view.
func TestFlow2_UC112_HostRequiredBlocksEsc(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostRequired = true
	got := drive(m, pressEsc())
	if got.mode != viewGroups {
		t.Errorf("mode = %v, want viewGroups (Esc blocked by hostRequired)", got.mode)
	}
}

// UC-113: Group rename Enter with empty name — loading stays false.
func TestFlow2_UC113_GroupRenameEmptyName(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 1 // "work"
	m.groupRenameMode = true
	si := textinput.New()
	si.SetValue("")
	m.settingsInput = si
	got := drive(m, pressEnter())
	// Empty name: rename sub-mode exits but no dispatch → loading stays false.
	if got.loading {
		t.Error("loading should be false when rename submitted with empty name")
	}
}

// ── Group G — Group Picker ────────────────────────────────────────────────────

// UC-114: Inline new-group Enter (non-empty, install assignment) → loading=true.
func TestFlow2_UC114_InlineNewGroupEnter(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 1
	m.pickerCreatingGroup = true
	m.pickerPurposeInstall = true
	m.upgradingKeys = make(map[string]bool)
	si := textinput.New()
	si.SetValue("mygroup")
	m.settingsInput = si
	got := drive(m, pressEnter())
	if !got.loading {
		t.Error("loading should be true after entering new group name")
	}
	if got.mode != viewList {
		t.Errorf("mode = %v, want viewList after picker close", got.mode)
	}
}

// UC-115: Inline new-group Enter (non-empty, claim) → loading=true.
func TestFlow2_UC115_InlineNewGroupEnterClaim(t *testing.T) {
	// Build a model with an orphan tool selected.
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Tracked: false},
	}
	m := baseModel(tools)
	m.discoveredKeys = map[string]bool{"ripgrep\x00brew": true}
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 1
	m.pickerCreatingGroup = true
	m.pickerPurposeClaim = true
	m.upgradingKeys = make(map[string]bool)
	si := textinput.New()
	si.SetValue("mygroup")
	m.settingsInput = si
	got := drive(m, pressEnter())
	if !got.loading {
		t.Error("loading should be true after claim new group")
	}
	if got.mode != viewList {
		t.Errorf("mode = %v, want viewList", got.mode)
	}
}

// UC-116: Inline new-group Enter (empty name) → close picker, no dispatch.
func TestFlow2_UC116_InlineNewGroupEmptyName(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 1
	m.pickerCreatingGroup = true
	m.upgradingKeys = make(map[string]bool)
	si := textinput.New()
	si.SetValue("")
	m.settingsInput = si
	got := drive(m, pressEnter())
	if got.mode != viewList {
		t.Errorf("mode = %v, want viewList after empty group name", got.mode)
	}
	if got.loading {
		t.Error("loading should be false when no group was created")
	}
}

// ── Group H — Message Handlers ────────────────────────────────────────────────

// UC-117: createGroupDoneMsg — success positions cursor.
func TestFlow2_UC117_CreateGroupDoneMsg(t *testing.T) {
	t.Run("success positions cursor on new group", func(t *testing.T) {
		got := drive(baseModel(nil), createGroupDoneMsg{
			name:       "work",
			groupNames: []string{"work"},
		})
		if got.groupCreating {
			t.Error("groupCreating should be false after createGroupDoneMsg")
		}
		if got.loading {
			t.Error("loading should be false")
		}
		// allGroupNames = ["base", "work"] → "work" is at index 1.
		if got.groupCursor != 1 {
			t.Errorf("groupCursor = %d, want 1", got.groupCursor)
		}
	})

	t.Run("error sets status", func(t *testing.T) {
		got := drive(baseModel(nil), createGroupDoneMsg{err: errors.New("conflict")})
		if got.loading {
			t.Error("loading should be false on error")
		}
	})
}

func TestFlow2_CreateGroupDonePropagatesToHostViewsAndPickers(t *testing.T) {
	m := hostsModel()
	info := &app.HostInfo{
		Hosts: m.hostInfo.Hosts,
	}
	got := drive(m, createGroupDoneMsg{
		name:            "dev",
		groupNames:      []string{"dev", "personal", "work"},
		toolMemberships: map[string][]string{},
		toolGroups:      map[string]string{},
		hostInfo:        info,
	})

	if !slices.Contains(got.groupNames, "dev") {
		t.Fatalf("groupNames = %v, want dev", got.groupNames)
	}
	if out := renderGroups(got); !strings.Contains(out, "dev") {
		t.Fatalf("hosts tab did not render newly-created group:\n%s", out)
	}

	got.assignmentSection = 0
	got.hostCursor = 0
	var cmds []tea.Cmd
	got.startHostGroupEdit(&cmds)
	if !slices.Contains(got.hostGroupPicker, "dev") {
		t.Fatalf("host group picker = %v, want dev", got.hostGroupPicker)
	}
}

// UC-118: groupChangedMsg — success, cursor clamp.
func TestFlow2_UC118_GroupChangedMsg(t *testing.T) {
	m := baseModel(nil)
	m.groupNames = []string{"work", "dev"}
	m.groupCursor = 2 // will be out of bounds after delete
	got := drive(m, groupChangedMsg{
		detail:     "✓ deleted dev",
		groupNames: []string{"work"},
	})
	if got.groupCursor > 1 {
		t.Errorf("groupCursor = %d, should be clamped to ≤1", got.groupCursor)
	}
}

func TestFlow2_GroupToolsChangedMsgRefreshesGroupsAndMemberships(t *testing.T) {
	ripgrep := &database.ToolCache{Name: "ripgrep", Provider: "system", Tracked: true}
	eslint := &database.ToolCache{Name: "eslint", Provider: "node", Tracked: true}
	m := baseModel([]*database.ToolCache{ripgrep, eslint})
	m.groupNames = []string{"old"}
	m.toolMemberships = map[string][]string{
		toolKey("ripgrep", "system"): {"old"},
	}
	m.toolGroups = map[string]string{
		toolKey("ripgrep", "system"): "old",
	}

	got := drive(m, groupToolsChangedMsg{
		detail: "✓ updated 1 tool settings for work",
		tools:  []*database.ToolCache{ripgrep, eslint},
		toolMemberships: map[string][]string{
			toolKey("eslint", "node"): {"work"},
		},
		toolGroups: map[string]string{
			toolKey("eslint", "node"): "work",
		},
		groupNames: []string{"work"},
	})

	if got.loading {
		t.Fatal("loading should be false")
	}
	if !slices.Equal(got.groupNames, []string{"work"}) {
		t.Fatalf("groupNames = %v, want [work]", got.groupNames)
	}
	if got.toolGroups[toolKey("eslint", "node")] != "work" {
		t.Fatalf("toolGroups = %v, want eslint assigned to work", got.toolGroups)
	}
	if got.toolMemberships[toolKey("ripgrep", "system")] != nil {
		t.Fatalf("stale ripgrep membership remained: %v", got.toolMemberships)
	}
}

func TestFlow2_GroupDotsChangedMsgRefreshesEntriesAndMemberships(t *testing.T) {
	m := dotsModel()
	m.dotMemberships = map[string][]string{"nvim": {"old"}}

	got := drive(m, groupDotsChangedMsg{
		detail:         "✓ updated 1 dotfiles for work",
		dotMemberships: map[string][]string{"nvim": {"work"}},
		entries: []app.DotStatus{
			{Name: "nvim", TargetPath: "~/.config/nvim", Group: "work", Health: app.HealthOK},
		},
		gitStatus: "clean",
	})

	if got.loading {
		t.Fatal("loading should be false")
	}
	if !slices.Equal(got.dotMemberships["nvim"], []string{"work"}) {
		t.Fatalf("dotMemberships[nvim] = %v, want [work]", got.dotMemberships["nvim"])
	}
	if len(got.dotsEntries) != 1 || got.dotsEntries[0].Group != "work" {
		t.Fatalf("dotsEntries = %#v, want refreshed work entry", got.dotsEntries)
	}
	if got.dotsGitStatus != "clean" {
		t.Fatalf("dotsGitStatus = %q, want clean", got.dotsGitStatus)
	}
}

// UC-119: hostGroupChangedMsg — add group.
func TestFlow2_UC119_HostGroupChangedMsgAdd(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "ripgrep", Provider: "system", Tracked: true}})
	key := toolKey("ripgrep", "system")
	m.toolMemberships = map[string][]string{key: {"dev"}}
	got := drive(m, hostGroupChangedMsg{
		host:  "work",
		group: "dev",
		added: true,
		info: &app.HostInfo{
			Active: "work",
			Hosts: map[string]config.HostAssignment{
				"work": {Groups: []string{"dev"}},
			},
		},
	})
	if got.loading {
		t.Error("loading should be false")
	}
	if got.hostInfo == nil {
		t.Error("hostInfo should be set")
	}
	if got.toolGroups[key] != "dev" {
		t.Fatalf("toolGroups[%q] = %q, want dev after active host group change", key, got.toolGroups[key])
	}
}

// UC-120: hostGroupChangedMsg — remove group.
func TestFlow2_UC120_HostGroupChangedMsgRemove(t *testing.T) {
	got := drive(baseModel(nil), hostGroupChangedMsg{
		host:  "work",
		group: "dev",
		added: false,
	})
	if got.loading {
		t.Error("loading should be false")
	}
}

// UC-121: hostGroupChangedMsg — host deleted.
func TestFlow2_UC121_HostGroupChangedMsgDelete(t *testing.T) {
	m := baseModel(nil)
	m.hostCursor = 0
	got := drive(m, hostGroupChangedMsg{
		host:  "work",
		group: "",
		info: &app.HostInfo{
			Hosts: map[string]config.HostAssignment{},
		},
	})
	if got.loading {
		t.Error("loading should be false")
	}
}

// UC-125: debouncedSearchMsg — cache miss fires search (searching=true).
func TestFlow2_UC125_DebouncedSearchMsgCacheMiss(t *testing.T) {
	m := baseModel(nil)
	m.ctx = context.Background() // required by startSearch → context.WithCancel
	m.mode = viewSearch
	m.searchGen = 1
	m.searchCache = make(map[string]searchCacheEntry)
	got := drive(m, debouncedSearchMsg{query: "ripgrep", gen: 1})
	if !got.searching {
		t.Error("searching should be true after cache-miss debouncedSearchMsg")
	}
}

// UC-126: debouncedSearchMsg — stale gen dropped.
func TestFlow2_UC126_DebouncedSearchMsgStale(t *testing.T) {
	m := baseModel(nil)
	m.searchGen = 5
	m.searchCache = make(map[string]searchCacheEntry)
	got := drive(m, debouncedSearchMsg{query: "foo", gen: 3})
	if got.searching {
		t.Error("searching should remain false for stale debouncedSearchMsg")
	}
	if got.searchTools != nil {
		t.Error("searchTools should remain nil for stale msg")
	}
}

// UC-127: searchResultsMsg — stale gen dropped.
func TestFlow2_UC127_SearchResultsMsgStale(t *testing.T) {
	m := baseModel(nil)
	m.searchGen = 5
	got := drive(m, searchResultsMsg{query: "foo", gen: 3, tools: threeTools()})
	if got.searchTools != nil {
		t.Error("searchTools should be nil for stale searchResultsMsg")
	}
	if got.searching {
		t.Error("searching should be false for stale msg")
	}
}

// UC-128: searchResultsMsg — success caches results.
func TestFlow2_UC128_SearchResultsMsgSuccess(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSearch
	m.searchGen = 2
	m.searchCache = make(map[string]searchCacheEntry)
	got := drive(m, searchResultsMsg{query: "rg", gen: 2, tools: threeTools()})
	if got.searching {
		t.Error("searching should be false after searchResultsMsg")
	}
	if len(got.searchTools) != 3 {
		t.Errorf("searchTools = %d, want 3", len(got.searchTools))
	}
	if _, ok := got.searchCache[searchCacheKey("rg", "")]; !ok {
		t.Error("searchCache should contain 'rg' after searchResultsMsg")
	}
}

// UC-129: allProvidersDoneMsg — effectiveSystemManager updated.
func TestFlow2_UC129_AllProvidersDoneMsg(t *testing.T) {
	got := drive(baseModel(nil), allProvidersDoneMsg{
		tools:                  threeTools(),
		effectiveSystemManager: "homebrew",
	})
	if got.effectiveSystemManager != "homebrew" {
		t.Errorf("effectiveSystemManager = %q, want %q", got.effectiveSystemManager, "homebrew")
	}
}

// ── Group I — Search blurred state ────────────────────────────────────────────

// UC-130: Blurred viewSearch — j moves cursor.
func TestFlow2_UC130_BlurredSearchJMovesCursor(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewSearch
	// filter is not focused by default in baseModel
	got := drive(m, pressRune('j'))
	if got.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after j in blurred viewSearch", got.cursor)
	}
}

// UC-131: Blurred viewSearch — ] cycles provider tabs.
func TestFlow2_UC131_BlurredSearchTabCycle(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewSearch
	m.applyFilter()
	// providerNames must have >1 entry to enable cycling.
	if len(m.providerNames) <= 1 {
		t.Skip("need >1 provider for tab cycle test")
	}
	got := drive(m, pressRune(']'))
	if got.providerTabIdx != 1 {
		t.Errorf("providerTabIdx = %d, want 1 after ] in blurred viewSearch", got.providerTabIdx)
	}
}

// UC-132: Blurred viewSearch — / refocuses input.
func TestFlow2_UC132_BlurredSearchSlashRefocusesInput(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewSearch
	m.filter.SetValue("git")
	// filter not focused -> / reopens search editing without changing the query.
	got := drive(m, pressRune('/'))
	if !got.filter.Focused() {
		t.Error("filter should be focused after / in blurred viewSearch")
	}
	if got.filter.Value() != "git" {
		t.Fatalf("filter = %q, want existing query preserved", got.filter.Value())
	}
}

func TestFlow2_BlurredSearchActionKeyTriggersSelectedRowAction(t *testing.T) {
	m := baseModel(oneMissing())
	m.mode = viewSearch
	m.filter.SetValue("curl")
	m.filter.Blur()
	m.applyFilter()

	got := drive(m, pressRune('i'))
	if got.filter.Focused() {
		t.Fatal("filter should stay blurred after a row action")
	}
	if got.filter.Value() != "curl" {
		t.Fatalf("filter = %q, want action key not appended to query", got.filter.Value())
	}
	if !got.loading {
		t.Fatal("install action should start for selected search result")
	}
	if got.rowOpKey != toolKey("curl", "brew") {
		t.Fatalf("rowOpKey = %q, want selected curl row", got.rowOpKey)
	}
}

// ── Group J — Command Palette navigation ──────────────────────────────────────

// UC-133: j/k navigate commandCursor.
func TestFlow2_UC133_CommandPaletteJK(t *testing.T) {
	t.Run("j increments commandCursor", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewCommand
		m.commandCursor = -1
		m.commandSuggestions = buildPalette(m)
		if len(m.commandSuggestions) < 2 {
			t.Skip("need at least 2 suggestions")
		}
		got := drive(m, pressRune('j'))
		if got.commandCursor != 0 {
			t.Errorf("commandCursor = %d, want 0 after first j from -1", got.commandCursor)
		}
	})

	t.Run("k at -1 stays at -1 (clamped at 0)", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewCommand
		m.commandCursor = -1
		m.commandSuggestions = buildPalette(m)
		got := drive(m, pressRune('k'))
		// k when cursor=-1 → max(-1-1, 0) or stays 0; it tries to decrement from 0
		// which is already at the floor. Should stay ≥ -1.
		if got.commandCursor < -1 {
			t.Errorf("commandCursor = %d, should not go below -1", got.commandCursor)
		}
	})
}

// UC-134: Enter in command palette with cursor on a suggestion executes it.
func TestFlow2_UC134_CommandPaletteEnter(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewCommand
	m.commandSuggestions = buildPalette(m)
	if len(m.commandSuggestions) == 0 {
		t.Skip("no palette suggestions available")
	}
	m.commandCursor = 0 // select first suggestion
	got := drive(m, pressEnter())
	// After Enter the palette closes.
	if got.mode == viewCommand {
		t.Errorf("mode = %v, want command palette closed after Enter", got.mode)
	}
	if got.commandSuggestions != nil {
		t.Error("commandSuggestions should be nil after palette Enter")
	}
}

// UC-135: Esc from command palette returns to list.
func TestFlow2_UC135_CommandPaletteEsc(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewCommand
	m.commandSuggestions = buildPalette(m)
	got := drive(m, pressEsc())
	if got.mode != viewList {
		t.Errorf("mode = %v, want viewList after Esc from palette", got.mode)
	}
}

// UC-136: WindowSizeMsg with file picker open clamps picker height without panic.
func TestFlow2_UC136_WindowSizeMsgWithFilePicker(t *testing.T) {
	m := baseModel(nil)
	m.showFilePicker = true
	got := drive(m, tea.WindowSizeMsg{Width: 120, Height: 50})
	if got.width != 120 {
		t.Errorf("width = %d, want 120", got.width)
	}
	if got.height != 50 {
		t.Errorf("height = %d, want 50", got.height)
	}
	// Verifies no panic from picker height clamping.
}

func TestFlow2_DotsAddFilePickerAllowsFiles(t *testing.T) {
	m := dotsModel()
	got := drive(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !got.showFilePicker {
		t.Fatal("showFilePicker should be true after dots add")
	}
	if !got.filePickerForDotAdd {
		t.Fatal("filePickerForDotAdd should be true after dots add")
	}
	if !got.filePickerAllowFiles || !got.dotsFilePicker.allowFiles {
		t.Fatal("dots add picker should allow files")
	}
}

// ── UC-137: i on untracked uninstalled tool → group picker ───────────────────

func TestFlow2_UC137_InstallUntrackedTool(t *testing.T) {
	untracked := &database.ToolCache{Name: "fzf", Provider: "brew", Installed: false, Tracked: false}
	m := baseModel(nil)
	m.discoveredTools = []*database.ToolCache{untracked}
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	m.upgradingKeys = make(map[string]bool)

	got := drive(m, tea.KeyPressMsg{Code: 'i', Text: "i"})
	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", got.mode)
	}
	if got.loading {
		t.Error("loading should stay false until group selection is confirmed")
	}
	if !got.pickerPurposeInstall {
		t.Error("pickerPurposeInstall should be true for install-and-add")
	}
}

// ── UC-138: R key is a no-op when scanningProviders already has entries ───────

func TestFlow2_UC138_RefreshBlockedWhileScanning(t *testing.T) {
	m := baseModel(threeTools())
	m.scanningProviders = map[string]bool{"brew": true}

	got := drive(m, tea.KeyPressMsg{Code: 'R', Text: "R"})
	if got.loading {
		t.Error("loading should stay false when R pressed while scan already in flight")
	}
	if !got.scanningProviders["brew"] {
		t.Error("scanningProviders should be unchanged when R is blocked")
	}
}

// ── UC-139: s IsRepeat in dots view → no-op ───────────────────────────────────

func TestFlow2_UC139_DotsSyncIsRepeatNoOp(t *testing.T) {
	got := drive(dotsModel(), tea.KeyPressMsg{Code: 's', Text: "s", IsRepeat: true})
	if got.dotsLoading {
		t.Error("dotsLoading should stay false after s IsRepeat in dots view")
	}
}

// ── UC-140: IsRepeat guards on all viewList action keys ───────────────────────

func TestFlow2_UC140_ViewListIsRepeatNoOps(t *testing.T) {
	outdatedTool := func() []*database.ToolCache {
		return []*database.ToolCache{{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true}}
	}

	cases := []struct {
		name  string
		setup func() Model
		msg   tea.KeyPressMsg
		check func(t *testing.T, got Model)
	}{
		{
			name: "i IsRepeat",
			setup: func() Model {
				m := baseModel(oneMissing())
				m.upgradingKeys = make(map[string]bool)
				return m
			},
			msg:   tea.KeyPressMsg{Code: 'i', Text: "i", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name:  "d IsRepeat",
			setup: func() Model { return baseModel(oneInstalled()) },
			msg:   tea.KeyPressMsg{Code: 'd', Text: "d", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name: "u IsRepeat",
			setup: func() Model {
				m := baseModel(outdatedTool())
				m.upgradingKeys = make(map[string]bool)
				return m
			},
			msg:   tea.KeyPressMsg{Code: 'u', Text: "u", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name: "U IsRepeat",
			setup: func() Model {
				m := baseModel(outdatedTool())
				m.upgradingKeys = make(map[string]bool)
				m.sectionCounts = map[section]int{sectionUpdates: 1}
				return m
			},
			msg:   tea.KeyPressMsg{Code: 'U', Text: "U", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name:  "s IsRepeat",
			setup: func() Model { return baseModel(threeTools()) },
			msg:   tea.KeyPressMsg{Code: 's', Text: "s", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name: "R IsRepeat",
			setup: func() Model {
				m := baseModel(threeTools())
				m.scanningProviders = make(map[string]bool)
				return m
			},
			msg:   tea.KeyPressMsg{Code: 'R', Text: "R", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name: "c IsRepeat",
			setup: func() Model {
				orphan := &database.ToolCache{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
				m := baseModel(nil)
				m.discoveredTools = []*database.ToolCache{orphan}
				m.rebuildDiscoveredKeys()
				m.applyFilter()
				m.upgradingKeys = make(map[string]bool)
				return m
			},
			msg: tea.KeyPressMsg{Code: 'c', Text: "c", IsRepeat: true},
			check: func(t *testing.T, got Model) {
				if got.mode != viewList {
					t.Errorf("mode = %v, want viewList (c IsRepeat should not open group picker)", got.mode)
				}
			},
		},
		{
			name:  "x IsRepeat",
			setup: func() Model { return baseModel(oneInstalled()) },
			msg:   tea.KeyPressMsg{Code: 'x', Text: "x", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name:  "p IsRepeat (PinProvider)",
			setup: func() Model { return wrongProvModel() },
			msg:   tea.KeyPressMsg{Code: 'p', Text: "p", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
		{
			name:  "r IsRepeat (MigrateProvider)",
			setup: func() Model { return wrongProvModel() },
			msg:   tea.KeyPressMsg{Code: 'r', Text: "r", IsRepeat: true},
			check: func(t *testing.T, got Model) { assertNotLoading(t, got) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := drive(tc.setup(), tc.msg)
			tc.check(t, got)
		})
	}
}

// ── UC-141: Esc in dots view clears both dotsConfirmIdx and dotsOverwriteIdx ──

func TestFlow2_UC141_DotsEscClearsBothIndicesSimultaneously(t *testing.T) {
	m := dotsModel()
	m.dotsConfirmIdx = 0
	m.dotsOverwriteIdx = 0
	m.dotsLocalIdx = 0
	m.dotsVariantIdx = 0
	m.dotsVariantMode = dotsVariantCreate

	got := drive(m, pressEsc())
	if got.dotsConfirmIdx != -1 {
		t.Errorf("dotsConfirmIdx = %d, want -1 after Esc with both indices armed", got.dotsConfirmIdx)
	}
	if got.dotsOverwriteIdx != -1 {
		t.Errorf("dotsOverwriteIdx = %d, want -1 after Esc with both indices armed", got.dotsOverwriteIdx)
	}
	if got.dotsLocalIdx != -1 {
		t.Errorf("dotsLocalIdx = %d, want -1 after Esc with all indices armed", got.dotsLocalIdx)
	}
	if got.dotsVariantIdx != -1 || got.dotsVariantMode != dotsVariantNone {
		t.Errorf("variant prompt = idx:%d mode:%v, want cleared after Esc", got.dotsVariantIdx, got.dotsVariantMode)
	}
}

// assertNotLoading is a helper that fails if model.loading is true.
func assertNotLoading(t *testing.T, m Model) {
	t.Helper()
	if m.loading {
		t.Error("loading should be false (IsRepeat no-op)")
	}
}

// ── UC-142: Setup step 3 — node manager selection ─────────────────────────────

func TestFlow2_UC142_SetupStep3NodeMgr(t *testing.T) {
	t.Run("down moves cursor", func(t *testing.T) {
		got := drive(setupStep3Model(), tea.KeyPressMsg{Code: 'j', Text: "j"})
		if got.setupNodeMgrIdx != 1 {
			t.Errorf("setupNodeMgrIdx = %d, want 1 after j", got.setupNodeMgrIdx)
		}
	})
	t.Run("up does not go below 0", func(t *testing.T) {
		got := drive(setupStep3Model(), tea.KeyPressMsg{Code: 'k', Text: "k"})
		if got.setupNodeMgrIdx != 0 {
			t.Errorf("setupNodeMgrIdx = %d, want 0 after k at top", got.setupNodeMgrIdx)
		}
	})
	t.Run("enter on auto creates host without showing step 4", func(t *testing.T) {
		got := drive(setupStep3Model(), pressEnter())
		if got.setupStep == 4 {
			t.Fatalf("setupStep = %d, host confirmation screen should not be shown", got.setupStep)
		}
		if got.setupStep != 5 {
			t.Fatalf("setupStep = %d, want dotfile decision while host is created", got.setupStep)
		}
		if !got.loading {
			t.Error("loading should be true while creating the host automatically")
		}
		if got.settingsInput.Focused() {
			t.Error("settingsInput should not be focused during automatic host creation")
		}
	})
	t.Run("enter on bun sets loading=true", func(t *testing.T) {
		m := setupStep3Model()
		m.setupNodeMgrIdx = 1 // bun
		got := drive(m, pressEnter())
		if !got.loading {
			t.Error("loading should be true when saving non-auto manager")
		}
	})
	t.Run("esc creates host without showing step 4", func(t *testing.T) {
		got := drive(setupStep3Model(), pressEsc())
		if got.setupStep == 4 {
			t.Fatalf("setupStep = %d, host confirmation screen should not be shown", got.setupStep)
		}
		if got.setupStep != 5 {
			t.Fatalf("setupStep = %d, want dotfile decision while host is created", got.setupStep)
		}
		if !got.loading {
			t.Error("loading should be true while creating the host automatically")
		}
	})
	t.Run("setupNodeMgrDoneMsg creates host without showing step 4", func(t *testing.T) {
		got := drive(loadingSetup(3), setupNodeMgrDoneMsg{})
		if got.setupStep == 4 {
			t.Fatalf("setupStep = %d, host confirmation screen should not be shown", got.setupStep)
		}
		if got.setupStep != 5 {
			t.Fatalf("setupStep = %d, want dotfile decision while host is created", got.setupStep)
		}
		if !got.loading {
			t.Error("loading should be true while creating the host automatically")
		}
		if got.settingsInput.Focused() {
			t.Error("settingsInput should not be focused during automatic host creation")
		}
	})
}

// ── UC-144–146: Group filter pills ({/}) ─────────────────────────────────────

// multiGroupModel returns a baseModel pre-loaded with tools spread across
// "base", "work", and "personal" groups, so group-filter tests have
// a realistic fixture without touching the real app layer.
func multiGroupModel() Model {
	brew := func(name, group string) *database.ToolCache {
		return &database.ToolCache{Name: name, Provider: "brew", Installed: true}
	}
	tools := []*database.ToolCache{
		brew("git", "base"),
		brew("ripgrep", "work"),
		brew("fzf", "personal"),
	}
	m := baseModel(tools)
	m.groupNames = []string{"work", "personal"}
	m.toolGroups = map[string]string{
		toolKey("git", "brew"):     "base",
		toolKey("ripgrep", "brew"): "work",
		toolKey("fzf", "brew"):     "personal",
	}
	m.applyFilter()
	return m
}

// UC-144: } cycles group filter forward; groupFilter and groupTabIdx stay in sync.
func TestFlow2_UC144_GroupNextFilter(t *testing.T) {
	t.Run("} at idx=0 advances to host (idx=1)", func(t *testing.T) {
		m := multiGroupModel()
		got := drive(m, tea.KeyPressMsg{Code: '}', Text: "}"})
		if got.groupTabIdx != 1 {
			t.Errorf("groupTabIdx = %d, want 1", got.groupTabIdx)
		}
		if got.groupFilter != shortHostname() {
			t.Errorf("groupFilter = %q, want host", got.groupFilter)
		}
	})
	t.Run("} at last idx wraps to all (idx=0)", func(t *testing.T) {
		m := multiGroupModel()
		m.groupTabIdx = 3 // "work" = last
		m.groupFilter = "work"
		got := drive(m, tea.KeyPressMsg{Code: '}', Text: "}"})
		if got.groupTabIdx != 0 {
			t.Errorf("groupTabIdx = %d, want 0 (wrapped)", got.groupTabIdx)
		}
		if got.groupFilter != "" {
			t.Errorf("groupFilter = %q, want empty (all)", got.groupFilter)
		}
	})
}

// UC-145: { cycles group filter backward.
func TestFlow2_UC145_GroupPrevFilter(t *testing.T) {
	t.Run("{ at idx=0 wraps to last", func(t *testing.T) {
		m := multiGroupModel()
		got := drive(m, tea.KeyPressMsg{Code: '{', Text: "{"})
		allGroups := buildAllGroupNames(m.groupNames)
		if got.groupTabIdx != len(allGroups) {
			t.Errorf("groupTabIdx = %d, want %d (wrapped)", got.groupTabIdx, len(allGroups))
		}
	})
	t.Run("{ at idx=2 steps back to idx=1", func(t *testing.T) {
		m := multiGroupModel()
		m.groupTabIdx = 2
		m.groupFilter = "work"
		got := drive(m, tea.KeyPressMsg{Code: '{', Text: "{"})
		if got.groupTabIdx != 1 {
			t.Errorf("groupTabIdx = %d, want 1", got.groupTabIdx)
		}
		if got.groupFilter != shortHostname() {
			t.Errorf("groupFilter = %q, want host", got.groupFilter)
		}
	})
}

// UC-146: newColWidths reserves group column when visible tools have a host
// or reusable group badge.
func TestFlow2_UC146_GroupColAlwaysVisible(t *testing.T) {
	// Two tools both in the host group.
	tools := []*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true},
		{Name: "curl", Provider: "brew", Installed: true},
	}
	tg := map[string]string{
		toolKey("git", "brew"):  "host",
		toolKey("curl", "brew"): "host",
	}

	t.Run("host group only → cols.group shows host badge", func(t *testing.T) {
		cols := newColWidths(tools, tg, nil, "", "", "", 120)
		if cols.group < len("[host]") {
			t.Errorf("cols.group = %d, too narrow for [host]", cols.group)
		}
	})

	t.Run("with reusable groups → cols.group>0 even if all tools are in host group", func(t *testing.T) {
		cols := newColWidths(tools, tg, []string{"work"}, "", "", "", 120)
		if cols.group == 0 {
			t.Error("cols.group should be > 0 when reusable groups exist")
		}
		if cols.group < len("[work]") {
			t.Errorf("cols.group = %d, too narrow for [work]", cols.group)
		}
	})
}

// UC-143: Setup step 2 with node disabled → auto-create host.
func TestFlow2_UC143_SetupStep2NodeDisabled(t *testing.T) {
	m := Model{
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        textinput.New(),
		commandInput:  textinput.New(),
		settingsInput: textinput.New(),
		mode:          viewSetup,
		setupStep:     2,
		upgradingKeys: make(map[string]bool),
		setupProviders: []app.SetupProviderOption{
			{Name: "system", Label: "system", Enabled: true},
		},
		dangerConfirmRow: -1,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		width:            120,
		height:           80,
	}
	got := drive(m, pressEnter())
	if got.setupStep == 4 {
		t.Fatalf("setupStep = %d, host confirmation screen should not be shown", got.setupStep)
	}
	if got.setupStep != 5 {
		t.Fatalf("setupStep = %d, want dotfile decision while host is created", got.setupStep)
	}
	if !got.loading {
		t.Error("loading should be true while creating the host automatically")
	}
	if got.settingsInput.Focused() {
		t.Error("settingsInput should not be focused during automatic host creation")
	}
}
