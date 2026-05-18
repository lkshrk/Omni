package tui

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// baseModel returns an already-loaded model with the given tools.
// Bypasses the async DB load so tests are synchronous and deterministic.
func baseModel(tools []*database.ToolCache) Model {
	fi := textinput.New()
	fi.Placeholder = "filter…"
	fi.CharLimit = 64
	ci := textinput.New()
	ci.Placeholder = "type a command…"
	ci.CharLimit = 64
	si := textinput.New()
	si.Placeholder = "~/dotfiles"
	si.CharLimit = 256
	m := Model{
		keys:             DefaultKeyMap(),
		spinner:          spinner.New(),
		filter:           fi,
		commandInput:     ci,
		settingsInput:    si,
		mode:             viewList,
		allTools:         tools,
		visibleTools:     tools,
		dotsConfirmIdx:   -1,
		dotsOverwriteIdx: -1,
		dotsLocalIdx:     -1,
		dotsIgnoreIdx:    -1,
		dotsVariantIdx:   -1,
		dangerConfirmRow: -1,
		width:            120,
		height:           80, // realistic terminal size so scroll window doesn't clip test output
	}
	// Populate sectionCounts so View() and Update() reads are consistent.
	m.applyFilter()
	return m
}

// drive feeds messages sequentially into a model and returns the final state.
func drive(m Model, msgs ...tea.Msg) Model {
	var tm tea.Model = m
	for _, msg := range msgs {
		tm, _ = tm.Update(msg)
	}
	return tm.(Model)
}

func pressRune(r rune) tea.Msg { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func pressEnter() tea.Msg      { return tea.KeyPressMsg{Code: tea.KeyEnter} }
func pressEsc() tea.Msg        { return tea.KeyPressMsg{Code: tea.KeyEsc} }
func pressTab() tea.Msg        { return tea.KeyPressMsg{Code: tea.KeyTab} }
func pressShiftTab() tea.Msg   { return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift} }
func pressCtrlD() tea.Msg      { return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl} }
func pressCtrlU() tea.Msg      { return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl} }
func pressCtrlF() tea.Msg      { return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl} }
func pressCtrlB() tea.Msg      { return tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl} }
func pressHome() tea.Msg       { return tea.KeyPressMsg{Code: tea.KeyHome} }

func threeTools() []*database.ToolCache {
	return []*database.ToolCache{
		{Name: "git", Provider: "brew"},
		{Name: "node", Provider: "npm"},
		{Name: "python", Provider: "pip"},
	}
}

func TestModel_CursorNavigation(t *testing.T) {
	tests := []struct {
		name       string
		msgs       []tea.Msg
		wantCursor int
	}{
		// j/k
		{"j moves down", []tea.Msg{pressRune('j')}, 1},
		{"j wraps to top from bottom", []tea.Msg{pressRune('j'), pressRune('j'), pressRune('j')}, 0},
		{"k wraps to bottom from top", []tea.Msg{pressRune('k')}, 2},
		{"j then k returns to 0", []tea.Msg{pressRune('j'), pressRune('k')}, 0},
		{"j j k lands on 1", []tea.Msg{pressRune('j'), pressRune('j'), pressRune('k')}, 1},
		// home/G — top/bottom
		{"home jumps to top from middle", []tea.Msg{pressRune('j'), pressHome()}, 0},
		{"G jumps to bottom", []tea.Msg{pressRune('G')}, 2},
		{"G then home returns to top", []tea.Msg{pressRune('G'), pressHome()}, 0},
		{"G clamps on already-last", []tea.Msg{pressRune('j'), pressRune('j'), pressRune('G')}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := drive(baseModel(threeTools()), tt.msgs...)
			if m.cursor != tt.wantCursor {
				t.Errorf("cursor = %d, want %d", m.cursor, tt.wantCursor)
			}
		})
	}
}

func TestModel_TopBottomOnEmptyList(t *testing.T) {
	t.Run("home on empty list stays at 0", func(t *testing.T) {
		m := drive(baseModel(nil), pressHome())
		if m.cursor != 0 {
			t.Errorf("cursor = %d, want 0", m.cursor)
		}
	})

	t.Run("G on empty list stays at 0", func(t *testing.T) {
		m := drive(baseModel(nil), pressRune('G'))
		if m.cursor != 0 {
			t.Errorf("cursor = %d, want 0", m.cursor)
		}
	})
}

func TestApplyFilter_PreservesWorkflowCursor(t *testing.T) {
	t.Run("same index selects next visible row after current row disappears", func(t *testing.T) {
		m := baseModel(threeTools())
		m.cursor = 1
		m.allTools = []*database.ToolCache{
			{Name: "git", Provider: "brew"},
			{Name: "python", Provider: "pip"},
		}

		m.applyFilter()

		if m.cursor != 1 {
			t.Fatalf("cursor = %d, want 1", m.cursor)
		}
		if got := m.selectedTool(); got == nil || got.Name != "python" {
			t.Fatalf("selected tool = %v, want python", got)
		}
	})

	t.Run("last removed clamps to previous visible row", func(t *testing.T) {
		m := baseModel(threeTools())
		m.cursor = 2
		m.allTools = []*database.ToolCache{
			{Name: "git", Provider: "brew"},
			{Name: "node", Provider: "npm"},
		}

		m.applyFilter()

		if m.cursor != 1 {
			t.Fatalf("cursor = %d, want 1", m.cursor)
		}
		if got := m.selectedTool(); got == nil || got.Name != "node" {
			t.Fatalf("selected tool = %v, want node", got)
		}
	})

	t.Run("empty list keeps cursor at zero", func(t *testing.T) {
		m := baseModel(threeTools())
		m.cursor = 2
		m.allTools = nil

		m.applyFilter()

		if m.cursor != 0 {
			t.Fatalf("cursor = %d, want 0", m.cursor)
		}
		if got := m.selectedTool(); got != nil {
			t.Fatalf("selected tool = %v, want nil", got)
		}
	})
}

func TestApplyFilter_SortsToolsBySectionThenName(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "zoxide", Provider: "brew", Installed: true, Tracked: true},
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "bat", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "fd", Provider: "brew", Installed: true, Tracked: true},
		{Name: "ignored-b", Provider: "brew", Installed: true, Tracked: true},
		{Name: "ignored-a", Provider: "brew", Installed: true, Tracked: true},
	})
	m.ignoreSet = map[string]bool{"ignored-a": true, "ignored-b": true}

	m.applyFilter()

	want := []string{"bat", "ripgrep", "fd", "zoxide", "ignored-a", "ignored-b"}
	if len(m.visibleTools) != len(want) {
		t.Fatalf("visibleTools len = %d, want %d", len(m.visibleTools), len(want))
	}
	for i, name := range want {
		if got := m.visibleTools[i].Name; got != name {
			t.Fatalf("visibleTools[%d] = %q, want %q (all: %v)", i, got, name, toolNames(m.visibleTools))
		}
	}
}

func toolNames(tools []*database.ToolCache) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	return names
}

func TestModel_PageNavigation(t *testing.T) {
	// Build a longer list to make page/half-page meaningful.
	manyTools := func(n int) []*database.ToolCache {
		tools := make([]*database.ToolCache, n)
		for i := range tools {
			tools[i] = &database.ToolCache{Name: "tool", Provider: "brew"}
		}
		return tools
	}

	t.Run("ctrl+d half-page down moves cursor forward", func(t *testing.T) {
		m := baseModel(manyTools(20))
		m.height = 30 // gives listAvailableHeight ~25, half ~12
		got := drive(m, pressCtrlD())
		if got.cursor <= 0 {
			t.Errorf("ctrl+d should advance cursor, got %d", got.cursor)
		}
	})

	t.Run("ctrl+u half-page up from bottom moves cursor back", func(t *testing.T) {
		m := baseModel(manyTools(20))
		m.height = 30
		// Go to bottom first, then page up.
		got := drive(m, pressRune('G'), pressCtrlU())
		if got.cursor >= 19 {
			t.Errorf("ctrl+u should retreat cursor from bottom, got %d", got.cursor)
		}
	})

	t.Run("ctrl+d clamps at last item", func(t *testing.T) {
		m := baseModel(manyTools(3))
		m.height = 30
		got := drive(m, pressCtrlD(), pressCtrlD(), pressCtrlD())
		if got.cursor != 2 {
			t.Errorf("ctrl+d should clamp at last item (2), got %d", got.cursor)
		}
	})

	t.Run("ctrl+u clamps at first item", func(t *testing.T) {
		m := baseModel(manyTools(3))
		m.height = 30
		got := drive(m, pressCtrlU())
		if got.cursor != 0 {
			t.Errorf("ctrl+u at top should stay at 0, got %d", got.cursor)
		}
	})

	t.Run("ctrl+f full-page down moves cursor further than half-page", func(t *testing.T) {
		m := baseModel(manyTools(20))
		m.height = 30
		half := drive(m, pressCtrlD())
		full := drive(m, pressCtrlF())
		if full.cursor <= half.cursor {
			t.Errorf("ctrl+f (%d) should advance further than ctrl+d (%d)", full.cursor, half.cursor)
		}
	})

	t.Run("ctrl+b full-page up from bottom moves cursor back", func(t *testing.T) {
		m := baseModel(manyTools(20))
		m.height = 30
		got := drive(m, pressRune('G'), pressCtrlB())
		if got.cursor >= 19 {
			t.Errorf("ctrl+b should retreat cursor from bottom, got %d", got.cursor)
		}
	})
}

func TestModel_FilterMode(t *testing.T) {
	t.Run("slash enters filter mode", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('/'))
		if m.mode != viewSearch {
			t.Errorf("mode = %v, want viewSearch", m.mode)
		}
	})

	t.Run("esc exits filter mode", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('/'), pressEsc())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
	})

	t.Run("enter blurs input but keeps search bar visible", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('j'), pressRune('/'), pressEnter())
		// Enter submits the search but keeps the search bar visible (mode stays viewSearch).
		if m.mode != viewSearch {
			t.Errorf("mode = %v, want viewSearch (search bar stays visible after Enter)", m.mode)
		}
		if m.filter.Focused() {
			t.Error("filter should be blurred after Enter in search mode")
		}
	})

	t.Run("typing in search mode live-filters allTools", func(t *testing.T) {
		// 'g' matches only "git" — live filter runs immediately against allTools.
		m := drive(baseModel(threeTools()), pressRune('/'), pressRune('g'))
		if len(m.visibleTools) != 1 {
			t.Errorf("visibleTools = %d, want 1 (live filter on allTools)", len(m.visibleTools))
		}
		if len(m.visibleTools) > 0 && m.visibleTools[0].Name != "git" {
			t.Errorf("visibleTools[0] = %q, want git", m.visibleTools[0].Name)
		}
	})

	t.Run("esc exits filter mode and clears filter", func(t *testing.T) {
		// Esc returns to viewList and resets the filter so all tools are visible.
		m := drive(baseModel(threeTools()), pressRune('/'), pressRune('g'), pressEsc())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
		if len(m.visibleTools) != 3 {
			t.Errorf("visibleTools = %d, want 3 (filter cleared)", len(m.visibleTools))
		}
	})
}

func TestModel_EnterKey(t *testing.T) {
	// enter = primary action: installs if tool is missing, no-op if already installed.
	// Mode never changes (no separate detail view — accordion expansion is inline).
	t.Run("enter on missing tool triggers install (loading=true)", func(t *testing.T) {
		// threeTools all have Installed=false by default.
		m := drive(baseModel(threeTools()), pressEnter())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
		if !m.loading {
			t.Error("loading should be true after enter on uninstalled tool")
		}
	})

	t.Run("enter on installed tool is a no-op", func(t *testing.T) {
		tools := []*database.ToolCache{{Name: "git", Provider: "brew", Installed: true}}
		m := drive(baseModel(tools), pressEnter())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
		if m.loading {
			t.Error("loading should stay false for already-installed tool")
		}
	})

	t.Run("enter on empty list is a no-op", func(t *testing.T) {
		m := drive(baseModel(nil), pressEnter())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList on empty list", m.mode)
		}
		if m.loading {
			t.Error("loading should stay false on empty list")
		}
	})
}

func TestModel_ToolsLoadedMsg(t *testing.T) {
	t.Run("success clears loading and populates tools", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		got := drive(m, toolsLoadedMsg{tools: threeTools()})

		if got.loading {
			t.Error("loading should be false after toolsLoadedMsg")
		}
		if len(got.visibleTools) != 3 {
			t.Errorf("visibleTools = %d, want 3", len(got.visibleTools))
		}
		if got.err != nil {
			t.Errorf("unexpected err: %v", got.err)
		}
	})

	t.Run("success from viewStatus sets mode to viewStatus", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		got := drive(m, toolsLoadedMsg{tools: threeTools()})
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus after successful load", got.mode)
		}
	})

	t.Run("success with dots repo starts background dots refresh", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		got := drive(m, toolsLoadedMsg{tools: threeTools(), settings: config.Settings{DotsRepo: "/tmp/dots"}})
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus after successful load", got.mode)
		}
		if !got.dotsLoading {
			t.Error("dotsLoading should start after initial tools load when dots repo is configured")
		}
	})

	t.Run("launch batch status follows latest bulk activity", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true, width: 100, setupReloading: true}
		got := drive(m, toolsLoadedMsg{
			settings:            config.Settings{DotsRepo: "/tmp/dots"},
			configuredProviders: []string{"brew"},
		})
		if got.statusMsg != "Syncing dots…" {
			t.Fatalf("statusMsg = %q, want initial dots operation status", got.statusMsg)
		}
		if got.progressText != "Refreshing providers: brew…" {
			t.Fatalf("progressText = %q, want provider refresh to replace dots activity", got.progressText)
		}
		out := stripANSIEscapeSequences(renderFooterStatusLayer(got, 80))
		if !strings.Contains(out, "Refreshing providers: brew…") || strings.Contains(out, "Syncing dots…") {
			t.Fatalf("footer status = %q, want latest provider activity instead of stale dots status", out)
		}

		dotsGen := got.dotsOpGen
		scanGen := got.scanGen
		got = drive(got, dotsSyncedMsg{gen: dotsGen})
		got = drive(got, providerScannedMsg{gen: scanGen, provider: "brew"})
		if got.progressText != "Finding local tools…" {
			t.Fatalf("progressText = %q, want discovery activity after provider scan", got.progressText)
		}
		out = stripANSIEscapeSequences(renderFooterStatusLayer(got, 80))
		if !strings.Contains(out, "Finding local tools…") {
			t.Fatalf("footer status = %q, want discovery activity after dots finished", out)
		}

		got = drive(got, allProvidersDoneMsg{gen: scanGen}, discoveredRefreshedMsg{gen: got.discoveryGen})
		if got.launchBatchActive {
			t.Fatal("launchBatchActive should be false after all launch work finishes")
		}
		if got.statusMsg != "" || got.progressText != "" {
			t.Fatalf("statusMsg=%q progressText=%q, want cleared after successful launch batch", got.statusMsg, got.progressText)
		}
	})

	t.Run("launch batch reports dots and provider errors after all startup work finishes", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true, setupReloading: true}
		got := drive(m, toolsLoadedMsg{
			settings:            config.Settings{DotsRepo: "/tmp/dots"},
			configuredProviders: []string{"brew"},
		})
		if !got.launchBatchActive {
			t.Fatal("launchBatchActive should be true while startup dots sync and provider scans are running")
		}
		scanGen := got.scanGen
		dotsGen := got.dotsOpGen

		got = drive(got, providerScannedMsg{gen: scanGen, provider: "brew", err: errors.New("brew unavailable")})
		if got.statusIsErr {
			t.Fatalf("provider error should be deferred during launch batch, status=%q", got.statusMsg)
		}
		got = drive(got, dotsSyncedMsg{gen: dotsGen, err: errors.New("dots conflict")})
		if got.statusIsErr {
			t.Fatalf("dots error should be deferred during launch batch, status=%q", got.statusMsg)
		}
		got = drive(got, allProvidersDoneMsg{gen: scanGen})
		got = drive(got, discoveredRefreshedMsg{gen: got.discoveryGen})

		if got.launchBatchActive {
			t.Fatal("launchBatchActive should be false after startup work finishes")
		}
		for _, want := range []string{"launch completed with 2 errors", "scan failed for brew: brew unavailable", "dots conflict"} {
			if !strings.Contains(got.statusMsg, want) {
				t.Fatalf("final launch status missing %q: %q", want, got.statusMsg)
			}
		}
		if !got.statusIsErr {
			t.Fatal("final launch status should be an error")
		}
	})

	t.Run("successful launch batch clears stale dots activity status", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true, setupReloading: true}
		got := drive(m, toolsLoadedMsg{settings: config.Settings{DotsRepo: "/tmp/dots"}})
		if !got.launchBatchActive {
			t.Fatal("launchBatchActive should be true while startup dots sync is running")
		}
		got = drive(got, dotsSyncedMsg{gen: got.dotsOpGen})
		if got.launchBatchActive {
			t.Fatal("launchBatchActive should be false after startup dots sync finishes")
		}
		if got.statusMsg != "" {
			t.Fatalf("statusMsg = %q, want cleared after successful launch batch", got.statusMsg)
		}
		if got.progressText != "" {
			t.Fatalf("progressText = %q, want cleared after successful launch batch", got.progressText)
		}
	})

	t.Run("normal launch does not activate launch batch", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		got := drive(m, toolsLoadedMsg{
			settings:            config.Settings{DotsRepo: "/tmp/dots"},
			configuredProviders: []string{"brew"},
		})
		if got.launchBatchActive {
			t.Fatal("launchBatchActive should be false on normal launch (non-bootstrap)")
		}
	})

	t.Run("success with dots reload target preserves dots tab", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true, mode: viewDots, setupBackgroundMode: viewDots}
		got := drive(m, toolsLoadedMsg{tools: threeTools(), settings: config.Settings{DotsRepo: "/tmp/dots"}})
		if got.mode != viewDots {
			t.Errorf("mode = %v, want viewDots after dots-targeted reload", got.mode)
		}
		if !got.dotsLoading {
			t.Error("dotsLoading should start after returning to Dots tab")
		}
	})

	t.Run("success from viewSetup step0 advances to step1", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true, mode: viewSetup, setupStep: 0}
		m.allTools = []*database.ToolCache{{Name: "snapshot", Provider: "brew"}}
		got := drive(m, toolsLoadedMsg{tools: threeTools()})
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", got.mode)
		}
		if got.setupStep != 1 {
			t.Errorf("setupStep = %v, want 1", got.setupStep)
		}
		if len(got.allTools) != 1 || got.allTools[0].Name != "snapshot" {
			t.Fatalf("allTools changed during onboarding step 0: %+v", got.allTools)
		}
	})

	t.Run("noConfig sets mode to viewSetup", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		got := drive(m, toolsLoadedMsg{noConfig: true})
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup when no config", got.mode)
		}
	})

	t.Run("noHost does not start main data refresh", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		m.allTools = []*database.ToolCache{{Name: "snapshot", Provider: "brew"}}
		cmds := m.handleToolsLoadedMsg(toolsLoadedMsg{
			tools:               threeTools(),
			noHost:              true,
			configuredProviders: []string{"brew"},
		})
		if m.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", m.mode)
		}
		if len(cmds) != 0 {
			t.Fatalf("commands = %d, want 0 while onboarding is active", len(cmds))
		}
		if len(m.scanningProviders) != 0 {
			t.Fatalf("scanningProviders = %v, want none while onboarding is active", m.scanningProviders)
		}
		if len(m.allTools) != 1 || m.allTools[0].Name != "snapshot" {
			t.Fatalf("allTools changed during onboarding: %+v", m.allTools)
		}
	})

	t.Run("toolsLoadedMsg stores dots history", func(t *testing.T) {
		got := drive(baseModel(nil), toolsLoadedMsg{
			dotsHistory:    []app.DotsHistoryEntry{{Operation: "sync", Status: "success", Summary: "sync completed"}},
			dotsHistoryErr: "history warning",
		})
		if len(got.dotsHistory) != 1 || got.dotsHistory[0].Operation != "sync" {
			t.Fatalf("dotsHistory = %+v, want initial sync history", got.dotsHistory)
		}
		if got.dotsHistoryErr != "history warning" {
			t.Fatalf("dotsHistoryErr = %q, want history warning", got.dotsHistoryErr)
		}
	})

	t.Run("error clears loading and sets err", func(t *testing.T) {
		m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
		got := drive(m, toolsLoadedMsg{err: errors.New("db unavailable")})

		if got.loading {
			t.Error("loading should be false even on error")
		}
		if got.err == nil {
			t.Error("expected err to be set")
		}
	})
}

func TestModel_SetupMode(t *testing.T) {
	setupModel := func() Model {
		return Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), mode: viewSetup}
	}

	t.Run("enter starts config creation", func(t *testing.T) {
		got := drive(setupModel(), pressEnter())
		if !got.loading {
			t.Error("loading should be true after pressing enter")
		}
	})

	t.Run("y shortcut starts config creation", func(t *testing.T) {
		got := drive(setupModel(), pressRune('y'))
		if !got.loading {
			t.Error("loading should be true after pressing y")
		}
	})

	t.Run("Y (uppercase) also starts config creation", func(t *testing.T) {
		got := drive(setupModel(), tea.KeyPressMsg{Code: 'Y'})
		if !got.loading {
			t.Error("loading should be true after pressing Y")
		}
	})

	t.Run("other keys ignored in setup mode", func(t *testing.T) {
		got := drive(setupModel(), pressRune('j'), pressRune('/'))
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup; other keys should be ignored", got.mode)
		}
		if got.loading {
			t.Error("loading should not change from unrelated keys")
		}
	})
}

