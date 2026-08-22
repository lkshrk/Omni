package tui

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
)

// Step 2 uses the same provider-selection UI as step 1.
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

func setupStep3Model() Model {
	m := Model{
		keys:             DefaultKeyMap(),
		spinner:          spinner.New(),
		filter:           textinput.New(),
		commandInput:     textinput.New(),
		settingsInput:    textinput.New(),
		mode:             viewSetup,
		setupStep:        3,
		editingPriority:  true,
		priorityDraft:    []string{"brew", "bun", "npm", "uv", "pip"},
		priorityDisabled: map[string]bool{},
		priorityAvailable: map[string]bool{
			"brew": true, "bun": true, "npm": true, "uv": true, "pip": true,
		},
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

func TestFlow2_UC66_BackgroundColorMsg(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC67_FocusBlur(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC68_PasteMsgSearch(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC69_PasteMsgCommand(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewCommand
	m.commandInput.Focus()
	got := drive(m, tea.PasteMsg{Content: "sync"})
	if got.commandInput.Value() != "sync" {
		t.Errorf("commandInput.Value() = %q, want %q", got.commandInput.Value(), "sync")
	}
}

func TestFlow2_UC70_SecondQQuits(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.confirmQuit = true
	_, cmd := m.Update(pressRune('q'))
	if cmd == nil {
		t.Error("second q should return a non-nil quit command")
	}
}

func TestFlow2_UC71_QWithHostRequired(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	// The armed state only lives in the footer legend, so a silent disarm made the next q re-arm instead of exiting — indistinguishable from a hung app.
	if got.statusMsg != "quit confirmation expired — press q again" {
		t.Fatalf("statusMsg = %q, want the expiry notice", got.statusMsg)
	}
}

func TestConfirmTimeoutClearsListConfirmationStatus(t *testing.T) {
	t.Parallel()
	tool := &app.ToolView{Name: "typescript", Provider: "node", Installed: true, Tracked: true, InstalledWith: "npm"}
	m := baseModel([]*app.ToolView{tool})
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
	t.Parallel()
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
	if len(cmds) != 1 {
		t.Fatalf("confirmation timeout commands = %d, want the quit expiry status", len(cmds))
	}
	if m.hasActiveConfirmation() {
		t.Fatal("confirmation state should be cleared after timeout")
	}
	if m.statusMsg != "quit confirmation expired — press q again" {
		t.Fatalf("statusMsg = %q, want the quit expiry notice", m.statusMsg)
	}
}

func TestGlobalNavigationClearsActiveConfirmations(t *testing.T) {
	t.Parallel()
	tool := &app.ToolView{Name: "bat", Provider: "brew", Installed: true, Tracked: true}
	cases := []struct {
		name     string
		model    Model
		key      tea.Msg
		wantMode viewMode
	}{
		{
			name: "list delete confirm tab",
			model: func() Model {
				m := baseModel([]*app.ToolView{tool})
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
			wantMode: viewSkills,
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
				m := baseModel([]*app.ToolView{tool})
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

func TestFlow2_UC72_SetupStep2AllEnabled(t *testing.T) {
	t.Parallel()
	t.Run("all enabled with node Enter advances to step 3 (node manager)", func(t *testing.T) {
		got := drive(setupStep2Model(), pressEnter())
		if got.setupStep != 3 {
			t.Errorf("setupStep = %d, want 3 (node manager step)", got.setupStep)
		}
		if got.settingsInput.Focused() {
			t.Error("settingsInput should NOT be focused at step 3 (node manager)")
		}
	})

	t.Run("some disabled Enter sets loading", func(t *testing.T) {
		got := drive(setupStep2Model(), pressRune(' '), pressEnter())
		if !got.loading {
			t.Error("loading should be true when disabled providers need saving")
		}
	})
}

func TestFlow2_SetupNoHostCopyAndGroups(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC76_SetupStep5(t *testing.T) {
	t.Parallel()
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
		got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen, discovered: []*app.ToolView{{Name: "orphan"}}})
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
		got := drive(m, toolsLoadedMsg{tools: []*app.ToolView{{Name: "git"}}})
		if !got.setupReloading || !got.descRefreshing {
			t.Fatalf("setup reload should wait for descriptions, setupReloading=%v descRefreshing=%v", got.setupReloading, got.descRefreshing)
		}
		if got.progressText != descriptionRefreshStatus {
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

func TestFlow2_UC77_SetupStep6Esc(t *testing.T) {
	t.Parallel()
	got := drive(setupStep6Model(), pressEsc())
	if !got.loading {
		t.Error("loading should be true after Esc at step 6 with no file picker")
	}
}

func TestFlow2_UC78_SetupImportDoneMsg(t *testing.T) {
	t.Parallel()

	m := loadingSetup(1)
	m.allTools = []*app.ToolView{{Name: "snapshot", Provider: "brew"}}
	got := drive(m, setupImportDoneMsg{added: 2})
	if got.setupStep != 3 {
		t.Fatalf("setupStep = %d, want 3 (priority editor) after import", got.setupStep)
	}
	if !got.editingPriority {
		t.Error("editingPriority should be true after advancing past providers")
	}
	if len(got.allTools) != 1 || got.allTools[0].Name != "snapshot" {
		t.Fatalf("allTools changed before onboarding finished: %+v", got.allTools)
	}
}

func TestFlow2_UC79_SetupProvidersDoneMsg(t *testing.T) {
	t.Parallel()

	got := drive(loadingSetup(2), setupProvidersDoneMsg{})
	if got.setupStep != 3 {
		t.Fatalf("setupStep = %d, want 3 (priority editor) after providers saved", got.setupStep)
	}
	if !got.editingPriority {
		t.Error("editingPriority should be true after advancing past providers")
	}
}

func TestFlow2_UC80_SetupHostDoneMsg(t *testing.T) {
	t.Parallel()
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
	// Status is set via an async cmd so statusMsg may not be populated synchronously; check that loading cleared and the step advanced instead.
}

func TestFlow2_SetupErrorsStayOnCurrentStep(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC81_FilePickerEscClosesPicker(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.showFilePicker = true
	got := drive(m, pressEsc())
	if got.showFilePicker {
		t.Error("showFilePicker should be false after Esc")
	}
}

func TestFlow2_UC82_FilePickerPickSettings(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC83_FilePickerPickSetup(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC84_DotsRefreshKey(t *testing.T) {
	t.Parallel()
	got := drive(dotsModel(), pressRune('R'))
	if !got.dotsLoading {
		t.Error("dotsLoading should be true after R (refresh) key in dots tab")
	}
}

func TestFlow2_UC85_DotsRefreshKeyRepeat(t *testing.T) {
	t.Parallel()
	got := drive(dotsModel(), tea.KeyPressMsg{Code: 'R', Text: "R", IsRepeat: true})
	if got.dotsLoading {
		t.Error("dotsLoading should stay false when R is a repeated key press")
	}
}

func TestFlow2_UC85b_DotsOldDiscoverKeyNoop(t *testing.T) {
	t.Parallel()
	got := drive(dotsModel(), pressRune('D'))
	if got.dotsLoading {
		t.Error("D should not trigger any dots operation (discover moved to R)")
	}
}

func TestFlow2_UC89_SettingsAutoCommit(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC90_SettingsAutoPush(t *testing.T) {
	t.Parallel()
	msgs := append(toSettings(), nj(settingsRowDotsPush)...)
	msgs = append(msgs, pressRune(' '))
	got := drive(baseModel(nil), msgs...)
	if !got.settings.DotsGit.AutoPush {
		t.Error("AutoPush should be true after toggling Push Changes")
	}
}

func TestFlow2_UC91_DangerResetSettings(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC92_DangerResetCache(t *testing.T) {
	t.Parallel()
	msgs := append(toSettings(), nj(settingsRowResetCache)...)
	msgs = append(msgs, pressEnter()) // Enter arms danger confirmation
	msgs = append(msgs, pressEnter()) // second Enter: fires reset
	got := drive(baseModel(nil), msgs...)
	if !got.loading {
		t.Error("loading should be true after confirming reset cache")
	}
}

func TestFlow2_UC93_DangerDisableDots(t *testing.T) {
	got := drive(openSettingsDotsSyncChoice(t), pressRune('y')) // fires doDisableDots keeping local files
	if !got.loading {
		t.Error("loading should be true after confirming disable dots")
	}
	if got.dangerConfirmRow != -1 {
		t.Errorf("dangerConfirmRow = %d, want -1", got.dangerConfirmRow)
	}
}

func TestFlow2_UC94_PriorityEditorGrabCarryUp(t *testing.T) {
	t.Parallel()

	msgs := append(toSettings(), nj(settingsRowProviderPriority)...)
	msgs = append(msgs, pressEnter())
	msgs = append(msgs, pressRune('j'))
	msgs = append(msgs, pressRune(' '))
	msgs = append(msgs, pressRune('k'))
	msgs = append(msgs, pressRune(' '))
	got := drive(baseModel(nil), msgs...)
	if !got.editingPriority {
		t.Error("editingPriority should still be true")
	}
	if got.priorityCursor != 0 {
		t.Errorf("priorityCursor = %d, want 0 after carry up", got.priorityCursor)
	}
	if got.priorityHolding {
		t.Error("priorityHolding should be false after dropping")
	}
	if len(got.priorityDraft) < 2 {
		t.Fatal("priorityDraft should have at least 2 items")
	}
	original := []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "bun", "pnpm", "npm", "uv", "pip"}
	if got.priorityDraft[0] != original[1] {
		t.Errorf("priorityDraft[0] = %q, want %q (carried up)", got.priorityDraft[0], original[1])
	}
}

func TestFlow2_UC95_GroupSectionUpAtTop(t *testing.T) {
	t.Parallel()
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 0
	got := drive(m, pressRune('k'))
	if got.assignmentSection != 0 {
		t.Errorf("assignmentSection = %d, want 0 after k at top of groups", got.assignmentSection)
	}
}

func TestFlow2_UC97_HostSectionDownAtLast(t *testing.T) {
	t.Parallel()
	m := hostsModel()
	m.hostCursor = 1
	got := drive(m, pressRune('j'))
	if got.assignmentSection != 1 {
		t.Errorf("assignmentSection = %d, want 1 after j at last host", got.assignmentSection)
	}
	if got.groupCursor != 0 {
		t.Errorf("groupCursor = %d, want 0 after entering group section", got.groupCursor)
	}
}

func TestFlow2_UC98_GroupSectionDownAtLast(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC99_HostRenameKey(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	m := hostsModel()
	m.hostInfo.Active = "alpha"
	m.hostCursor = 1

	got := drive(m, pressEnter())

	if got.hostInfo.Active != "alpha" {
		t.Fatalf("active host = %q, want alpha", got.hostInfo.Active)
	}
}

func TestFlow2_UC100_HostRenameMode(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestFlow2_UC101_EditHostGroups(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestFlow2_UC102_HDoesNotOpenLegacyHostMapping(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC103_HostGroupPickerNavigation(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC104_NewGroupCreating(t *testing.T) {
	t.Parallel()
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

func TestFlow2_HostCreationFromGroupSection(t *testing.T) {
	t.Parallel()
	m := hostsModel()
	m.assignmentSection = 1
	got := drive(m, pressRune('p'))
	if !got.groupCreating {
		t.Error("host popup should open after p")
	}
	if got.assignmentSection != 0 || got.settingsInput.Placeholder != "hostname…" {
		t.Fatalf("host creation state = section %d placeholder %q", got.assignmentSection, got.settingsInput.Placeholder)
	}
}

func TestFlow2_HostCreationFromHostSection(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "beta")
	a := newHostsFlowApp(t)
	m := hostsModel()
	m.app = a
	m.ctx = context.Background()
	m.assignmentSection = 0
	tm, _ := m.Update(pressRune('p'))
	got := tm.(Model)
	if !got.groupCreating {
		t.Error("host popup should be open after p")
	}
	if got.assignmentSection != 0 {
		t.Errorf("assignmentSection = %d, want host section unchanged", got.assignmentSection)
	}
	got.settingsInput.SetValue("Aardvark.EXAMPLE")
	tm, cmd := got.Update(pressEnter())
	got = tm.(Model)
	if cmd != nil {
		t.Fatal("host creation should wait for copy-or-fresh choice")
	}
	if got.hostCreateStep != 1 || got.hostCreateName != "aardvark" {
		t.Fatalf("host copy choice state = step %d name %q", got.hostCreateStep, got.hostCreateName)
	}
	tm, cmd = got.Update(pressEsc())
	got = tm.(Model)
	if cmd == nil {
		t.Fatal("start-fresh command missing")
	}
	msg := runLastBatchCommand(t, cmd)
	changed, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("host creation command returned %T", msg)
	}
	if changed.err != nil || changed.info == nil {
		t.Fatalf("host creation result = %+v", changed)
	}
	if changed.host != "aardvark" {
		t.Fatalf("created host identity = %q, want canonical aardvark", changed.host)
	}
	got = drive(got, changed)
	if _, ok := got.hostInfo.Hosts["aardvark"]; !ok {
		t.Fatalf("created host missing from refreshed result: %+v", got.hostInfo.Hosts)
	}
	if got.selectedHostName() != "aardvark" {
		t.Fatalf("selected host = %q, want aardvark", got.selectedHostName())
	}
}

func TestFlow2_HostCreationCanCopyExistingHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "beta")
	a := newHostsFlowApp(t)
	m := hostsModel()
	m.app = a
	m.ctx = context.Background()

	got := drive(m, pressRune('p'))
	got.settingsInput.SetValue("gamma")
	tm, _ := got.Update(pressEnter())
	got = tm.(Model)
	if got.hostCreateStep != 1 {
		t.Fatalf("hostCreateStep = %d, want copy prompt", got.hostCreateStep)
	}
	tm, _ = got.Update(pressEnter())
	got = tm.(Model)
	if got.hostCreateStep != 2 {
		t.Fatalf("hostCreateStep = %d, want source picker", got.hostCreateStep)
	}
	tm, cmd := got.Update(pressEnter())
	got = tm.(Model)
	if cmd == nil {
		t.Fatal("copy-host command missing")
	}
	msg := runLastBatchCommand(t, cmd)
	changed, ok := msg.(hostGroupChangedMsg)
	if !ok || changed.err != nil || changed.info == nil {
		t.Fatalf("copy-host result = %#v", msg)
	}
	got = drive(got, changed)
	created, ok := got.hostInfo.Hosts["gamma"]
	if !ok || !slices.Equal(created.Groups, []string{"apps"}) {
		t.Fatalf("copied host = %+v, want alpha groups [apps]", created)
	}
}

func TestFlow2_HostCreationRejectsEmptyCanonicalName(t *testing.T) {
	m := hostsModel()
	got := drive(m, pressRune('p'))
	got.settingsInput.SetValue(".")
	tm, _ := got.Update(pressEnter())
	got = tm.(Model)
	if got.hostCreateStep != 0 || got.loading {
		t.Fatalf("invalid hostname started creation: step=%d loading=%v", got.hostCreateStep, got.loading)
	}
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "hostname is required") {
		t.Fatalf("invalid hostname status = %q error=%v", got.statusMsg, got.statusIsErr)
	}
}

func TestFlow2_HostCreateChoiceOwnsInput(t *testing.T) {
	m := hostsModel()
	m.hostCreateStep = 1
	m.hostCreateName = "gamma"
	got := drive(m, pressTab(), pressRune(':'), pressRune('?'), pressRune('q'))
	if got.mode != viewGroups || got.hostCreateStep != 1 {
		t.Fatalf("host modal escaped: mode=%v step=%d", got.mode, got.hostCreateStep)
	}
	if got.help.ShowAll || got.confirmQuit {
		t.Fatalf("global overlay escaped host modal: help=%v quit=%v", got.help.ShowAll, got.confirmQuit)
	}
	if got.mainTabsClickable() {
		t.Fatal("main tabs should not be clickable through host modal")
	}

	got.hostCreateStep = 2
	got.setupCopyHostIdx = 0
	got = drive(got, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if got.setupCopyHostIdx != 1 {
		t.Fatalf("mouse wheel moved source index to %d, want 1", got.setupCopyHostIdx)
	}
}

func TestDoCreateHost_ExistingCanonicalHostIsNotReportedCreated(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "beta")
	m := hostsModel()
	m.app = newHostsFlowApp(t)

	msg := m.doCreateHost("ALPHA.EXAMPLE")()
	changed, ok := msg.(hostGroupChangedMsg)
	if !ok {
		t.Fatalf("host creation command returned %T", msg)
	}
	if changed.err != nil || changed.host != "alpha" || changed.detail != "host alpha already exists" {
		t.Fatalf("existing host result = %+v", changed)
	}
}

func TestFlow2_UC105_DeleteHostGroupBlocked(t *testing.T) {
	t.Parallel()
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 0
	got := drive(m, pressRune('d'))
	if got.groupDeleteConfirm {
		t.Error("groupDeleteConfirm should not be set for host group (index 0)")
	}
}

func TestFlow2_UC106_RenameHostGroupBlocked(t *testing.T) {
	t.Parallel()
	m := hostsModel()
	m.assignmentSection = 1
	m.groupCursor = 0
	got := drive(m, pressRune('r'))
	if got.groupRenameMode {
		t.Error("groupRenameMode should not be set for host group (index 0)")
	}
}

func TestFlow2_GroupRenameNegativeCursorIsNoop(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestFlow2_UC109_GroupSectionToolsKey(t *testing.T) {
	t.Parallel()
	m := hostsModel()
	m.allTools = []*app.ToolView{
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
	t.Parallel()
	m := hostsModel()
	m.mode = viewGroups
	m.assignmentSection = 0
	m.groupCursor = 1
	m.allTools = []*app.ToolView{
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
	t.Parallel()
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
	t.Parallel()
	m := hostsModel()
	m.mode = viewGroupTools
	m.groupToolsEditor.group = "work"
	m.allTools = []*app.ToolView{
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
	t.Parallel()
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

func TestFlow2_UC112_HostRequiredBlocksEsc(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostRequired = true
	got := drive(m, pressEsc())
	if got.mode != viewGroups {
		t.Errorf("mode = %v, want viewGroups (Esc blocked by hostRequired)", got.mode)
	}
}

func TestFlow2_UC113_GroupRenameEmptyName(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC114_InlineNewGroupEnter(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC115_InlineNewGroupEnterClaim(t *testing.T) {
	t.Parallel()

	tools := []*app.ToolView{
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

func TestFlow2_UC116_InlineNewGroupEmptyName(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC117_CreateGroupDoneMsg(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestFlow2_UC118_GroupChangedMsg(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	ripgrep := &app.ToolView{Name: "ripgrep", Provider: "system", Tracked: true}
	eslint := &app.ToolView{Name: "eslint", Provider: "node", Tracked: true}
	m := baseModel([]*app.ToolView{ripgrep, eslint})
	m.groupNames = []string{"old"}
	m.toolMemberships = map[string][]string{
		toolKey("ripgrep", "system"): {"old"},
	}
	m.toolGroups = map[string]string{
		toolKey("ripgrep", "system"): "old",
	}

	got := drive(m, groupToolsChangedMsg{
		detail: "✓ updated 1 tool settings for work",
		tools:  []*app.ToolView{ripgrep, eslint},
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
	t.Parallel()
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

func TestFlow2_UC119_HostGroupChangedMsgAdd(t *testing.T) {
	t.Parallel()
	m := baseModel([]*app.ToolView{{Name: "ripgrep", Provider: "system", Tracked: true}})
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

func TestFlow2_UC120_HostGroupChangedMsgRemove(t *testing.T) {
	t.Parallel()
	got := drive(baseModel(nil), hostGroupChangedMsg{
		host:  "work",
		group: "dev",
		added: false,
	})
	if got.loading {
		t.Error("loading should be false")
	}
}

func TestFlow2_UC121_HostGroupChangedMsgDelete(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC125_DebouncedSearchMsgCacheMiss(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC126_DebouncedSearchMsgStale(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC127_SearchResultsMsgStale(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC128_SearchResultsMsgSuccess(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC129_AllProvidersDoneMsg(t *testing.T) {
	t.Parallel()
	got := drive(baseModel(nil), allProvidersDoneMsg{
		tools:                  threeTools(),
		effectiveSystemManager: "homebrew",
	})
	if got.effectiveSystemManager != "homebrew" {
		t.Errorf("effectiveSystemManager = %q, want %q", got.effectiveSystemManager, "homebrew")
	}
}

func TestFlow2_UC130_BlurredSearchJMovesCursor(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.mode = viewSearch
	got := drive(m, pressRune('j'))
	if got.cursor != 1 {
		t.Errorf("cursor = %d, want 1 after j in blurred viewSearch", got.cursor)
	}
}

func TestFlow2_UC131_BlurredSearchTabCycle(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC132_BlurredSearchSlashRefocusesInput(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestFlow2_UC133_CommandPaletteArrows(t *testing.T) {
	t.Parallel()
	t.Run("down increments commandCursor", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewCommand
		m.commandCursor = -1
		m.commandSuggestions = buildPalette(m)
		if len(m.commandSuggestions) < 2 {
			t.Skip("need at least 2 suggestions")
		}
		got := drive(m, pressDown())
		if got.commandCursor != 0 {
			t.Errorf("commandCursor = %d, want 0 after first down from -1", got.commandCursor)
		}
	})

	t.Run("up at -1 stays at -1 (clamped at 0)", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewCommand
		m.commandCursor = -1
		m.commandSuggestions = buildPalette(m)
		got := drive(m, pressUp())
		if got.commandCursor < -1 {
			t.Errorf("commandCursor = %d, should not go below -1", got.commandCursor)
		}
	})
}

func TestFlow2_UC134_CommandPaletteEnter(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewCommand
	m.commandSuggestions = buildPalette(m)
	if len(m.commandSuggestions) == 0 {
		t.Skip("no palette suggestions available")
	}
	m.commandCursor = 0 // select first suggestion
	got := drive(m, pressEnter())
	if got.mode == viewCommand {
		t.Errorf("mode = %v, want command palette closed after Enter", got.mode)
	}
	if got.commandSuggestions != nil {
		t.Error("commandSuggestions should be nil after palette Enter")
	}
}

func TestFlow2_UC135_CommandPaletteEsc(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewCommand
	m.commandSuggestions = buildPalette(m)
	got := drive(m, pressEsc())
	if got.mode != viewList {
		t.Errorf("mode = %v, want viewList after Esc from palette", got.mode)
	}
}

func TestFlow2_UC136_WindowSizeMsgWithFilePicker(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.showFilePicker = true
	got := drive(m, tea.WindowSizeMsg{Width: 120, Height: 50})
	if got.width != 120 {
		t.Errorf("width = %d, want 120", got.width)
	}
	if got.height != 50 {
		t.Errorf("height = %d, want 50", got.height)
	}
}

func TestFlow2_DotsAddFilePickerAllowsFiles(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC137_InstallUntrackedTool(t *testing.T) {
	t.Parallel()
	untracked := &app.ToolView{Name: "fzf", Provider: "brew", Installed: false, Tracked: false}
	m := baseModel(nil)
	m.discoveredTools = []*app.ToolView{untracked}
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

func TestFlow2_UC138_RefreshBlockedWhileScanning(t *testing.T) {
	t.Parallel()
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

func TestFlow2_UC139_DotsSyncIsRepeatNoOp(t *testing.T) {
	t.Parallel()
	got := drive(dotsModel(), tea.KeyPressMsg{Code: 's', Text: "s", IsRepeat: true})
	if got.dotsLoading {
		t.Error("dotsLoading should stay false after s IsRepeat in dots view")
	}
}

func TestFlow2_UC140_ViewListIsRepeatNoOps(t *testing.T) {
	t.Parallel()
	outdatedTool := func() []*app.ToolView {
		return []*app.ToolView{{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true}}
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
				orphan := &app.ToolView{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
				m := baseModel(nil)
				m.discoveredTools = []*app.ToolView{orphan}
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

func TestFlow2_UC141_DotsEscClearsBothIndicesSimultaneously(t *testing.T) {
	t.Parallel()
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

func assertNotLoading(t *testing.T, m Model) {
	t.Helper()
	if m.loading {
		t.Error("loading should be false (IsRepeat no-op)")
	}
}

func TestFlow2_UC142_SetupStep3PriorityEditor(t *testing.T) {
	t.Parallel()
	t.Run("keys route through priority editor while editingPriority", func(t *testing.T) {
		m := setupStep3Model()
		got := drive(m, tea.KeyPressMsg{Code: 'x', Text: "x"})
		if !got.priorityDisabled["brew"] {
			t.Error("x should toggle brew into priorityDisabled")
		}
		got2 := drive(got, tea.KeyPressMsg{Code: 'x', Text: "x"})
		if got2.priorityDisabled["brew"] {
			t.Error("second x should remove brew from priorityDisabled")
		}
	})

	t.Run("space sets priorityHolding while editingPriority", func(t *testing.T) {
		got := drive(setupStep3Model(), tea.KeyPressMsg{Code: ' ', Text: " "})
		if !got.priorityHolding {
			t.Error("space should set priorityHolding = true")
		}
	})

	t.Run("esc closes editor and advances wizard to step 5", func(t *testing.T) {
		got := drive(setupStep3Model(), pressEsc())
		if got.editingPriority {
			t.Error("editingPriority should be false after esc")
		}
		if got.setupStep != 5 {
			t.Errorf("setupStep = %d after esc, want 5 (host creation)", got.setupStep)
		}
	})

	t.Run("enter closes editor and advances wizard to step 5", func(t *testing.T) {
		// With a nil app the save cmd is a no-op, but editingPriority must close and the wizard must advance past step 3.
		got := drive(setupStep3Model(), pressEnter())
		if got.editingPriority {
			t.Error("editingPriority should be false after enter")
		}
		if got.setupStep != 5 {
			t.Errorf("setupStep = %d after enter, want 5 (host creation)", got.setupStep)
		}
	})
}

// Tools spread across "base", "work" and "personal" so group-filter tests have a realistic fixture without touching the real app layer.
func multiGroupModel() Model {
	brew := func(name, group string) *app.ToolView {
		return &app.ToolView{Name: name, Provider: "brew", Installed: true}
	}
	tools := []*app.ToolView{
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

func TestFlow2_UC144_GroupNextFilter(t *testing.T) {
	t.Parallel()
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