func TestModel_OpCompleteMsg(t *testing.T) {
	t.Run("success sets checkmark status", func(t *testing.T) {
		got := drive(baseModel(nil), opCompleteMsg{message: "sync complete"})
		if got.statusMsg != "✓ sync complete" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
		if got.loading {
			t.Error("loading should be false after opCompleteMsg")
		}
	})

	t.Run("error sets cross status", func(t *testing.T) {
		got := drive(baseModel(nil), opCompleteMsg{err: errors.New("provider not found")})
		if got.statusMsg != "✗ provider not found" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
	})

	t.Run("success with tools updates visible list", func(t *testing.T) {
		got := drive(baseModel(nil), opCompleteMsg{message: "done", tools: threeTools()})
		if len(got.visibleTools) != 3 {
			t.Errorf("visibleTools = %d, want 3", len(got.visibleTools))
		}
	})
}

func pressCtrlC() tea.Msg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

func TestModel_HelpOverlay(t *testing.T) {
	t.Run("? toggles help overlay on", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('?'))
		if !m.help.ShowAll {
			t.Error("help.ShowAll should be true after ?")
		}
	})

	t.Run("? toggles help overlay off", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('?'), pressRune('?'))
		if m.help.ShowAll {
			t.Error("help.ShowAll should be false after second ?")
		}
	})

	t.Run("esc closes help overlay", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('?'), pressEsc())
		if m.help.ShowAll {
			t.Error("help.ShowAll should be false after esc")
		}
	})

	t.Run("help overlay captures background keys", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressRune('?'), pressTab(), pressRune('j'), pressRune('q'))
		if !m.help.ShowAll {
			t.Fatal("help.ShowAll should stay true")
		}
		if m.mode != viewList {
			t.Fatalf("mode = %v, want viewList", m.mode)
		}
		if m.cursor != 0 {
			t.Fatalf("cursor = %d, want unchanged 0", m.cursor)
		}
		if m.confirmQuit {
			t.Fatal("q should not arm quit confirmation behind help")
		}
	})

	t.Run("esc with help closed goes to normal esc handling", func(t *testing.T) {
		// In list mode with help closed, esc has no special effect on help.
		m := drive(baseModel(threeTools()), pressEsc())
		if m.help.ShowAll {
			t.Error("help.ShowAll should stay false")
		}
	})
}

func TestModel_ConfirmQuit(t *testing.T) {
	t.Run("first q sets confirmQuit and shows prompt", func(t *testing.T) {
		m := drive(baseModel(nil), pressRune('q'))
		if !m.confirmQuit {
			t.Error("confirmQuit should be true after first q")
		}
		if m.quitConfirmKey != "q" {
			t.Errorf("quitConfirmKey = %q, want q", m.quitConfirmKey)
		}
		if m.statusMsg != "" {
			t.Errorf("statusMsg = %q, want empty; quit confirmation belongs in footer", m.statusMsg)
		}
	})

	t.Run("first ctrl+c sets confirmQuit", func(t *testing.T) {
		m := drive(baseModel(nil), pressCtrlC())
		if !m.confirmQuit {
			t.Error("confirmQuit should be true after first ctrl+c")
		}
		if m.quitConfirmKey != "ctrl+c" {
			t.Errorf("quitConfirmKey = %q, want ctrl+c", m.quitConfirmKey)
		}
	})

	t.Run("ctrl+c cancels active package action instead of quitting", func(t *testing.T) {
		m := baseModel(threeTools())
		m.loading = true
		cancelled := false
		m.activeActionCancel = func() { cancelled = true }
		m.startRowOperation("git", "brew", "Deleting git…")
		m.upgradingKeys = map[string]bool{"git\x00brew": true}

		got := drive(m, pressCtrlC())
		if !cancelled {
			t.Fatal("active action cancel func was not called")
		}
		if got.confirmQuit {
			t.Fatal("ctrl+c should not arm quit confirmation while an action is cancellable")
		}
		if got.loading || got.rowOpKey != "" || len(got.upgradingKeys) != 0 {
			t.Fatalf("active action state not cleared: loading=%v row=%q upgrades=%v", got.loading, got.rowOpKey, got.upgradingKeys)
		}
		if got.statusMsg != "cancelled" || got.statusIsErr {
			t.Fatalf("status = %q err=%v, want cancelled non-error", got.statusMsg, got.statusIsErr)
		}
	})

	t.Run("any other key resets confirmQuit and clears prompt", func(t *testing.T) {
		m := drive(baseModel(nil), pressRune('q'), pressRune('j'))
		if m.confirmQuit {
			t.Error("confirmQuit should reset after non-quit key")
		}
		if m.statusMsg != "" {
			t.Error("statusMsg should be cleared after reset")
		}
		if m.quitConfirmKey != "" {
			t.Errorf("quitConfirmKey = %q, want empty after reset", m.quitConfirmKey)
		}
	})
}

func TestModel_QuitCancelsBackgroundContext(t *testing.T) {
	parent := context.Background()
	m := New(nil, parent)

	searchCtx, searchCancel := context.WithCancel(context.Background())
	m.searchCancel = searchCancel
	actionCancelled := false
	m.activeActionCancel = func() { actionCancelled = true }
	m.searching = true
	m.loading = true
	m.scanningProviders = map[string]bool{"brew": true}
	m.upgradingKeys = map[string]bool{"ripgrep\x00brew": true}
	m.confirmQuit = true

	tm, _ := m.Update(pressRune('q'))
	got := tm.(Model)

	if got.ctx.Err() == nil {
		t.Fatal("model context is not cancelled after confirmed quit")
	}
	if searchCtx.Err() == nil {
		t.Fatal("search context is not cancelled after confirmed quit")
	}
	if !actionCancelled {
		t.Fatal("active action context is not cancelled after confirmed quit")
	}
	if got.loading || got.searching || len(got.scanningProviders) != 0 || len(got.upgradingKeys) != 0 {
		t.Fatalf("background state still active after quit: loading=%v searching=%v scanning=%v upgrading=%v",
			got.loading, got.searching, got.scanningProviders, got.upgradingKeys)
	}
}

func TestModel_CancelledOperationDoesNotCreateRowError(t *testing.T) {
	m := baseModel(threeTools())
	m.loading = true
	m.startRowOperation("git", "brew", "Deleting git…")

	got := drive(m, opCompleteMsg{err: context.Canceled})
	if got.loading || got.rowOpKey != "" {
		t.Fatalf("cancelled operation should clear loading/row state, got loading=%v row=%q", got.loading, got.rowOpKey)
	}
	if len(got.rowErrors) != 0 {
		t.Fatalf("cancelled operation should not create row error, got %#v", got.rowErrors)
	}
	if got.statusMsg != "cancelled" || got.statusIsErr {
		t.Fatalf("status = %q err=%v, want cancelled non-error", got.statusMsg, got.statusIsErr)
	}
}

func TestModel_KeysIgnoredWhileLoading(t *testing.T) {
	m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
	got := drive(m, pressRune('j'), pressRune('j'), pressEnter(), pressRune('/'), pressRune(':'))

	if got.cursor != 0 {
		t.Errorf("cursor should not change while loading, got %d", got.cursor)
	}
	if got.mode != viewStatus {
		t.Errorf("mode should not change while loading, got %v", got.mode)
	}
}

func TestStartCurrentProviderScans_DedupesConcreteCoveredByEcosystem(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "git", Provider: "brew"}})
	m.configuredProviders = []string{"system", "brew"}
	m.effectiveSystemManager = "brew"

	cmds := m.startCurrentProviderScans()
	if len(cmds) != 2 {
		t.Fatalf("scan commands = %d, want spinner plus one provider scan", len(cmds))
	}
	if len(m.scanningProviders) != 1 || !m.scanningProviders["system"] {
		t.Fatalf("scanningProviders = %v, want only logical system scan", m.scanningProviders)
	}
}

func TestRefreshInstalledProviders_DedupesConcreteCoveredByEcosystem(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "fd", Provider: "system", InstalledWith: "brew"},
		{Name: "git", Provider: "brew"},
	})
	m.configuredProviders = []string{"system", "brew"}
	m.effectiveSystemManager = "brew"

	cmds := m.refreshInstalledProviders()
	if len(cmds) != 2 {
		t.Fatalf("scan commands = %d, want spinner plus one provider scan", len(cmds))
	}
	if len(m.scanningProviders) != 1 || !m.scanningProviders["system"] {
		t.Fatalf("scanningProviders = %v, want only logical system scan", m.scanningProviders)
	}
}

func TestModel_SettingsTab(t *testing.T) {
	// Tab order: Dashboard → Tools → Dots → Groups → Settings → Dashboard.
	// Within Groups, j/k cascades through sections; Tab switches main tabs.
	t.Run("tab from dashboard opens list", func(t *testing.T) {
		m := baseModel(threeTools())
		m.mode = viewStatus
		got := drive(m, pressTab())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
	})

	t.Run("tab from list opens dots", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab())
		if m.mode != viewDots {
			t.Errorf("mode = %v, want viewDots", m.mode)
		}
	})

	t.Run("shift tab from list opens status", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressShiftTab())
		if m.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", m.mode)
		}
		if !m.doctorRunning {
			t.Error("status tab should auto-run doctor when no result is loaded")
		}
	})

	t.Run("shift tab to status does not auto-run doctor while globally loading", func(t *testing.T) {
		base := baseModel(threeTools())
		base.loading = true
		m := drive(base, pressShiftTab())
		if m.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", m.mode)
		}
		if m.doctorRunning {
			t.Error("status tab should not auto-run doctor while another global operation is loading")
		}
	})

	t.Run("tab from dots opens groups", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab())
		if m.mode != viewGroups {
			t.Errorf("mode = %v, want viewGroups", m.mode)
		}
	})

	t.Run("tab from groups opens settings", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab(), pressTab())
		if m.mode != viewSettings {
			t.Errorf("mode = %v, want viewSettings", m.mode)
		}
	})

	t.Run("tab from settings opens status", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab(), pressTab(), pressTab())
		if m.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", m.mode)
		}
	})

	t.Run("tab from settings returns to list", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab(), pressTab(), pressTab(), pressTab())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
	})

	t.Run("esc from hosts returns to list", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab(), pressEsc())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList after esc from hosts", m.mode)
		}
	})

	t.Run("esc from settings returns to list", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab(), pressTab(), pressEsc())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList after esc from settings", m.mode)
		}
	})

	t.Run("tab from filter does not switch tabs", func(t *testing.T) {
		// In viewSearch, tab is consumed by the textinput — mode stays viewSearch.

		m := drive(baseModel(threeTools()), pressRune('/'), pressTab())
		if m.mode != viewSearch {
			t.Error("tab should not switch tabs from filter mode")
		}
	})
}

func TestModel_StatusTabActions(t *testing.T) {
	t.Run("enter starts doctor without locking loading", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.settings.DotsRepo = "/repo/dotfiles"
		m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced}}
		m.doctorErr = "previous failure"
		m.statusCursor = statusRowIndex(statusRows(m), "Doctor")
		got := drive(m, pressEnter())
		if !got.doctorRunning {
			t.Fatal("enter in status tab should start doctor")
		}
		if got.loading {
			t.Fatal("doctor should run as a status activity, not as a global loading lock")
		}
	})

	t.Run("refresh starts doctor", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		got := drive(m, pressRune('R'))
		if !got.doctorRunning {
			t.Fatal("refresh in status tab should start doctor")
		}
	})

	t.Run("back returns to tools", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		got := drive(m, pressEsc())
		if got.mode != viewList {
			t.Fatalf("mode = %v, want viewList", got.mode)
		}
	})
}

func TestModel_SettingsToggle(t *testing.T) {
	t.Run("space toggles auto import on", func(t *testing.T) {
		m := drive(baseModel(nil), append(toSettings(), pressRune(' '))...)
		if !m.settings.AutoImport {
			t.Error("auto import should be true after toggle")
		}
	})

	t.Run("enter does not toggle auto import", func(t *testing.T) {
		m := drive(baseModel(nil), append(toSettings(), pressEnter())...)
		if m.settings.AutoImport {
			t.Error("auto import should stay false after enter")
		}
	})

	t.Run("double toggle returns to false", func(t *testing.T) {
		m := drive(baseModel(nil), append(toSettings(), pressRune(' '), pressRune(' '))...)
		if m.settings.AutoImport {
			t.Error("auto import should be false after two toggles")
		}
	})
}

func TestModel_ToolsLoadedMsg_Settings(t *testing.T) {
	m := Model{keys: DefaultKeyMap(), spinner: spinner.New(), filter: textinput.New(), loading: true}
	got := drive(m, toolsLoadedMsg{
		tools:    threeTools(),
		settings: config.Settings{AutoImport: true},
	})
	if !got.settings.AutoImport {
		t.Error("settings.AutoImport should be true from toolsLoadedMsg")
	}
}

func TestSectionOf(t *testing.T) {
	if sectionOf(&database.ToolCache{Installed: true, Outdated: true}) != sectionUpdates {
		t.Error("installed+outdated → sectionUpdates")
	}
	if sectionOf(&database.ToolCache{Installed: true, Outdated: false}) != sectionInstalled {
		t.Error("installed+current → sectionInstalled")
	}
	if sectionOf(&database.ToolCache{Installed: false}) != sectionAvailable {
		t.Error("not installed → sectionAvailable (used for search results; displaySection handles config-missing→sectionOutOfSync)")
	}
}

func TestCountSection(t *testing.T) {
	tools := []*database.ToolCache{
		{Installed: true, Outdated: true, Tracked: true},
		{Installed: true, Outdated: false, Tracked: true},
		{Installed: false, Tracked: true},
		{Installed: false, Tracked: true},
	}
	m := baseModel(tools)
	if got := m.countSection(sectionUpdates); got != 1 {
		t.Errorf("countSection(updates) = %d, want 1", got)
	}
	if got := m.countSection(sectionInstalled); got != 1 {
		t.Errorf("countSection(installed) = %d, want 1", got)
	}
	if got := m.countSection(sectionOutOfSync); got != 2 {
		t.Errorf("countSection(outOfSync/missing) = %d, want 2", got)
	}
}

func TestCycleNodeManager(t *testing.T) {
	if got := cycleNodeManager(""); got != "bun" {
		t.Errorf("cycleNodeManager(\"\") = %q, want bun", got)
	}
	if got := cycleNodeManager("bun"); got != "pnpm" {
		t.Errorf("cycleNodeManager(\"bun\") = %q, want pnpm", got)
	}
	if got := cycleNodeManager("pnpm"); got != "npm" {
		t.Errorf("cycleNodeManager(\"pnpm\") = %q, want npm", got)
	}
	if got := cycleNodeManager("npm"); got != "" {
		t.Errorf("cycleNodeManager(\"npm\") = %q, want \"\"", got)
	}
}

func TestModel_SettingsCursor(t *testing.T) {
	t.Run("j moves cursor down in settings", func(t *testing.T) {
		m := drive(baseModel(nil), append(toSettings(), pressRune('j'))...)
		if m.settingsCursor != 1 {
			t.Errorf("settingsCursor = %d, want 1", m.settingsCursor)
		}
	})

	t.Run("cursor wraps to top from bottom", func(t *testing.T) {
		// Exactly numSettingRows j presses from row 0 wraps back to 0.
		msgs := append(toSettings(), nj(numSettingRows)...)
		m := drive(baseModel(nil), msgs...)
		if m.settingsCursor != 0 {
			t.Errorf("settingsCursor = %d, want 0 (wrapped)", m.settingsCursor)
		}
	})

	t.Run("k moves cursor up", func(t *testing.T) {
		msgs := append(toSettings(), pressRune('j'), pressRune('k'))
		m := drive(baseModel(nil), msgs...)
		if m.settingsCursor != 0 {
			t.Errorf("settingsCursor = %d, want 0", m.settingsCursor)
		}
	})
}

func TestModel_SettingsVisibleRowsMutateExpectedFields(t *testing.T) {
	toSettingsRow := func(row int) []tea.Msg {
		msgs := toSettings()
		for range row {
			msgs = append(msgs, pressRune('j'))
		}
		return msgs
	}

	for _, tc := range []struct {
		name   string
		row    int
		action tea.Msg
		assert func(t *testing.T, m Model)
	}{
		{
			name:   "import installed tools",
			row:    0,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !m.settings.AutoImport {
					t.Fatal("row 0 should toggle AutoImport")
				}
			},
		},
		{
			name:   "system provider order",
			row:    settingsRowSystemPriority,
			action: pressEnter(),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !m.editingPriority {
					t.Fatalf("row %d should open provider order editor", settingsRowSystemPriority)
				}
			},
		},
		{
			name:   "system provider",
			row:    settingsRowSystemProvider,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !slices.Contains(m.settings.DisabledProviders, "system") {
					t.Fatalf("row %d should toggle system provider, disabled providers = %v", settingsRowSystemProvider, m.settings.DisabledProviders)
				}
			},
		},
		{
			name:   "node provider",
			row:    settingsRowNodeProvider,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !slices.Contains(m.settings.DisabledProviders, "node") {
					t.Fatalf("row %d should toggle node provider, disabled providers = %v", settingsRowNodeProvider, m.settings.DisabledProviders)
				}
			},
		},
		{
			name:   "python provider",
			row:    settingsRowPythonProvider,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !slices.Contains(m.settings.DisabledProviders, "python") {
					t.Fatalf("row %d should toggle python provider, disabled providers = %v", settingsRowPythonProvider, m.settings.DisabledProviders)
				}
			},
		},
		{
			name:   "node manager",
			row:    settingsRowNodeManager,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if got := m.settings.EcosystemManager("node"); got != "bun" {
					t.Fatalf("row %d should cycle node manager to bun, got %q", settingsRowNodeManager, got)
				}
			},
		},
		{
			name:   "python manager",
			row:    settingsRowPythonManager,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if got := m.settings.EcosystemManager("python"); got != "uv" {
					t.Fatalf("row %d should cycle python manager to uv, got %q", settingsRowPythonManager, got)
				}
			},
		},
		{
			name:   "repository",
			row:    settingsRowDotsRepo,
			action: pressEnter(),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !m.showFilePicker {
					t.Fatalf("row %d should open dots repository file picker", settingsRowDotsRepo)
				}
			},
		},
		{
			name:   "dotfile sync",
			row:    settingsRowDotsSync,
			action: pressEnter(),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if m.dangerConfirmRow != settingsRowDotsSync {
					t.Fatalf("row %d should ask keep-local choice, dangerConfirmRow = %d", settingsRowDotsSync, m.dangerConfirmRow)
				}
			},
		},
		{
			name:   "commit changes",
			row:    settingsRowDotsCommit,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !m.settings.DotsGit.AutoCommit {
					t.Fatalf("row %d should toggle dots auto commit", settingsRowDotsCommit)
				}
			},
		},
		{
			name:   "push changes",
			row:    settingsRowDotsPush,
			action: pressRune(' '),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !m.settings.DotsGit.AutoPush {
					t.Fatalf("row %d should toggle dots auto push", settingsRowDotsPush)
				}
			},
		},
		{
			name:   "doctor",
			row:    settingsRowDoctor,
			action: pressEnter(),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if !m.doctorRunning {
					t.Fatalf("doctor row should start diagnostics, doctorRunning=%v loading=%v", m.doctorRunning, m.loading)
				}
			},
		},
		{
			name:   "reset settings",
			row:    settingsRowResetSettings,
			action: pressEnter(),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if m.dangerConfirmRow != settingsRowResetSettings {
					t.Fatalf("reset settings row should arm confirmation, dangerConfirmRow = %d", m.dangerConfirmRow)
				}
			},
		},
		{
			name:   "reset cache",
			row:    settingsRowResetCache,
			action: pressEnter(),
			assert: func(t *testing.T, m Model) {
				t.Helper()
				if m.dangerConfirmRow != settingsRowResetCache {
					t.Fatalf("reset cache row should arm confirmation, dangerConfirmRow = %d", m.dangerConfirmRow)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := baseModel(nil)
			base.settings.DotsRepo = "~/dotfiles"
			msgs := append(toSettingsRow(tc.row), tc.action)
			tc.assert(t, drive(base, msgs...))
		})
	}
}

func TestModel_SettingsReminderToggleStartsServiceCommand(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "remindertoggle")
	a, _ := newCmdApp(t, &okProvider{name: "brew"}, nil)
	m := modelForCmds(a)
	m.settings.DotsRepo = "/tmp/dotfiles"
	m.settingsCursor = settingsRowDotsReminder
	m.dotsReminderService = &app.DotsReminderService{Installed: false}

	var cmds []tea.Cmd
	m.handleSettingsRowAction(&cmds)

	if !m.loading {
		t.Fatal("reminder toggle should enter loading state")
	}
	if !strings.Contains(m.statusMsg, "Enabling dotfile reminders") {
		t.Fatalf("statusMsg = %q, want enabling reminder status", m.statusMsg)
	}
	if len(cmds) != 2 {
		t.Fatalf("reminder toggle commands = %d, want spinner + service command", len(cmds))
	}
}

func TestModel_SettingsWatchTogglePromptsForStow(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "watchtoggle")
	t.Setenv("PATH", t.TempDir())
	a, _ := newCmdApp(t, &okProvider{name: "brew"}, nil)
	m := modelForCmds(a)
	m.settings.DotsRepo = "/tmp/dotfiles"
	m.settingsCursor = settingsRowDotsWatch
	m.dotsWatchService = &app.DotsWatchService{Installed: false}

	var cmds []tea.Cmd
	m.handleSettingsRowAction(&cmds)

	if !m.stowInstallPrompt {
		t.Fatal("watch toggle should prompt for GNU Stow before enabling service")
	}
	if m.stowInstallAction != stowInstallDotsWatch {
		t.Fatalf("stowInstallAction = %v, want stowInstallDotsWatch", m.stowInstallAction)
	}
	if m.loading {
		t.Fatal("watch toggle should wait for stow confirmation before loading")
	}
	if len(cmds) != 0 {
		t.Fatalf("watch toggle commands = %d, want none until stow prompt is answered", len(cmds))
	}
}

func TestModel_SettingsServiceToggleRequiresDotsRepo(t *testing.T) {
	m := baseModel(nil)
	m.settingsCursor = settingsRowDotsReminder

	var cmds []tea.Cmd
	m.handleSettingsRowAction(&cmds)

	if m.loading {
		t.Fatal("service toggle without dots repo should not start loading")
	}
	if m.statusMsg != "Dots not configured." {
		t.Fatalf("statusMsg = %q, want dots not configured", m.statusMsg)
	}
	if len(cmds) != 1 {
		t.Fatalf("service toggle without repo commands = %d, want status clear timer", len(cmds))
	}
}

func TestModel_SettingsReminderIntervalPickerSetsPendingValue(t *testing.T) {
	m := baseModel(nil)
	m.settingsCursor = settingsRowDotsReminderInterval
	m.dotsReminderInterval = 15 * time.Minute

	var cmds []tea.Cmd
	m.handleSettingsEditAction(&cmds)
	if !m.editingServiceDuration {
		t.Fatal("reminder interval row should open duration picker")
	}
	cmds = m.handleSettingsServiceDurationKeyMsg(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if len(cmds) != 0 {
		t.Fatalf("adjusting duration produced %d commands, want none", len(cmds))
	}
	cmds = m.handleSettingsServiceDurationKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.editingServiceDuration {
		t.Fatal("enter should close duration picker")
	}
	if m.dotsReminderInterval != 30*time.Minute {
		t.Fatalf("dotsReminderInterval = %s, want 30m", m.dotsReminderInterval)
	}
	if m.loading {
		t.Fatal("pending interval change should not start service work when reminder is disabled")
	}
	if len(cmds) != 1 {
		t.Fatalf("pending interval change commands = %d, want status clear timer", len(cmds))
	}
}

func TestModel_SettingsWatchDebouncePickerUpdatesInstalledService(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "watchdebounce")
	a, _ := newCmdApp(t, &okProvider{name: "brew"}, nil)
	m := modelForCmds(a)
	m.settingsCursor = settingsRowDotsWatchDebounce
	m.dotsWatchService = &app.DotsWatchService{Installed: true, Debounce: time.Second}
	m.dotsWatchDebounce = time.Second
	m.stowInstalled = true

	var cmds []tea.Cmd
	m.handleSettingsEditAction(&cmds)
	cmds = m.handleSettingsServiceDurationKeyMsg(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if len(cmds) != 0 {
		t.Fatalf("adjusting duration produced %d commands, want none", len(cmds))
	}
	cmds = m.handleSettingsServiceDurationKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.editingServiceDuration {
		t.Fatal("enter should close duration picker")
	}
	if m.dotsWatchDebounce != time.Second {
		t.Fatalf("dotsWatchDebounce = %s, want old 1s value until service update succeeds", m.dotsWatchDebounce)
	}
	if m.dotsWatchDebounceNext != 2*time.Second {
		t.Fatalf("dotsWatchDebounceNext = %s, want pending 2s value", m.dotsWatchDebounceNext)
	}
	if !m.loading {
		t.Fatal("installed watch debounce update should start service work")
	}
	if len(cmds) != 2 {
		t.Fatalf("installed watch debounce update commands = %d, want spinner + service command", len(cmds))
	}

	m = drive(m, dotsServiceChangedMsg{
		kind:    dotsWatchServiceKind,
		enabled: true,
		watch:   &app.DotsWatchService{Installed: true, Debounce: 2 * time.Second},
	})
	if m.dotsWatchDebounce != 2*time.Second {
		t.Fatalf("dotsWatchDebounce after success = %s, want 2s", m.dotsWatchDebounce)
	}
	if m.dotsWatchDebounceNext != 0 {
		t.Fatalf("dotsWatchDebounceNext after success = %s, want cleared", m.dotsWatchDebounceNext)
	}
}

func TestModel_SettingsWatchDebouncePickerPromptsForStowBeforeInstalledServiceUpdate(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "watchdebouncestow")
	t.Setenv("PATH", t.TempDir())
	a, _ := newCmdApp(t, &okProvider{name: "brew"}, nil)
	m := modelForCmds(a)
	m.settingsCursor = settingsRowDotsWatchDebounce
	m.dotsWatchService = &app.DotsWatchService{Installed: true, Debounce: time.Second}
	m.dotsWatchDebounce = time.Second

	var cmds []tea.Cmd
	m.handleSettingsEditAction(&cmds)
	cmds = m.handleSettingsServiceDurationKeyMsg(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if len(cmds) != 0 {
		t.Fatalf("adjusting duration produced %d commands, want none", len(cmds))
	}
	cmds = m.handleSettingsServiceDurationKeyMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.stowInstallPrompt {
		t.Fatal("installed watch debounce update should prompt for GNU Stow before service work")
	}
	if m.stowInstallAction != stowInstallDotsWatch {
		t.Fatalf("stowInstallAction = %v, want stowInstallDotsWatch", m.stowInstallAction)
	}
	if m.dotsWatchDebounce != time.Second {
		t.Fatalf("dotsWatchDebounce = %s, want old 1s value until service update succeeds", m.dotsWatchDebounce)
	}
	if m.loading {
		t.Fatal("installed watch debounce update should wait for stow prompt before loading")
	}
	if len(cmds) != 0 {
		t.Fatalf("installed watch debounce update commands = %d, want none until stow prompt is answered", len(cmds))
	}
}

func TestModel_DotsWatchDebounceRevertsWhenInstalledServiceUpdateFails(t *testing.T) {
	oldService := &app.DotsWatchService{Installed: true, Debounce: time.Second}
	m := baseModel(nil)
	m.dotsWatchService = oldService
	m.dotsWatchDebounce = 2 * time.Second
	m.loading = true

	got := drive(m, dotsServiceChangedMsg{
		kind:    dotsWatchServiceKind,
		enabled: true,
		watch:   &app.DotsWatchService{Installed: true, Debounce: 5 * time.Second},
		err:     errors.New("install watch service: stow not found"),
	})

	if got.loading {
		t.Fatal("service result should clear loading")
	}
	if got.dotsWatchService != oldService {
		t.Fatalf("dotsWatchService was replaced on error: %+v", got.dotsWatchService)
	}
	if got.dotsWatchDebounce != time.Second {
		t.Fatalf("dotsWatchDebounce = %s, want restored service value 1s", got.dotsWatchDebounce)
	}
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "stow not found") {
		t.Fatalf("status = (%q, err=%v), want stow error", got.statusMsg, got.statusIsErr)
	}
}

func TestModel_DotsReminderIntervalRevertsWhenInstalledServiceUpdateFails(t *testing.T) {
	oldService := &app.DotsReminderService{Installed: true, Interval: 2 * time.Hour}
	m := baseModel(nil)
	m.dotsReminderService = oldService
	m.dotsReminderInterval = 4 * time.Hour
	m.loading = true

	got := drive(m, dotsServiceChangedMsg{
		kind:     dotsReminderServiceKind,
		enabled:  true,
		reminder: &app.DotsReminderService{Installed: true, Interval: 8 * time.Hour},
		err:      errors.New("install reminder service: launchctl failed"),
	})

	if got.loading {
		t.Fatal("service result should clear loading")
	}
	if got.dotsReminderService != oldService {
		t.Fatalf("dotsReminderService was replaced on error: %+v", got.dotsReminderService)
	}
	if got.dotsReminderInterval != 2*time.Hour {
		t.Fatalf("dotsReminderInterval = %s, want restored service value 2h", got.dotsReminderInterval)
	}
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "launchctl failed") {
		t.Fatalf("status = (%q, err=%v), want launchctl error", got.statusMsg, got.statusIsErr)
	}
}

func TestModel_DotsServiceChangedMsgUpdatesStatus(t *testing.T) {
	got := drive(baseModel(nil), dotsServiceChangedMsg{
		kind:     dotsReminderServiceKind,
		enabled:  true,
		reminder: &app.DotsReminderService{Installed: true, Interval: 4 * time.Hour},
	})
	if got.loading {
		t.Fatal("service result should clear loading")
	}
	if got.dotsReminderService == nil || !got.dotsReminderService.Installed {
		t.Fatalf("reminder service status not updated: %+v", got.dotsReminderService)
	}
	if got.dotsReminderInterval != 4*time.Hour {
		t.Fatalf("dotsReminderInterval = %s, want 4h", got.dotsReminderInterval)
	}
	if !strings.Contains(got.statusMsg, "dotfile reminder service enabled") {
		t.Fatalf("statusMsg = %q, want enabled status", got.statusMsg)
	}
}

func TestModel_DoctorDoneMsgStoresResult(t *testing.T) {
	result := &app.DoctorResult{Summary: app.DoctorSummary{OK: 2, Warn: 1}}
	m := baseModel(nil)
	m.doctorRunning = true

	got := drive(m, doctorDoneMsg{result: result})
	if got.doctorRunning {
		t.Fatalf("doctor result should clear doctor running state, loading=%v running=%v", got.loading, got.doctorRunning)
	}
	if got.doctorResult != result {
		t.Fatalf("doctorResult not stored: %+v", got.doctorResult)
	}
	if got.statusIsErr || !strings.Contains(got.statusMsg, "doctor complete") {
		t.Fatalf("status = (%q, err=%v), want success", got.statusMsg, got.statusIsErr)
	}
}

func TestModel_DoctorDoneMsgDoesNotClearUnrelatedLoading(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.doctorRunning = true

	got := drive(m, doctorDoneMsg{result: &app.DoctorResult{}})
	if !got.loading {
		t.Fatal("doctor completion should not clear unrelated global loading")
	}
	if got.doctorRunning {
		t.Fatal("doctorRunning should still be cleared")
	}
}

func TestModel_DotsRepoEdit(t *testing.T) {
	// navigate to settings, then down to settingsRowDotsRepo (row 7)
	toRow4 := func() []tea.Msg {
		return append(toSettings(), nj(settingsRowDotsRepo)...)
	}

	t.Run("enter on row 7 shows file picker", func(t *testing.T) {
		msgs := append(toRow4(), pressEnter())
		m := drive(baseModel(nil), msgs...)
		if !m.showFilePicker {
			t.Error("expected showFilePicker=true after enter on row 7")
		}
	})

	t.Run("space on row 7 is no-op", func(t *testing.T) {
		msgs := append(toRow4(), pressRune(' '))
		m := drive(baseModel(nil), msgs...)
		if m.showFilePicker {
			t.Error("expected showFilePicker=false after space on row 7")
		}
	})

	t.Run("esc closes file picker without saving", func(t *testing.T) {
		m := baseModel(nil)
		m.settings.DotsRepo = "~/original"
		msgs := append(toRow4(), pressEnter(), pressEsc())
		m = drive(m, msgs...)
		if m.showFilePicker {
			t.Error("expected showFilePicker=false after esc")
		}
		if m.settings.DotsRepo != "~/original" {
			t.Errorf("DotsRepo = %q, want ~/original (unchanged after esc)", m.settings.DotsRepo)
		}
	})
}

func TestModel_HostsTab(t *testing.T) {
	t.Run("tab from dots opens hosts", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab())
		if m.mode != viewGroups {
			t.Errorf("mode = %v, want viewGroups", m.mode)
		}
	})

	t.Run("esc from hosts returns to list", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab(), pressEsc())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
	})

	t.Run("j/k navigate host cursor", func(t *testing.T) {
		m := drive(baseModel(threeTools()), pressTab(), pressTab())
		// hostCursor starts at 0; hostInfo is nil so Down is clamped
		if m.hostCursor != 0 {
			t.Errorf("hostCursor = %d, want 0", m.hostCursor)
		}
	})
}

func TestModel_ProviderSubtabs(t *testing.T) {
	modelWithProviders := func() Model {
		m := baseModel(threeTools()) // git/brew, node/npm, python/pip
		m.applyFilter()              // populates providerNames from allTools
		return m
	}

	t.Run("] advances provider tab", func(t *testing.T) {
		m := drive(modelWithProviders(), pressRune(']'))
		if m.providerTabIdx != 1 {
			t.Errorf("providerTabIdx = %d, want 1", m.providerTabIdx)
		}
	})

	t.Run("] wraps around past last", func(t *testing.T) {
		m := modelWithProviders()
		n := len(m.providerNames) // 3 providers (brew, npm, pip)
		inputs := make([]tea.Msg, n+1)
		for i := range inputs {
			inputs[i] = pressRune(']')
		}
		m = drive(m, inputs...)
		if m.providerTabIdx != 0 {
			t.Errorf("providerTabIdx = %d, want 0 (wrapped)", m.providerTabIdx)
		}
	})

	t.Run("[ at All wraps to last", func(t *testing.T) {
		m := drive(modelWithProviders(), pressRune('['))
		if m.providerTabIdx == 0 {
			t.Errorf("providerTabIdx = 0, want last provider index")
		}
	})

	t.Run("provider filter narrows visibleTools", func(t *testing.T) {
		m := modelWithProviders()
		// Find the "system" ecosystem index (brew maps to system).
		sysIdx := 0
		for i, p := range m.providerNames {
			if p == "system" {
				sysIdx = i + 1
				break
			}
		}
		m.providerTabIdx = sysIdx
		m.applyFilter()
		for _, t2 := range m.visibleTools {
			if providerEcosystem(t2.Provider) != "system" {
				t.Errorf("visibleTools contains non-system tool %q (provider %q)", t2.Name, t2.Provider)
			}
		}
	})

	t.Run("provider filter applies to search results", func(t *testing.T) {
		m := modelWithProviders()
		// Activate python filter.
		for i, p := range m.providerNames {
			if p == "python" {
				m.providerTabIdx = i + 1
				break
			}
		}
		// Inject search results containing both a python and a brew tool.
		m.searchTools = []*database.ToolCache{
			{Name: "requests", Provider: "pip"}, // python ecosystem → should appear
			{Name: "jq", Provider: "brew"},      // system ecosystem → should be filtered
		}
		m.applyFilter()
		for _, t2 := range m.visibleTools {
			if providerEcosystem(t2.Provider) != "python" {
				t.Errorf("search result %q (provider %q) leaked through python filter", t2.Name, t2.Provider)
			}
		}
	})

	t.Run("esc clears active tool filters and search results", func(t *testing.T) {
		m := modelWithProviders()
		cancelled := false
		m.searchCancel = func() { cancelled = true }
		m.searching = true
		m.searchTools = []*database.ToolCache{{Name: "ripgrep", Provider: "system"}}
		m.filter.SetValue("rip")
		m.providerTabIdx = 1
		m.groupFilter = "work"
		m.groupTabIdx = 1
		m.applyFilter()

		got := drive(m, pressEsc())
		if !cancelled {
			t.Fatal("esc should cancel in-flight search")
		}
		if got.mode != viewList {
			t.Fatalf("mode = %v, want viewList", got.mode)
		}
		if got.filter.Value() != "" || got.providerTabIdx != 0 || got.groupFilter != "" || got.groupTabIdx != 0 {
			t.Fatalf("filters not cleared: query=%q provider=%d group=%q groupIdx=%d", got.filter.Value(), got.providerTabIdx, got.groupFilter, got.groupTabIdx)
		}
		if got.searching || len(got.searchTools) != 0 {
			t.Fatalf("search state not cleared: searching=%v tools=%d", got.searching, len(got.searchTools))
		}
	})

	t.Run("group filter hides discovered tools without config group", func(t *testing.T) {
		orphan := &database.ToolCache{Name: "fzf", Provider: "system", InstalledWith: "brew", Installed: true, Tracked: false}
		m := modelWithProviders()
		m.groupNames = []string{"base", "work"}
		m.groupFilter = "base"
		m.discoveredTools = []*database.ToolCache{orphan}
		m.rebuildDiscoveredKeys()
		m.applyFilter()
		for _, t2 := range m.visibleTools {
			if t2.Name == "fzf" {
				t.Fatal("discovered tool without config group should not appear in a group-filtered view")
			}
		}
	})
}

func TestHandleOpCompleteMsg_RemovesInstalledSearchResultAndCache(t *testing.T) {
	key := toolKey("ripgrep", "system")
	stale := &database.ToolCache{Name: "ripgrep", Provider: "system", Package: "ripgrep", Tracked: false}
	installed := &database.ToolCache{Name: "ripgrep", Provider: "system", Package: "ripgrep", Installed: true, Tracked: true}
	m := baseModel(nil)
	m.mode = viewSearch
	m.filter.SetValue("ripgrep")
	m.filter.Blur()
	m.searchTools = []*database.ToolCache{stale}
	m.searchCache = map[string]searchCacheEntry{
		searchCacheKey("ripgrep", ""): {tools: []*database.ToolCache{stale}},
	}
	m.applyFilter()

	m.handleOpCompleteMsg(opCompleteMsg{
		message:              "installed ripgrep and added to config",
		tools:                []*database.ToolCache{installed},
		removeDiscoveredKeys: []string{key},
	})

	if len(m.searchTools) != 0 {
		t.Fatalf("searchTools = %d, want stale installed result removed", len(m.searchTools))
	}
	if got := len(m.searchCache[searchCacheKey("ripgrep", "")].tools); got != 0 {
		t.Fatalf("cached search results = %d, want stale installed result removed", got)
	}
	if len(m.visibleTools) != 1 || m.visibleTools[0].Name != "ripgrep" || !m.visibleTools[0].Installed || !m.visibleTools[0].Tracked {
		t.Fatalf("visibleTools = %+v, want refreshed installed config row", m.visibleTools)
	}
}

func TestModel_GroupPicker(t *testing.T) {
	modelWithGroups := func() Model {
		t.Setenv("OMNI_HOSTNAME", "host")
		m := baseModel(threeTools())
		m.groupNames = []string{"work"}
		m.toolGroups = map[string]string{
			"git\x00brew":   "host",
			"node\x00npm":   "work",
			"python\x00pip": "work",
		}
		return m
	}

	t.Run("g opens group membership picker when groups exist", func(t *testing.T) {
		m := drive(modelWithGroups(), pressRune('g'))
		if m.mode != viewGroupMembership {
			t.Errorf("mode = %v, want viewGroupMembership", m.mode)
		}
		if len(m.pickerGroups) == 0 {
			t.Error("pickerGroups should be populated")
		}
	})

	t.Run("esc from picker returns to list", func(t *testing.T) {
		m := drive(modelWithGroups(), pressRune('g'), pressEsc())
		if m.mode != viewList {
			t.Errorf("mode = %v, want viewList", m.mode)
		}
	})

	t.Run("g on empty list is no-op", func(t *testing.T) {
		m := drive(baseModel(nil), pressRune('g'))
		if m.mode == viewGroupMembership {
			t.Error("group picker should not open with no tools")
		}
	})

	t.Run("g with no groups opens membership picker with host", func(t *testing.T) {
		t.Setenv("OMNI_HOSTNAME", "host")
		m := drive(baseModel(threeTools()), pressRune('g'))
		if m.mode != viewGroupMembership {
			t.Errorf("mode = %v, want viewGroupMembership", m.mode)
		}
		want := []string{"host", groupPickerNewSentinel}
		if len(m.pickerGroups) != len(want) || m.pickerGroups[0] != want[0] {
			t.Errorf("pickerGroups = %v, want %v", m.pickerGroups, want)
		}
	})
}

func TestModel_DotsRootRowsOpenGroupMembershipPicker(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "settings.json")
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Groups: []*config.GroupConfig{
			{Name: shortHostname(), Special: "host", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
			{Name: "work"},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	a := app.New(cfgPath)
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewDots
	m.settings.DotsRepo = "/repo"
	m.dotsLoaded = true
	m.groupNames = []string{shortHostname(), "work"}
	m.dotsEntries = []app.DotStatus{{
		Name:    "nvim",
		State:   app.DotStateSynced,
		Actions: []app.DotAction{app.DotActionRemove},
	}}

	got := drive(m, pressRune('g'))
	if got.mode != viewGroupMembership {
		t.Fatalf("mode = %v, want viewGroupMembership", got.mode)
	}
	if got.pickerMembershipKind != pickerMembershipDot || got.pickerMembershipName != "nvim" {
		t.Fatalf("picker target kind=%q name=%q, want dot/nvim", got.pickerMembershipKind, got.pickerMembershipName)
	}
	if !slices.Contains(got.dotMemberships["nvim"], shortHostname()) {
		t.Fatalf("dotMemberships[nvim] = %v, want host group", got.dotMemberships["nvim"])
	}
}

func TestModel_GroupMembershipPicker_SpaceSelectsAndSaves(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "ripgrep", Provider: "system", Tracked: true}})
	key := toolKey("ripgrep", "system")
	m.mode = viewGroupMembership
	m.pickerGroups = []string{"base", "work"}
	m.pickerCursor = 1
	m.toolMemberships = map[string][]string{key: {"base"}}
	m.pickerMembershipKey = key
	m.pickerOriginalGroups = []string{"base"}

	got := drive(m, pressRune(' '))
	if !slices.Equal(got.toolMemberships[key], []string{"work"}) {
		t.Fatalf("space should select work as the only membership, got %v", got.toolMemberships[key])
	}
	if !got.loading {
		t.Fatal("space should save selected membership changes")
	}
	if got.mode != viewList {
		t.Fatalf("mode = %v, want viewList after save", got.mode)
	}
}

func TestModel_GroupMembershipPicker_TargetSurvivesCursorMove(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	rgKey := toolKey("ripgrep", "system")
	fdKey := toolKey("fd", "system")
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Tracked: true},
		{Name: "fd", Provider: "system", Tracked: true},
	})
	m.toolMemberships = map[string][]string{
		rgKey: {"host"},
		fdKey: {"work"},
	}

	m.cursor = 1
	m.openGroupMembershipPicker()
	m.cursor = 0

	name, memberships, ok := m.selectedMembershipTarget()
	if !ok {
		t.Fatal("selectedMembershipTarget should still resolve the opened picker target")
	}
	if name != "ripgrep" {
		t.Fatalf("selectedMembershipTarget name = %q, want ripgrep", name)
	}
	if !slices.Equal(memberships, []string{"host"}) {
		t.Fatalf("selectedMembershipTarget memberships = %v, want [host]", memberships)
	}

	m.setSelectedMemberships([]string{"work"})
	if !slices.Equal(m.toolMemberships[rgKey], []string{"work"}) {
		t.Fatalf("ripgrep memberships = %v, want [work]", m.toolMemberships[rgKey])
	}
	if !slices.Equal(m.toolMemberships[fdKey], []string{"work"}) {
		t.Fatalf("fd memberships = %v, want unchanged [work]", m.toolMemberships[fdKey])
	}
}

func TestModel_GroupPickerUsesCapturedClaimToolAfterCursorMoves(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: true},
		{Name: "zoxide", Provider: "system", Installed: true},
	})
	m.openGroupPicker(true)
	m.cursor = 1
	m.pickerGroups = []string{"work"}

	got := drive(m, pressEnter())
	if got.statusMsg != "Adding ripgrep to config…" {
		t.Fatalf("statusMsg = %q, want captured ripgrep target", got.statusMsg)
	}
	if got.pickerActionToolSet {
		t.Fatal("picker action target should be cleared after selection")
	}
}

func TestModel_GroupPickerUsesCapturedInstallToolAfterCursorMoves(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system"},
		{Name: "zoxide", Provider: "system"},
	})
	m.openInstallGroupPicker()
	m.cursor = 1
	m.pickerGroups = []string{"work"}

	got := drive(m, pressEnter())
	if got.statusMsg != "Installing ripgrep…" {
		t.Fatalf("statusMsg = %q, want captured ripgrep target", got.statusMsg)
	}
	if got.rowOpKey != toolKey("ripgrep", "system") {
		t.Fatalf("rowOpKey = %q, want ripgrep/system", got.rowOpKey)
	}
	if got.pickerActionToolSet {
		t.Fatal("picker action target should be cleared after selection")
	}
}

func TestModel_GroupMembershipPicker_NewGroupDraft(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	m := baseModel([]*database.ToolCache{{Name: "ripgrep", Provider: "system", Tracked: true}})
	key := toolKey("ripgrep", "system")
	m.mode = viewGroupMembership
	m.pickerGroups = []string{"host", groupPickerNewSentinel}
	m.pickerCursor = 1
	m.toolMemberships = map[string][]string{key: {"host"}}
	m.pickerMembershipKey = key
	m.pickerOriginalGroups = []string{"host"}
	m.hostInfo = &app.HostInfo{
		Active: "main",
		Hosts:  map[string]config.HostAssignment{"main": {Groups: []string{"host"}}},
	}

	got := drive(m, pressEnter())
	if !got.pickerCreatingGroup {
		t.Fatal("enter on new-group row should open inline input")
	}
	got.settingsInput.SetValue("work")
	got = drive(got, pressEnter())
	if got.pickerCreatingGroup {
		t.Fatal("new group input should close after submit")
	}
	if !slices.Equal(got.toolMemberships[key], []string{"work"}) {
		t.Fatalf("new group should become the only selected group, got memberships %v", got.toolMemberships[key])
	}
	if !groupInActiveHost(got, "work") {
		t.Fatal("new group should be treated as part of the current host while staged")
	}
	if got.pickerGroups[len(got.pickerGroups)-1] != groupPickerNewSentinel {
		t.Fatalf("new-group row should remain last, got %v", got.pickerGroups)
	}
}

func TestModel_ProviderScopePicker_SpaceSelectsAndSaves(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "ripgrep", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true}})
	m.mode = viewProviderScope
	m.scopeOptions = []scopeOption{
		{kind: "provider-host", label: "this tool on this host", detail: "brew"},
		{kind: "provider-tool", label: "this tool everywhere", detail: "brew"},
	}
	m.scopeTarget = *m.selectedTool()
	m.scopeTargetSet = true
	m.scopeCursor = 1

	got := drive(m, pressRune(' '))
	if !got.loading {
		t.Fatal("space should save selected provider scope")
	}
	if got.mode != viewList {
		t.Fatalf("mode = %v, want viewList after save", got.mode)
	}
	if got.statusMsg != "Pinning provider for ripgrep…" {
		t.Fatalf("statusMsg = %q, want provider save to start", got.statusMsg)
	}
}

func TestModel_IgnoreScopePicker_SpaceStillStages(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "ripgrep", Provider: "system", Tracked: true}})
	m.mode = viewIgnoreScope
	m.scopeOptions = []scopeOption{
		{kind: "tool", label: "this tool everywhere"},
		{kind: "group", label: "this group"},
	}
	m.scopeTarget = *m.selectedTool()
	m.scopeTargetSet = true
	m.scopeCursor = 1

	got := drive(m, pressRune(' '))
	if got.loading {
		t.Fatal("space should only stage multi-scope ignore changes")
	}
	if !got.scopeOptions[1].checked {
		t.Fatalf("space should toggle highlighted ignore scope: %+v", got.scopeOptions)
	}

	got = drive(got, pressEnter())
	if !got.loading {
		t.Fatal("enter should save staged ignore scope changes")
	}
}

func TestModel_ScopePickerUsesCapturedToolAfterCursorMoves(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true},
		{Name: "zoxide", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true},
	}
	m := baseModel(tools)
	if got := m.selectedTool(); got == nil || got.Name != "ripgrep" {
		t.Fatalf("selectedTool = %+v, want ripgrep", got)
	}
	m.openProviderScopePicker(m.selectedTool())
	m.cursor = 1
	m.scopeOptions = []scopeOption{
		{kind: "provider-host", label: "this tool on this host", detail: "brew", checked: true},
		{kind: "provider-tool", label: "this tool everywhere", detail: "brew"},
	}

	got := drive(m, pressEnter())
	if got.statusMsg != "Pinning provider for ripgrep…" {
		t.Fatalf("statusMsg = %q, want captured ripgrep target", got.statusMsg)
	}
	if got.scopeTargetSet {
		t.Fatal("scope target should be cleared after save")
	}
}

func TestModel_IgnoreScopePickerUsesCapturedToolAfterCursorMoves(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Tracked: true},
		{Name: "zoxide", Provider: "system", Tracked: true},
	}
	m := baseModel(tools)
	if got := m.selectedTool(); got == nil || got.Name != "ripgrep" {
		t.Fatalf("selectedTool = %+v, want ripgrep", got)
	}
	m.openIgnoreScopePicker(m.selectedTool())
	m.cursor = 1
	m.scopeOptions = []scopeOption{
		{kind: "tool", label: "this tool everywhere", checked: true, initialChecked: false},
	}

	got := drive(m, pressEnter())
	if got.statusMsg != "Updating ignore scope for ripgrep…" {
		t.Fatalf("statusMsg = %q, want captured ripgrep target", got.statusMsg)
	}
	if got.scopeTargetSet {
		t.Fatal("scope target should be cleared after save")
	}
}

func TestModel_SettingsSavedMsg(t *testing.T) {
	t.Run("success sets status", func(t *testing.T) {
		got := drive(baseModel(nil), settingsSavedMsg{})
		if got.statusMsg != "✓ settings saved" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
	})

	t.Run("error sets status", func(t *testing.T) {
		got := drive(baseModel(nil), settingsSavedMsg{err: errors.New("disk full")})
		if got.statusMsg != "✗ disk full" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
	})
}

// ─── Priority editor ──────────────────────────────────────────────────────────

// goToPriorityRow navigates to Settings and moves the cursor to Provider Order.
func goToPriorityRow() []tea.Msg {
	return append(toSettings(), nj(settingsRowSystemPriority)...)
}

func TestModel_PriorityEditor_Open(t *testing.T) {
	msgs := append(goToPriorityRow(), pressEnter())
	m := drive(baseModel(nil), msgs...)
	if !m.editingPriority {
		t.Fatal("editingPriority should be true after enter on priority row")
	}
	if len(m.priorityDraft) != 6 {
		t.Fatalf("priorityDraft len = %d, want 6 (default system providers)", len(m.priorityDraft))
	}
	if m.priorityCursor != 0 {
		t.Errorf("priorityCursor = %d, want 0", m.priorityCursor)
	}
}

func TestModel_PriorityEditor_SpaceDoesNotOpen(t *testing.T) {
	msgs := append(goToPriorityRow(), pressRune(' '))
	m := drive(baseModel(nil), msgs...)
	if m.editingPriority {
		t.Fatal("space should not open priority editor")
	}
}

func TestModel_PriorityEditor_OpenFromSettings(t *testing.T) {
	base := baseModel(nil)
	base.settings = tuiSettingsWithPriority("brew", "apt")
	msgs := append(goToPriorityRow(), pressEnter())
	m := drive(base, msgs...)
	if !m.editingPriority {
		t.Fatal("editingPriority should be true")
	}
	if len(m.priorityDraft) != 6 || m.priorityDraft[0] != "brew" || m.priorityDraft[1] != "apt" {
		t.Errorf("priorityDraft = %v, want [brew apt ...system defaults]", m.priorityDraft)
	}
}

func TestModel_PriorityEditor_JKNavigation(t *testing.T) {
	msgs := append(goToPriorityRow(), pressEnter(), pressRune('j'))
	m := drive(baseModel(nil), msgs...)
	if m.priorityCursor != 1 {
		t.Errorf("priorityCursor = %d, want 1 after j", m.priorityCursor)
	}

	msgs = append(msgs, pressRune('k'))
	m = drive(baseModel(nil), msgs...)
	if m.priorityCursor != 0 {
		t.Errorf("priorityCursor = %d, want 0 after j+k", m.priorityCursor)
	}
}

func TestModel_PriorityEditor_CursorClamps(t *testing.T) {
	// k at top — should stay at 0
	msgs := append(goToPriorityRow(), pressEnter(), pressRune('k'))
	m := drive(baseModel(nil), msgs...)
	if m.priorityCursor != 0 {
		t.Errorf("priorityCursor = %d, want 0 (clamped at top)", m.priorityCursor)
	}

	// j past last item — should stay at the last default system provider
	manyJ := []tea.Msg{pressRune('j'), pressRune('j'), pressRune('j'), pressRune('j'), pressRune('j')}
	msgs = append(goToPriorityRow(), append([]tea.Msg{pressEnter()}, manyJ...)...)
	m = drive(baseModel(nil), msgs...)
	if m.priorityCursor != 5 {
		t.Errorf("priorityCursor = %d, want 5 (clamped at bottom)", m.priorityCursor)
	}
}

func TestModel_PriorityEditor_ShiftJMoveDown(t *testing.T) {
	// Open editor; default draft is the concrete system provider priority.
	// cursor starts at 0 (apt); shift+j should swap apt down.
	msgs := append(goToPriorityRow(), pressEnter(), pressRune('J'))
	m := drive(baseModel(nil), msgs...)
	if m.priorityDraft[0] != "apk" || m.priorityDraft[1] != "apt" {
		t.Errorf("priorityDraft = %v after J, want [apk apt ...]", m.priorityDraft)
	}
	if m.priorityCursor != 1 {
		t.Errorf("priorityCursor = %d, want 1 (follows item)", m.priorityCursor)
	}
}

func TestModel_PriorityEditor_ShiftKMoveUp(t *testing.T) {
	// Move cursor to index 1, then shift+k should swap apk up.
	msgs := append(goToPriorityRow(), pressEnter(), pressRune('j'), pressRune('K'))
	m := drive(baseModel(nil), msgs...)
	if m.priorityDraft[0] != "apk" || m.priorityDraft[1] != "apt" {
		t.Errorf("priorityDraft = %v after j+K, want [apk apt ...]", m.priorityDraft)
	}
	if m.priorityCursor != 0 {
		t.Errorf("priorityCursor = %d, want 0 (follows item)", m.priorityCursor)
	}
}

func TestModel_PriorityEditor_SaveOnEnter(t *testing.T) {
	// Reorder and confirm; settings should update.
	msgs := append(goToPriorityRow(), pressEnter(), pressRune('J'), pressEnter())
	m := drive(baseModel(nil), msgs...)
	if m.editingPriority {
		t.Error("editingPriority should be false after enter")
	}
	if priority := m.settings.EcosystemPriority("system"); len(priority) == 0 || priority[0] != "apk" {
		t.Errorf("system priority = %v, want [apk apt ...]", priority)
	}
}

func TestModel_PriorityEditor_DiscardOnEsc(t *testing.T) {
	base := baseModel(nil)
	base.settings = tuiSettingsWithPriority("brew", "apt", "apk")
	// Reorder and then esc — original settings should be unchanged.
	msgs := append(goToPriorityRow(), pressEnter(), pressRune('J'), pressEsc())
	m := drive(base, msgs...)
	if m.editingPriority {
		t.Error("editingPriority should be false after esc")
	}
	if priority := m.settings.EcosystemPriority("system"); len(priority) == 0 || priority[0] != "brew" {
		t.Errorf("system priority = %v, want original [brew ...]", priority)
	}
}

func TestModel_PriorityEditor_FiltersNonSystemProviders(t *testing.T) {
	base := baseModel(nil)
	base.settings = tuiSettingsWithPriority("brew", "pip", "node", "apt", "pip3", "brew")
	msgs := append(goToPriorityRow(), pressEnter(), pressEnter())
	m := drive(base, msgs...)
	if got := m.settings.EcosystemPriority("system"); slices.Contains(got, "pip") || slices.Contains(got, "pip3") || slices.Contains(got, "node") {
		t.Fatalf("system priority = %v, should not include non-system providers/managers", got)
	}
	if got := m.settings.EcosystemPriority("system"); len(got) < 2 || got[0] != "brew" || got[1] != "apt" {
		t.Fatalf("system priority = %v, want stale list sanitized while preserving system order", got)
	}
}

// ─── Delete / Upgrade / UpgradeAll key handlers ──────────────────────────────

func TestModel_KeyD_DeleteRequiresConfirmation(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true},
	}
	m := baseModel(tools)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('d'))
	if got.loading {
		t.Fatal("loading should stay false before delete confirmation")
	}
	if got.listConfirm.action != listConfirmDelete {
		t.Fatalf("listConfirm.action = %q, want delete", got.listConfirm.action)
	}
	got = drive(got, pressRune('d'))
	if !got.loading {
		t.Error("expected loading=true after confirmed delete")
	}
}

func TestModel_KeyD_DeleteNoopWhenNotInstalled(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: false},
	}
	m := baseModel(tools)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('d'))
	if got.loading {
		t.Error("loading should stay false for uninstalled tool")
	}
}

func TestModel_KeyU_UpgradeSetsKey(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true},
	}
	m := baseModel(tools)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('u'))
	if !got.upgradingKeys["ripgrep\x00brew"] {
		t.Error("expected upgradingKeys entry for ripgrep after 'u'")
	}
	if !got.loading {
		t.Error("expected loading=true while single upgrade is active")
	}
}

func TestModel_KeyU_UpgradeNoopWhenNotOutdated(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: false},
	}
	m := baseModel(tools)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('u'))
	if len(got.upgradingKeys) != 0 {
		t.Error("expected no upgrading keys for non-outdated tool")
	}
}

func TestModel_KeyCapU_UpgradeAllSetsWildcard(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
	}
	m := baseModel(tools)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('U'))
	if !got.upgradingKeys["*"] {
		t.Error("expected upgradingKeys[*]=true after 'U' with outdated tools")
	}
	if !got.loading {
		t.Error("expected loading=true while upgrade-all progress stream is active")
	}
}

func TestModel_KeyCapU_UpgradeAllNoopWhenNoUpdates(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: false},
	}
	m := baseModel(tools)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('U'))
	if got.upgradingKeys["*"] {
		t.Error("expected no wildcard upgrade when no outdated tools")
	}
}

// ─── Setup wizard step 1 ─────────────────────────────────────────────────────

func TestModel_SetupStep1_YSetsLoading(t *testing.T) {
	m := Model{
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        textinput.New(),
		commandInput:  textinput.New(),
		mode:          viewSetup,
		setupStep:     1,
		upgradingKeys: make(map[string]bool),
	}
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system", enabled: true},
		{name: "node", label: "node", enabled: true},
	}
	got := drive(m, pressEnter())
	if !got.loading {
		t.Error("expected loading=true after enter in setup step 1")
	}
}

func TestModel_SetupStep1_SpaceTogglesProvider(t *testing.T) {
	m := Model{
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        textinput.New(),
		commandInput:  textinput.New(),
		settingsInput: textinput.New(),
		mode:          viewSetup,
		setupStep:     1,
		upgradingKeys: make(map[string]bool),
		setupProviders: []setupProviderRow{
			{name: "system", label: "system", enabled: true},
			{name: "node", label: "node", enabled: true},
		},
	}
	got := drive(m, pressRune(' '))
	if got.setupProviders[0].enabled {
		t.Error("expected first provider toggled off after space")
	}
	if !got.setupProviders[1].enabled {
		t.Error("second provider should remain enabled")
	}
}

// ─── buildSetupProvidersFromManagers ─────────────────────────────────────────

func TestBuildSetupProviders_MultipleBins(t *testing.T) {
	rows := buildSetupProvidersFromManagers(
		map[string]string{"system": "brew"},
		[]string{"uv"},
		[]string{"pnpm", "npm"},
		config.Settings{},
	)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].label != "system(brew)" {
		t.Errorf("system label = %q, want system(brew)", rows[0].label)
	}
	if rows[1].label != "node(pnpm • npm)" {
		t.Errorf("node label = %q, want node(pnpm • npm)", rows[1].label)
	}
	if rows[2].label != "python(uv)" {
		t.Errorf("python label = %q, want python(uv)", rows[2].label)
	}
}

func TestBuildSetupProviders_SingleBin(t *testing.T) {
	rows := buildSetupProvidersFromManagers(map[string]string{}, []string{"pip3"}, []string{"bun"}, config.Settings{})
	if rows[1].label != "node(bun)" {
		t.Errorf("node label = %q, want node(bun)", rows[1].label)
	}
	if rows[2].label != "python(pip3)" {
		t.Errorf("python label = %q, want python(pip3)", rows[2].label)
	}
}

func TestBuildSetupProviders_NoBins(t *testing.T) {
	rows := buildSetupProvidersFromManagers(map[string]string{}, nil, nil, config.Settings{})
	for _, row := range rows {
		if row.label != row.name {
			t.Errorf("%s: label %q should equal name when no bins detected", row.name, row.label)
		}
		if !row.enabled {
			t.Errorf("%s: expected enabled=true", row.name)
		}
	}
}

func TestBuildSetupProviders_AllEnabled(t *testing.T) {
	rows := buildSetupProvidersFromManagers(map[string]string{"system": "apt"}, []string{"uv"}, []string{"npm"}, config.Settings{})
	for _, row := range rows {
		if !row.enabled {
			t.Errorf("%s: expected enabled=true by default", row.name)
		}
	}
}

func TestBuildSetupProviders_RespectsDisabledProviders(t *testing.T) {
	settings := config.Settings{DisabledProviders: []string{"node", "python"}}
	rows := buildSetupProvidersFromManagers(
		map[string]string{"system": "brew"},
		[]string{"uv"},
		[]string{"npm"},
		settings,
	)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	// system is not in DisabledProviders — should be enabled.
	if !rows[0].enabled {
		t.Errorf("system: expected enabled=true (not in DisabledProviders)")
	}
	// node is in DisabledProviders — should be disabled.
	if rows[1].enabled {
		t.Errorf("node: expected enabled=false (in DisabledProviders)")
	}
	// python is in DisabledProviders — should be disabled.
	if rows[2].enabled {
		t.Errorf("python: expected enabled=false (in DisabledProviders)")
	}
}

// ─── Group picker ─────────────────────────────────────────────────────────────

func TestModel_GroupPicker_EnterSetsLoading(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true},
	}
	m := baseModel(tools)
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"work", "personal"}
	m.pickerCursor = 0
	m.pickerPurposeClaim = true
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressEnter())
	if !got.loading {
		t.Error("expected loading=true after enter in group picker")
	}
}

func TestModel_GroupPicker_EscReturnsToList(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"work"}
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressEsc())
	if got.mode != viewList {
		t.Errorf("expected viewList after esc in group picker, got %v", got.mode)
	}
	if got.pickerGroups != nil {
		t.Error("expected pickerGroups cleared after esc")
	}
}

func TestVisibleGroupNames_UsesActiveHostGroups(t *testing.T) {
	m := baseModel(nil)
	m.groupNames = []string{"archive", "personal", "work"}
	m.hostInfo = &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"base", "work"}},
		},
	}
	got := visibleGroupNames(m)
	want := []string{"work"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visibleGroupNames = %v, want %v", got, want)
	}
}

func TestVisibleGroupNames_IncludesCurrentMachineGroup(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost.example.com")
	m := baseModel(nil)
	m.groupNames = []string{"archive", "testhost", "work"}
	m.hostInfo = &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"base"}},
		},
	}
	got := visibleGroupNames(m)
	want := []string{"testhost"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visibleGroupNames = %v, want %v", got, want)
	}
}

func TestDotAddOpensGroupPicker(t *testing.T) {
	m := dotsModel()
	m.openGroupPickerForDotAdd("/home/test/.config/zed", "~/.config/zed")
	if m.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", m.mode)
	}
	if !m.pickerPurposeDotAdd {
		t.Fatal("pickerPurposeDotAdd should be true")
	}
	if m.pickerDotAddPath != "/home/test/.config/zed" {
		t.Fatalf("pickerDotAddPath = %q, want /home/test/.config/zed", m.pickerDotAddPath)
	}
}

func TestPrioritizedPickerGroups_ActiveHostGroupsFirst(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	m := baseModel(nil)
	m.groupNames = []string{"archive", "personal", "work"}
	m.hostInfo = &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"base", "work"}},
		},
	}
	got := prioritizedPickerGroups(m)
	want := []string{"host", "work", "archive", "personal"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("prioritizedPickerGroups = %v, want %v", got, want)
	}
}

// ─── FullHelp ─────────────────────────────────────────────────────────────────

func TestKeyMap_FullHelp_ReturnsColumns(t *testing.T) {
	km := DefaultKeyMap()
	cols := km.FullHelp()
	if len(cols) < 3 {
		t.Errorf("FullHelp() returned %d columns, want >= 3", len(cols))
	}
	for i, col := range cols {
		if len(col) == 0 {
			t.Errorf("column %d is empty", i)
		}
	}
}

// ─── Message handlers ─────────────────────────────────────────────────────────

func TestModel_ClearStatusMsg_ClearsStatus(t *testing.T) {
	m := baseModel(nil)
	m.statusMsg = "some status"
	got := drive(m, clearStatusMsg{})
	if got.statusMsg != "" {
		t.Errorf("clearStatusMsg: statusMsg = %q, want empty", got.statusMsg)
	}
}

func TestModel_ProgressMsg_SetsProgressText(t *testing.T) {
	m := baseModel(nil)
	got := drive(m, progressMsg{text: "installing…"})
	if got.progressText != "installing…" {
		t.Errorf("progressMsg: progressText = %q, want 'installing…'", got.progressText)
	}
}

func TestModel_ProgressMsg_RefreshesFinishedToolBeforeBatchDone(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: false, Tracked: true},
		{Name: "fd", Provider: "system", Installed: false, Tracked: true},
	})
	key := toolKey("ripgrep", "system")
	m.progressGen = 3
	m.loading = true
	m.bulkPendingKeys = map[string]bool{key: true, toolKey("fd", "system"): true}

	got := drive(m, progressMsg{
		gen:     3,
		text:    "Installed ripgrep",
		rowKey:  key,
		rowDone: true,
		tools: []*database.ToolCache{
			{Name: "ripgrep", Provider: "system", Installed: true, Tracked: true},
			{Name: "fd", Provider: "system", Installed: false, Tracked: true},
		},
	})

	if !got.loading {
		t.Fatal("batch should stay loading until progressDoneMsg")
	}
	if got.bulkPendingKeys[key] {
		t.Fatal("finished row should leave pending state before batch completion")
	}
	refreshed := false
	for _, tool := range got.visibleTools {
		if tool.Name == "ripgrep" && tool.Installed {
			refreshed = true
			break
		}
	}
	if !refreshed {
		t.Fatalf("finished row should refresh immediately, got %+v", got.visibleTools)
	}
}

func TestModel_ProgressMsg_IgnoresStaleGeneration(t *testing.T) {
	m := baseModel(nil)
	m.progressGen = 2
	m.progressText = "current"
	got := drive(m, progressMsg{gen: 1, text: "stale"})
	if got.progressText != "current" {
		t.Errorf("progressText = %q, want current", got.progressText)
	}
}

func TestModel_ProgressMsg_IgnoresLateSameGenerationAfterDone(t *testing.T) {
	m := baseModel(nil)
	m.progressGen = 4
	m.loading = true
	got := drive(m,
		progressDoneMsg{gen: 4, message: "install complete"},
		progressMsg{gen: 4, text: "late progress"},
	)
	if got.progressText != "" {
		t.Errorf("late progressText = %q, want empty after final progressDoneMsg", got.progressText)
	}
	if got.loading {
		t.Error("loading should stay false after final progressDoneMsg")
	}
}

func TestModel_ProgressStreamClosedMsg_IsNotFinalResult(t *testing.T) {
	ch := make(chan progressUpdate)
	m := baseModel(nil)
	m.progressGen = 3
	m.progressCh = ch
	m.loading = true
	m.progressText = "installing…"
	got := drive(m, progressStreamClosedMsg{gen: 3})
	if !got.loading {
		t.Error("stream close must not clear loading before the operation result arrives")
	}
	if got.progressText != "installing…" {
		t.Errorf("progressText = %q, want existing progress text", got.progressText)
	}
	if got.progressCh != nil {
		t.Fatal("progressCh should be cleared after stream close")
	}
	got = drive(got, progressDoneMsg{gen: 3, message: "install complete"})
	if got.loading {
		t.Error("final progressDoneMsg should clear loading after stream close")
	}
	if got.progressText != "" {
		t.Errorf("progressText = %q, want cleared by final result", got.progressText)
	}
}

func TestModel_ProgressDoneMsg_Success(t *testing.T) {
	m := baseModel(nil)
	m.upgradingKeys = map[string]bool{"*": true}
	m.loading = true
	got := drive(m, progressDoneMsg{key: "*", message: "upgrades complete", tools: threeTools()})
	if got.loading {
		t.Error("loading should be false after progressDoneMsg")
	}
	if got.upgradingKeys["*"] {
		t.Error("wildcard key should be removed after progressDoneMsg")
	}
	if len(got.visibleTools) != 3 {
		t.Errorf("visibleTools = %d, want 3", len(got.visibleTools))
	}
}

func TestCloneSettingsSnapshot_PreservesExplicitEmptyDisabledProviders(t *testing.T) {
	in := config.Settings{DisabledProviders: []string{}}
	got := cloneSettingsSnapshot(in)
	if got.DisabledProviders == nil {
		t.Fatal("DisabledProviders = nil, want explicit empty slice")
	}
	got.DisabledProviders = append(got.DisabledProviders, "node")
	if len(in.DisabledProviders) != 0 {
		t.Fatalf("snapshot aliases input DisabledProviders, input now %v", in.DisabledProviders)
	}
}

func TestModel_ProgressDoneMsg_IgnoresStaleGeneration(t *testing.T) {
	m := baseModel(nil)
	m.progressGen = 2
	m.loading = true
	m.upgradingKeys = map[string]bool{"*": true}
	got := drive(m, progressDoneMsg{gen: 1, key: "*", message: "old complete", tools: threeTools()})
	if !got.loading {
		t.Error("stale progressDoneMsg must not clear loading")
	}
	if !got.upgradingKeys["*"] {
		t.Error("stale progressDoneMsg must not clear active wildcard upgrade")
	}
	if got.statusMsg != "" {
		t.Errorf("statusMsg = %q, want empty after stale progressDoneMsg", got.statusMsg)
	}
}

func TestModel_ProgressDoneMsg_Error(t *testing.T) {
	m := baseModel(nil)
	m.upgradingKeys = map[string]bool{"*": true}
	got := drive(m, progressDoneMsg{key: "*", err: errors.New("network error")})
	if !stringContains(got.statusMsg, "network error") {
		t.Errorf("statusMsg = %q, want error text", got.statusMsg)
	}
}

func TestModel_ProgressDoneMsg_ErrorRefreshesTools(t *testing.T) {
	m := baseModel(nil)
	m.upgradingKeys = map[string]bool{"*": true}
	tools := threeTools()
	got := drive(m, progressDoneMsg{key: "*", err: errors.New("ripgrep failed"), tools: tools})
	if !got.statusIsErr {
		t.Fatal("statusIsErr = false, want true")
	}
	if len(got.visibleTools) != len(tools) {
		t.Fatalf("visibleTools = %d, want refreshed successful partial results", len(got.visibleTools))
	}
}

func TestModel_ProviderScannedMsg_ClearsScanningProviders(t *testing.T) {
	m := baseModel(nil)
	m.scanningProviders = map[string]bool{"brew": true}
	got := drive(m, providerScannedMsg{provider: "brew"})
	if len(got.scanningProviders) != 0 {
		t.Errorf("scanningProviders should be empty after last provider scanned, got %v", got.scanningProviders)
	}
}

func TestModel_ProviderScannedMsg_RefreshStatusShowsRemainingProviders(t *testing.T) {
	m := baseModel(nil)
	m.scanningProviders = map[string]bool{"brew": true, "node": true}
	m.progressText = providerRefreshStatus(m.scanningProviders)

	got := drive(m, providerScannedMsg{provider: "brew"})

	if got.progressText != "Refreshing providers: node…" {
		t.Fatalf("progressText = %q, want remaining provider", got.progressText)
	}
	if len(got.bulkPendingKeys) > 0 {
		t.Fatalf("refresh should not mark tool rows pending, got %v", got.bulkPendingKeys)
	}
}

func TestModel_ProviderScannedMsg_IgnoresStaleGeneration(t *testing.T) {
	m := baseModel(nil)
	m.scanGen = 2
	m.scanningProviders = map[string]bool{"brew": true}
	got := drive(m, providerScannedMsg{gen: 1, provider: "brew"})
	if !got.scanningProviders["brew"] {
		t.Fatalf("stale providerScannedMsg removed current scan state: %v", got.scanningProviders)
	}
}

func TestModel_ProviderScannedMsg_IgnoresUnknownProvider(t *testing.T) {
	m := baseModel(nil)
	m.scanGen = 2
	m.scanningProviders = map[string]bool{"brew": true}
	got := drive(m, providerScannedMsg{gen: 2, provider: "npm"})
	if len(got.scanningProviders) != 1 || !got.scanningProviders["brew"] {
		t.Fatalf("unknown providerScannedMsg changed scan state: %v", got.scanningProviders)
	}
	if len(got.allTools) != 0 {
		t.Fatalf("unknown providerScannedMsg triggered final refresh: %v", toolNames(got.allTools))
	}
}

func TestProviderScanFailureStatus_DeadlineIsConcise(t *testing.T) {
	err := errors.Join(
		errors.New("upserting installed status for system/fd: context deadline exceeded"),
		context.DeadlineExceeded,
	)
	got := providerScanFailureStatus("system", err)
	if got != "scan timed out for system" {
		t.Fatalf("status = %q, want concise timeout", got)
	}
	if strings.Contains(got, "upserting") || strings.Contains(got, "listing tools") {
		t.Fatalf("status should not expose internals: %q", got)
	}
}

func TestModel_AllProvidersDoneMsg_RefreshesTools(t *testing.T) {
	m := baseModel(nil)
	got := drive(m, allProvidersDoneMsg{tools: threeTools()})
	if len(got.allTools) != 3 {
		t.Errorf("allTools = %d, want 3", len(got.allTools))
	}
}

func TestModel_AllProvidersDoneMsg_IgnoresStaleGeneration(t *testing.T) {
	oldTools := []*database.ToolCache{{Name: "old", Provider: "brew"}}
	m := baseModel(oldTools)
	m.scanGen = 2
	got := drive(m, allProvidersDoneMsg{gen: 1, tools: threeTools()})
	if len(got.allTools) != 1 || got.allTools[0].Name != "old" {
		t.Fatalf("stale allProvidersDoneMsg overwrote tools: %v", toolNames(got.allTools))
	}
}

func TestModel_DiscoveredRefreshedMsg_IgnoresStaleGeneration(t *testing.T) {
	oldDiscovered := []*database.ToolCache{{Name: "old-orphan", Provider: "brew", Installed: true, Tracked: false}}
	m := baseModel(nil)
	m.discoveryGen = 2
	m.discoveredTools = oldDiscovered
	m.rebuildDiscoveredKeys()
	got := drive(m, discoveredRefreshedMsg{
		gen:        1,
		discovered: []*database.ToolCache{{Name: "new-orphan", Provider: "brew", Installed: true, Tracked: false}},
	})
	if len(got.discoveredTools) != 1 || got.discoveredTools[0].Name != "old-orphan" {
		t.Fatalf("stale discoveredRefreshedMsg overwrote discovered tools: %v", toolNames(got.discoveredTools))
	}
	if !got.discoveredKeys["old-orphan\x00brew"] {
		t.Fatalf("stale discoveredRefreshedMsg rebuilt discovered keys: %v", got.discoveredKeys)
	}
}

func TestModel_GroupChangedMsg_Success(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	got := drive(m, groupChangedMsg{detail: "✓ git added to work", tools: threeTools()})
	if got.loading {
		t.Error("loading should be false after groupChangedMsg")
	}
	if !stringContains(got.statusMsg, "git added") {
		t.Errorf("statusMsg = %q, want 'git added'", got.statusMsg)
	}
}

func TestModel_GroupChangedMsg_Error(t *testing.T) {
	m := baseModel(nil)
	got := drive(m, groupChangedMsg{err: errors.New("file not found")})
	if !stringContains(got.statusMsg, "file not found") {
		t.Errorf("statusMsg = %q, want error text", got.statusMsg)
	}
}

// ─── cyclePythonManager ───────────────────────────────────────────────────────

func TestCyclePythonManager(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "uv"},
		{"uv", "pip3"},
		{"pip3", ""},
		{"other", ""},
	}
	for _, tt := range tests {
		if got := cyclePythonManager(tt.in); got != tt.want {
			t.Errorf("cyclePythonManager(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ─── Palette command execution ────────────────────────────────────────────────

func TestModel_PaletteSync_SetsLoading(t *testing.T) {
	// Simulate: palette is already open with "sync" as the only suggestion.
	// Pressing enter should execute the sync command's run func, setting loading=true.
	m := Model{
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        textinput.New(),
		commandInput:  textinput.New(),
		mode:          viewCommand,
		commandCursor: -1,
		upgradingKeys: make(map[string]bool),
	}
	m.commandInput.Focus()
	// Pre-populate the suggestion list (single suggestion → auto-chosen on enter).
	syncCmd := mustPaletteCommand(t, m, "tools sync")
	m.commandSuggestions = []palCmd{syncCmd}
	got := drive(m, pressEnter())
	if !got.loading {
		t.Error("expected loading=true after executing 'sync' palette command")
	}
}

func TestModel_PaletteConsolidate_SetsLoading(t *testing.T) {
	m := Model{
		keys:               DefaultKeyMap(),
		spinner:            spinner.New(),
		filter:             textinput.New(),
		commandInput:       textinput.New(),
		mode:               viewCommand,
		commandCursor:      -1,
		upgradingKeys:      make(map[string]bool),
		consolidateOptions: []app.EcosystemMigration{{Ecosystem: "node", Manager: "bun"}},
	}
	m.commandInput.Focus()
	allCmds := buildPalette(m)
	// Find the consolidate command.
	var consolidateCmd palCmd
	for _, c := range allCmds {
		if c.name == "tools consolidate node bun" {
			consolidateCmd = c
			break
		}
	}
	m.commandSuggestions = []palCmd{consolidateCmd}
	got := drive(m, pressEnter())
	if !got.loading {
		t.Error("expected loading=true after executing consolidate palette command")
	}
}

func TestModel_PaletteDotsCommandsStartDotsOperations(t *testing.T) {
	cases := []struct {
		name       string
		wantStatus string
	}{
		{name: "dots pull", wantStatus: "Pulling…"},
		{name: "dots commit", wantStatus: "Committing dots…"},
		{name: "dots push", wantStatus: "Pushing…"},
		{name: "dots sync", wantStatus: "Syncing dots…"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.mode = viewCommand
			m.commandInput.Focus()
			m.commandCursor = -1
			m.settings.DotsRepo = "/repo/dotfiles"
			m.commandSuggestions = []palCmd{mustPaletteCommand(t, m, tc.name)}

			got := drive(m, pressEnter())
			if got.mode != viewDots {
				t.Fatalf("mode = %v, want viewDots", got.mode)
			}
			if !got.dotsLoaded {
				t.Fatal("dotsLoaded = false, want true")
			}
			if !got.dotsLoading {
				t.Fatal("dotsLoading = false, want true")
			}
			if got.statusMsg != tc.wantStatus {
				t.Fatalf("statusMsg = %q, want %q", got.statusMsg, tc.wantStatus)
			}
		})
	}
}

func mustPaletteCommand(t *testing.T, m Model, name string) palCmd {
	t.Helper()
	for _, cmd := range buildPalette(m) {
		if cmd.name == name {
			return cmd
		}
	}
	t.Fatalf("palette command %q not found", name)
	return palCmd{}
}

// ─── Dots tab ────────────────────────────────────────────────────────────────

func dotsModel() Model {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo/dotfiles"
	m.dotsEntries = []app.DotStatus{
		{Name: "gitconfig", SourcePath: "/repo/gitconfig", TargetPath: "~/.gitconfig", Health: app.HealthConflict, State: app.DotStateConflict, Actions: []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove, app.DotActionIgnore}},
		{Name: "zshrc", SourcePath: "/repo/zshrc", TargetPath: "~/.zshrc", Health: app.HealthMissing, State: app.DotStateMissing, Actions: []app.DotAction{app.DotActionSync, app.DotActionRemove, app.DotActionIgnore}},
		{Name: "nvim", SourcePath: "/repo/nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK, State: app.DotStateSynced, Actions: []app.DotAction{app.DotActionRemove, app.DotActionIgnore}},
	}
	return m
}

func dotsVariantFlowModel(t *testing.T, withVariant bool) Model {
	t.Helper()
	t.Setenv("OMNI_HOSTNAME", "laptop.local")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	entry := config.DotEntry{Name: "nvim", Path: "~/.config/nvim"}
	pkg := "nvim"
	if withVariant {
		pkg = "nvim@laptop"
		entry.Hosts = map[string]config.DotVariant{
			"laptop": {Package: pkg},
		}
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{{
			Name:    "laptop",
			Special: "host",
			Dots:    []config.DotEntry{entry},
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	a := app.New(cfgPath)
	a.CacheDir = cfgDir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewDots
	m.dotsLoaded = true
	m.stowInstalled = true
	m.settings = config.Settings{DotsRepo: repoDir}
	m.dotMemberships = map[string][]string{"nvim": {"laptop"}}
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		Package:    pkg,
		SourcePath: filepath.Join(repoDir, "dotfiles", pkg, ".config", "nvim"),
		TargetPath: "~/.config/nvim",
		Health:     app.HealthOK,
		State:      app.DotStateSynced,
		Actions:    []app.DotAction{app.DotActionRemove, app.DotActionIgnore},
		Group:      "laptop",
	}}
	return m
}

func TestModel_DotsSyncAllMarksRowsPending(t *testing.T) {
	got := drive(dotsModel(), pressRune('S'))
	if !got.dotsLoading {
		t.Fatal("dotsLoading should start after sync all")
	}
	if len(got.dotsPendingNames) != 3 {
		t.Fatalf("dotsPendingNames len = %d, want 3 (%v)", len(got.dotsPendingNames), got.dotsPendingNames)
	}
	if !strings.Contains(got.progressText, "0/3") {
		t.Fatalf("progressText = %q, want 0/3 progress", got.progressText)
	}
}

func TestModel_DotsSyncAllEntryOrderMatchesRenderedSections(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/repo/dotfiles"
	m.dotsEntries = []app.DotStatus{
		{Name: "synced", State: app.DotStateSynced},
		{Name: "ignored", State: app.DotStateIgnored},
		{Name: "conflict", State: app.DotStateConflict},
		{Name: "missing", State: app.DotStateMissing},
	}

	got := dotsSyncAllEntryOrder(m)
	want := []string{"conflict", "missing", "synced", "ignored"}
	if !slices.Equal(got, want) {
		t.Fatalf("dots sync-all order = %v, want rendered order %v", got, want)
	}
}

func TestModel_DotsProgressMsgUpdatesRowStateAndSnapshot(t *testing.T) {
	m := dotsModel()
	m.beginDotsOperation("Syncing dots…")
	gen := m.dotsOpGen
	m.dotsPendingNames = map[string]bool{"nvim": true, "zshrc": true}

	got := drive(m, dotsProgressMsg{
		gen:  gen,
		text: dotsSyncProgressText("nvim", 1, 2, false, nil),
		name: "nvim",
	})
	if got.dotsActiveName != "nvim" {
		t.Fatalf("dotsActiveName = %q, want nvim", got.dotsActiveName)
	}
	if got.dotsPendingNames["nvim"] {
		t.Fatal("active dots row should no longer be pending")
	}
	if !strings.Contains(got.progressText, "1/2") {
		t.Fatalf("progressText = %q, want 1/2 progress", got.progressText)
	}

	got = drive(got, dotsProgressMsg{
		gen:     gen,
		text:    dotsSyncProgressText("nvim", 1, 2, true, nil),
		name:    "nvim",
		done:    true,
		entries: []app.DotStatus{{Name: "nvim", State: app.DotStateSynced}},
	})
	if got.dotsActiveName != "" {
		t.Fatalf("dotsActiveName = %q, want cleared", got.dotsActiveName)
	}
	if got.dotsPendingNames["nvim"] {
		t.Fatal("done dots row should no longer be pending")
	}
	if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "nvim" {
		t.Fatalf("dotsEntries = %#v, want refreshed nvim snapshot", got.dotsEntries)
	}
}

func TestModel_DotsTab_Navigation(t *testing.T) {
	t.Run("j moves cursor down", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('j'))
		if m.dotsCursor != 1 {
			t.Errorf("dotsCursor = %d, want 1", m.dotsCursor)
		}
	})

	t.Run("k moves cursor up", func(t *testing.T) {
		m := dotsModel()
		m.dotsCursor = 2
		m = drive(m, pressRune('k'))
		if m.dotsCursor != 1 {
			t.Errorf("dotsCursor = %d, want 1", m.dotsCursor)
		}
	})

	t.Run("cursor wraps to bottom from top", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('k'))
		if m.dotsCursor != 2 {
			t.Errorf("dotsCursor = %d, want 2 (wrapped to bottom)", m.dotsCursor)
		}
	})

	t.Run("cursor wraps to top from bottom", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('j'), pressRune('j'), pressRune('j'))
		if m.dotsCursor != 0 {
			t.Errorf("dotsCursor = %d, want 0 (wrapped to top)", m.dotsCursor)
		}
	})
}

func TestModel_DotsTab_ConfirmDelete(t *testing.T) {
	t.Run("first d arms confirm", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('d'))
		if m.dotsConfirmIdx != 0 {
			t.Errorf("dotsConfirmIdx = %d, want 0 after first d", m.dotsConfirmIdx)
		}
	})

	t.Run("esc cancels confirm", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('d'), pressEsc())
		if m.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after esc", m.dotsConfirmIdx)
		}
	})

	t.Run("navigation cancels confirm", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('d'), pressRune('j'))
		if m.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after navigation", m.dotsConfirmIdx)
		}
	})

	t.Run("d on empty list is no-op", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		m.settings.DotsRepo = "/repo/dotfiles"
		m = drive(m, pressRune('d'))
		if m.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 on empty list", m.dotsConfirmIdx)
		}
	})

	t.Run("d again while armed does not confirm", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('d'), pressRune('d'))
		if m.dotsLoading {
			t.Error("dotsLoading should stay false until y/n keep-local choice")
		}
		if m.dotsConfirmIdx != 0 {
			t.Errorf("dotsConfirmIdx = %d, want 0 after second d is swallowed", m.dotsConfirmIdx)
		}
	})

	t.Run("other actions are swallowed while armed", func(t *testing.T) {
		m := drive(dotsModel(), pressRune('d'), pressRune('a'))
		if m.showFilePicker {
			t.Error("file picker should not open while delete keep-local choice is armed")
		}
		if m.dotsConfirmIdx != 0 {
			t.Errorf("dotsConfirmIdx = %d, want 0 after unrelated action", m.dotsConfirmIdx)
		}
	})
}

func TestModel_DotsTab_HostVariantFlow(t *testing.T) {
	t.Run("v opens create confirmation when host has no variant", func(t *testing.T) {
		m := drive(dotsVariantFlowModel(t, false), pressRune('v'))
		if m.dotsVariantIdx != 0 {
			t.Fatalf("dotsVariantIdx = %d, want 0", m.dotsVariantIdx)
		}
		if m.dotsVariantMode != dotsVariantCreate {
			t.Fatalf("dotsVariantMode = %v, want create", m.dotsVariantMode)
		}
	})

	t.Run("second v starts create operation", func(t *testing.T) {
		m := drive(dotsVariantFlowModel(t, false), pressRune('v'), pressRune('v'))
		if !m.dotsLoading {
			t.Fatal("dotsLoading should start after confirming variant creation")
		}
		if m.dotsVariantIdx != -1 || m.dotsVariantMode != dotsVariantNone {
			t.Fatalf("variant prompt = idx:%d mode:%v, want cleared", m.dotsVariantIdx, m.dotsVariantMode)
		}
		if !strings.Contains(m.statusMsg, "Creating variant for nvim") {
			t.Fatalf("statusMsg = %q, want create variant status", m.statusMsg)
		}
	})

	t.Run("v opens remove confirmation when host variant is active", func(t *testing.T) {
		m := drive(dotsVariantFlowModel(t, true), pressRune('v'))
		if m.dotsVariantIdx != 0 {
			t.Fatalf("dotsVariantIdx = %d, want 0", m.dotsVariantIdx)
		}
		if m.dotsVariantMode != dotsVariantRemove {
			t.Fatalf("dotsVariantMode = %v, want remove", m.dotsVariantMode)
		}
	})

	t.Run("second v starts remove operation", func(t *testing.T) {
		m := drive(dotsVariantFlowModel(t, true), pressRune('v'), pressRune('v'))
		if !m.dotsLoading {
			t.Fatal("dotsLoading should start after confirming variant removal")
		}
		if !strings.Contains(m.statusMsg, "Removing variant for nvim") {
			t.Fatalf("statusMsg = %q, want remove variant status", m.statusMsg)
		}
	})

	t.Run("esc cancels variant prompt", func(t *testing.T) {
		m := drive(dotsVariantFlowModel(t, false), pressRune('v'), pressEsc())
		if m.dotsVariantIdx != -1 || m.dotsVariantMode != dotsVariantNone {
			t.Fatalf("variant prompt = idx:%d mode:%v, want cleared", m.dotsVariantIdx, m.dotsVariantMode)
		}
	})
}

func TestModel_DotsTab_Messages(t *testing.T) {
	t.Run("dotsLoadedMsg sets entries", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "nvim", Health: app.HealthOK}}
		m := drive(baseModel(nil), dotsLoadedMsg{entries: entries, gitStatus: "M  nvim/init.lua"})
		if len(m.dotsEntries) != 1 {
			t.Errorf("dotsEntries len = %d, want 1", len(m.dotsEntries))
		}
		if !m.dotsLoaded {
			t.Error("dotsLoaded should be true")
		}
		if m.dotsGitStatus == "" {
			t.Error("dotsGitStatus should be set")
		}
	})

	t.Run("dotsLoadedMsg with error sets statusMsg", func(t *testing.T) {
		m := drive(baseModel(nil), dotsLoadedMsg{err: errors.New("no repo")})
		if m.statusMsg == "" {
			t.Error("expected statusMsg to be set on error")
		}
	})

	t.Run("dotsHistoryLoadedMsg updates history", func(t *testing.T) {
		entries := []app.DotsHistoryEntry{{Operation: "sync", Status: "success", Summary: "sync completed"}}
		m := drive(baseModel(nil), dotsHistoryLoadedMsg{entries: entries})
		if len(m.dotsHistory) != 1 || m.dotsHistory[0].Operation != "sync" {
			t.Fatalf("dotsHistory = %+v, want sync entry", m.dotsHistory)
		}
		if m.dotsHistoryErr != "" {
			t.Fatalf("dotsHistoryErr = %q, want empty", m.dotsHistoryErr)
		}
	})

	t.Run("dotsHistoryLoadedMsg error preserves history", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsHistory = []app.DotsHistoryEntry{{Operation: "commit", Status: "success", Summary: "commit completed"}}
		got := drive(m, dotsHistoryLoadedMsg{err: errors.New("history db unavailable")})
		if len(got.dotsHistory) != 1 || got.dotsHistory[0].Operation != "commit" {
			t.Fatalf("dotsHistory = %+v, want previous history preserved", got.dotsHistory)
		}
		if !strings.Contains(got.dotsHistoryErr, "history db unavailable") {
			t.Fatalf("dotsHistoryErr = %q, want history db unavailable", got.dotsHistoryErr)
		}
	})

	t.Run("dotsPreparedMsg populates list without clearing active sync", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsPreparing = true
		m.dotsPrepareGen = 1
		m.beginDotsOperation("Syncing dots…")
		entries := []app.DotStatus{{Name: "nvim", Health: app.HealthOK}}

		got := drive(m, dotsPreparedMsg{gen: 1, entries: entries, gitStatus: "M  nvim/init.lua"})
		if got.dotsPreparing {
			t.Fatal("dotsPreparing should clear after snapshot")
		}
		if !got.dotsLoading {
			t.Fatal("dots snapshot must not clear the active sync")
		}
		if !got.dotsLoaded || len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "nvim" {
			t.Fatalf("dots snapshot did not populate entries: loaded=%v entries=%+v", got.dotsLoaded, got.dotsEntries)
		}
	})

	t.Run("stale dotsPreparedMsg does not overwrite completed sync", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsPreparing = true
		m.dotsPrepareGen = 1
		m.dotsOpGen = 1
		m.dotsLoaded = true
		m.dotsEntries = []app.DotStatus{{Name: "fresh", Health: app.HealthOK}}

		got := drive(m, dotsPreparedMsg{gen: 1, opGen: 0, entries: []app.DotStatus{{Name: "stale", Health: app.HealthConflict}}})
		if got.dotsPreparing {
			t.Fatal("dotsPreparing should clear after stale snapshot")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "fresh" {
			t.Fatalf("stale snapshot overwrote entries: %+v", got.dotsEntries)
		}
	})

	t.Run("stale dots status result is ignored", func(t *testing.T) {
		m := baseModel(nil)
		m.beginDotsOperation("Loading dots…")
		oldGen := m.dotsOpGen
		m.beginDotsOperation("Syncing dots…")
		currentGen := m.dotsOpGen
		m.dotsEntries = []app.DotStatus{{Name: "current", Health: app.HealthOK}}

		got := drive(m, dotsLoadedMsg{gen: oldGen, entries: []app.DotStatus{{Name: "stale", Health: app.HealthConflict}}})
		if !got.dotsLoading {
			t.Fatal("stale dots result must not clear the active loading state")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "current" {
			t.Fatalf("stale dots result changed entries: %+v", got.dotsEntries)
		}

		got = drive(got, dotsLoadedMsg{gen: currentGen, entries: []app.DotStatus{{Name: "fresh", Health: app.HealthOK}}})
		if got.dotsLoading {
			t.Fatal("current dots result should clear loading")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "fresh" {
			t.Fatalf("current dots result did not update entries: %+v", got.dotsEntries)
		}
	})

	t.Run("begin dots operation cancels previous operation", func(t *testing.T) {
		m := baseModel(nil)
		cancelled := false
		m.dotsCancel = func() { cancelled = true }
		m.beginDotsOperation("Discovering dots…")
		if !cancelled {
			t.Fatal("previous dots operation was not cancelled")
		}
		if !m.dotsLoading || m.statusMsg != "Discovering dots…" || m.dotsOpGen != 1 {
			t.Fatalf("dots operation state = loading:%v status:%q gen:%d", m.dotsLoading, m.statusMsg, m.dotsOpGen)
		}
	})

	t.Run("dotsPulledMsg sets entries and statusMsg", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "zsh", Health: app.HealthOK}}
		m := drive(baseModel(nil), dotsPulledMsg{entries: entries})
		if m.statusMsg != "✓ pulled" {
			t.Errorf("statusMsg = %q", m.statusMsg)
		}
		if len(m.dotsEntries) != 1 {
			t.Errorf("dotsEntries len = %d, want 1", len(m.dotsEntries))
		}
	})

	t.Run("dotsDiscoveredMsg reports candidate count", func(t *testing.T) {
		m := drive(baseModel(nil), dotsDiscoveredMsg{entries: []app.DotStatus{
			{Name: "kitty", TargetPath: "~/.config/kitty"},
			{Name: "zshrc", TargetPath: "~/.zshrc"},
		}, discoveredCount: 2})
		if m.dotsLoading {
			t.Error("dotsLoading should be false after discovery completes")
		}
		if m.statusMsg != "✓ discovered 2 candidates" {
			t.Errorf("statusMsg = %q", m.statusMsg)
		}
		if len(m.dotsEntries) != 2 {
			t.Errorf("dotsEntries len = %d, want refreshed discovered rows", len(m.dotsEntries))
		}
	})

	t.Run("dotsDiscoveredMsg clears filters so candidates are visible", func(t *testing.T) {
		fi := textinput.New()
		fi.SetValue("no-match")
		m := baseModel(nil)
		m.filter = fi
		m.dotsSearchActive = true
		m.dotsEntries = []app.DotStatus{{Name: "tracked", Group: "base"}}
		got := drive(m, dotsDiscoveredMsg{entries: []app.DotStatus{
			{Name: "kitty", TargetPath: "~/.config/kitty", State: app.DotStateLocalOnly},
		}, discoveredCount: 1})
		if got.dotsSearchActive || got.filter.Value() != "" {
			t.Fatalf("filters not cleared: search=%v filter=%q", got.dotsSearchActive, got.filter.Value())
		}
		if visible := filteredDotsEntries(got); len(visible) != 1 || visible[0].Name != "kitty" {
			t.Fatalf("visible entries = %#v, want discovered kitty", visible)
		}
	})

	t.Run("dotsDiscoveredMsg selects first transient candidate", func(t *testing.T) {
		got := drive(baseModel(nil), dotsDiscoveredMsg{entries: []app.DotStatus{
			{Name: "tracked-missing", TargetPath: "~/.tracked", State: app.DotStateMissing, Group: "base"},
			{Name: "candidate", TargetPath: "~/.candidate", State: app.DotStateLocalOnly},
		}, discoveredCount: 1})
		rows := dotsVisibleRows(got)
		if got.dotsCursor < 0 || got.dotsCursor >= len(rows) {
			t.Fatalf("dotsCursor = %d outside rows %#v", got.dotsCursor, rows)
		}
		if rows[got.dotsCursor].entry.Name != "candidate" {
			t.Fatalf("selected row = %q, want candidate; rows=%#v", rows[got.dotsCursor].entry.Name, rows)
		}
	})

	t.Run("dotsPushedMsg sets statusMsg", func(t *testing.T) {
		m := drive(baseModel(nil), dotsPushedMsg{})
		if m.statusMsg != "✓ pushed" {
			t.Errorf("statusMsg = %q", m.statusMsg)
		}
	})

	t.Run("dotsPushedMsg with error", func(t *testing.T) {
		m := drive(baseModel(nil), dotsPushedMsg{err: errors.New("push failed")})
		if m.statusMsg != "✗ push failed" {
			t.Errorf("statusMsg = %q", m.statusMsg)
		}
	})

	t.Run("dotsPushedMsg with error still updates entries", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsEntries = []app.DotStatus{{Name: "old", State: app.DotStateConflict}}
		got := drive(m, dotsPushedMsg{
			entries: []app.DotStatus{{Name: "fresh", State: app.DotStateSynced}},
			err:     errors.New("push failed"),
		})
		if got.statusMsg != "✗ push failed" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "fresh" {
			t.Fatalf("dotsEntries = %#v, want refreshed entries despite error", got.dotsEntries)
		}
	})

	t.Run("dotsDeletedMsg clears confirmIdx", func(t *testing.T) {
		m := dotsModel()
		m.dotsConfirmIdx = 1
		m = drive(m, dotsDeletedMsg{name: "nvim", entries: m.dotsEntries})
		if m.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after remove", m.dotsConfirmIdx)
		}
	})

	t.Run("dotsDeletedMsg clamps cursor", func(t *testing.T) {
		// After removing the last entry, cursor should clamp to 0.
		entries := []app.DotStatus{{Name: "nvim", Health: app.HealthOK}}
		m := dotsModel()
		m.dotsCursor = 2
		m = drive(m, dotsDeletedMsg{name: "gitconfig", entries: entries})
		if m.dotsCursor != 0 {
			t.Errorf("dotsCursor = %d, want 0 after cursor clamp", m.dotsCursor)
		}
	})

	t.Run("dotsIgnoredMsg with error still updates entries", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsEntries = []app.DotStatus{{Name: "old", State: app.DotStateConflict}}
		got := drive(m, dotsIgnoredMsg{
			name:    "old",
			pattern: "old",
			entries: []app.DotStatus{{Name: "old", State: app.DotStateIgnored}},
			err:     errors.New("ignore failed"),
		})
		if got.statusMsg != "✗ ignore failed" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].State != app.DotStateIgnored {
			t.Fatalf("dotsEntries = %#v, want refreshed entries despite error", got.dotsEntries)
		}
	})

	t.Run("dotsVariantChangedMsg sets status and updates entries", func(t *testing.T) {
		m := baseModel(nil)
		m.beginDotsOperation("Creating variant for nvim…")
		gen := m.dotsOpGen
		got := drive(m, dotsVariantChangedMsg{
			gen:     gen,
			name:    "nvim",
			info:    app.DotVariantInfo{Name: "nvim", Host: "laptop", Package: "nvim@laptop"},
			entries: []app.DotStatus{{Name: "nvim", Package: "nvim@laptop", State: app.DotStateSynced}},
		})
		if got.dotsLoading {
			t.Fatal("dotsLoading should clear after variant change")
		}
		if got.statusMsg != "✓ created variant nvim@laptop for nvim" {
			t.Fatalf("statusMsg = %q", got.statusMsg)
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Package != "nvim@laptop" {
			t.Fatalf("dotsEntries = %#v, want variant package snapshot", got.dotsEntries)
		}
	})

	t.Run("dotsVariantChangedMsg with error still updates entries", func(t *testing.T) {
		m := baseModel(nil)
		m.beginDotsOperation("Removing variant for nvim…")
		gen := m.dotsOpGen
		got := drive(m, dotsVariantChangedMsg{
			gen:     gen,
			name:    "nvim",
			removed: true,
			entries: []app.DotStatus{{Name: "nvim", Package: "nvim", State: app.DotStateSynced}},
			err:     errors.New("variant failed"),
		})
		if got.statusMsg != "✗ variant failed" {
			t.Fatalf("statusMsg = %q", got.statusMsg)
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Package != "nvim" {
			t.Fatalf("dotsEntries = %#v, want refreshed entries despite error", got.dotsEntries)
		}
	})
}

func TestModel_DotsTab_OverwriteConfirm(t *testing.T) {
	conflictModel := func() Model {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		m.settings.DotsRepo = "/repo/dotfiles"
		m.dotsEntries = []app.DotStatus{
			{
				Name:       "gitconfig",
				Health:     app.HealthConflict,
				State:      app.DotStateConflict,
				TargetPath: "~/.gitconfig",
				Actions:    []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove},
			},
		}
		return m
	}

	t.Run("use repo on conflict arms repo confirm", func(t *testing.T) {
		m := drive(conflictModel(), pressRune('u'))
		if m.dotsOverwriteIdx != 0 {
			t.Errorf("dotsOverwriteIdx = %d, want 0 after first use-repo on conflict", m.dotsOverwriteIdx)
		}
		if m.dotsLocalIdx != -1 {
			t.Errorf("dotsLocalIdx = %d, want -1 when repo confirm is armed", m.dotsLocalIdx)
		}
	})

	t.Run("use local on conflict arms local confirm", func(t *testing.T) {
		m := drive(conflictModel(), pressRune('l'))
		if m.dotsLocalIdx != 0 {
			t.Errorf("dotsLocalIdx = %d, want 0 after first use-local on conflict", m.dotsLocalIdx)
		}
		if m.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 when local confirm is armed", m.dotsOverwriteIdx)
		}
	})

	t.Run("esc cancels repo confirm", func(t *testing.T) {
		m := drive(conflictModel(), pressRune('u'), pressEsc())
		if m.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 after esc", m.dotsOverwriteIdx)
		}
	})

	t.Run("navigation cancels repo confirm", func(t *testing.T) {
		m := drive(conflictModel(), pressRune('u'), pressRune('j'))
		if m.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 after navigation", m.dotsOverwriteIdx)
		}
	})

	t.Run("delete is swallowed while overwrite confirm is armed", func(t *testing.T) {
		m := conflictModel()
		m.dotsOverwriteIdx = 0
		m = drive(m, pressRune('d'))
		if m.dotsOverwriteIdx != 0 {
			t.Errorf("dotsOverwriteIdx = %d, want 0 while overwrite confirm remains armed", m.dotsOverwriteIdx)
		}
		if m.dotsLocalIdx != -1 {
			t.Errorf("dotsLocalIdx = %d, want -1", m.dotsLocalIdx)
		}
		if m.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 because delete should not arm", m.dotsConfirmIdx)
		}
	})

	t.Run("repo confirm is swallowed while delete confirm is armed", func(t *testing.T) {
		m := conflictModel()
		m.dotsConfirmIdx = 0
		m = drive(m, pressRune('u'))
		if m.dotsConfirmIdx != 0 {
			t.Errorf("dotsConfirmIdx = %d, want 0 while delete confirm remains armed", m.dotsConfirmIdx)
		}
		if m.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 because repo confirm should not arm", m.dotsOverwriteIdx)
		}
	})

	t.Run("use repo on non-conflict entry is no-op", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		m.settings.DotsRepo = "/repo/dotfiles"
		m.dotsEntries = []app.DotStatus{
			{Name: "nvim", Health: app.HealthOK},
		}
		m = drive(m, pressRune('u'))
		if m.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 for non-conflict entry", m.dotsOverwriteIdx)
		}
	})
}

func TestModel_DotsTab_FixedMsg(t *testing.T) {
	t.Run("snapshot clears armed confirmations", func(t *testing.T) {
		m := dotsModel()
		m.dotsConfirmIdx = 0
		m.dotsOverwriteIdx = 1
		m.dotsLocalIdx = 1
		m.dotsIgnoreIdx = 2
		m.applyDotsSnapshot([]app.DotStatus{{Name: "zshrc", Health: app.HealthMissing}}, "", nil)
		if m.dotsConfirmIdx != -1 || m.dotsOverwriteIdx != -1 || m.dotsLocalIdx != -1 || m.dotsIgnoreIdx != -1 {
			t.Fatalf("confirmation indexes = %d/%d/%d/%d, want all -1",
				m.dotsConfirmIdx, m.dotsOverwriteIdx, m.dotsLocalIdx, m.dotsIgnoreIdx)
		}
	})

	t.Run("dotsFixedMsg clears conflict confirms and sets statusMsg", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "gitconfig", Health: app.HealthOK}}
		m := dotsModel()
		m.dotsOverwriteIdx = 2
		m.dotsLocalIdx = 2
		m = drive(m, dotsFixedMsg{name: "gitconfig", entries: entries})
		if m.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 after fixed", m.dotsOverwriteIdx)
		}
		if m.dotsLocalIdx != -1 {
			t.Errorf("dotsLocalIdx = %d, want -1 after fixed", m.dotsLocalIdx)
		}
		if m.statusMsg != "✓ resolved gitconfig" {
			t.Errorf("statusMsg = %q, want '✓ resolved gitconfig'", m.statusMsg)
		}
		if len(m.dotsEntries) != 1 {
			t.Errorf("dotsEntries len = %d, want 1", len(m.dotsEntries))
		}
	})

	t.Run("dotsFixedMsg with error sets error statusMsg", func(t *testing.T) {
		m := drive(baseModel(nil), dotsFixedMsg{name: "nvim", err: errors.New("backup failed")})
		if m.statusMsg != "✗ backup failed" {
			t.Errorf("statusMsg = %q", m.statusMsg)
		}
	})

	t.Run("dotsFixedMsg with error still updates entries", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict}}
		got := drive(m, dotsFixedMsg{
			name:    "nvim",
			entries: []app.DotStatus{{Name: "nvim", State: app.DotStateSynced}},
			err:     errors.New("resolve failed"),
		})
		if got.statusMsg != "✗ resolve failed" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].State != app.DotStateSynced {
			t.Fatalf("dotsEntries = %#v, want refreshed entries despite error", got.dotsEntries)
		}
	})
}

// ─── Maintenance state machine ────────────────────────────────────────────────

func TestDangerZone_SettingsCursor(t *testing.T) {
	t.Run("reset settings enter sets dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetSettings)...)
		msgs = append(msgs, pressEnter())
		m := drive(baseModel(nil), msgs...)
		if m.dangerConfirmRow != settingsRowResetSettings {
			t.Errorf("dangerConfirmRow = %d, want %d", m.dangerConfirmRow, settingsRowResetSettings)
		}
	})

	t.Run("reset settings space is no-op", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetSettings)...)
		msgs = append(msgs, pressRune(' '))
		m := drive(baseModel(nil), msgs...)
		if m.dangerConfirmRow != -1 {
			t.Errorf("dangerConfirmRow = %d after space, want -1", m.dangerConfirmRow)
		}
	})

	t.Run("reset cache enter sets dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetCache)...)
		msgs = append(msgs, pressEnter())
		m := drive(baseModel(nil), msgs...)
		if m.dangerConfirmRow != settingsRowResetCache {
			t.Errorf("dangerConfirmRow = %d, want %d", m.dangerConfirmRow, settingsRowResetCache)
		}
	})

	t.Run("sync row space is no-op", func(t *testing.T) {
		base := baseModel(nil)
		base.settings.DotsRepo = "~/dotfiles"
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressRune(' '))
		m := drive(base, msgs...)
		if m.dangerConfirmRow != -1 {
			t.Errorf("dangerConfirmRow = %d after space, want -1", m.dangerConfirmRow)
		}
	})

	t.Run("esc cancels dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetSettings)...)
		msgs = append(msgs, pressEnter(), pressEsc())
		m := drive(baseModel(nil), msgs...)
		if m.dangerConfirmRow != -1 {
			t.Errorf("dangerConfirmRow = %d after esc, want -1", m.dangerConfirmRow)
		}
	})

	t.Run("sync row with no DotsRepo sets statusMsg", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressEnter())
		m := drive(baseModel(nil), msgs...)
		if m.statusMsg == "" {
			t.Error("expected statusMsg when DotsRepo not configured")
		}
	})

	t.Run("sync row with DotsRepo asks keep-local choice", func(t *testing.T) {
		base := baseModel(nil)
		base.settings.DotsRepo = "~/dotfiles"
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressEnter())
		m := drive(base, msgs...)
		if m.dangerConfirmRow != settingsRowDotsSync {
			t.Errorf("dangerConfirmRow = %d, want %d", m.dangerConfirmRow, settingsRowDotsSync)
		}
	})

	t.Run("keep-local choice ignores arrow keys", func(t *testing.T) {
		base := baseModel(nil)
		base.settings.DotsRepo = "~/dotfiles"
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		right := tea.KeyPressMsg{Code: tea.KeyRight}
		msgs = append(msgs, pressEnter(), right)
		m := drive(base, msgs...)
		if m.loading {
			t.Error("right arrow should not confirm dots disable")
		}
		if m.dangerConfirmRow != settingsRowDotsSync {
			t.Errorf("dangerConfirmRow = %d, want %d", m.dangerConfirmRow, settingsRowDotsSync)
		}
	})

	t.Run("keep-local choice enter does not confirm", func(t *testing.T) {
		base := baseModel(nil)
		base.settings.DotsRepo = "~/dotfiles"
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressEnter(), pressEnter())
		m := drive(base, msgs...)
		if m.loading {
			t.Error("enter should not confirm dots disable")
		}
		if m.dangerConfirmRow != settingsRowDotsSync {
			t.Errorf("dangerConfirmRow = %d, want %d", m.dangerConfirmRow, settingsRowDotsSync)
		}
	})

	t.Run("confirm enter triggers execution (loading=true)", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetSettings)...)
		msgs = append(msgs, pressEnter(), pressEnter()) // open confirm + execute
		m := drive(baseModel(nil), msgs...)
		// After the second enter, loading=true and dangerConfirmRow=-1.
		if m.dangerConfirmRow != -1 {
			t.Errorf("dangerConfirmRow = %d, want -1 after execution", m.dangerConfirmRow)
		}
		if !m.loading {
			t.Error("loading should be true after executing danger action")
		}
	})
}

func TestDangerZone_DangerOpDoneMsg(t *testing.T) {
	t.Run("success sets statusMsg with detail", func(t *testing.T) {
		m := drive(baseModel(nil), dangerOpDoneMsg{action: "reset-settings", detail: "settings cleared"})
		if !stringContains(m.statusMsg, "settings cleared") {
			t.Errorf("statusMsg = %q, want detail in status", m.statusMsg)
		}
		if m.loading {
			t.Error("loading should be false after dangerOpDoneMsg")
		}
	})

	t.Run("error sets error statusMsg", func(t *testing.T) {
		m := drive(baseModel(nil), dangerOpDoneMsg{action: "delete-host", err: errors.New("write failed")})
		if !stringContains(m.statusMsg, "✗") {
			t.Errorf("statusMsg = %q, want error prefix ✗", m.statusMsg)
		}
		if !stringContains(m.statusMsg, "delete-host") {
			t.Errorf("statusMsg = %q, want action name in error", m.statusMsg)
		}
	})

	t.Run("reload=true starts loading", func(t *testing.T) {
		m := drive(baseModel(nil), dangerOpDoneMsg{action: "reset-cache", reload: true})
		if !m.loading {
			t.Error("loading should be true when reload=true")
		}
	})
}

// ─── activityLabel ───────────────────────────────────────────────────────────

func TestActivityLabel_Branches(t *testing.T) {
	cases := []struct {
		name string
		m    Model
		want string
	}{
		{"searching", Model{searching: true}, "Searching…"},
		{"scanning", Model{scanningProviders: map[string]bool{"brew": true}}, "Refreshing providers: brew…"},
		{"finding local tools", Model{providerSnapshotRefreshing: true}, "Finding local tools…"},
		{"descriptions", Model{descRefreshing: true}, "Refreshing tool descriptions…"},
		{"dotsLoading", Model{dotsLoading: true}, "Loading dots…"},
		{"doctorRunning", Model{doctorRunning: true}, "Running doctor…"},
		{"default", Model{}, "Loading…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := activityLabel(tc.m); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ─── selectedHostName ─────────────────────────────────────────────────────

func TestSelectedHostName_NilInfo(t *testing.T) {
	m := Model{}
	if got := m.selectedHostName(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestSelectedHostName_ValidCursor(t *testing.T) {
	m := Model{
		hostInfo: &app.HostInfo{
			Hosts: map[string]config.HostAssignment{
				"alpha": {},
				"beta":  {},
			},
		},
		hostCursor: 0,
	}
	// sorted: ["alpha", "beta"] → cursor 0 → "alpha"
	if got := m.selectedHostName(); got != "alpha" {
		t.Errorf("got %q, want alpha", got)
	}
}

func TestSelectedHostName_OutOfRange(t *testing.T) {
	m := Model{
		hostInfo: &app.HostInfo{
			Hosts: map[string]config.HostAssignment{"alpha": {}},
		},
		hostCursor: 99,
	}
	if got := m.selectedHostName(); got != "" {
		t.Errorf("out-of-range cursor: got %q, want empty", got)
	}
}

// ─── windowTitle ─────────────────────────────────────────────────────────────

func TestWindowTitle_Modes(t *testing.T) {
	cases := []struct {
		mode viewMode
		want string
	}{
		{viewDots, "omni — dots"},
		{viewGroups, "omni — groups"},
		{viewSettings, "omni — settings"},
		{viewSetup, "omni — setup"},
		{viewList, "omni"},
		{viewSearch, "omni"},
	}
	for _, tc := range cases {
		m := Model{mode: tc.mode}
		if got := m.windowTitle(); got != tc.want {
			t.Errorf("mode %d: got %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// ─── shortHostname ───────────────────────────────────────────────────────────

func TestShortHostname_OmniHostnameEnvTrimsDot(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "myhost.local.example.com")
	if got := shortHostname(); got != "myhost" {
		t.Errorf("got %q, want myhost", got)
	}
}

func TestShortHostname_OmniHostnameEnvTrimsSpace(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "  myhost.local  ")
	if got := shortHostname(); got != "myhost" {
		t.Errorf("got %q, want myhost", got)
	}
}

func TestShortHostname_OmniHostnameNoDot(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "mymachine")
	if got := shortHostname(); got != "mymachine" {
		t.Errorf("got %q, want mymachine", got)
	}
}

func TestShortHostname_OmniHostnameEmpty_FallsBackToOsHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "")
	// Can't assert exact value (machine-dependent), just verify no panic and type.
	got := shortHostname()
	_ = got
}

func TestDefaultSetupHostName_NoHostsUsesHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workstation")
	m := baseModel(nil)
	m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{}}
	if got := m.defaultSetupHostName(); got != "workstation" {
		t.Fatalf("defaultSetupHostName = %q, want workstation", got)
	}
}

func TestDefaultSetupHostName_UnknownHostsUsesHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workstation")
	m := baseModel(nil)
	if got := m.defaultSetupHostName(); got != "workstation" {
		t.Fatalf("defaultSetupHostName = %q, want workstation", got)
	}
}

func TestDefaultSetupHostName_ExistingHostsUsesHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "workstation.local")
	m := baseModel(nil)
	m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{"work": {}}}
	if got := m.defaultSetupHostName(); got != "workstation" {
		t.Fatalf("defaultSetupHostName = %q, want workstation", got)
	}
}

// ─── Migration UX regression tests ───────────────────────────────────────────
//
// These cover three bugs that were fixed together:
//   1. InstalledWith was written as the ecosystem provider name ("node") instead of
//      the concrete backend ("bun") for bulk-checked providers.
//      Covered by TestRefreshInstalled_BulkPath_ConcreteResolver in the app layer.
//
//   2. The migration keypress did not set m.migrating=true, so a concurrent
//      progressDoneMsg (from the launch scan channel closing) could clear
//      m.loading before migrateProviderDoneMsg arrived, killing the spinner.
//
//   3. installedRefreshedMsg and updatesRefreshedMsg cleared m.statusMsg
//      unconditionally, wiping the "Migrating…" message mid-flight.

// wrongProvTool returns an installed/tracked tool that will register as
// syncWrongProv when the model's effectiveNodeManager is "bun".
func wrongProvTool() *database.ToolCache {
	return &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		InstalledWith: "npm", // installed via npm but bun is the effective manager
		Installed:     true,
		Tracked:       true,
	}
}

// wrongProvModel returns a baseModel loaded with one syncWrongProv tool.
func wrongProvModel() Model {
	m := baseModel([]*database.ToolCache{wrongProvTool()})
	m.effectiveNodeManager = "bun"
	m.upgradingKeys = make(map[string]bool)
	return m
}

func TestSyncStatusOf_PinnedProviderMismatchWinsOverDefault(t *testing.T) {
	tool := &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		InstalledWith: "bun",
		Installed:     true,
		Tracked:       true,
	}
	m := baseModel([]*database.ToolCache{tool})
	m.effectiveNodeManager = "bun"
	m.toolProviderPins = map[string]string{"typescript": "npm"}

	if got := m.syncStatusOf(tool); got != syncWrongProv {
		t.Fatalf("syncStatusOf pinned mismatch = %v, want syncWrongProv", got)
	}
}

func TestPinnedProvider_KeyPressArmsClearOverride(t *testing.T) {
	tool := &database.ToolCache{
		Name:          "typescript",
		Provider:      "node",
		InstalledWith: "npm",
		Installed:     true,
		Tracked:       true,
	}
	m := baseModel([]*database.ToolCache{tool})
	m.effectiveNodeManager = "bun"
	m.toolProviderPins = map[string]string{"typescript": "npm"}
	m.upgradingKeys = make(map[string]bool)
	m.applyFilter()

	got := drive(m, pressRune('p'))
	if got.listConfirm.action != listConfirmClearProviderOverride {
		t.Fatalf("listConfirm.action = %q, want clear provider override", got.listConfirm.action)
	}
	if got.loading {
		t.Fatal("loading should stay false before clear override confirmation")
	}

	got = drive(got, pressRune('p'))
	if !got.loading {
		t.Fatal("loading should be true after confirmed clear override")
	}
	if !got.migrating {
		t.Fatal("migrating should be true when clearing an installed provider override")
	}
	if got.rowOpKey != toolKey("typescript", "node") {
		t.Fatalf("rowOpKey = %q, want selected tool key", got.rowOpKey)
	}
}

// TestMigration_KeyPressSetsFlags verifies that confirming 'r' on a syncWrongProv
// tool sets both m.loading and m.migrating and populates the status message.
// Regression: previously m.migrating was never set, breaking the race guard.
func TestMigration_KeyPressSetsFlags(t *testing.T) {
	m := wrongProvModel()
	got := drive(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if got.loading {
		t.Error("loading should stay false before MigrateProvider confirmation")
	}
	if got.listConfirm.action != listConfirmReinstallDefault {
		t.Fatalf("listConfirm.action = %q, want reinstall-default", got.listConfirm.action)
	}
	got = drive(got, tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !got.loading {
		t.Error("loading should be true after confirmed MigrateProvider keypress")
	}
	if !got.migrating {
		t.Error("migrating should be true after confirmed MigrateProvider keypress — regression: was never set")
	}
	if !stringContains(got.statusMsg, "Reinstalling") {
		t.Errorf("statusMsg = %q, want 'Reinstalling…' prefix", got.statusMsg)
	}
	if !stringContains(got.statusMsg, "default (node)") {
		t.Errorf("statusMsg = %q, want default provider detail", got.statusMsg)
	}
	if got.rowOpKey != toolKey("typescript", "node") {
		t.Errorf("rowOpKey = %q, want selected tool key", got.rowOpKey)
	}
	if !stringContains(got.rowOpStatus, "Reinstalling") {
		t.Errorf("rowOpStatus = %q, want Reinstalling status", got.rowOpStatus)
	}
}

// TestMigration_ProgressDoneMsg_DoesNotClearLoadingWhileMigrating verifies that
// a progressDoneMsg arriving while m.migrating=true does NOT clear m.loading.
// Regression: the launch scan's channel-close fired progressDoneMsg which set
// m.loading=false, making the spinner disappear mid-migration.
func TestMigration_ProgressDoneMsg_DoesNotClearLoadingWhileMigrating(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.migrating = true
	got := drive(m, progressDoneMsg{}) // simulate launch scan channel closing
	if !got.loading {
		t.Error("progressDoneMsg must NOT clear m.loading while m.migrating=true — regression: was clearing it")
	}
	if !got.migrating {
		t.Error("progressDoneMsg must not clear m.migrating; only migrateProviderDoneMsg owns that flag")
	}
}

// TestMigration_ProviderScannedMsg_DoesNotClearStatusWhileMigrating verifies
// that providerScannedMsg (the last provider finishing) does not wipe
// m.statusMsg when m.migrating=true.
// Regression: previously installedRefreshedMsg/updatesRefreshedMsg would clear
// the "Migrating…" banner unconditionally mid-flight.
func TestMigration_AllProvidersDoneMsg_DoesNotClearStatusWhileMigrating(t *testing.T) {
	m := baseModel(nil)
	m.migrating = true
	m.statusMsg = "Migrating typescript to correct provider…"
	// allProvidersDoneMsg is where status clearing happens; must be suppressed during migration.
	got := drive(m, allProvidersDoneMsg{tools: threeTools()})
	if got.statusMsg == "" {
		t.Error("allProvidersDoneMsg must NOT clear statusMsg while m.migrating=true — regression: was clearing it")
	}
}

// TestMigration_DoneMsg_ClearsBothFlags verifies that migrateProviderDoneMsg
// correctly clears m.loading and m.migrating and sets a success status.
func TestMigration_DoneMsg_ClearsBothFlags(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.migrating = true
	m.statusMsg = "Migrating typescript to correct provider…"
	got := drive(m, migrateProviderDoneMsg{name: "typescript", tools: threeTools()})
	if got.loading {
		t.Error("loading should be false after migrateProviderDoneMsg success")
	}
	if got.migrating {
		t.Error("migrating should be false after migrateProviderDoneMsg success")
	}
	if !stringContains(got.statusMsg, "✓") {
		t.Errorf("statusMsg = %q, want ✓ prefix on success", got.statusMsg)
	}
	if len(got.visibleTools) != 3 {
		t.Errorf("visibleTools = %d, want 3 (from msg.tools)", len(got.visibleTools))
	}
}

// TestMigration_DoneMsg_Error_ClearsBothFlags verifies that even on error
// both m.loading and m.migrating are cleared by migrateProviderDoneMsg.
func TestMigration_DoneMsg_Error_ClearsBothFlags(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.migrating = true
	got := drive(m, migrateProviderDoneMsg{name: "typescript", err: errFake("migrate failed")})
	if got.loading {
		t.Error("loading should be false after migrateProviderDoneMsg error")
	}
	if got.migrating {
		t.Error("migrating should be false after migrateProviderDoneMsg error")
	}
	if !stringContains(got.statusMsg, "✗") {
		t.Errorf("statusMsg = %q, want ✗ prefix on error", got.statusMsg)
	}
}

// errFake is a minimal error implementation for table-driven tests.
type errFake string

func (e errFake) Error() string { return string(e) }

// stringContains is a helper to check string containment.
func stringContains(s, sub string) bool {
	return len(s) > 0 && len(sub) > 0 && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}

// ─── Cursor reveal on tab switch ─────────────────────────────────────────────

// toSettingsRaw switches to the settings tab without the reveal press.
// 3 tabs from list: list→dots→groups→settings.
func toSettingsRaw() []tea.Msg {
	return []tea.Msg{pressTab(), pressTab(), pressTab()}
}

// TestCursorReveal_FirstDownAfterTabSwitch verifies that the first navigation
// keypress after a tab switch reveals the cursor at its current position (row 0)
// without moving it, and that the second keypress navigates normally.
func TestCursorReveal_FirstDownAfterTabSwitch(t *testing.T) {
	// After 3 tabs cursorHidden is true and settingsCursor is 0.
	// First j should reveal (cursorHidden→false) but NOT advance the cursor.
	m := drive(baseModel(nil), append(toSettingsRaw(), pressRune('j'))...)
	if m.mode != viewSettings {
		t.Fatalf("mode = %v, want viewSettings", m.mode)
	}
	if m.cursorHidden {
		t.Error("cursorHidden should be false after first keypress")
	}
	if m.settingsCursor != 0 {
		t.Errorf("settingsCursor = %d after first j, want 0 (revealed, not moved)", m.settingsCursor)
	}

	// Second j should now navigate: cursor moves from 0 → 1.
	m2 := drive(m, pressRune('j'))
	if m2.settingsCursor != 1 {
		t.Errorf("settingsCursor = %d after second j, want 1 (navigated)", m2.settingsCursor)
	}
}

// TestCursorReveal_ActionKeyNotConsumed verifies that a non-navigation key
// (Enter) is NOT consumed by the reveal logic and fires its action on row 0.
func TestCursorReveal_ActionKeyNotConsumed(t *testing.T) {
	// Row 0 in settings is AutoImport; space toggles it.
	// After raw tab-switch (cursorHidden=true), space should still toggle.
	m := drive(baseModel(nil), append(toSettingsRaw(), pressRune(' '))...)
	if m.mode != viewSettings {
		t.Fatalf("mode = %v, want viewSettings", m.mode)
	}
	if !m.settings.AutoImport {
		t.Error("space on row 0 (AutoImport) should toggle setting even when cursor was hidden")
	}
}

// TestCursorReveal_CursorHiddenFalseAfterKeypress verifies that any keypress
// clears cursorHidden, regardless of whether it was a navigation key.
func TestCursorReveal_CursorHiddenFalseAfterKeypress(t *testing.T) {
	// Verify cursorHidden is set after the tab switch.
	mHidden := drive(baseModel(nil), toSettingsRaw()...)
	if !mHidden.cursorHidden {
		t.Fatal("cursorHidden should be true immediately after tab switch to settings")
	}

	// Any keypress (here: j) must clear cursorHidden.
	mRevealed := drive(mHidden, pressRune('j'))
	if mRevealed.cursorHidden {
		t.Error("cursorHidden should be false after any keypress")
	}

	// Verify for a non-navigation key too (space).
	mHidden2 := drive(baseModel(nil), toSettingsRaw()...)
	mRevealed2 := drive(mHidden2, pressRune(' '))
	if mRevealed2.cursorHidden {
		t.Error("cursorHidden should be false after non-navigation keypress")
	}
}

// ─── Wrap-around cursor navigation ───────────────────────────────────────────

// TestWrapAround_ListTab verifies that Up at index 0 wraps to last and Down at
// last wraps to first in the list (tools) view.
func TestWrapAround_ListTab(t *testing.T) {
	// k at cursor 0 should wrap to last item (index 2).
	m := drive(baseModel(threeTools()), pressRune('k'))
	if m.cursor != 2 {
		t.Errorf("cursor after k at 0 = %d, want 2 (wrap to bottom)", m.cursor)
	}

	// j at cursor 2 should wrap back to 0.
	m2 := drive(m, pressRune('j'))
	if m2.cursor != 0 {
		t.Errorf("cursor after j at 2 = %d, want 0 (wrap to top)", m2.cursor)
	}
}

// TestWrapAround_SettingsTab verifies that Up at row 0 wraps to the last
// settings row and Down at the last row wraps back to 0.
func TestWrapAround_SettingsTab(t *testing.T) {
	// toSettings() = 3 tabs + reveal j; cursor is at 0 and ready to navigate.
	// k at row 0 should wrap to numSettingRows-1.
	m := drive(baseModel(nil), append(toSettings(), pressRune('k'))...)
	if m.settingsCursor != numSettingRows-1 {
		t.Errorf("settingsCursor after k at 0 = %d, want %d (wrap to bottom)", m.settingsCursor, numSettingRows-1)
	}

	// j at the last row should wrap back to 0.
	m2 := drive(m, pressRune('j'))
	if m2.settingsCursor != 0 {
		t.Errorf("settingsCursor after j at last row = %d, want 0 (wrap to top)", m2.settingsCursor)
	}
}

// TestWrapAround_DotsTab verifies that Up at dotsCursor 0 wraps to the last
// visible dot entry and Down at the last entry wraps to 0.
func TestWrapAround_DotsTab(t *testing.T) {
	// dotsModel() has 3 entries; dotsCursor starts at 0 and cursorHidden is false.
	m := dotsModel()

	// k at cursor 0 should wrap to index 2.
	got := drive(m, pressRune('k'))
	if got.dotsCursor != 2 {
		t.Errorf("dotsCursor after k at 0 = %d, want 2 (wrap to bottom)", got.dotsCursor)
	}

	// j at cursor 2 should wrap back to 0.
	got2 := drive(got, pressRune('j'))
	if got2.dotsCursor != 0 {
		t.Errorf("dotsCursor after j at 2 = %d, want 0 (wrap to top)", got2.dotsCursor)
	}
}

// TestCursorReveal_GroupsTab verifies that the first navigation keypress after
// switching to the groups tab reveals the cursor at hostCursor 0 without
// moving it, and that the second keypress navigates normally.
func TestCursorReveal_GroupsTab(t *testing.T) {
	// Build a model in list mode with 2 hosts so the groups tab has content.
	m := baseModel(nil)
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{
			"alpha": {Groups: []string{"work"}},
			"beta":  {Groups: []string{"personal"}},
		},
	}
	m.groupNames = []string{"work", "personal"}

	// Two Tab presses: list → dots → groups. cursorHidden should be true.
	mGroups := drive(m, pressTab(), pressTab())
	if mGroups.mode != viewGroups {
		t.Fatalf("mode = %v, want viewGroups after 2 tabs", mGroups.mode)
	}
	if !mGroups.cursorHidden {
		t.Fatal("cursorHidden should be true immediately after tab switch to groups")
	}

	// First j: reveal only — cursorHidden clears, hostCursor stays at 0.
	mRevealed := drive(mGroups, pressRune('j'))
	if mRevealed.cursorHidden {
		t.Error("cursorHidden should be false after first j")
	}
	if mRevealed.hostCursor != 0 {
		t.Errorf("hostCursor = %d after first j, want 0 (revealed, not moved)", mRevealed.hostCursor)
	}

	// Second j: navigate — hostCursor moves to 1 OR assignmentSection advances.
	mNav := drive(mRevealed, pressRune('j'))
	navigated := mNav.hostCursor == 1 || mNav.assignmentSection == 1
	if !navigated {
		t.Errorf("after second j: hostCursor=%d assignmentSection=%d, want cursor moved (hostCursor=1 or assignmentSection=1)",
			mNav.hostCursor, mNav.assignmentSection)
	}
}

// TestWrapAround_GroupsTab_UpWrapsToGroups verifies that pressing Up (k) at
// the top of the hosts section (assignmentSection=0, hostCursor=0) wraps the
// cursor to the bottom of the groups section.
func TestWrapAround_GroupsTab_UpWrapsToGroups(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 0
	m.hostCursor = 0

	got := drive(m, pressRune('k'))

	if got.assignmentSection != 1 {
		t.Errorf("assignmentSection = %d after k at top of hosts, want 1 (groups section)", got.assignmentSection)
	}
	allGroups := buildAllGroupNames(m.groupNames)
	lastIdx := len(allGroups) - 1
	if got.groupCursor != lastIdx {
		t.Errorf("groupCursor = %d after wrap, want %d (last group index)", got.groupCursor, lastIdx)
	}
}

// TestWrapAround_GroupsTab_DownWrapsToHosts verifies that pressing Down (j) at
// the bottom of the groups section wraps the cursor back to the top of the
// hosts section (assignmentSection=0, hostCursor=0).
func TestWrapAround_GroupsTab_DownWrapsToHosts(t *testing.T) {
	m := hostsModel()
	allGroups := buildAllGroupNames(m.groupNames)
	m.assignmentSection = 1
	m.groupCursor = len(allGroups) - 1

	got := drive(m, pressRune('j'))

	if got.assignmentSection != 0 {
		t.Errorf("assignmentSection = %d after j at last group, want 0 (hosts section)", got.assignmentSection)
	}
	if got.hostCursor != 0 {
		t.Errorf("hostCursor = %d after wrap, want 0 (top of hosts)", got.hostCursor)
	}
}

// ── Global C keybinding: commit dotfiles from any main tab ───────────────────

// TestGlobalDotsCommit_FromToolsTab verifies that pressing C on the tools tab
// (viewList, the default) starts the dots commit operation when DotsRepo is set
// and dotsGitStatus is non-empty.
func TestGlobalDotsCommit_FromToolsTab(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/repo/dotfiles"
	m.dotsGitStatus = "M somefile"

	got := drive(m, pressRune('C'))

	if !got.dotsLoading {
		t.Error("dotsLoading should be true after C on tools tab with pending changes")
	}
}

// TestGlobalDotsCommit_FromDotsTab verifies that pressing C while on the dots
// tab also starts the commit operation.
func TestGlobalDotsCommit_FromDotsTab(t *testing.T) {
	m := dotsModel()
	m.dotsGitStatus = "M somefile"

	got := drive(m, pressRune('C'))

	if !got.dotsLoading {
		t.Error("dotsLoading should be true after C on dots tab with pending changes")
	}
}

// TestGlobalDotsCommit_FromSettingsTab verifies that pressing C while on the
// settings tab also starts the commit operation.
func TestGlobalDotsCommit_FromSettingsTab(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/repo/dotfiles"
	m.dotsGitStatus = "M somefile"
	msgs := toSettings()
	msgs = append(msgs, pressRune('C'))

	got := drive(m, msgs...)

	if !got.dotsLoading {
		t.Error("dotsLoading should be true after C on settings tab with pending changes")
	}
}

// TestGlobalDotsCommit_NoRepoShowsError verifies that pressing C when DotsRepo
// is empty sets an error status and does not start a commit operation.
func TestGlobalDotsCommit_NoRepoShowsError(t *testing.T) {
	m := baseModel(nil)
	// DotsRepo is empty by default

	got := drive(m, pressRune('C'))

	if got.dotsLoading {
		t.Error("dotsLoading should stay false when DotsRepo is not configured")
	}
	if !got.statusIsErr {
		t.Error("statusIsErr should be true when DotsRepo is not configured")
	}
	if !strings.Contains(got.statusMsg, "set dots_repo") {
		t.Errorf("statusMsg = %q, want message containing 'set dots_repo'", got.statusMsg)
	}
}

// TestGlobalDotsCommit_DisabledShowsError verifies that pressing C when dots
// sync is disabled sets an error status and does not start a commit operation.
func TestGlobalDotsCommit_DisabledShowsError(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/repo/dotfiles"
	m.settings.DotsDisabled = config.BoolPtr(true)
	m.dotsGitStatus = "M somefile"

	got := drive(m, pressRune('C'))

	if got.dotsLoading {
		t.Error("dotsLoading should stay false when dots sync is disabled")
	}
	if !got.statusIsErr {
		t.Error("statusIsErr should be true when dots sync is disabled")
	}
	if !strings.Contains(got.statusMsg, "disabled") {
		t.Errorf("statusMsg = %q, want message containing 'disabled'", got.statusMsg)
	}
}

// TestGlobalDotsCommit_NothingToCommit verifies that pressing C when
// dotsGitStatus is empty (nothing to commit) is a no-op: no operation starts
// and no error status is shown.
func TestGlobalDotsCommit_NothingToCommit(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "/repo/dotfiles"
	m.dotsGitStatus = "" // nothing to commit

	got := drive(m, pressRune('C'))

	if got.dotsLoading {
		t.Error("dotsLoading should stay false when there is nothing to commit")
	}
	if strings.Contains(got.statusMsg, "Committing") {
		t.Errorf("statusMsg = %q, should not mention committing when nothing to commit", got.statusMsg)
	}
}

// ─── Group Reassign Queue ──────────────────────────────────────────────────────

// TestStartGroupReassignQueue_OpensFirstPicker verifies that calling
// startGroupReassignQueue with two names opens a group picker for the first
// tool and leaves the second in pendingGroupReassign.
func TestStartGroupReassignQueue_OpensFirstPicker(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "brew"},
		{Name: "fd", Provider: "brew"},
	})
	m.startGroupReassignQueue([]string{"ripgrep", "fd"})

	if m.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", m.mode)
	}
	if !m.pickerPurposeReassign {
		t.Fatal("pickerPurposeReassign should be true")
	}
	if m.pickerActionTool.Name != "ripgrep" {
		t.Errorf("pickerActionTool.Name = %q, want %q", m.pickerActionTool.Name, "ripgrep")
	}
	if len(m.pendingGroupReassign) != 1 || m.pendingGroupReassign[0] != "fd" {
		t.Errorf("pendingGroupReassign = %v, want [fd]", m.pendingGroupReassign)
	}
	// Reassign pickers must NOT set m.loading — it blocks key input and
	// stale groupChangedMsg from prior tool would clear it for the next picker.
	if m.loading {
		t.Error("m.loading should be false for reassign picker (fire-and-forget)")
	}
}

// TestStartGroupReassignQueue_Empty verifies that calling startGroupReassignQueue
// with a nil or empty slice is a no-op (mode stays viewList).
func TestStartGroupReassignQueue_Empty(t *testing.T) {
	m := baseModel(nil)

	m.startGroupReassignQueue(nil)
	if m.mode != viewList {
		t.Errorf("nil: mode = %v, want viewList", m.mode)
	}

	m.startGroupReassignQueue([]string{})
	if m.mode != viewList {
		t.Errorf("empty: mode = %v, want viewList", m.mode)
	}
}

// TestCloseGroupPicker_ChainsReassign verifies that when a group picker
// completes with pickerPurposeReassign=true and more tools remain in
// pendingGroupReassign, closeGroupPicker opens the next picker.
func TestCloseGroupPicker_ChainsReassign(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "fd", Provider: "brew"},
	})
	// Simulate being mid-reassign for "ripgrep", with "fd" still queued.
	m.mode = viewGroupPicker
	m.pickerPurposeReassign = true
	m.pendingGroupReassign = []string{"fd"}

	m.closeGroupPicker()

	if m.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker (chained to next tool)", m.mode)
	}
	if !m.pickerPurposeReassign {
		t.Fatal("pickerPurposeReassign should still be true for chained picker")
	}
	if m.pickerActionTool.Name != "fd" {
		t.Errorf("pickerActionTool.Name = %q, want %q", m.pickerActionTool.Name, "fd")
	}
	if len(m.pendingGroupReassign) != 0 {
		t.Errorf("pendingGroupReassign = %v, want empty", m.pendingGroupReassign)
	}
}

// TestCloseGroupPicker_LastInQueueCleansUp verifies that when no more tools
// remain in pendingGroupReassign, closeGroupPicker returns to viewList and
// clears reassignCreatedGroups.
func TestCloseGroupPicker_LastInQueueCleansUp(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroupPicker
	m.pickerPurposeReassign = true
	m.pendingGroupReassign = nil
	m.reassignCreatedGroups = []string{"dev"}

	m.closeGroupPicker()

	if m.mode != viewList {
		t.Fatalf("mode = %v, want viewList after last reassign", m.mode)
	}
	if m.reassignCreatedGroups != nil {
		t.Errorf("reassignCreatedGroups = %v, want nil after queue drained", m.reassignCreatedGroups)
	}
}

// TestCancelGroupPicker_DrainsQueue verifies that cancelGroupPicker clears
// pendingGroupReassign and reassignCreatedGroups, then closes the picker.
func TestCancelGroupPicker_DrainsQueue(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroupPicker
	m.pickerPurposeReassign = true
	m.pendingGroupReassign = []string{"fd", "bat"}
	m.reassignCreatedGroups = []string{"dev"}

	m.cancelGroupPicker()

	if m.pendingGroupReassign != nil {
		t.Errorf("pendingGroupReassign = %v, want nil after cancel", m.pendingGroupReassign)
	}
	if m.reassignCreatedGroups != nil {
		t.Errorf("reassignCreatedGroups = %v, want nil after cancel", m.reassignCreatedGroups)
	}
	if m.mode != viewList {
		t.Errorf("mode = %v, want viewList after cancel", m.mode)
	}
}

// TestReassignCreatedGroups_CarryForward verifies that groups created during an
// earlier reassign picker are included in the groups offered by the next picker.
func TestReassignCreatedGroups_CarryForward(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "fd", Provider: "brew"},
	})
	// Simulate: first picker created "dev" group; next tool is "fd".
	m.pickerCreatedGroups = []string{"dev"}
	m.reassignCreatedGroups = nil
	m.pendingGroupReassign = []string{"fd"}

	m.openNextReassignPicker()

	if m.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker", m.mode)
	}
	if m.pickerActionTool.Name != "fd" {
		t.Errorf("pickerActionTool.Name = %q, want %q", m.pickerActionTool.Name, "fd")
	}
	found := false
	for _, g := range m.pickerGroups {
		if g == "dev" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("pickerGroups = %v, want \"dev\" to be carried forward", m.pickerGroups)
	}
	// pickerCreatedGroups resets for the new picker; carry-forward is in reassignCreatedGroups.
	if len(m.pickerCreatedGroups) != 0 {
		t.Errorf("pickerCreatedGroups = %v, want empty (reset per picker)", m.pickerCreatedGroups)
	}
}

// TestProgressDoneMsg_StartsReassignQueue verifies that a progressDoneMsg
// carrying claimedNames triggers startGroupReassignQueue: the first tool's
// picker opens with pickerPurposeReassign=true.
func TestProgressDoneMsg_StartsReassignQueue(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "brew"},
	})
	m.progressGen = 1

	got := drive(m, progressDoneMsg{
		gen:          1,
		claimedNames: []string{"ripgrep"},
		tools: []*database.ToolCache{
			{Name: "ripgrep", Provider: "brew"},
		},
	})

	if got.mode != viewGroupPicker {
		t.Fatalf("mode = %v, want viewGroupPicker after progressDoneMsg with claimedNames", got.mode)
	}
	if !got.pickerPurposeReassign {
		t.Fatal("pickerPurposeReassign should be true")
	}
	if got.pickerActionTool.Name != "ripgrep" {
		t.Errorf("pickerActionTool.Name = %q, want %q", got.pickerActionTool.Name, "ripgrep")
	}
}

// TestReassignQueue_E2E_FullCycle drives the complete flow:
// progressDoneMsg → picker for tool 1 → Enter → chains to tool 2 →
// groupChangedMsg from tool 1 arrives (shouldn't break) → Enter →
// queue drains → viewList.
func TestReassignQueue_E2E_FullCycle(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "ripgrep", Provider: "brew"},
		{Name: "fd", Provider: "brew"},
	}
	m := baseModel(tools)
	m.progressGen = 1
	m.groupNames = []string{"dev"}

	// Step 1: progressDoneMsg triggers queue with 2 claimed tools.
	m = drive(m, progressDoneMsg{
		gen:          1,
		claimedNames: []string{"ripgrep", "fd"},
		tools:        tools,
	})
	if m.mode != viewGroupPicker {
		t.Fatalf("step1: mode = %v, want viewGroupPicker", m.mode)
	}
	if m.pickerActionTool.Name != "ripgrep" {
		t.Fatalf("step1: picker tool = %q, want ripgrep", m.pickerActionTool.Name)
	}
	if len(m.pendingGroupReassign) != 1 {
		t.Fatalf("step1: pending = %v, want [fd]", m.pendingGroupReassign)
	}
	// pickerGroups should have "dev" + sentinel.
	if len(m.pickerGroups) < 2 {
		t.Fatalf("step1: pickerGroups = %v, want at least [dev, sentinel]", m.pickerGroups)
	}

	// Step 2: Press Enter on first group — selects it, closes picker, chains to fd.
	// pickerCursor=0 points to "dev".
	m = drive(m, pressEnter())
	if m.mode != viewGroupPicker {
		t.Fatalf("step2: mode = %v, want viewGroupPicker (chained to fd)", m.mode)
	}
	if m.pickerActionTool.Name != "fd" {
		t.Fatalf("step2: picker tool = %q, want fd", m.pickerActionTool.Name)
	}
	if len(m.pendingGroupReassign) != 0 {
		t.Fatalf("step2: pending = %v, want empty", m.pendingGroupReassign)
	}
	if m.loading {
		t.Error("step2: m.loading should be false between reassign pickers")
	}

	// Step 3: Stale groupChangedMsg from ripgrep's move arrives — should not break fd's picker.
	m = drive(m, groupChangedMsg{detail: "✓ ripgrep → dev"})
	if m.mode != viewGroupPicker {
		t.Fatalf("step3: mode = %v, want viewGroupPicker (fd still active)", m.mode)
	}
	if m.pickerActionTool.Name != "fd" {
		t.Fatalf("step3: picker tool = %q, want fd (unchanged)", m.pickerActionTool.Name)
	}

	// Step 4: Press Enter on fd's picker — last in queue, should return to viewList.
	m = drive(m, pressEnter())
	if m.mode != viewList {
		t.Fatalf("step4: mode = %v, want viewList after queue drained", m.mode)
	}
	if m.pickerPurposeReassign {
		t.Error("step4: pickerPurposeReassign should be false")
	}
	if m.reassignCreatedGroups != nil {
		t.Error("step4: reassignCreatedGroups should be nil")
	}
}
