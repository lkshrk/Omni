package tui

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

func oneInstalledOutdated() []*app.ToolView {
	return []*app.ToolView{{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true}}
}

func oneInstalledOutdatedUnknown() []*app.ToolView {
	return []*app.ToolView{{Name: "actionlint", Provider: "script", Installed: true, OutdatedUnknown: true, Tracked: true}}
}

func oneInstalled() []*app.ToolView {
	return []*app.ToolView{{Name: "ripgrep", Provider: "brew", Installed: true, Tracked: true}}
}

func oneMissing() []*app.ToolView {
	return []*app.ToolView{{Name: "curl", Provider: "brew", Installed: false, Tracked: true}}
}

func manyTools(n int) []*app.ToolView {
	out := make([]*app.ToolView, n)
	for i := range out {
		out[i] = &app.ToolView{Name: "tool", Provider: "brew"}
	}
	return out
}

func toSettings() []tea.Msg {
	// Tab order: Tools→Dots→Agents→Groups→Settings; fourth tab lands on settings.
	return []tea.Msg{pressTab(), pressTab(), pressTab(), pressTab(), pressRune('j')}
}

func nj(n int) []tea.Msg {
	msgs := make([]tea.Msg, n)
	for i := range msgs {
		msgs[i] = pressRune('j')
	}
	return msgs
}

func settingsDotsSyncReadyModel(t *testing.T) Model {
	t.Helper()
	m, repoDir := newDotsModelForCmds(t)
	if err := m.app.SaveDotsDisabled(context.Background(), false); err != nil {
		t.Fatalf("SaveDotsDisabled: %v", err)
	}
	m.settings = config.Settings{DotsRepo: repoDir}
	m.mode = viewSettings
	m.settingsCursor = settingsRowDotsSync
	m.dangerConfirmRow = -1
	m.stowInstalled = true
	return m
}

func openSettingsDotsSyncChoice(t *testing.T) Model {
	t.Helper()
	return drive(settingsDotsSyncReadyModel(t), pressEnter())
}

func setupStep1Model() Model {
	m := Model{
		keys:          DefaultKeyMap(),
		spinner:       spinner.New(),
		filter:        textinput.New(),
		commandInput:  textinput.New(),
		settingsInput: textinput.New(),
		mode:          viewSetup,
		setupStep:     1,
		upgradingKeys: make(map[string]bool),
		setupProviders: []app.SetupProviderOption{
			{Name: "system", Label: "system", Enabled: true},
			{Name: "node", Label: "node", Enabled: true},
		},
	}
	return m
}

func TestFlow_UC01_CursorNavigation(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())

	t.Run("j moves cursor down", func(t *testing.T) {
		got := drive(m, pressRune('j'))
		if got.cursor != 1 {
			t.Errorf("cursor = %d, want 1", got.cursor)
		}
	})

	t.Run("k at top wraps to bottom", func(t *testing.T) {
		got := drive(m, pressRune('k'))
		if got.cursor != 2 {
			t.Errorf("cursor = %d, want 2 (wrapped)", got.cursor)
		}
	})

	t.Run("j wraps to top from last item", func(t *testing.T) {
		got := drive(m, pressRune('j'), pressRune('j'), pressRune('j'))
		if got.cursor != 0 {
			t.Errorf("cursor = %d, want 0 (wrapped)", got.cursor)
		}
	})

	t.Run("j then k returns to 0", func(t *testing.T) {
		got := drive(m, pressRune('j'), pressRune('k'))
		if got.cursor != 0 {
			t.Errorf("cursor = %d, want 0", got.cursor)
		}
	})
}

func TestFlow_UC02_TopBottom(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())

	t.Run("G jumps to bottom", func(t *testing.T) {
		got := drive(m, pressRune('G'))
		if got.cursor != 2 {
			t.Errorf("cursor = %d, want 2", got.cursor)
		}
	})

	t.Run("home jumps to top", func(t *testing.T) {
		got := drive(m, pressRune('G'), pressHome())
		if got.cursor != 0 {
			t.Errorf("cursor = %d, want 0", got.cursor)
		}
	})
}

func TestFlow_UC03_PageNavigation(t *testing.T) {
	t.Parallel()
	m := baseModel(manyTools(20))
	m.height = 30

	t.Run("ctrl+d half-page down advances cursor", func(t *testing.T) {
		got := drive(m, pressCtrlD())
		if got.cursor <= 0 {
			t.Errorf("ctrl+d should advance cursor, got %d", got.cursor)
		}
	})

	t.Run("ctrl+u half-page up from bottom retreats cursor", func(t *testing.T) {
		got := drive(m, pressRune('G'), pressCtrlU())
		if got.cursor >= 19 {
			t.Errorf("ctrl+u should retreat cursor, got %d", got.cursor)
		}
	})

	t.Run("ctrl+f full-page down further than half-page", func(t *testing.T) {
		half := drive(m, pressCtrlD())
		full := drive(m, pressCtrlF())
		if full.cursor <= half.cursor {
			t.Errorf("ctrl+f (%d) should advance further than ctrl+d (%d)", full.cursor, half.cursor)
		}
	})

	t.Run("ctrl+b full-page up from bottom retreats cursor", func(t *testing.T) {
		got := drive(m, pressRune('G'), pressCtrlB())
		if got.cursor >= 19 {
			t.Errorf("ctrl+b should retreat cursor, got %d", got.cursor)
		}
	})
}

func TestFlow_UC04_EmptyListNavigation(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	msgs := []tea.Msg{pressRune('j'), pressRune('k'), pressHome(), pressRune('G')}
	got := drive(m, msgs...)
	if got.cursor != 0 {
		t.Errorf("cursor = %d on empty list, want 0", got.cursor)
	}
}

func TestFlow_UC05_SearchMode(t *testing.T) {
	t.Parallel()
	t.Run("/ enters viewSearch", func(t *testing.T) {
		got := drive(baseModel(threeTools()), pressRune('/'))
		if got.mode != viewSearch {
			t.Errorf("mode = %v, want viewSearch", got.mode)
		}
	})

	t.Run("/ clears stale filter value", func(t *testing.T) {
		m := baseModel(threeTools())
		m.filter.SetValue("s")
		got := drive(m, pressRune('/'))
		if got.filter.Value() != "" {
			t.Errorf("filter = %q, want empty after entering search", got.filter.Value())
		}
	})

	t.Run("filter keeps tool failure messages", func(t *testing.T) {
		m := baseModel(threeTools())
		m.setToolActionError(toolKey("git", "brew"), "provider not found")
		m.filter.SetValue("git")
		m.applyFilter()
		if m.rowErrors[toolKey("git", "brew")] != "provider not found" {
			t.Fatalf("rowErrors = %#v, want git failure to survive filtering", m.rowErrors)
		}
		out := renderList(m)
		if !strings.Contains(out, "provider not found") {
			t.Fatalf("filtered list should still show git failure, got:\n%s", out)
		}
	})

	t.Run("Esc exits viewSearch and clears filter", func(t *testing.T) {
		got := drive(baseModel(threeTools()), pressRune('/'), pressEsc())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
		if got.filter.Value() != "" {
			t.Error("filter should be cleared after esc")
		}
	})

	t.Run("Enter blurs input (stays viewSearch)", func(t *testing.T) {
		got := drive(baseModel(threeTools()), pressRune('/'), pressEnter())
		if got.mode != viewSearch {
			t.Errorf("mode = %v, want viewSearch after Enter", got.mode)
		}
		if got.filter.Focused() {
			t.Error("filter should be blurred after Enter")
		}
	})
}

func TestFlow_UC06_LiveFilter(t *testing.T) {
	t.Parallel(
	// 'g' matches only "git" among [git/brew, node/npm, python/pip].
	)

	got := drive(baseModel(threeTools()), pressRune('/'), pressRune('g'))
	if len(got.visibleTools) != 1 {
		t.Errorf("visibleTools = %d, want 1 after live filter", len(got.visibleTools))
	}
	if len(got.visibleTools) > 0 && got.visibleTools[0].Name != "git" {
		t.Errorf("visibleTools[0] = %q, want git", got.visibleTools[0].Name)
	}
}

func TestFlow_UC07_ProviderFilterPills(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.applyFilter() // populates providerNames

	t.Run("] advances provider tab", func(t *testing.T) {
		got := drive(m, pressRune(']'))
		if got.providerTabIdx != 1 {
			t.Errorf("providerTabIdx = %d, want 1", got.providerTabIdx)
		}
	})

	t.Run("] wraps around past last", func(t *testing.T) {
		n := len(m.providerNames) // 3 providers
		msgs := make([]tea.Msg, n+1)
		for i := range msgs {
			msgs[i] = pressRune(']')
		}
		got := drive(m, msgs...)
		if got.providerTabIdx != 0 {
			t.Errorf("providerTabIdx = %d, want 0 (wrapped)", got.providerTabIdx)
		}
	})

	t.Run("[ at All wraps to last provider", func(t *testing.T) {
		got := drive(m, pressRune('['))
		if got.providerTabIdx == 0 {
			t.Error("[ at All should wrap to last provider index")
		}
	})
}

func TestFlow_UC08_EscClearsFilter(t *testing.T) {
	t.Parallel()
	t.Run("Esc clears provider filter", func(t *testing.T) {
		m := baseModel(threeTools())
		m.applyFilter()
		m.providerTabIdx = 1
		got := drive(m, pressEsc())
		if got.providerTabIdx != 0 {
			t.Errorf("providerTabIdx = %d, want 0 after esc", got.providerTabIdx)
		}
	})

	t.Run("Esc clears group filter and resets groupTabIdx", func(t *testing.T) {
		m := baseModel(threeTools())
		m.groupFilter = "work"
		m.groupTabIdx = 2
		got := drive(m, pressEsc())
		if got.groupFilter != "" {
			t.Errorf("groupFilter = %q, want empty after esc", got.groupFilter)
		}
		if got.groupTabIdx != 0 {
			t.Errorf("groupTabIdx = %d, want 0 after esc", got.groupTabIdx)
		}
	})
}

func TestFlow_UC09_EnterOnTool(t *testing.T) {
	t.Parallel()
	t.Run("Enter on uninstalled tool sets loading", func(t *testing.T) {
		got := drive(baseModel(oneMissing()), pressEnter())
		if !got.loading {
			t.Error("loading should be true after enter on uninstalled tool")
		}
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
	})

	t.Run("Enter on installed tool is no-op", func(t *testing.T) {
		got := drive(baseModel(oneInstalled()), pressEnter())
		if got.loading {
			t.Error("loading should stay false for already-installed tool")
		}
	})

	t.Run("Enter on empty list is no-op", func(t *testing.T) {
		got := drive(baseModel(nil), pressEnter())
		if got.loading {
			t.Error("loading should stay false on empty list")
		}
	})
}

func TestFlow_UC10_InstallKey(t *testing.T) {
	t.Parallel()
	t.Run("i on uninstalled tracked tool sets loading", func(t *testing.T) {
		got := drive(baseModel(oneMissing()), pressRune('i'))
		if !got.loading {
			t.Error("loading should be true after i on uninstalled tracked tool")
		}
		if got.rowOpKey != toolKey("curl", "brew") {
			t.Errorf("rowOpKey = %q, want curl row operation", got.rowOpKey)
		}
		if got.rowOpStatus != "Installing curl…" {
			t.Errorf("rowOpStatus = %q, want Installing curl…", got.rowOpStatus)
		}
	})

	t.Run("i on installed tool is no-op", func(t *testing.T) {
		got := drive(baseModel(oneInstalled()), pressRune('i'))
		if got.loading {
			t.Error("loading should stay false when tool is already installed")
		}
	})
}

func TestFlow_UC11_DeleteKey(t *testing.T) {
	t.Parallel()
	t.Run("d on installed tool arms confirmation", func(t *testing.T) {
		got := drive(baseModel(oneInstalled()), pressRune('d'))
		if got.loading {
			t.Error("loading should stay false before delete confirmation")
		}
		if got.listConfirm.action != listConfirmDelete {
			t.Fatalf("listConfirm.action = %q, want delete", got.listConfirm.action)
		}
	})

	t.Run("second d confirms delete", func(t *testing.T) {
		got := drive(baseModel(oneInstalled()), pressRune('d'), pressRune('d'))
		if !got.loading {
			t.Error("loading should be true after confirmed delete")
		}
		if got.rowOpKey != toolKey("ripgrep", "brew") {
			t.Errorf("rowOpKey = %q, want ripgrep row operation", got.rowOpKey)
		}
		if got.rowOpStatus != "Deleting ripgrep…" {
			t.Errorf("rowOpStatus = %q, want Deleting ripgrep…", got.rowOpStatus)
		}
	})

	t.Run("d on uninstalled tool is no-op", func(t *testing.T) {
		got := drive(baseModel(oneMissing()), pressRune('d'))
		if got.loading {
			t.Error("loading should stay false when tool is not installed")
		}
	})
}

func TestFlow_UC12_UpgradeKey(t *testing.T) {
	t.Parallel()
	t.Run("u on outdated tool sets upgradingKeys", func(t *testing.T) {
		m := baseModel(oneInstalledOutdated())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('u'))
		if !got.upgradingKeys["ripgrep\x00brew"] {
			t.Error("expected upgradingKeys entry after u")
		}
	})

	t.Run("u on non-outdated tool is no-op", func(t *testing.T) {
		m := baseModel(oneInstalled())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('u'))
		if len(got.upgradingKeys) != 0 {
			t.Error("upgradingKeys should remain empty for non-outdated tool")
		}
	})

	t.Run("u on unknown-outdated tool upgrades and offers the hint", func(t *testing.T) {
		m := baseModel(oneInstalledOutdatedUnknown())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('u'))
		if !got.upgradingKeys["actionlint\x00script"] {
			t.Error("expected upgradingKeys entry for a tool whose outdated state is unknown")
		}
		var offered bool
		for _, hint := range toolInlineHints(got, oneInstalledOutdatedUnknown()[0]) {
			if hint.desc == "upgrade" {
				offered = true
			}
		}
		if !offered {
			t.Error("expected the upgrade hint for a tool whose outdated state is unknown")
		}
	})
}

func TestFlow_UC13_UpgradeAllKey(t *testing.T) {
	t.Parallel()
	t.Run("U with outdated tools sets wildcard key", func(t *testing.T) {
		m := baseModel(oneInstalledOutdated())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('U'))
		if !got.upgradingKeys["*"] {
			t.Error("expected wildcardupgradingKeys[*] after U with updates")
		}
	})

	t.Run("U with no outdated tools is no-op", func(t *testing.T) {
		m := baseModel(oneInstalled())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('U'))
		if got.upgradingKeys["*"] {
			t.Error("upgradingKeys[*] should not be set when no updates available")
		}
	})
}

func TestFlow_UC14_SyncKey(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('S'))
	if got.loading {
		t.Error("loading should stay false before sync-all confirmation")
	}
	if got.listConfirm.action != listConfirmSyncAll {
		t.Fatalf("listConfirm.action = %q, want sync-all", got.listConfirm.action)
	}
	if got.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want empty; sync-all confirmation belongs in footer hints", got.statusMsg)
	}
	got = drive(got, pressRune('S'))
	if !got.loading {
		t.Error("loading should be true after confirmed S")
	}
	if got.progressCh == nil {
		t.Error("progressCh should be non-nil after confirmed S")
	}
}

func TestFlow_UC14_LowercaseSyncNoopOnTools(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	got := drive(m, pressRune('s'))
	if got.loading {
		t.Error("loading should stay false after lowercase s on tools")
	}
	if got.progressCh != nil {
		t.Error("progressCh should stay nil after lowercase s on tools")
	}
}

func TestFlow_UC15_RefreshKey(t *testing.T) {
	t.Parallel()
	m := baseModel(oneInstalled())
	m.app = newScanPlanTestApp(t, &scanPlanProvider{name: "brew"})
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('R'))
	if len(got.scanningProviders) == 0 {
		t.Error("scanningProviders should be populated after R")
	}
	if !got.loading {
		t.Error("loading should be enabled immediately after R")
	}
	if got.progressText == "" {
		t.Error("progressText should be visible immediately after R")
	}
}

func TestFlow_UC16_GroupPickerOpen(t *testing.T) {
	t.Parallel()
	t.Run("g on tool opens group membership picker", func(t *testing.T) {
		got := drive(baseModel(threeTools()), pressRune('g'))
		if got.mode != viewGroupMembership {
			t.Errorf("mode = %v, want viewGroupMembership", got.mode)
		}
	})

	t.Run("g on empty list is no-op", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune('g'))
		if got.mode == viewGroupMembership {
			t.Error("group picker should not open on empty list")
		}
	})

	t.Run("Esc from group membership picker returns to list", func(t *testing.T) {
		got := drive(baseModel(threeTools()), pressRune('g'), pressEsc())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
		if got.pickerGroups != nil {
			t.Error("pickerGroups should be nil after esc")
		}
	})
}

func TestFlow_UC17_ClaimOrphan(t *testing.T) {
	t.Parallel()

	orphan := &app.ToolView{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
	m := baseModel(nil)
	m.discoveredTools = []*app.ToolView{orphan}
	m.rebuildDiscoveredKeys()
	m.applyFilter() // merges orphan into visibleTools
	m.upgradingKeys = make(map[string]bool)

	got := drive(m, pressRune('c'))
	if got.mode != viewGroupPicker {
		t.Errorf("mode = %v, want viewGroupPicker after c on orphan", got.mode)
	}
	if !got.pickerPurposeClaim {
		t.Error("pickerPurposeClaim should be true when claiming orphan")
	}
}

func TestFlow_UC18_IgnoreKey(t *testing.T) {
	t.Parallel()
	t.Run("x on tool opens ignore scope picker", func(t *testing.T) {
		m := baseModel(oneInstalled())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('x'))
		if got.mode != viewIgnoreScope {
			t.Errorf("mode = %v, want viewIgnoreScope", got.mode)
		}
	})

	t.Run("e on ignored tool opens edit picker", func(t *testing.T) {
		m := baseModel(oneInstalled())
		m.ignoreSet = map[string]bool{m.visibleTools[0].Name: true}
		m.applyFilter()
		got := drive(m, pressRune('e'))
		if got.mode != viewIgnoreScope {
			t.Errorf("mode = %v, want viewIgnoreScope", got.mode)
		}
	})

	t.Run("x on empty list is no-op", func(t *testing.T) {
		m := baseModel(nil)
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('x'))
		if got.loading {
			t.Error("loading should stay false on empty list")
		}
	})
}

func TestFlow_UC19_PinMigrateProvider(t *testing.T) {
	t.Parallel()
	m := wrongProvModel()

	t.Run("p opens provider scope picker", func(t *testing.T) {
		got := drive(m, pressRune('p'))
		if got.mode != viewProviderScope {
			t.Errorf("mode = %v, want viewProviderScope", got.mode)
		}
	})

	t.Run("i arms reinstall confirmation", func(t *testing.T) {
		got := drive(m, tea.KeyPressMsg{Code: 'i', Text: "i"})
		if got.loading {
			t.Error("loading should stay false before reinstall confirmation")
		}
		if got.listConfirm.action != listConfirmReinstallDefault {
			t.Fatalf("listConfirm.action = %q, want reinstall-default", got.listConfirm.action)
		}
	})

	t.Run("r still arms reinstall confirmation", func(t *testing.T) {
		got := drive(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
		if got.listConfirm.action != listConfirmReinstallDefault {
			t.Fatalf("listConfirm.action = %q, want reinstall-default", got.listConfirm.action)
		}
	})

	t.Run("second i confirms reinstall with default (sets loading + migrating)", func(t *testing.T) {
		got := drive(m, tea.KeyPressMsg{Code: 'i', Text: "i"}, tea.KeyPressMsg{Code: 'i', Text: "i"})
		if !got.loading {
			t.Error("loading should be true after confirmed i")
		}
		if !got.migrating {
			t.Error("migrating should be true after confirmed i — regression: was never set")
		}
	})

	t.Run("o is no-op on non-syncWrongProv tool", func(t *testing.T) {
		m2 := baseModel(oneInstalled())
		m2.upgradingKeys = make(map[string]bool)
		got := drive(m2, pressRune('o'))
		if got.loading {
			t.Error("loading should stay false when tool is not syncWrongProv")
		}
	})
}

func TestFlow_UC20_HelpOverlay(t *testing.T) {
	t.Parallel()
	t.Run("? opens help overlay", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune('?'))
		if !got.help.ShowAll {
			t.Error("help.ShowAll should be true after ?")
		}
	})

	t.Run("? again closes help overlay", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune('?'), pressRune('?'))
		if got.help.ShowAll {
			t.Error("help.ShowAll should be false after second ?")
		}
	})

	t.Run("Esc closes help overlay", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune('?'), pressEsc())
		if got.help.ShowAll {
			t.Error("help.ShowAll should be false after esc")
		}
	})
}

func TestFlow_UC21_ConfirmQuit(t *testing.T) {
	t.Parallel()
	t.Run("first q sets confirmQuit for footer prompt", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune('q'))
		if !got.confirmQuit {
			t.Error("confirmQuit should be true after first q")
		}
		if got.statusMsg != "" {
			t.Errorf("statusMsg = %q, want empty; quit prompt belongs in footer", got.statusMsg)
		}
		if got.quitConfirmKey != "q" {
			t.Errorf("quitConfirmKey = %q, want q", got.quitConfirmKey)
		}
	})

	t.Run("other key resets confirmQuit", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune('q'), pressRune('j'))
		if got.confirmQuit {
			t.Error("confirmQuit should reset after non-quit key")
		}
		if got.statusMsg != "" {
			t.Error("statusMsg should be cleared")
		}
	})
}

func TestFlow_UC22_KeysIgnoredWhileLoading(t *testing.T) {
	t.Parallel()
	m := Model{
		keys:    DefaultKeyMap(),
		spinner: spinner.New(),
		filter:  textinput.New(),
		loading: true,
	}
	got := drive(m, pressRune('j'), pressRune('j'), pressEnter(), pressRune('/'), pressRune(':'))
	if got.cursor != 0 {
		t.Errorf("cursor should not change while loading, got %d", got.cursor)
	}
	if got.mode != viewStatus {
		t.Errorf("mode should not change while loading, got %v", got.mode)
	}
}

func TestFlow_UC23_CommandPalette(t *testing.T) {
	t.Parallel()
	t.Run(": opens command palette", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune(':'))
		if got.mode != viewCommand {
			t.Errorf("mode = %v, want viewCommand", got.mode)
		}
	})

	t.Run("Esc closes command palette", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune(':'), pressEsc())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList after esc from palette", got.mode)
		}
	})

	t.Run("Enter with unknown input sets error status", func(t *testing.T) {
		got := drive(baseModel(nil), pressRune(':'), pressRune('z'), pressRune('z'), pressEnter())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList after unknown command", got.mode)
		}
	})
}

func TestFlow_UC24_TabCycle(t *testing.T) {
	t.Run("dashboard → list", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		got := drive(m, pressTab())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
	})

	t.Run("list → agents", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab())
		if got.mode != viewSkills {
			t.Errorf("mode = %v, want viewSkills", got.mode)
		}
	})

	t.Run("list → dots loads from app config", func(t *testing.T) {
		m, _ := newDotsModelForCmds(t)
		m.settings = config.Settings{}
		got := drive(m, pressTab())
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots", got.mode)
		}
		if !got.dotsLoading {
			t.Fatal("dotsLoading should start from app DotsConfigured despite stale local settings")
		}
	})

	t.Run("list → dots skips stale local repo when app is unconfigured", func(t *testing.T) {
		prov := &okProvider{name: "brew"}
		a, _ := newCmdApp(t, prov, nil)
		m := modelForCmds(a)
		m.settings = config.Settings{DotsRepo: "/stale/dotfiles"}
		got := drive(m, pressTab())
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots", got.mode)
		}
		if got.dotsLoading {
			t.Fatal("dotsLoading should not start from stale local settings when app has no dots repo")
		}
	})

	t.Run("dots → groups", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab())
		if got.mode != viewGroups {
			t.Errorf("mode = %v, want viewGroups", got.mode)
		}
	})

	t.Run("groups → settings", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab(), pressTab())
		if got.mode != viewSettings {
			t.Errorf("mode = %v, want viewSettings", got.mode)
		}
	})

	t.Run("settings → dashboard", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab(), pressTab(), pressTab())
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", got.mode)
		}
	})

	t.Run("dashboard → list", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab(), pressTab(), pressTab(), pressTab())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList (full cycle)", got.mode)
		}
	})

	t.Run("Esc from settings returns to list", func(t *testing.T) {
		got := drive(baseModel(nil), append(toSettings(), pressEsc())...)
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList after esc from settings", got.mode)
		}
	})

	t.Run("shift tab cycles backward", func(t *testing.T) {
		got := drive(baseModel(nil), pressShiftTab())
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus after shift+tab from list", got.mode)
		}
		got = drive(got, pressShiftTab())
		if got.mode != viewSettings {
			t.Errorf("mode = %v, want viewSettings after second shift+tab", got.mode)
		}
	})
}

func TestFlow_UC24_ClickTabs(t *testing.T) {
	t.Parallel()
	clickTab := func(m Model, target viewMode) Model {
		for _, zone := range mainTabHitZones(m) {
			if zone.mode == target {
				return drive(m, tea.MouseClickMsg{X: (zone.start + zone.end) / 2, Y: 0, Button: tea.MouseLeft})
			}
		}
		t.Fatalf("missing tab hit zone for mode %v", target)
		return m
	}

	t.Run("click dots tab switches to dots", func(t *testing.T) {
		got := clickTab(baseModel(nil), viewDots)
		if got.mode != viewDots {
			t.Errorf("mode = %v, want viewDots", got.mode)
		}
	})

	t.Run("click status tab switches to status", func(t *testing.T) {
		got := clickTab(baseModel(nil), viewStatus)
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", got.mode)
		}
	})

	t.Run("click settings tab switches to settings", func(t *testing.T) {
		got := clickTab(baseModel(nil), viewSettings)
		if got.mode != viewSettings {
			t.Errorf("mode = %v, want viewSettings", got.mode)
		}
	})

	t.Run("click tabs ignored while modal is open", func(t *testing.T) {
		m := baseModel(nil)
		m.showFilePicker = true
		got := clickTab(m, viewDots)
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList while file picker captures clicks", got.mode)
		}
	})
}

func TestFlow_UC24_ClickToolFilters(t *testing.T) {
	t.Parallel()
	clickFilter := func(m Model, kind toolFilterKind, idx int) Model {
		for _, zone := range toolFilterHitZones(m) {
			if zone.kind == kind && zone.index == idx {
				return drive(m, tea.MouseClickMsg{X: (zone.start + zone.end) / 2, Y: zone.y, Button: tea.MouseLeft})
			}
		}
		t.Fatalf("missing filter hit zone for kind %v index %d", kind, idx)
		return m
	}

	t.Run("click provider pill filters tools", func(t *testing.T) {
		m := baseModel(threeTools())
		m.applyFilter()
		got := clickFilter(m, toolFilterProvider, 1)
		if got.providerTabIdx != 1 {
			t.Fatalf("providerTabIdx = %d, want 1", got.providerTabIdx)
		}
		if got.cursor != 0 {
			t.Fatalf("cursor = %d, want reset to 0", got.cursor)
		}
	})

	t.Run("click group pill filters tools", func(t *testing.T) {
		m := baseModel(threeTools())
		m.groupNames = []string{"work"}
		m.toolGroups = make(map[string]string)
		m.toolGroups[toolKey("git", "brew")] = "work"
		m.applyFilter()
		got := clickFilter(m, toolFilterGroup, 2)
		if got.groupTabIdx != 2 || got.groupFilter != "work" {
			t.Fatalf("group filter = %q/%d, want work/2", got.groupFilter, got.groupTabIdx)
		}
	})
}

func TestDotsRepoSetupFromDotsTabKeepsDotsContext(t *testing.T) {
	t.Parallel()
	prov := &okProvider{name: "brew"}
	a, _ := newCmdApp(t, prov, nil)
	m := baseModel(nil)
	m.app = a
	m.ctx = context.Background()
	m.mode = viewDots
	cacheDotsAvailability(&m, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo})

	got := drive(m, pressEnter())
	if got.mode != viewSetup {
		t.Fatalf("mode = %v, want viewSetup for setup popup", got.mode)
	}
	if got.setupBackgroundMode != viewDots {
		t.Fatalf("setupBackgroundMode = %v, want viewDots", got.setupBackgroundMode)
	}
	if got.setupStep != 5 {
		t.Fatalf("setupStep = %d, want 5", got.setupStep)
	}
}

func TestDotsEmptyStateEnterDoesNotStartOnboarding(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewDots
	setDotsRepoForTest(&m, "/tmp/dots")
	m.dotsLoaded = true

	got := drive(m, pressEnter())
	if got.mode != viewDots {
		t.Fatalf("mode = %v, want viewDots", got.mode)
	}
	if got.loading || got.dotsLoading {
		t.Fatalf("loading=%v dotsLoading=%v, want no onboarding or sync", got.loading, got.dotsLoading)
	}
}

func TestFlow_UC25_TabBlockedWhenHostRequired(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostRequired = true
	got := drive(m, pressTab())
	if got.mode != viewGroups {
		t.Errorf("mode = %v, want viewGroups (tab blocked)", got.mode)
	}
}

func TestFlow_UC26_SetupStep0(t *testing.T) {
	t.Parallel()
	setupModel := func() Model {
		return Model{
			keys:    DefaultKeyMap(),
			spinner: spinner.New(),
			filter:  textinput.New(),
			mode:    viewSetup,
		}
	}

	t.Run("enter opens settings import picker", func(t *testing.T) {
		got := drive(setupModel(), pressEnter())
		if !got.showFilePicker {
			t.Error("showFilePicker should be true after enter in step 0")
		}
	})

	t.Run("y shortcut opens settings import picker", func(t *testing.T) {
		got := drive(setupModel(), pressRune('y'))
		if !got.showFilePicker {
			t.Error("showFilePicker should be true after y in step 0")
		}
	})

	t.Run("Y (uppercase) also opens settings import picker", func(t *testing.T) {
		got := drive(setupModel(), tea.KeyPressMsg{Code: 'Y'})
		if !got.showFilePicker {
			t.Error("showFilePicker should be true after Y in step 0")
		}
	})

	t.Run("n starts config creation", func(t *testing.T) {
		got := drive(setupModel(), pressRune('n'))
		if !got.loading {
			t.Error("loading should be true after n in step 0")
		}
		if got.setupStep != setupStepCreateConfig {
			t.Fatalf("setupStep = %d, want create config step", got.setupStep)
		}
	})

	t.Run("other keys ignored in step 0", func(t *testing.T) {
		got := drive(setupModel(), pressRune('j'), pressRune('/'))
		if got.loading {
			t.Error("loading should not change from unrelated keys in step 0")
		}
	})
}

func TestFlow_UC27_SetupStep1(t *testing.T) {
	t.Parallel()
	t.Run("space toggles first provider off", func(t *testing.T) {
		got := drive(setupStep1Model(), pressRune(' '))
		if got.setupProviders[0].Enabled {
			t.Error("first provider should be toggled off after space")
		}
		if !got.setupProviders[1].Enabled {
			t.Error("second provider should remain enabled")
		}
	})

	t.Run("j moves setupProviderIdx down", func(t *testing.T) {
		got := drive(setupStep1Model(), pressRune('j'))
		if got.setupProviderIdx != 1 {
			t.Errorf("setupProviderIdx = %d, want 1", got.setupProviderIdx)
		}
	})

	t.Run("k at top clamps at 0", func(t *testing.T) {
		got := drive(setupStep1Model(), pressRune('k'))
		if got.setupProviderIdx != 0 {
			t.Errorf("setupProviderIdx = %d, want 0", got.setupProviderIdx)
		}
	})

	t.Run("Enter submits (loading=true)", func(t *testing.T) {
		got := drive(setupStep1Model(), pressEnter())
		if !got.loading {
			t.Error("loading should be true after Enter in step 1")
		}
	})
}

// groupNames is non-empty so startSetupGroupSelection lands on step 9 instead of falling through to finishSetupWithReload.
func setupAgentsStepModel() Model {
	return Model{
		keys:                DefaultKeyMap(),
		spinner:             spinner.New(),
		filter:              textinput.New(),
		commandInput:        textinput.New(),
		settingsInput:       textinput.New(),
		mode:                viewSetup,
		agentsEnabled:       true,
		groupNames:          []string{"work"},
		setupBackgroundMode: viewSettings,
	}
}

func TestSetupAgentsStep_SkipsWhenAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.agentsEnabled = false
	var cmds []tea.Cmd
	m.startSetupAgentsStep(&cmds)

	if m.setupStep == 4 {
		t.Fatal("setupStep should not be 4 when agentsEnabled is false")
	}
	if m.setupStep != 9 {
		t.Errorf("setupStep = %d, want 9 (group selection)", m.setupStep)
	}
}

func TestSetupAgentsStep_SkipsWhenNoAgentsDetected(t *testing.T) {
	// testguard isolates HOME package-wide, but pin an empty temp dir here too so this test's "no agents installed" branch stays deterministic.
	t.Setenv("HOME", t.TempDir())
	m := setupAgentsStepModel()
	var cmds []tea.Cmd
	m.startSetupAgentsStep(&cmds)

	if m.setupStep == 4 {
		t.Fatal("setupStep should not be 4 when no agents are detected on the machine")
	}
}

func TestSetupAgentsStep_PopulatesStateWhenNotSkipped(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.setupAgentsList = []app.AgentInfo{
		{ID: "claude-code", Display: "Claude Code"},
		{ID: "codex", Display: "Codex"},
	}
	m.setupAgentsDiffLoaded = true
	m.setupAgentsUnmanagedSkills = 3
	m.setupAgentsUnmanagedMcp = 2
	m.setupAgentsUnmanagedPlugins = 1

	if m.setupStep != 4 {
		t.Fatalf("setupStep = %d, want 4", m.setupStep)
	}
	if len(m.setupAgentsList) != 2 {
		t.Fatalf("setupAgentsList = %d entries, want 2", len(m.setupAgentsList))
	}
	if !m.setupAgentsDiffLoaded {
		t.Error("setupAgentsDiffLoaded should be true")
	}
	if m.setupAgentsUnmanagedSkills != 3 || m.setupAgentsUnmanagedMcp != 2 || m.setupAgentsUnmanagedPlugins != 1 {
		t.Errorf("unmanaged counts = %d/%d/%d, want 3/2/1",
			m.setupAgentsUnmanagedSkills, m.setupAgentsUnmanagedMcp, m.setupAgentsUnmanagedPlugins)
	}
}

func TestSetupAgentsStep_PressIWhileLoadedArmsImport(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.setupAgentsDiffLoaded = true
	m.setupAgentsDiffLoading = false

	got := drive(m, pressRune('i'))
	if !got.loading {
		t.Error("loading should be true after pressing i on a loaded step 4")
	}
	if got.setupStep != 4 {
		t.Errorf("setupStep = %d, want 4 (still on agents step until import completes)", got.setupStep)
	}
}

func TestSetupAgentsStep_PressIWhileDiffLoadingIsNoOp(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.setupAgentsDiffLoading = true

	got := drive(m, pressRune('i'))
	if got.loading {
		t.Error("loading should stay false while setupAgentsDiffLoading is true")
	}
	if got.setupStep != 4 {
		t.Errorf("setupStep = %d, want 4", got.setupStep)
	}
}

func TestSetupAgentsStep_PressSAdvancesWithoutImport(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.setupAgentsDiffLoaded = true

	got := drive(m, pressRune('s'))
	if got.setupStep != 9 {
		t.Errorf("setupStep = %d, want 9", got.setupStep)
	}
	if got.loading {
		t.Error("loading should stay false; skip does not dispatch a mutation")
	}
}

func TestHandleSetupAgentsDiffMsg_Success(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.setupAgentsDiffLoading = true

	m.handleSetupAgentsDiffMsg(setupAgentsDiffMsg{
		unmanagedSkills:  5,
		unmanagedMcp:     4,
		unmanagedPlugins: 2,
	})

	if m.setupAgentsDiffLoading {
		t.Error("setupAgentsDiffLoading should be false after success")
	}
	if !m.setupAgentsDiffLoaded {
		t.Error("setupAgentsDiffLoaded should be true after success")
	}
	if m.setupAgentsUnmanagedSkills != 5 || m.setupAgentsUnmanagedMcp != 4 || m.setupAgentsUnmanagedPlugins != 2 {
		t.Errorf("unmanaged counts = %d/%d/%d, want 5/4/2",
			m.setupAgentsUnmanagedSkills, m.setupAgentsUnmanagedMcp, m.setupAgentsUnmanagedPlugins)
	}
}

func TestHandleSetupAgentsImportDoneMsg_SuccessAdvances(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.loading = true

	m.handleSetupAgentsImportDoneMsg(setupAgentsImportDoneMsg{skills: 2, mcp: 1, plugins: 0})

	if m.setupStep != 9 {
		t.Errorf("setupStep = %d, want 9 after successful import", m.setupStep)
	}
	if m.loading {
		t.Error("loading should be false after import completes")
	}
}

func TestHandleSetupAgentsImportDoneMsg_ErrorDoesNotAdvance(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.loading = true

	m.handleSetupAgentsImportDoneMsg(setupAgentsImportDoneMsg{err: errors.New("adopt failed")})

	if m.setupStep != 4 {
		t.Errorf("setupStep = %d, want 4 (unchanged on error)", m.setupStep)
	}
	if m.loading {
		t.Error("loading should be false after error")
	}
}

func TestSetupAgentsStep_BackgroundModePreservedAcrossSkip(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.setupAgentsDiffLoaded = true

	got := drive(m, pressRune('s'))
	if got.setupBackgroundMode != viewSettings {
		t.Errorf("setupBackgroundMode = %v, want viewSettings (unchanged)", got.setupBackgroundMode)
	}
}

func TestSetupAgentsStep_BackgroundModePreservedAcrossImportDone(t *testing.T) {
	t.Parallel()
	m := setupAgentsStepModel()
	m.setupStep = 4
	m.loading = true

	m.handleSetupAgentsImportDoneMsg(setupAgentsImportDoneMsg{skills: 1, mcp: 0, plugins: 0})

	if m.setupBackgroundMode != viewSettings {
		t.Errorf("setupBackgroundMode = %v, want viewSettings (unchanged)", m.setupBackgroundMode)
	}
}

func TestFlow_UC29_ToolsLoadedMsg(t *testing.T) {
	t.Parallel()
	loadingModel := func() Model {
		return Model{
			keys:          DefaultKeyMap(),
			spinner:       spinner.New(),
			filter:        textinput.New(),
			loading:       true,
			upgradingKeys: make(map[string]bool),
		}
	}

	t.Run("noConfig sets viewSetup", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{noConfig: true})
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", got.mode)
		}
		if got.loading {
			t.Error("loading should be false")
		}
	})

	t.Run("error clears loading and sets err", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{err: errors.New("db unavailable")})
		if got.loading {
			t.Error("loading should be false on error")
		}
		if got.err == nil {
			t.Error("err should be set")
		}
	})

	t.Run("noHost sets viewSetup step 2", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{noHost: true})
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", got.mode)
		}
		if got.setupStep != 2 {
			t.Errorf("setupStep = %d, want 2", got.setupStep)
		}
	})

	t.Run("noHost with existing hosts starts copy prompt", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{
			noHost:   true,
			hostInfo: &app.HostInfo{Hosts: map[string]config.HostAssignment{"workstation": {}}},
		})
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", got.mode)
		}
		if got.setupStep != 7 {
			t.Errorf("setupStep = %d, want 7", got.setupStep)
		}
	})

	t.Run("success from fresh config creation advances to step 1", func(t *testing.T) {
		m := loadingModel()
		m.mode = viewSetup
		m.setupStep = setupStepCreateConfig
		got := drive(m, toolsLoadedMsg{tools: threeTools()})
		if got.setupStep != 1 {
			t.Errorf("setupStep = %d, want 1", got.setupStep)
		}
	})

	t.Run("configured host first launch stays on dashboard", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{
			tools: threeTools(),
			hostInfo: &app.HostInfo{Active: "testhost", Hosts: map[string]config.HostAssignment{
				"testhost": {},
			}},
		})
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", got.mode)
		}
		if got.setupStep != 0 {
			t.Errorf("setupStep = %d, want 0", got.setupStep)
		}
	})

	t.Run("success populates allTools and sets viewStatus", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{tools: threeTools()})
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", got.mode)
		}
		if len(got.allTools) != 3 {
			t.Errorf("allTools = %d, want 3", len(got.allTools))
		}
	})
}

func TestFlow_UC30_SettingsNavigation(t *testing.T) {
	t.Parallel()
	t.Run("j moves settingsCursor down", func(t *testing.T) {
		msgs := append(toSettings(), pressRune('j'))
		got := drive(baseModel(nil), msgs...)
		if got.settingsCursor != 1 {
			t.Errorf("settingsCursor = %d, want 1", got.settingsCursor)
		}
	})

	t.Run("k moves settingsCursor up", func(t *testing.T) {
		msgs := append(toSettings(), pressRune('j'), pressRune('k'))
		got := drive(baseModel(nil), msgs...)
		if got.settingsCursor != 0 {
			t.Errorf("settingsCursor = %d, want 0", got.settingsCursor)
		}
	})

	t.Run("cursor wraps to top from last row", func(t *testing.T) {
		msgs := append(toSettings(), nj(numSettingRows)...)
		got := drive(baseModel(nil), msgs...)
		if got.settingsCursor != 0 {
			t.Errorf("settingsCursor = %d, want 0 (wrapped)", got.settingsCursor)
		}
	})
}

func TestFlow_UC31_SettingsToggleAutoImport(t *testing.T) {
	t.Parallel()
	t.Run("space toggles AutoImport on", func(t *testing.T) {
		msgs := append(toSettings(), pressRune(' '))
		got := drive(baseModel(nil), msgs...)
		if !got.settings.AutoImport {
			t.Error("AutoImport should be true after toggle")
		}
	})

	t.Run("double toggle returns to false", func(t *testing.T) {
		msgs := append(toSettings(), pressRune(' '), pressRune(' '))
		got := drive(baseModel(nil), msgs...)
		if got.settings.AutoImport {
			t.Error("AutoImport should be false after two toggles")
		}
	})
}

func TestFlow_UC32_SettingsOpenFilePicker(t *testing.T) {
	t.Parallel()
	msgs := append(toSettings(), nj(settingsRowDotsRepo)...)
	msgs = append(msgs, pressEnter())
	got := drive(baseModel(nil), msgs...)
	if !got.showFilePicker {
		t.Errorf("showFilePicker should be true after enter on dots repo row (row %d)", settingsRowDotsRepo)
	}
}

func TestFlow_UC33_FilePickerEscCloses(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	setDotsRepoForTest(&m, "~/dotfiles")
	msgs := append(toSettings(), nj(settingsRowDotsRepo)...)
	msgs = append(msgs, pressEnter(), pressEsc())
	got := drive(m, msgs...)
	if got.showFilePicker {
		t.Error("showFilePicker should be false after esc")
	}
	if got.settings.DotsRepo != "~/dotfiles" {
		t.Errorf("DotsRepo changed to %q, want unchanged", got.settings.DotsRepo)
	}
}

func TestFlow_UC34_DangerZoneConfirm(t *testing.T) {
	t.Run("reset settings Enter sets dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetSettings)...)
		msgs = append(msgs, pressEnter())
		got := drive(baseModel(nil), msgs...)
		if got.dangerConfirmRow != settingsRowResetSettings {
			t.Errorf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowResetSettings)
		}
	})

	t.Run("Esc cancels dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetSettings)...)
		msgs = append(msgs, pressEnter(), pressEsc())
		got := drive(baseModel(nil), msgs...)
		if got.dangerConfirmRow != -1 {
			t.Errorf("dangerConfirmRow = %d after esc, want -1", got.dangerConfirmRow)
		}
	})

	t.Run("reset cache Enter sets dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowResetCache)...)
		msgs = append(msgs, pressEnter())
		got := drive(baseModel(nil), msgs...)
		if got.dangerConfirmRow != settingsRowResetCache {
			t.Errorf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowResetCache)
		}
	})

	t.Run("bootstrap Enter sets dangerConfirmRow", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowBootstrap)...)
		msgs = append(msgs, pressEnter())
		got := drive(baseModel(nil), msgs...)
		if got.dangerConfirmRow != settingsRowBootstrap {
			t.Errorf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowBootstrap)
		}
	})

	t.Run("bootstrap confirm enters activation setup", func(t *testing.T) {
		base := baseModel(nil)
		base.hostInfo = &app.HostInfo{Active: "testhost", Hosts: map[string]config.HostAssignment{"testhost": {}}}
		msgs := append(toSettings(), nj(settingsRowBootstrap)...)
		msgs = append(msgs, pressEnter(), pressEnter())
		got := drive(base, msgs...)
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", got.mode)
		}
		if got.setupStep != 10 {
			t.Errorf("setupStep = %d, want 10", got.setupStep)
		}
		if got.setupBackgroundMode != viewSettings {
			t.Errorf("setupBackgroundMode = %v, want viewSettings", got.setupBackgroundMode)
		}
	})

	t.Run("sync row with no DotsRepo sets statusMsg", func(t *testing.T) {
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressEnter())
		got := drive(baseModel(nil), msgs...)
		if got.statusMsg == "" {
			t.Error("expected statusMsg when DotsRepo not configured")
		}
	})

	t.Run("sync row with DotsRepo asks keep-local choice", func(t *testing.T) {
		got := openSettingsDotsSyncChoice(t)
		if got.dangerConfirmRow != settingsRowDotsSync {
			t.Errorf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowDotsSync)
		}
	})

	t.Run("sync row uses app enabled despite stale local disabled setting", func(t *testing.T) {
		base, repoDir := newDotsModelForCmds(t)
		if err := base.app.SaveDotsDisabled(context.Background(), false); err != nil {
			t.Fatalf("SaveDotsDisabled: %v", err)
		}
		base.settings = config.Settings{
			DotsRepo:     repoDir,
			DotsDisabled: config.BoolPtr(true),
		}
		availability, err := base.app.DotsSyncAvailability()
		if err != nil {
			t.Fatalf("DotsSyncAvailability: %v", err)
		}
		if availability.Reason != app.DotsSyncAvailabilityReady {
			t.Fatalf("app availability = %+v, want ready", availability)
		}
		base.mode = viewSettings
		base.settingsCursor = settingsRowDotsSync
		base.dangerConfirmRow = -1
		base.stowInstalled = true
		got := drive(base, pressEnter())
		if got.dotsLoading {
			t.Fatalf("settings dots sync row should not enable dots when app config is already enabled: status=%q danger=%d", got.statusMsg, got.dangerConfirmRow)
		}
		if got.dangerConfirmRow != settingsRowDotsSync {
			t.Fatalf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowDotsSync)
		}
	})

	t.Run("sync row uses app disabled despite stale local enabled setting", func(t *testing.T) {
		base, repoDir := newDotsModelForCmds(t)
		if err := base.app.SaveDotsDisabled(context.Background(), true); err != nil {
			t.Fatalf("SaveDotsDisabled: %v", err)
		}
		base.settings = config.Settings{DotsRepo: repoDir}
		cacheDotsAvailability(&base, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: repoDir})
		base.mode = viewSettings
		base.settingsCursor = settingsRowDotsSync
		base.dangerConfirmRow = -1
		base.stowInstalled = true
		got := drive(base, pressEnter())
		if got.dangerConfirmRow == settingsRowDotsSync {
			t.Fatal("settings dots sync row should enable dots instead of asking to disable when app config is disabled")
		}
		if !got.dotsLoading || !strings.Contains(got.progressText, "Enabling dots") {
			t.Fatalf("dotsLoading=%v progress=%q, want enabling dots operation", got.dotsLoading, got.progressText)
		}
	})
}

func TestFlow_UC35_DangerDotsDisableKeepLocalChoice(t *testing.T) {
	openChoice := func() Model {
		return openSettingsDotsSyncChoice(t)
	}

	t.Run("y confirms disable and keeps local files", func(t *testing.T) {
		got := drive(openChoice(), pressRune('y'))
		if !got.loading {
			t.Error("loading should be true after y")
		}
	})

	t.Run("n confirms disable and removes local files", func(t *testing.T) {
		got := drive(openChoice(), pressRune('n'))
		if !got.loading {
			t.Error("loading should be true after n")
		}
	})

	t.Run("Enter does not confirm choice", func(t *testing.T) {
		got := drive(openChoice(), pressEnter())
		if got.loading {
			t.Error("enter should not confirm keep-local choice")
		}
		if got.dangerConfirmRow != settingsRowDotsSync {
			t.Errorf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowDotsSync)
		}
	})

	t.Run("Esc cancels keep-local choice", func(t *testing.T) {
		got := drive(openChoice(), pressEsc())
		if got.dangerConfirmRow != -1 {
			t.Errorf("dangerConfirmRow = %d, want -1 after esc", got.dangerConfirmRow)
		}
	})
}

func TestFlow_UC36_PriorityEditor(t *testing.T) {
	t.Parallel()

	toPriority := func() []tea.Msg {
		return append(toSettings(), nj(settingsRowProviderPriority)...)
	}

	t.Run("Enter on priority row opens editor", func(t *testing.T) {
		msgs := append(toPriority(), pressEnter())
		got := drive(baseModel(nil), msgs...)
		if !got.editingPriority {
			t.Error("editingPriority should be true after enter on priority row")
		}
	})

	t.Run("j/k navigate priorityCursor", func(t *testing.T) {
		msgs := append(toPriority(), pressEnter(), pressRune('j'))
		got := drive(baseModel(nil), msgs...)
		if got.priorityCursor != 1 {
			t.Errorf("priorityCursor = %d, want 1 after j", got.priorityCursor)
		}

		msgs = append(msgs, pressRune('k'))
		got = drive(baseModel(nil), msgs...)
		if got.priorityCursor != 0 {
			t.Errorf("priorityCursor = %d, want 0 after j+k", got.priorityCursor)
		}
	})

	t.Run("grab+j+drop carries item down", func(t *testing.T) {
		msgs := append(toPriority(), pressEnter(), pressRune(' '), pressRune('j'), pressRune(' '))
		got := drive(baseModel(nil), msgs...)
		// Default draft is [brew, apt, apk, ...]; grab+j+drop carries brew↓ → [apt, brew, apk, ...].
		if len(got.priorityDraft) < 2 || got.priorityDraft[0] != "apt" || got.priorityDraft[1] != "brew" {
			t.Errorf("priorityDraft = %v after grab+j+drop, want [apt brew ...]", got.priorityDraft)
		}
	})

	t.Run("Esc discards changes", func(t *testing.T) {
		base := baseModel(nil)
		base.settings = tuiSettingsWithPriority("brew", "apt", "apk")
		msgs := append(toPriority(), pressEnter(), pressRune('J'), pressEsc())
		got := drive(base, msgs...)
		if got.editingPriority {
			t.Error("editingPriority should be false after esc")
		}
		if priority := got.settings.ProviderPriority; len(priority) == 0 || priority[0] != "brew" {
			t.Errorf("system priority = %v after esc, want original order", priority)
		}
	})

	t.Run("Enter saves reordered priority", func(t *testing.T) {
		msgs := append(toPriority(), pressEnter(), pressRune(' '), pressRune('j'), pressRune(' '), pressEnter())
		got := drive(baseModel(nil), msgs...)
		if got.editingPriority {
			t.Error("editingPriority should be false after enter")
		}
		// After grab+j+drop at cursor=0, draft is [apt, brew, apk, ...]; Confirm persists that order.
		if got := got.settings.ProviderPriority; len(got) == 0 || got[0] != "apt" {
			t.Errorf("provider_priority = %v after grab+j+drop+enter, want apt first", got)
		}
	})
}

func TestFlow_UC37_GroupsNavigation(t *testing.T) {
	t.Parallel()
	toHosts := func() []tea.Msg { return []tea.Msg{pressTab(), pressTab(), pressTab()} }

	t.Run("Esc from hosts returns to list", func(t *testing.T) {
		got := drive(baseModel(nil), append(toHosts(), pressEsc())...)
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
	})

	t.Run("j navigates hostCursor down", func(t *testing.T) {
		m := baseModel(nil)
		m.hostInfo = &app.HostInfo{
			Hosts: map[string]config.HostAssignment{
				"alpha": {},
				"beta":  {},
			},
		}
		// First j after tab switch reveals cursor; second j navigates.
		msgs := append(toHosts(), pressRune('j'), pressRune('j'))
		got := drive(m, msgs...)
		if got.hostCursor < 1 && got.assignmentSection < 1 {
			t.Error("j in hosts tab should move cursor or section")
		}
	})
}

func TestFlow_UC38_NewHostFromHostsTab(t *testing.T) {
	t.Parallel()
	toHosts := func() []tea.Msg { return []tea.Msg{pressTab(), pressTab(), pressTab()} }

	t.Run("n still opens group creation", func(t *testing.T) {
		got := drive(baseModel(nil), append(toHosts(), pressRune('n'))...)
		if !got.groupCreating {
			t.Error("groupCreating should be true after n")
		}
		if got.assignmentSection != 1 || got.settingsInput.Placeholder != "group name…" {
			t.Fatalf("group creation state = section %d placeholder %q", got.assignmentSection, got.settingsInput.Placeholder)
		}
	})

	t.Run("p opens host creation", func(t *testing.T) {
		got := drive(baseModel(nil), append(toHosts(), pressRune('p'))...)
		if !got.groupCreating {
			t.Error("groupCreating should be true after p")
		}
		if got.assignmentSection != 0 || got.settingsInput.Placeholder != "hostname…" {
			t.Fatalf("host creation state = section %d placeholder %q", got.assignmentSection, got.settingsInput.Placeholder)
		}
	})
}

func TestFlow_UC39_HostDeleteConfirm(t *testing.T) {
	t.Parallel()
	toHosts := func() []tea.Msg { return []tea.Msg{pressTab(), pressTab(), pressTab()} }

	m := baseModel(nil)
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{"work": {}},
	}

	t.Run("d on a host arms delete confirm", func(t *testing.T) {
		got := drive(m, append(toHosts(), pressRune('d'))...)
		if !got.hostDeleteConfirm {
			t.Error("hostDeleteConfirm should be true after d")
		}
	})

	t.Run("Esc cancels host delete confirm", func(t *testing.T) {
		got := drive(m, append(toHosts(), pressRune('d'), pressEsc())...)
		if got.hostDeleteConfirm {
			t.Error("hostDeleteConfirm should be false after esc")
		}
	})
}

func TestFlow_UC40_GroupPickerNav(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"base", "work", "+ new group…"}
	m.pickerCursor = 0
	m.pickerPurposeClaim = true
	m.upgradingKeys = make(map[string]bool)

	t.Run("j moves pickerCursor", func(t *testing.T) {
		got := drive(m, pressRune('j'))
		if got.pickerCursor != 1 {
			t.Errorf("pickerCursor = %d, want 1", got.pickerCursor)
		}
	})

	t.Run("k moves pickerCursor up", func(t *testing.T) {
		m2 := m
		m2.pickerCursor = 1
		got := drive(m2, pressRune('k'))
		if got.pickerCursor != 0 {
			t.Errorf("pickerCursor = %d, want 0", got.pickerCursor)
		}
	})

	t.Run("Enter on real group sets loading", func(t *testing.T) {
		got := drive(m, pressEnter())
		if !got.loading {
			t.Error("loading should be true after selecting a real group")
		}
	})

	t.Run("Esc returns to list", func(t *testing.T) {
		got := drive(m, pressEsc())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList after esc from picker", got.mode)
		}
	})
}

func TestFlow_UC41_GroupPickerSentinel(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 0
	m.upgradingKeys = make(map[string]bool)

	t.Run("Enter on sentinel opens text input", func(t *testing.T) {
		got := drive(m, pressRune('j'), pressEnter())
		if !got.pickerCreatingGroup {
			t.Error("pickerCreatingGroup should be true after selecting sentinel")
		}
	})

	t.Run("Esc from text input closes picker entirely", func(t *testing.T) {
		got := drive(m, pressRune('j'), pressEnter(), pressEsc())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList after esc from sentinel input", got.mode)
		}
		if got.pickerCreatingGroup {
			t.Error("pickerCreatingGroup should be false after esc")
		}
	})
}

func TestFlow_UC42_DotsNavigation(t *testing.T) {
	t.Parallel()
	m := dotsModel()

	t.Run("j moves dotsCursor down", func(t *testing.T) {
		got := drive(m, pressRune('j'))
		if got.dotsCursor != 1 {
			t.Errorf("dotsCursor = %d, want 1", got.dotsCursor)
		}
	})

	t.Run("k moves dotsCursor up", func(t *testing.T) {
		m2 := dotsModel()
		m2.dotsCursor = 2
		got := drive(m2, pressRune('k'))
		if got.dotsCursor != 1 {
			t.Errorf("dotsCursor = %d, want 1", got.dotsCursor)
		}
	})

	t.Run("cursor wraps to bottom from top", func(t *testing.T) {
		got := drive(m, pressRune('k'))
		if got.dotsCursor != 2 {
			t.Errorf("dotsCursor = %d, want 2 (wrapped to bottom)", got.dotsCursor)
		}
	})

	t.Run("cursor wraps to top from bottom", func(t *testing.T) {
		got := drive(m, pressRune('j'), pressRune('j'), pressRune('j'))
		if got.dotsCursor != 0 {
			t.Errorf("dotsCursor = %d, want 0 (wrapped to top)", got.dotsCursor)
		}
	})
}

func TestFlow_UC43_DotsDeleteConfirm(t *testing.T) {
	t.Parallel()
	t.Run("d arms confirm (dotsConfirmIdx=cursor)", func(t *testing.T) {
		got := drive(dotsModel(), pressRune('d'))
		if got.dotsConfirmIdx != 0 {
			t.Errorf("dotsConfirmIdx = %d, want 0", got.dotsConfirmIdx)
		}
	})

	t.Run("Esc cancels confirm", func(t *testing.T) {
		got := drive(dotsModel(), pressRune('d'), pressEsc())
		if got.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after esc", got.dotsConfirmIdx)
		}
	})

	t.Run("navigation clears confirm", func(t *testing.T) {
		got := drive(dotsModel(), pressRune('d'), pressRune('j'))
		if got.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after navigation", got.dotsConfirmIdx)
		}
	})

	t.Run("d on empty list is no-op", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		setDotsRepoForTest(&m, "/repo")
		got := drive(m, pressRune('d'))
		if got.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 on empty list", got.dotsConfirmIdx)
		}
	})

	t.Run("y on armed entry keeps local and starts delete", func(t *testing.T) {
		got := drive(dotsModel(), pressRune('d'), pressRune('y'))
		if !got.dotsLoading {
			t.Error("dotsLoading should be true after y confirms delete")
		}
	})

	t.Run("n on armed entry removes local and starts delete", func(t *testing.T) {
		got := drive(dotsModel(), pressRune('d'), pressRune('n'))
		if !got.dotsLoading {
			t.Error("dotsLoading should be true after n confirms delete")
		}
	})
}

func TestFlow_UC44_DotsSyncKey(t *testing.T) {
	t.Parallel()
	m := dotsModel()
	m.dotsCursor = 1
	got := drive(m, pressRune('s'))
	if !got.dotsLoading {
		t.Error("dotsLoading should be true after s on syncable dots row")
	}
}

func TestFlow_DotsDisabledBlocksMutatingKeys(t *testing.T) {
	appBackedDotsModel := func(t *testing.T) Model {
		t.Helper()
		appModel, repoDir := newDotsModelForCmds(t)
		m := dotsModel()
		m.app = appModel.app
		m.ctx = appModel.ctx
		m.settings = config.Settings{DotsRepo: repoDir}
		cacheDotsAvailability(&m, app.DotsSyncAvailability{Configured: true, Reason: app.DotsSyncAvailabilityReady, RepoPath: repoDir})
		m.dotMemberships = map[string][]string{"gitconfig": {"default"}}
		return m
	}
	disabledModel := func(t *testing.T) Model {
		t.Helper()
		m := appBackedDotsModel(t)
		if err := m.app.SaveDotsDisabled(context.Background(), true); err != nil {
			t.Fatalf("SaveDotsDisabled: %v", err)
		}
		cacheDotsAvailability(&m, app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityDisabled, RepoPath: m.settings.DotsRepo})
		return m
	}

	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{name: "add", msg: pressRune('a')},
		{name: "sync row", msg: pressRune('s')},
		{name: "sync all", msg: pressRune('S')},
		{name: "discover", msg: pressRune('D')},
		{name: "group", msg: pressRune('g')},
		{name: "use repo", msg: pressRune('u')},
		{name: "use local", msg: pressRune('l')},
		{name: "variant", msg: pressRune('v')},
		{name: "delete", msg: pressRune('d')},
		{name: "ignore", msg: pressRune('x')},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := drive(disabledModel(t), tc.msg)
			if got.mode != viewDots {
				t.Fatalf("mode = %v, want viewDots", got.mode)
			}
			if got.dotsLoading || got.loading {
				t.Fatalf("loading started for disabled dots key %q", tc.name)
			}
			if got.filePickerForDotAdd {
				t.Fatalf("file picker opened for disabled dots key %q", tc.name)
			}
			if got.dotsConfirmIdx != -1 || got.dotsOverwriteIdx != -1 || got.dotsLocalIdx != -1 || got.dotsIgnoreIdx != -1 || got.dotsVariantIdx != -1 {
				t.Fatalf("confirmation armed for disabled dots key %q", tc.name)
			}
		})
	}

	t.Run("enabled app ignores stale local disabled setting", func(t *testing.T) {
		m := appBackedDotsModel(t)
		m.settings.DotsDisabled = config.BoolPtr(true)
		m.dotsCursor = 1
		got := drive(m, pressRune('s'))
		if !got.dotsLoading {
			t.Fatal("syncable dots row should start when app config enables dots despite stale local disabled setting")
		}
	})
}

func TestRender_DotsDisabledHidesMutatingHints(t *testing.T) {
	t.Parallel()
	m := dotsModel()
	setDotsDisabledForTest(&m, "/repo/dotfiles", true)
	m.dotMemberships = map[string][]string{"gitconfig": {"default"}}

	footer := tabShortHelpBindings(&m)
	for _, binding := range footer {
		key := binding.Help().Key
		if key == "a" || key == "D" || key == "S" {
			t.Fatalf("disabled dots footer includes mutating binding %q", key)
		}
	}
	rowHints := renderContextHints(m, hintCtxDotsConflict, "")
	for _, blocked := range []string{"use repo", "use local", "variant", "delete", "ignore", "edit groups"} {
		if strings.Contains(rowHints, blocked) {
			t.Fatalf("disabled dots row hints include %q: %q", blocked, rowHints)
		}
	}
	help := renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
	for _, blocked := range []string{"discover", "sync all", "variant", "delete", "ignore", "use repo", "use local"} {
		if strings.Contains(help, blocked) {
			t.Fatalf("disabled dots help includes %q: %q", blocked, help)
		}
	}
	if !strings.Contains(help, "enable dots") {
		t.Fatalf("disabled dots help should keep enable path: %q", help)
	}
}

func TestRender_DotsHintsUseCachedDotsAvailability(t *testing.T) {
	t.Parallel()
	m := dotsModel()
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.settings.DotsDisabled = config.BoolPtr(true)
	m.dotMemberships = map[string][]string{"gitconfig": {"default"}}

	rowHints := dotsConflictHintItems(m)
	if !slices.ContainsFunc(rowHints, func(h hintItem) bool { return h.desc == "use repo" }) {
		t.Fatalf("enabled app should show use-repo hint despite stale disabled setting: %#v", rowHints)
	}

	m = dotsModel()
	m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
	m.dotsSyncAvailCached = app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo}

	help := helpActionGroups(m)
	if len(help) != 1 || len(help[0].items) != 1 || help[0].items[0].desc != "set up dots" {
		t.Fatalf("unconfigured app should show setup-only dots help despite stale repo: %#v", help)
	}
}

func TestFlow_UC45_DotsConflictOverwrite(t *testing.T) {
	t.Parallel()
	conflictModel := func() Model {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		setDotsRepoForTest(&m, "/repo")
		m.dotsEntries = []app.DotStatus{
			{Name: "gitconfig", Health: app.HealthConflict, State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
		}
		return m
	}

	t.Run("u on conflict arms use-repo confirm", func(t *testing.T) {
		got := drive(conflictModel(), pressRune('u'))
		if got.dotsOverwriteIdx != 0 {
			t.Errorf("dotsOverwriteIdx = %d, want 0", got.dotsOverwriteIdx)
		}
	})

	t.Run("l on conflict arms use-local confirm", func(t *testing.T) {
		got := drive(conflictModel(), pressRune('l'))
		if got.dotsLocalIdx != 0 {
			t.Errorf("dotsLocalIdx = %d, want 0", got.dotsLocalIdx)
		}
	})

	t.Run("u on conflict with missing actions still arms use-repo confirm", func(t *testing.T) {
		m := conflictModel()
		m.dotsEntries[0].Actions = nil
		got := drive(m, pressRune('u'))
		if got.dotsOverwriteIdx != 0 {
			t.Errorf("dotsOverwriteIdx = %d, want 0", got.dotsOverwriteIdx)
		}
	})

	t.Run("Esc cancels conflict choice confirm", func(t *testing.T) {
		got := drive(conflictModel(), pressRune('u'), pressEsc())
		if got.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 after esc", got.dotsOverwriteIdx)
		}
	})

	t.Run("second u on same entry resolves with repo (dotsLoading=true)", func(t *testing.T) {
		got := drive(conflictModel(), pressRune('u'), pressRune('u'))
		if !got.dotsLoading {
			t.Error("dotsLoading should be true after second u")
		}
	})

	t.Run("transient conflict exposes resolution instead of sync", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		setDotsRepoForTest(&m, "/repo")
		m.dotsEntries = []app.DotStatus{
			{Name: "claude", State: dots.StateUntrackedConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionIgnore}},
		}

		if got := drive(m, pressRune('s')); got.dotsLoading {
			t.Error("s should not start sync for a known transient conflict")
		}
		got := drive(m, pressRune('u'))
		if got.dotsOverwriteIdx != 0 {
			t.Errorf("dotsOverwriteIdx = %d, want 0", got.dotsOverwriteIdx)
		}
		got = drive(m, pressRune('u'), pressRune('u'))
		if !got.dotsLoading {
			t.Error("second u should resolve transient conflict")
		}
	})

	t.Run("u on non-conflict entry is no-op", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		setDotsRepoForTest(&m, "/repo")
		m.dotsEntries = []app.DotStatus{{Name: "nvim", Health: app.HealthOK, State: dots.StateSynced}}
		got := drive(m, pressRune('u'))
		if got.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 for non-conflict entry", got.dotsOverwriteIdx)
		}
	})
}

func TestFlow_DotsSynthesizedIgnoredChildUnignore(t *testing.T) {
	t.Parallel(
	// Merged ignored-child entries with Children expand instead of toggling the whole entry; individual children can then be unignored.
	)

	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		ConfigPath: "~/.config/nvim",
		Health:     app.HealthOK,
		State:      dots.StateIgnored,
		IsDir:      true,
		Children: []app.DotChild{{
			Name:    "lua",
			RelPath: "lua",
			Path:    "~/.config/nvim/lua",
			IsDir:   true,
			Depth:   1,
			Ignored: true,
			Children: []app.DotChild{{
				Name:    "plugin.lua",
				RelPath: "lua/plugin.lua",
				Path:    "~/.config/nvim/lua/plugin.lua",
				Depth:   2,
				Ignored: true,
			}},
		}},
	}}

	// First x = confirmation, second x = expands tree instead of dispatching
	got := drive(m, pressRune('x'), pressRune('x'))
	if got.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want %q after pressing x on merged ignored entry", got.dotsExpandedName, "nvim")
	}
}

func TestFlow_DotsMergedIgnoredExpandCollapse(t *testing.T) {
	t.Parallel(
	// Merged ignored entries expand/collapse with space like synced entries.
	)

	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{
		{
			Name:   "nvim",
			State:  dots.StateSynced,
			Health: app.HealthOK,
			Counts: app.DotFileCounts{Synced: 2},
		},
		{
			Name:       "nvim",
			TargetPath: "~/.config/nvim",
			State:      dots.StateIgnored,
			Health:     app.HealthOK,
			IsDir:      true,
			Children: []app.DotChild{
				{Name: "node_modules", RelPath: "node_modules", Path: "~/.config/nvim/node_modules", IsDir: true, Depth: 1, Ignored: true, Children: []app.DotChild{
					{Name: "pkg", RelPath: "node_modules/pkg", Path: "~/.config/nvim/node_modules/pkg", IsDir: true, Depth: 2, Ignored: true},
				}},
				{Name: "auth.json", RelPath: "auth.json", Path: "~/.config/nvim/auth.json", Depth: 1, Ignored: true},
			},
		},
	}

	toIgnored := drive(m, pressRune('j'))

	expanded := drive(toIgnored, pressRune(' '))
	if expanded.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want nvim after space on merged ignored entry", expanded.dotsExpandedName)
	}
	rows := dotsVisibleRows(expanded)
	if len(rows) != 4 { // synced nvim + ignored nvim + node_modules + auth.json
		t.Fatalf("visible rows = %d, want 4 (synced + ignored parent + 2 children)", len(rows))
	}

	collapsed := drive(expanded, pressRune(' '))
	if collapsed.dotsExpandedName != "" {
		t.Fatalf("dotsExpandedName = %q, want empty after collapsing", collapsed.dotsExpandedName)
	}
	if rows := dotsVisibleRows(collapsed); len(rows) != 2 {
		t.Fatalf("visible rows after collapse = %d, want 2", len(rows))
	}
}

func TestFlow_DotsExpandIgnoredDoesNotExpandSyncedSameName(t *testing.T) {
	t.Parallel(
	// Expanding an ignored entry must not also expand a synced entry with the same name; dotsExpandedState scopes expansion to the section.
	)

	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")

	syncedEntry := app.DotStatus{
		Name:   "nvim",
		State:  dots.StateSynced,
		Health: app.HealthOK,
		Counts: app.DotFileCounts{Synced: 2},
	}
	ignoredEntry := app.DotStatus{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateIgnored,
		Health:     app.HealthOK,
		IsDir:      true,
		Children: []app.DotChild{
			{Name: "node_modules", RelPath: "node_modules", Path: "~/.config/nvim/node_modules", IsDir: true, Depth: 1, Ignored: true},
			{Name: "auth.json", RelPath: "auth.json", Path: "~/.config/nvim/auth.json", Depth: 1, Ignored: true},
		},
	}
	m.dotsEntries = []app.DotStatus{syncedEntry, ignoredEntry}

	m = drive(m, pressRune('j'))

	m = drive(m, pressRune(' '))

	if m.dotsExpandedState != dots.StateIgnored {
		t.Fatalf("dotsExpandedState = %v, want dots.StateIgnored", m.dotsExpandedState)
	}

	// 2. Visible rows: synced nvim (collapsed) + ignored nvim + 2 children = 4.
	rows := dotsVisibleRows(m)
	if len(rows) != 4 {
		t.Fatalf("visible rows = %d, want 4 (synced collapsed + ignored parent + 2 children)", len(rows))
	}

	// 3. The synced nvim entry must NOT match the expanded state.
	if dotsEntryMatchesExpanded(m, syncedEntry) {
		t.Fatal("synced nvim incorrectly treated as expanded — dual-expand bug is present")
	}
}

func TestFlow_DotsMergedIgnoredNestedExpand(t *testing.T) {
	t.Parallel(
	// Expanding a child directory inside a merged ignored entry works.
	)

	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateIgnored,
		Health:     app.HealthOK,
		IsDir:      true,
		Children: []app.DotChild{
			{Name: "lua", RelPath: "lua", Path: "~/.config/nvim/lua", IsDir: true, Depth: 1, Ignored: true, Children: []app.DotChild{
				{Name: "config.lua", RelPath: "lua/config.lua", Path: "~/.config/nvim/lua/config.lua", Depth: 2, Ignored: true},
			}},
			{Name: "init.vim", RelPath: "init.vim", Path: "~/.config/nvim/init.vim", Depth: 1, Ignored: true},
		},
	}}

	expanded := drive(m, pressRune(' '))
	if expanded.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want nvim", expanded.dotsExpandedName)
	}
	rows := dotsVisibleRows(expanded)
	if len(rows) != 3 { // nvim + lua + init.vim
		t.Fatalf("visible rows = %d, want 3", len(rows))
	}

	down := drive(expanded, pressRune('j'))
	nestedExpanded := drive(down, pressRune(' '))
	rows = dotsVisibleRows(nestedExpanded)
	if len(rows) != 4 { // nvim + lua + config.lua + init.vim
		t.Fatalf("visible rows after nested expand = %d, want 4", len(rows))
	}
	if !nestedExpanded.dotsExpandedChildren[dotsChildExpandKey("nvim", "lua")] {
		t.Fatal("lua child not marked expanded in dotsExpandedChildren")
	}
}

func TestFlow_DotsMergedIgnoredChildUnignoreDispatch(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	group := tuiTestHostGroup()
	group.Dots = []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim", Ignore: []string{"*", "!/init.lua"}}}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups:   []*config.GroupConfig{group},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
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
	setDotsRepoForTest(&m, repoDir)
	m.dotsExpandedName = "nvim"
	m.dotsExpandedState = dots.StateIgnored
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateIgnored,
		Health:     app.HealthOK,
		IsDir:      true,
		Children: []app.DotChild{
			{Name: "node_modules", RelPath: "node_modules", Path: "~/.config/nvim/node_modules", IsDir: true, Depth: 1, Ignored: true},
		},
	}}

	got := drive(m, pressRune('j'), pressRune('x'))
	if got.dotsIgnoreIdx != 1 {
		t.Fatalf("dotsIgnoreIdx = %d, want 1 (child row confirmation)", got.dotsIgnoreIdx)
	}

	next, cmd := got.Update(pressRune('x'))
	got = next.(Model)
	if !got.dotsLoading {
		t.Fatal("dotsLoading = false, want include operation started")
	}
	msg := runLastBatchCommand(t, cmd)
	ignored, ok := msg.(dotsIgnoredMsg)
	if !ok {
		t.Fatalf("second x returned %T, want dotsIgnoredMsg", msg)
	}
	if ignored.err != nil {
		t.Fatalf("dotsIgnoredMsg err = %v", ignored.err)
	}
	if ignored.ignored {
		t.Fatal("dotsIgnoredMsg ignored = true, want include")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	gotIgnore := cfg.Groups[0].Dots[0].Ignore
	if !slices.Contains(gotIgnore, "!/node_modules") {
		t.Fatalf("ignore = %v, want !/node_modules include override", gotIgnore)
	}
}

func TestFlow_DotsChildRowsCanBeIgnored(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsExpandedName = "nvim"
	m.dotsExpandedState = dots.StateSynced
	m.dotsEntries = []app.DotStatus{{
		Name:    "nvim",
		Health:  app.HealthOK,
		State:   dots.StateSynced,
		Actions: []dots.Action{dots.ActionRemove, dots.ActionIgnore},
		Children: []app.DotChild{{
			Name:    "auth.json",
			RelPath: "hosts/work/auth.json",
			Path:    "~/.config/nvim/hosts/work/auth.json",
		}},
	}}

	got := drive(m, pressRune('j'), pressRune('x'))
	if got.dotsIgnoreIdx != 1 {
		t.Fatalf("dotsIgnoreIdx = %d, want child row index 1 after ignore confirmation", got.dotsIgnoreIdx)
	}
}

func TestFlow_DotsExpansionUsesSpaceAndNavigationDoesNotAutoExpand(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{
		{
			Name:   "alpha",
			Health: app.HealthOK,
			State:  dots.StateSynced,
			Children: []app.DotChild{
				{Name: "one", RelPath: "one", Path: "~/.config/alpha/one"},
				{Name: "two", RelPath: "two", Path: "~/.config/alpha/two"},
			},
		},
		{
			Name:   "beta",
			Health: app.HealthOK,
			State:  dots.StateSynced,
			Children: []app.DotChild{
				{Name: "child", RelPath: "child", Path: "~/.config/beta/child"},
			},
		},
	}

	initial := dotsVisibleRows(m)
	if len(initial) != 2 {
		t.Fatalf("visible rows before expand = %d, want only parent rows", len(initial))
	}

	expanded := drive(m, pressRune(' '))
	if expanded.dotsExpandedName != "alpha" {
		t.Fatalf("dotsExpandedName = %q, want alpha after space", expanded.dotsExpandedName)
	}
	if rows := dotsVisibleRows(expanded); len(rows) != 4 {
		t.Fatalf("visible rows after expand = %d, want alpha parent + 2 children + beta", len(rows))
	}

	inside := drive(expanded, pressRune('j'), pressRune('j'))
	if inside.dotsExpandedName != "alpha" {
		t.Fatalf("dotsExpandedName = %q, want alpha while cursor is inside its children", inside.dotsExpandedName)
	}

	got := drive(inside, pressRune('j'))
	visible := dotsVisibleRows(got)
	if got.dotsCursor >= len(visible) {
		t.Fatalf("cursor %d out of %d visible rows", got.dotsCursor, len(visible))
	}
	row := visible[got.dotsCursor]
	if row.entry.Name != "beta" || row.isChild {
		t.Fatalf("selected row = %+v, want beta parent without auto-expanding it", row)
	}
	if got.dotsExpandedName != "" {
		t.Fatalf("dotsExpandedName = %q, want collapsed after leaving alpha subtree", got.dotsExpandedName)
	}
}

func TestFlow_DotsOutOfSyncDirectoryCanExpand(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{{
		Name:    "nvim",
		State:   dots.StateConflict,
		Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove, dots.ActionIgnore},
		Children: []app.DotChild{{
			Name:    "init.lua",
			RelPath: "init.lua",
			Path:    "~/.config/nvim/init.lua",
			State:   dots.StateSynced,
		}},
	}}

	got := drive(m, pressRune(' '))
	if got.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want nvim", got.dotsExpandedName)
	}
	if rows := dotsVisibleRows(got); len(rows) != 2 || !rows[1].isChild {
		t.Fatalf("visible rows after expand = %#v, want parent plus child", rows)
	}
}

func TestFlow_DotsSubdirectoryCanExpand(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      dots.StateConflict,
		IsDir:      true,
		Children: []app.DotChild{{
			Name:      "lua",
			RelPath:   "lua",
			Path:      "~/.config/nvim/lua",
			State:     dots.StateConflict,
			IsDir:     true,
			FileCount: 1,
			Children: []app.DotChild{{
				Name:    "config.lua",
				RelPath: "lua/config.lua",
				Path:    "~/.config/nvim/lua/config.lua",
				State:   dots.StateMissing,
			}},
		}},
	}}

	got := drive(m, pressRune(' '), pressRune('j'), pressRune(' '))
	if got.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want parent to stay expanded", got.dotsExpandedName)
	}
	if !got.dotsExpandedChildren[dotsChildExpandKey("nvim", "lua")] {
		t.Fatalf("lua subdirectory should be expanded: %#v", got.dotsExpandedChildren)
	}
	rows := dotsVisibleRows(got)
	if len(rows) != 3 || !rows[2].isChild || rows[2].child.RelPath != "lua/config.lua" {
		t.Fatalf("visible rows after child expand = %#v, want nested config.lua", rows)
	}

	parentCollapsed := drive(got, pressRune('k'), pressRune(' '))
	if parentCollapsed.dotsExpandedName != "" {
		t.Fatalf("dotsExpandedName = %q, want parent collapsed", parentCollapsed.dotsExpandedName)
	}
	if len(parentCollapsed.dotsExpandedChildren) != 0 {
		t.Fatalf("collapsing parent should also collapse children: %#v", parentCollapsed.dotsExpandedChildren)
	}
	parentReexpanded := drive(parentCollapsed, pressRune(' '))
	if rows := dotsVisibleRows(parentReexpanded); len(rows) != 2 {
		t.Fatalf("visible rows after parent re-expand = %#v, want nested child still collapsed", rows)
	}

	got = drive(parentReexpanded, pressRune('j'), pressRune(' '))
	if !got.dotsExpandedChildren[dotsChildExpandKey("nvim", "lua")] {
		t.Fatalf("lua subdirectory should expand again after parent re-expand: %#v", got.dotsExpandedChildren)
	}

	got = drive(got, pressRune(' '))
	if got.dotsExpandedChildren[dotsChildExpandKey("nvim", "lua")] {
		t.Fatalf("lua subdirectory should collapse on second space: %#v", got.dotsExpandedChildren)
	}
	if rows := dotsVisibleRows(got); len(rows) != 2 {
		t.Fatalf("visible rows after child collapse = %#v, want parent plus direct child", rows)
	}
}

func TestFlow_UC46_OpCompleteMsg(t *testing.T) {
	t.Parallel()
	t.Run("success sets ✓ status and clears loading", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.rowOpKey = toolKey("ripgrep", "brew")
		m.rowOpStatus = "Installing ripgrep…"
		got := drive(m, opCompleteMsg{message: "installed ripgrep"})
		if got.loading {
			t.Error("loading should be false after opCompleteMsg")
		}
		if got.rowOpKey != "" || got.rowOpStatus != "" {
			t.Fatalf("row operation should clear after opCompleteMsg, got key=%q status=%q", got.rowOpKey, got.rowOpStatus)
		}
		if got.statusMsg != "✓ installed ripgrep" {
			t.Errorf("statusMsg = %q, want ✓ installed ripgrep", got.statusMsg)
		}
	})

	t.Run("error sets ✗ status", func(t *testing.T) {
		m := baseModel(nil)
		m.rowOpKey = toolKey("ripgrep", "brew")
		m.rowOpStatus = "Installing ripgrep…"
		got := drive(m, opCompleteMsg{err: errors.New("provider not found")})
		if got.statusMsg != "✗ provider not found" {
			t.Errorf("statusMsg = %q, want ✗ provider not found", got.statusMsg)
		}
		if got.rowOpKey != "" || got.rowOpStatus != "" {
			t.Fatalf("row operation should clear after failed opCompleteMsg, got key=%q status=%q", got.rowOpKey, got.rowOpStatus)
		}
		if got.rowErrors[toolKey("ripgrep", "brew")] != "provider not found" {
			t.Fatalf("rowErrors = %#v, want ripgrep row error", got.rowErrors)
		}
	})

	t.Run("error status shows the actual stderr problem", func(t *testing.T) {
		m := baseModel(nil)
		m.rowOpKey = toolKey("font-intel-one-mono", "brew")
		err := errors.New("brew install font-intel-one-mono: exit status 1 (stderr: Error: A font is already installed at /Library/Fonts/IntelOneMono.ttf)")
		got := drive(m, opCompleteMsg{err: err})
		want := "✗ A font is already installed at /Library/Fonts/IntelOneMono.ttf"
		if got.statusMsg != want {
			t.Fatalf("statusMsg = %q, want %q", got.statusMsg, want)
		}
	})

	t.Run("error status keeps multiline problem details", func(t *testing.T) {
		m := baseModel(nil)
		m.rowOpKey = toolKey("font-intel-one-mono", "brew")
		err := errors.New("brew install font: exit status 1 (stderr: Error: A font is already installed at:\n/Library/Fonts/IntelOneMono.ttf\nRemove it before reinstalling.)")
		got := drive(m, opCompleteMsg{err: err})
		want := "✗ A font is already installed at: /Library/Fonts/IntelOneMono.ttf Remove it before reinstalling."
		if got.statusMsg != want {
			t.Fatalf("statusMsg = %q, want %q", got.statusMsg, want)
		}
	})

	t.Run("with tools refreshes visibleTools", func(t *testing.T) {
		m := baseModel(nil)
		m.rowErrors = map[string]string{toolKey("ripgrep", "brew"): "provider not found"}
		got := drive(m, opCompleteMsg{message: "done", tools: threeTools()})
		if len(got.visibleTools) != 3 {
			t.Errorf("visibleTools = %d, want 3", len(got.visibleTools))
		}
		if len(got.rowErrors) != 0 {
			t.Fatalf("successful opCompleteMsg should clear row errors, got %#v", got.rowErrors)
		}
	})

	t.Run("removes key from upgradingKeys", func(t *testing.T) {
		m := baseModel(nil)
		m.upgradingKeys = map[string]bool{"ripgrep\x00brew": true}
		got := drive(m, opCompleteMsg{key: "ripgrep\x00brew", message: "upgraded"})
		if got.upgradingKeys["ripgrep\x00brew"] {
			t.Error("upgradingKeys entry should be removed after opCompleteMsg")
		}
	})
}

func TestFlow_UC47_ProgressDoneMsg(t *testing.T) {
	t.Parallel()
	t.Run("clears loading when not migrating", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, progressDoneMsg{message: "install complete"})
		if got.loading {
			t.Error("loading should be false after progressDoneMsg")
		}
		if got.statusMsg != "✓ install complete" {
			t.Errorf("statusMsg = %q, want ✓ install complete", got.statusMsg)
		}
	})

	t.Run("does not clear loading when migrating", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.migrating = true
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, progressDoneMsg{})
		if !got.loading {
			t.Error("progressDoneMsg must not clear loading while migrating — regression")
		}
	})

	t.Run("empty message does not show ✓ banner", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, progressDoneMsg{})
		if got.statusMsg != "" {
			t.Errorf("statusMsg = %q, want empty when no message", got.statusMsg)
		}
	})
}

func TestFlow_UC48_ProviderScanMsgs(t *testing.T) {
	t.Parallel()
	t.Run("providerScannedMsg removes entry from scanningProviders", func(t *testing.T) {
		m := baseModel(nil)
		m.scanningProviders = map[string]bool{"brew": true, "node": true}
		got := drive(m, providerScannedMsg{provider: "brew"})
		if got.scanningProviders["brew"] {
			t.Error("brew should be removed from scanningProviders")
		}
		if !got.scanningProviders["node"] {
			t.Error("node should still be in scanningProviders")
		}
	})

	t.Run("providerScannedMsg shows scan error after update check settles", func(t *testing.T) {
		m := baseModel(nil)
		m.scanningProviders = map[string]bool{"brew": true}
		scanErr := errors.New("db write failed")
		got := drive(m, providerScannedMsg{provider: "brew", err: scanErr})
		if got.statusMsg != "" {
			t.Fatalf("statusMsg = %q before update check settles, want deferred error", got.statusMsg)
		}
		got = drive(got,
			providerOutdatedCheckedMsg{provider: "brew"},
			outdatedProvidersDoneMsg{},
			allProvidersDoneMsg{},
			discoveredRefreshedMsg{gen: got.discoveryGen},
		)
		if !got.statusIsErr {
			t.Fatal("statusIsErr = false, want true")
		}
		want := app.ProviderScanFailureStatus("brew", scanErr)
		if got.statusMsg != want {
			t.Fatalf("statusMsg = %q, want %q", got.statusMsg, want)
		}
	})

	t.Run("last providerScannedMsg starts visible snapshot and background outdated checks", func(t *testing.T) {
		m := baseModel(nil)
		m.scanningProviders = map[string]bool{"brew": true}
		m.providerScanToolCounts = map[string]int{"brew": 2}
		got := drive(m, providerScannedMsg{provider: "brew"})
		if len(got.scanningProviders) != 0 {
			t.Fatalf("scanningProviders = %v, want empty", got.scanningProviders)
		}
		if !got.providerSnapshotRefreshing || !got.discoveryRefreshing {
			t.Fatalf("provider snapshot/discovery flags = %v/%v, want true/true", got.providerSnapshotRefreshing, got.discoveryRefreshing)
		}
		if !got.outdatedProviders["brew"] {
			t.Fatalf("outdatedProviders = %v, want brew", got.outdatedProviders)
		}
	})

	t.Run("allProvidersDoneMsg refreshes tools", func(t *testing.T) {
		m := baseModel(nil)
		m.rowErrors = map[string]string{toolKey("ripgrep", "brew"): "provider not found"}
		m.statusMsg = "Installing fd…"
		got := drive(m, allProvidersDoneMsg{tools: threeTools()})
		if len(got.allTools) != 3 {
			t.Errorf("allTools = %d, want 3", len(got.allTools))
		}
		if got.rowErrors[toolKey("ripgrep", "brew")] != "provider not found" {
			t.Fatalf("refresh should preserve row errors, got %#v", got.rowErrors)
		}
		if got.statusMsg != "Installing fd…" {
			t.Fatalf("refresh should preserve foreground status, got %q", got.statusMsg)
		}
	})

	t.Run("allProvidersDoneMsg does not clear status while migrating", func(t *testing.T) {
		m := baseModel(nil)
		m.migrating = true
		m.statusMsg = "Migrating typescript…"
		got := drive(m, allProvidersDoneMsg{tools: threeTools()})
		if got.statusMsg == "" {
			t.Error("statusMsg must not be cleared while migrating — regression")
		}
	})

	t.Run("allProvidersDoneMsg does not clear scan error", func(t *testing.T) {
		m := baseModel(nil)
		m.statusMsg = "scan failed for brew: db write failed"
		m.statusIsErr = true
		got := drive(m, allProvidersDoneMsg{tools: threeTools()})
		if got.statusMsg == "" {
			t.Error("statusMsg must not be cleared while scan error is visible")
		}
	})

	t.Run("outdated provider completion refreshes tools without blocking installed snapshot", func(t *testing.T) {
		m := baseModel(nil)
		m.outdatedProviders = map[string]bool{"brew": true}
		got := drive(m, providerOutdatedCheckedMsg{provider: "brew"})
		if len(got.outdatedProviders) != 0 {
			t.Fatalf("outdatedProviders = %v, want empty", got.outdatedProviders)
		}
		if !got.outdatedSnapshotRefreshing {
			t.Fatal("outdatedSnapshotRefreshing = false, want true")
		}
		got = drive(got, outdatedProvidersDoneMsg{tools: threeTools()})
		if got.outdatedSnapshotRefreshing {
			t.Fatal("outdatedSnapshotRefreshing = true, want false")
		}
		if len(got.allTools) != 3 {
			t.Fatalf("allTools = %d, want 3", len(got.allTools))
		}
	})
}

func TestFlow_UC49_GroupChangedMsg(t *testing.T) {
	t.Parallel()
	t.Run("success sets status and clears loading", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		got := drive(m, groupChangedMsg{detail: "✓ git added to work", tools: threeTools()})
		if got.loading {
			t.Error("loading should be false after groupChangedMsg")
		}
		if got.statusMsg != "✓ git added to work" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
	})

	t.Run("error sets ✗ status", func(t *testing.T) {
		got := drive(baseModel(nil), groupChangedMsg{err: errors.New("not found")})
		if got.statusMsg != "✗ not found" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
	})
}

func TestFlow_UC50_DangerOpDoneMsg(t *testing.T) {
	t.Parallel()
	t.Run("success shows detail in status", func(t *testing.T) {
		got := drive(baseModel(nil), dangerOpDoneMsg{action: "reset-settings", detail: "settings cleared"})
		if got.statusMsg != "✓ settings cleared" {
			t.Errorf("statusMsg = %q, want ✓ settings cleared", got.statusMsg)
		}
	})

	t.Run("error sets ✗ action in status", func(t *testing.T) {
		got := drive(baseModel(nil), dangerOpDoneMsg{action: "delete-host", err: errors.New("write failed")})
		if got.statusMsg != "✗ delete-host: write failed" {
			t.Errorf("statusMsg = %q", got.statusMsg)
		}
	})

	t.Run("reload=true sets loading", func(t *testing.T) {
		got := drive(baseModel(nil), dangerOpDoneMsg{action: "reset-cache", reload: true})
		if !got.loading {
			t.Error("loading should be true when dangerOpDoneMsg.reload=true")
		}
	})

	t.Run("disable-dots action clears dots state", func(t *testing.T) {
		m := dotsModel()
		m.dotsLoaded = true
		got := drive(m, dangerOpDoneMsg{action: "disable-dots"})
		if got.dotsLoaded {
			t.Error("dotsLoaded should be false after disable-dots")
		}
		if got.dotsEntries != nil {
			t.Error("dotsEntries should be nil after disable-dots")
		}
	})

	t.Run("enable-dots action switches to dots and reloads there", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		got := drive(m, dangerOpDoneMsg{action: "enable-dots", detail: "dots enabled", reload: true, mode: viewDots})
		if got.mode != viewDots {
			t.Fatalf("mode = %v, want viewDots after enable-dots", got.mode)
		}
		if got.setupBackgroundMode != viewDots {
			t.Fatalf("setupBackgroundMode = %v, want viewDots reload target", got.setupBackgroundMode)
		}
		if !got.loading {
			t.Fatal("loading should be true while reload starts")
		}
	})
}

func TestFlow_UC51_ClaimDoneMsg(t *testing.T) {
	t.Parallel()
	orphan := &app.ToolView{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
	m := baseModel(nil)
	m.discoveredTools = []*app.ToolView{orphan}
	m.rebuildDiscoveredKeys()
	m.applyFilter()
	m.loading = true

	t.Run("success removes tool from discoveredTools and shows status", func(t *testing.T) {
		got := drive(m, claimDoneMsg{name: "fzf", groupName: "work", tools: threeTools()})
		if got.loading {
			t.Error("loading should be false after claimDoneMsg")
		}
		if got.statusMsg != "✓ added fzf to config (work)" {
			t.Errorf("statusMsg = %q, want ✓ added fzf to config (work)", got.statusMsg)
		}
		if got.statusIsErr {
			t.Error("claim success status should be green")
		}
		for _, dt := range got.discoveredTools {
			if dt.Name == "fzf" {
				t.Error("fzf should be removed from discoveredTools after claim")
			}
		}
	})

	t.Run("error sets ✗ status", func(t *testing.T) {
		got := drive(m, claimDoneMsg{name: "fzf", err: errors.New("add failed")})
		if got.statusMsg != "✗ add failed" {
			t.Errorf("statusMsg = %q, want ✗ add failed", got.statusMsg)
		}
	})
}

func TestFlow_UC52_IgnoreDoneMsg(t *testing.T) {
	t.Parallel()
	t.Run("ignored=true adds to ignoreSet", func(t *testing.T) {
		m := baseModel(nil)
		m.ignoreSet = make(map[string]bool)
		got := drive(m, ignoreDoneMsg{name: "curl", ignored: true})
		if !got.ignoreSet["curl"] {
			t.Error("curl should be in ignoreSet after ignoreDoneMsg{ignored:true}")
		}
		if got.statusMsg != "✓ curl ignored" {
			t.Errorf("statusMsg = %q, want ✓ curl ignored", got.statusMsg)
		}
	})

	t.Run("ignored=false removes from ignoreSet", func(t *testing.T) {
		m := baseModel(nil)
		m.ignoreSet = map[string]bool{"curl": true}
		got := drive(m, ignoreDoneMsg{name: "curl", ignored: false})
		if got.ignoreSet["curl"] {
			t.Error("curl should be removed from ignoreSet after ignoreDoneMsg{ignored:false}")
		}
		if got.statusMsg != "✓ curl un-ignored" {
			t.Errorf("statusMsg = %q, want ✓ curl un-ignored", got.statusMsg)
		}
	})

	t.Run("tool-level ignore keeps row in ignored section", func(t *testing.T) {
		tool := oneInstalled()[0]
		m := baseModel(oneInstalled())
		got := drive(m, ignoreDoneMsg{
			name:          tool.Name,
			ignored:       true,
			tools:         []*app.ToolView{tool},
			ignoreLabels:  map[string]string{tool.Name: "tool"},
			toolIgnoreSet: map[string]bool{tool.Name: true},
		})
		if len(got.visibleTools) != 1 || got.visibleTools[0].Name != tool.Name {
			t.Fatalf("visibleTools = %#v, want ignored %q retained", got.visibleTools, tool.Name)
		}
		if got.displaySection(got.visibleTools[0]) != sectionIgnored {
			t.Fatalf("displaySection = %v, want sectionIgnored", got.displaySection(got.visibleTools[0]))
		}
		if got.sectionCounts[sectionIgnored] != 1 {
			t.Fatalf("sectionCounts[ignored] = %d, want 1", got.sectionCounts[sectionIgnored])
		}
		if got.cursor != 0 {
			t.Fatalf("cursor = %d, want 0 on ignored tool", got.cursor)
		}
	})
}

func TestFlow_SyncAllDoneRemovesClaimedDiscovered(t *testing.T) {
	t.Parallel()
	orphan := &app.ToolView{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
	m := baseModel(nil)
	m.loading = true
	m.discoveredTools = []*app.ToolView{orphan}
	m.rebuildDiscoveredKeys()
	m.applyFilter()

	got := drive(m, progressDoneMsg{
		message:      "sync complete — 0 installed, 1 added to config",
		tools:        threeTools(),
		claimedNames: []string{"fzf"},
	})
	if got.loading {
		t.Error("loading should be false after sync-all completion")
	}
	if got.discoveredKeys["brew\x00fzf"] {
		t.Error("claimed fzf should be removed from discovered keys")
	}
	if got.statusMsg != "✓ sync complete — 0 installed, 1 added to config" {
		t.Errorf("statusMsg = %q", got.statusMsg)
	}
}

func TestFlow_UC53_MigrateProviderDoneMsg(t *testing.T) {
	t.Parallel()
	t.Run("success clears loading+migrating and shows status", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.migrating = true
		m.rowOpKey = toolKey("typescript", "node")
		m.rowOpStatus = "Reinstalling typescript with default (node)…"
		got := drive(m, migrateProviderDoneMsg{name: "typescript", fromProvider: "npm", toProvider: "node", tools: threeTools()})
		if got.loading {
			t.Error("loading should be false after migrateProviderDoneMsg success")
		}
		if got.migrating {
			t.Error("migrating should be false after migrateProviderDoneMsg success")
		}
		if got.statusMsg != "✓ reinstalled typescript with default (node), removed npm" {
			t.Errorf("statusMsg = %q, want ✓ reinstalled typescript with default (node), removed npm", got.statusMsg)
		}
		if got.rowOpKey != "" || got.rowOpStatus != "" {
			t.Fatalf("row operation should clear after migrateProviderDoneMsg, got key=%q status=%q", got.rowOpKey, got.rowOpStatus)
		}
		if got.statusIsErr {
			t.Error("migrate success status should be green")
		}
	})

	t.Run("error clears loading+migrating and shows ✗", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.migrating = true
		got := drive(m, migrateProviderDoneMsg{name: "typescript", toProvider: "node", err: errors.New("migrate failed")})
		if got.loading {
			t.Error("loading should be false after migrateProviderDoneMsg error")
		}
		if got.migrating {
			t.Error("migrating should be false after migrateProviderDoneMsg error")
		}
		if got.statusMsg != "✗ migrate failed" {
			t.Errorf("statusMsg = %q, want ✗ migrate failed", got.statusMsg)
		}
		if got.rowErrors[toolKey("typescript", "node")] != "migrate failed" {
			t.Fatalf("rowErrors = %#v, want typescript row error", got.rowErrors)
		}
	})
}

func TestFlow_UC54_DotsMsgs(t *testing.T) {
	t.Parallel()
	t.Run("dotsLoadedMsg sets entries and marks loaded", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "nvim", Health: app.HealthOK}}
		got := drive(baseModel(nil), dotsLoadedMsg{entries: entries, gitStatus: "M nvim"})
		if !got.dotsLoaded {
			t.Error("dotsLoaded should be true")
		}
		if len(got.dotsEntries) != 1 {
			t.Errorf("dotsEntries = %d, want 1", len(got.dotsEntries))
		}
		if got.dotsGitStatus == "" {
			t.Error("dotsGitStatus should be set")
		}
	})

	t.Run("dotsLoadedMsg error sets statusMsg", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsLoading = true
		m.dotsEntries = []app.DotStatus{{Name: "existing", Health: app.HealthOK}}
		got := drive(m, dotsLoadedMsg{err: errors.New("no repo")})
		if got.statusMsg == "" {
			t.Error("statusMsg should be set on dotsLoadedMsg error")
		}
		if got.dotsLoading {
			t.Fatal("dotsLoading should be false after dotsLoadedMsg error")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "existing" {
			t.Fatalf("dotsLoadedMsg error should keep existing table, got %+v", got.dotsEntries)
		}
	})

	t.Run("dotsLoadedMsg error with partial entries still updates table", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "refreshed", Health: app.HealthConflict, State: dots.StateConflict}}
		got := drive(baseModel(nil), dotsLoadedMsg{entries: entries, err: errors.New("dots status: git failed")})
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "refreshed" {
			t.Fatalf("dotsLoadedMsg partial error should update table, got %+v", got.dotsEntries)
		}
		if !got.statusIsErr || !strings.Contains(got.statusMsg, "git failed") {
			t.Fatalf("status = err:%v msg:%q, want partial refresh error", got.statusIsErr, got.statusMsg)
		}
	})

	t.Run("dotsSyncedMsg sets entries and ✓ status", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "zsh", Health: app.HealthOK}}
		got := drive(baseModel(nil), dotsSyncedMsg{entries: entries})
		if got.statusMsg != "✓ dots synced" {
			t.Errorf("statusMsg = %q, want ✓ dots synced", got.statusMsg)
		}
		if !got.dotsLoaded {
			t.Fatal("dotsLoaded should be true after dotsSyncedMsg")
		}
	})

	t.Run("dotsSyncedMsg applies app returned settings", func(t *testing.T) {
		base := baseModel(nil)
		base.dotsLoading = true
		base.dotsOpGen = 1
		base.settings = config.Settings{DotsRepo: "/old"}

		got := drive(base, dotsSyncedMsg{
			gen:         1,
			settings:    config.Settings{DotsRepo: "/new"},
			hasSettings: true,
		})

		if got.settings.DotsRepo != "/new" {
			t.Fatalf("settings.DotsRepo = %q, want app returned settings", got.settings.DotsRepo)
		}
		if !got.dotsSyncAvailCached.Configured {
			t.Fatal("dots availability cache should be updated from app returned settings")
		}
	})

	t.Run("dotsSyncedMsg error still updates conflict entries", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "nvim", Health: app.HealthConflict, State: dots.StateConflict}}
		got := drive(baseModel(nil), dotsSyncedMsg{entries: entries, err: errors.New("dots sync: nvim: requires choosing use repo version or use local version")})
		if !got.dotsLoaded {
			t.Fatal("dotsLoaded should be true after failed sync refresh")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].State != dots.StateConflict {
			t.Fatalf("dotsEntries = %+v, want refreshed conflict entry", got.dotsEntries)
		}
		if !got.statusIsErr || !strings.Contains(got.statusMsg, "nvim: requires choosing") {
			t.Fatalf("status = err:%v msg:%q, want conflict error", got.statusIsErr, got.statusMsg)
		}
	})

	t.Run("dotsSyncedMsg error without refreshed entries keeps existing table", func(t *testing.T) {
		m := baseModel(nil)
		m.dotsLoading = true
		m.dotsEntries = []app.DotStatus{{Name: "existing", Health: app.HealthOK}}
		got := drive(m, dotsSyncedMsg{err: errors.New("dots status: failed")})
		if got.dotsLoading {
			t.Fatal("dotsLoading should be false after dotsSyncedMsg error")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].Name != "existing" {
			t.Fatalf("dotsSyncedMsg error should keep existing table, got %+v", got.dotsEntries)
		}
		if !got.statusIsErr || !strings.Contains(got.statusMsg, "dots status: failed") {
			t.Fatalf("status = err:%v msg:%q, want dots status error", got.statusIsErr, got.statusMsg)
		}
	})

	t.Run("dotsPulledMsg sets entries and ✓ status", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "zsh", Health: app.HealthOK}}
		got := drive(baseModel(nil), dotsPulledMsg{entries: entries})
		if got.statusMsg != "✓ pulled" {
			t.Errorf("statusMsg = %q, want ✓ pulled", got.statusMsg)
		}
	})

	t.Run("dotsPushedMsg sets ✓ status", func(t *testing.T) {
		got := drive(baseModel(nil), dotsPushedMsg{})
		if got.statusMsg != "✓ pushed" {
			t.Errorf("statusMsg = %q, want ✓ pushed", got.statusMsg)
		}
	})

	t.Run("dotsPushedMsg error sets ✗ status", func(t *testing.T) {
		got := drive(baseModel(nil), dotsPushedMsg{err: errors.New("push failed")})
		if got.statusMsg != "✗ push failed" {
			t.Errorf("statusMsg = %q, want ✗ push failed", got.statusMsg)
		}
	})
}

func TestFlow_UC55_DotsDeletedMsg(t *testing.T) {
	t.Parallel()
	t.Run("cursor clamped after remove", func(t *testing.T) {
		remaining := []app.DotStatus{{Name: "nvim", Health: app.HealthOK}}
		m := dotsModel()
		m.dotsCursor = 2
		got := drive(m, dotsDeletedMsg{name: "gitconfig", entries: remaining})
		if got.dotsCursor != 0 {
			t.Errorf("dotsCursor = %d, want 0 after cursor clamp", got.dotsCursor)
		}
		if got.dotsConfirmIdx != -1 {
			t.Errorf("dotsConfirmIdx = %d, want -1 after remove", got.dotsConfirmIdx)
		}
		if got.statusMsg != "✓ deleted gitconfig" {
			t.Errorf("statusMsg = %q, want ✓ deleted gitconfig", got.statusMsg)
		}
	})
}

func TestFlow_UC56_DotsFixedMsg(t *testing.T) {
	t.Parallel()
	t.Run("success clears overwriteIdx and sets status", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "gitconfig", Health: app.HealthOK}}
		m := dotsModel()
		m.dotsOverwriteIdx = 2
		got := drive(m, dotsFixedMsg{name: "gitconfig", entries: entries})
		if got.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1", got.dotsOverwriteIdx)
		}
		if got.statusMsg != "✓ resolved gitconfig" {
			t.Errorf("statusMsg = %q, want ✓ resolved gitconfig", got.statusMsg)
		}
	})

	t.Run("error sets ✗ status", func(t *testing.T) {
		got := drive(baseModel(nil), dotsFixedMsg{name: "nvim", err: errors.New("backup failed")})
		if got.statusMsg != "✗ backup failed" {
			t.Errorf("statusMsg = %q, want ✗ backup failed", got.statusMsg)
		}
	})
}

func TestFlow_UC57_MigrationRaceGuards(t *testing.T) {
	t.Parallel(
	// Regression: progressDoneMsg cleared loading mid-migration.
	)

	t.Run("progressDoneMsg does not clear loading while migrating", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		m.migrating = true
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, progressDoneMsg{})
		if !got.loading {
			t.Error("loading must remain true while migrating — regression test")
		}
	})

	// Regression: allProvidersDoneMsg wiped the "Migrating…" statusMsg.
	t.Run("allProvidersDoneMsg does not clear statusMsg while migrating", func(t *testing.T) {
		m := baseModel(nil)
		m.migrating = true
		m.statusMsg = "Migrating typescript…"
		got := drive(m, allProvidersDoneMsg{tools: threeTools()})
		if got.statusMsg == "" {
			t.Error("statusMsg must not be cleared while migrating — regression test")
		}
	})

	// Regression: confirmed r must set m.migrating.
	t.Run("confirmed r sets migrating flag", func(t *testing.T) {
		m := wrongProvModel()
		got := drive(m, tea.KeyPressMsg{Code: 'r', Text: "r"}, tea.KeyPressMsg{Code: 'r', Text: "r"})
		if !got.migrating {
			t.Error("migrating should be true after confirmed r — regression test")
		}
	})
}

func TestFlow_UC58_SettingsSavedMsg(t *testing.T) {
	t.Parallel()
	t.Run("success sets ✓ settings saved", func(t *testing.T) {
		got := drive(baseModel(nil), settingsSavedMsg{})
		if got.statusMsg != "✓ settings saved" {
			t.Errorf("statusMsg = %q, want ✓ settings saved", got.statusMsg)
		}
	})

	t.Run("error sets ✗ status", func(t *testing.T) {
		got := drive(baseModel(nil), settingsSavedMsg{err: errors.New("disk full")})
		if got.statusMsg != "✗ disk full" {
			t.Errorf("statusMsg = %q, want ✗ disk full", got.statusMsg)
		}
	})
}

func TestFlow_UC59_ProgressMsg(t *testing.T) {
	t.Parallel()
	got := drive(baseModel(nil), progressMsg{text: "installing…"})
	if got.progressText != "installing…" {
		t.Errorf("progressText = %q, want installing…", got.progressText)
	}

	t.Run("row start sets operation and clears pending", func(t *testing.T) {
		m := baseModel(nil)
		key := toolKey("ripgrep", "brew")
		m.bulkPendingKeys = map[string]bool{key: true}
		got := drive(m, progressMsg{rowKey: key, rowStatus: "Upgrading ripgrep…", text: "upgrading ripgrep…"})
		if got.bulkPendingKeys[key] {
			t.Fatalf("bulk pending key should clear when row starts")
		}
		if got.rowOpKey != key || got.rowOpStatus != "Upgrading ripgrep…" {
			t.Fatalf("row operation = %q/%q, want active ripgrep", got.rowOpKey, got.rowOpStatus)
		}
	})

	t.Run("row failure stores tool error", func(t *testing.T) {
		key := toolKey("ripgrep", "brew")
		got := drive(baseModel(nil), progressMsg{rowKey: key, rowErr: "upgrade failed", rowDone: true})
		if got.rowErrors[key] != "upgrade failed" {
			t.Fatalf("rowErrors = %#v, want ripgrep failure", got.rowErrors)
		}
	})
}

func TestFlow_UC60_ClearStatusMsg(t *testing.T) {
	t.Parallel()
	t.Run("matching gen clears statusMsg", func(t *testing.T) {
		m := baseModel(nil)
		m.statusMsg = "some status"
		m.statusGen = 5
		got := drive(m, clearStatusMsg{gen: 5})
		if got.statusMsg != "" {
			t.Errorf("statusMsg = %q, should be empty after clearStatusMsg", got.statusMsg)
		}
	})

	t.Run("stale gen does not clear statusMsg", func(t *testing.T) {
		m := baseModel(nil)
		m.statusMsg = "active status"
		m.statusGen = 5
		got := drive(m, clearStatusMsg{gen: 4}) // stale
		if got.statusMsg == "" {
			t.Error("statusMsg should NOT be cleared by stale gen")
		}
	})
}

func TestFlow_UC61_PaletteExecution(t *testing.T) {
	t.Parallel()
	t.Run("sync command sets loading", func(t *testing.T) {
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
		var syncCmd palCmd
		for _, cmd := range buildPalette(m) {
			if cmd.name == "tools sync" {
				syncCmd = cmd
				break
			}
		}
		m.commandSuggestions = []palCmd{syncCmd}
		got := drive(m, pressEnter())
		if !got.loading {
			t.Error("loading should be true after executing sync from palette")
		}
	})

	t.Run("unknown command sets error status", func(t *testing.T) {
		m := Model{
			keys:          DefaultKeyMap(),
			spinner:       spinner.New(),
			filter:        textinput.New(),
			commandInput:  textinput.New(),
			mode:          viewCommand,
			commandOrigin: viewList,
			commandCursor: -1,
			upgradingKeys: make(map[string]bool),
		}
		m.commandInput.Focus()
		got := drive(m,
			tea.KeyPressMsg{Code: 'z', Text: "z"},
			tea.KeyPressMsg{Code: 'z', Text: "z"},
			tea.KeyPressMsg{Code: 'z', Text: "z"},
			pressEnter(),
		)
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList after unknown command", got.mode)
		}
		if got.statusMsg != "✗ unknown command" {
			t.Errorf("statusMsg = %q, want ✗ unknown command", got.statusMsg)
		}
	})
}

func TestFlow_UC62_DiscoveredRefreshedMsg(t *testing.T) {
	t.Parallel()
	t.Run("success updates discoveredTools", func(t *testing.T) {
		orphan := &app.ToolView{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
		got := drive(baseModel(nil), discoveredRefreshedMsg{discovered: []*app.ToolView{orphan}})
		if len(got.discoveredTools) != 1 {
			t.Errorf("discoveredTools = %d, want 1", len(got.discoveredTools))
		}
	})

	t.Run("success with missing descriptions queues refresh", func(t *testing.T) {
		orphan := &app.ToolView{Name: "playwright", Provider: "node", Installed: true, Tracked: false}
		m := baseModel(nil)
		cmd := m.handleDiscoveredRefreshedMsg(discoveredRefreshedMsg{discovered: []*app.ToolView{orphan}})
		if cmd == nil {
			t.Fatal("expected description refresh command for discovered tool without description")
		}
	})

	t.Run("error sets status", func(t *testing.T) {
		got := drive(baseModel(nil), discoveredRefreshedMsg{err: errors.New("scan failed")})
		if got.statusMsg == "" {
			t.Error("statusMsg should be set on discoveredRefreshedMsg error")
		}
	})

	t.Run("success pruning last row clears discoveredTools", func(t *testing.T) {
		m := baseModel(nil)
		m.discoveredTools = []*app.ToolView{{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}}
		m.discoveryGen = 5
		got := drive(m, discoveredRefreshedMsg{gen: 5, discovered: nil, err: nil})
		if len(got.discoveredTools) != 0 {
			t.Errorf("discoveredTools = %d, want 0 after pruning refresh", len(got.discoveredTools))
		}
	})

	t.Run("error leaves discoveredTools untouched", func(t *testing.T) {
		m := baseModel(nil)
		m.discoveredTools = []*app.ToolView{{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}}
		m.discoveryGen = 5
		got := drive(m, discoveredRefreshedMsg{gen: 5, discovered: nil, err: errors.New("scan failed")})
		if len(got.discoveredTools) != 1 {
			t.Errorf("discoveredTools = %d, want 1 (unchanged) on error", len(got.discoveredTools))
		}
	})
}

func TestFlow_UC63_DescRefreshDoneMsg(t *testing.T) {
	t.Parallel()
	refreshedDiscovered := []*app.ToolView{{
		Name:        "playwright",
		Provider:    "node",
		Installed:   true,
		Tracked:     false,
		Description: "browser automation",
	}}
	got := drive(baseModel(nil), descRefreshDoneMsg{tools: threeTools(), discovered: refreshedDiscovered})
	if len(got.allTools) != 3 {
		t.Errorf("allTools = %d, want 3 after descRefreshDoneMsg", len(got.allTools))
	}
	if len(got.discoveredTools) != 1 || got.discoveredTools[0].Description != "browser automation" {
		t.Fatalf("discoveredTools = %+v, want refreshed discovered descriptions", got.discoveredTools)
	}

	m := baseModel([]*app.ToolView{{Name: "fresh", Provider: "brew"}})
	m.discoveredTools = []*app.ToolView{{Name: "old-orphan", Provider: "brew"}}
	m.descRefreshGen = 2
	got = drive(m, descRefreshDoneMsg{gen: 1, tools: threeTools(), discovered: refreshedDiscovered})
	if len(got.allTools) != 1 || got.allTools[0].Name != "fresh" {
		t.Fatalf("stale descRefreshDoneMsg replaced allTools with %+v", got.allTools)
	}
	if len(got.discoveredTools) != 1 || got.discoveredTools[0].Name != "old-orphan" {
		t.Fatalf("stale descRefreshDoneMsg replaced discoveredTools with %+v", got.discoveredTools)
	}

	m = baseModel([]*app.ToolView{{Name: "fresh", Provider: "brew"}})
	m.descRefreshGen = 2
	m.descRefreshing = true
	got = drive(m, descRefreshDoneMsg{gen: 2, err: errors.New("registry unavailable")})
	if got.descRefreshing {
		t.Fatal("descRefreshing should clear after refresh error")
	}
	if !got.statusIsErr || !strings.Contains(got.statusMsg, "description refresh failed") {
		t.Fatalf("status = %q err=%v, want description refresh failure", got.statusMsg, got.statusIsErr)
	}
}

func TestFlow_UC64_MouseWheelScroll(t *testing.T) {
	t.Parallel()
	m := baseModel(threeTools())
	m.cursor = 0

	t.Run("tools wheel down increments cursor", func(t *testing.T) {
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if got.cursor != 1 {
			t.Errorf("cursor = %d, want 1 after wheel down", got.cursor)
		}
	})

	t.Run("tools wheel up decrements cursor", func(t *testing.T) {
		m2 := m
		m2.cursor = 2
		got := drive(m2, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if got.cursor != 1 {
			t.Errorf("cursor = %d, want 1 after wheel up", got.cursor)
		}
	})

	t.Run("wheel up at top wraps to last row", func(t *testing.T) {
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if got.cursor != len(m.visibleTools)-1 {
			t.Errorf("cursor = %d, want %d (wrapped)", got.cursor, len(m.visibleTools)-1)
		}
	})

	t.Run("group picker popup wheel up at top clamps, does not wrap", func(t *testing.T) {
		p := baseModel(nil)
		p.mode = viewGroupPicker
		p.pickerGroups = []string{"base", "work", "+ new group…"}
		p.pickerCursor = 0

		got := drive(p, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if got.pickerCursor != 0 {
			t.Errorf("pickerCursor = %d, want 0 (clamped, not wrapped like a main tab)", got.pickerCursor)
		}
	})

	t.Run("dots wheel scrolls dots cursor", func(t *testing.T) {
		m := dotsModel()
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if got.dotsCursor != 1 {
			t.Errorf("dotsCursor = %d, want 1 after wheel down", got.dotsCursor)
		}
	})

	t.Run("settings wheel scrolls settings cursor", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if got.settingsCursor != 1 {
			t.Errorf("settingsCursor = %d, want 1 after wheel down", got.settingsCursor)
		}
	})

	t.Run("settings doctor wheel scrolls result", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		m.height = 18
		m.settingsCursor = settingsRowDoctor
		checks := make([]app.DoctorCheck, 12)
		for i := range checks {
			checks[i] = app.DoctorCheck{
				Label:   "Check " + string(rune('A'+i)),
				Status:  app.DoctorStatusOK,
				Message: "ok",
			}
		}
		m.doctorResult = &app.DoctorResult{
			Summary: app.DoctorSummary{OK: len(checks)},
			Checks:  checks,
		}
		before := stripANSIEscapeSequences(renderSettings(m))
		if strings.Contains(before, "Check L") {
			t.Fatalf("test setup should start above the final check:\n%s", before)
		}

		got := drive(m,
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
		)
		if got.settingsCursor != settingsRowDoctor {
			t.Fatalf("settingsCursor = %d, want doctor row", got.settingsCursor)
		}
		after := stripANSIEscapeSequences(renderSettings(got))
		if !strings.Contains(after, "Check L") {
			t.Fatalf("doctor result should scroll to the final check:\n%s", after)
		}
	})

	t.Run("hosts wheel scrolls host cursor", func(t *testing.T) {
		m := hostsModel()
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if got.hostCursor != 1 {
			t.Errorf("hostCursor = %d, want 1 after wheel down", got.hostCursor)
		}
	})

	t.Run("command palette wheel scrolls suggestions", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewCommand
		m.commandCursor = -1
		m.commandSuggestions = []palCmd{{name: "one"}, {name: "two"}}
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		if got.commandCursor != 0 {
			t.Errorf("commandCursor = %d, want 0 after wheel down", got.commandCursor)
		}
	})
}

func TestFlow_UC65_WindowSize(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	got := drive(m, tea.WindowSizeMsg{Width: 200, Height: 60})
	if got.width != 200 {
		t.Errorf("width = %d, want 200", got.width)
	}
	if got.height != 60 {
		t.Errorf("height = %d, want 60", got.height)
	}
}

// Does NOT wire a real app.App — use dotsModelWithChildAndApp for tests driving the full key handler, which requires m.app != nil.
func dotsModelWithChild(childRelPath string, childIgnored bool) Model {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo/dotfiles")
	cacheDotsAvailability(&m, app.DotsSyncAvailability{
		Configured: true,
		Reason:     app.DotsSyncAvailabilityReady,
		RepoPath:   "/repo/dotfiles",
	})
	m.dotsEntries = []app.DotStatus{
		{
			Name:       "config",
			SourcePath: "/repo/config",
			TargetPath: "~/.config",
			Health:     app.HealthOK,
			State:      dots.StateSynced,
			Actions:    []dots.Action{dots.ActionRemove, dots.ActionIgnore},
			Children: []app.DotChild{
				{RelPath: childRelPath, Ignored: childIgnored, IsDir: false},
			},
		},
	}
	m.dotsExpandedName = "config"
	m.dotsExpandedState = app.DotStatusState(m.dotsEntries[0])
	return m
}

// Wires a real in-memory app.App so the full MoveGroup key handler path can run.
func dotsModelWithChildAndApp(t *testing.T, childRelPath string, childIgnored bool) Model {
	t.Helper()
	appModel, repoDir := newDotsModelForCmds(t)
	m := dotsModelWithChild(childRelPath, childIgnored)
	m.app = appModel.app
	m.ctx = appModel.ctx
	cacheDotsAvailability(&m, app.DotsSyncAvailability{
		Configured: true,
		Reason:     app.DotsSyncAvailabilityReady,
		RepoPath:   repoDir,
	})
	return m
}

func TestFlow_UC66_DotChildExtractPickerOpens(t *testing.T) {
	// openDotGroupMembershipPicker requires m.app != nil, so use the app-backed helper.
	t.Run("g on extractable child opens group membership picker in extract mode", func(t *testing.T) {
		m := dotsModelWithChildAndApp(t, "nvim", false)
		visible := dotsVisibleRows(m)
		childIdx := -1
		for i, row := range visible {
			if row.isChild {
				childIdx = i
				break
			}
		}
		if childIdx < 0 {
			t.Fatal("expected a child row in visible rows, found none")
		}
		m.dotsCursor = childIdx

		got := drive(m, pressRune('g'))

		if got.mode != viewGroupMembership {
			t.Fatalf("mode = %v, want viewGroupMembership after g on extractable child", got.mode)
		}
		if got.pickerDotExtractParent != "config" {
			t.Errorf("pickerDotExtractParent = %q, want %q", got.pickerDotExtractParent, "config")
		}
		if got.pickerDotExtractSub != "nvim" {
			t.Errorf("pickerDotExtractSub = %q, want %q", got.pickerDotExtractSub, "nvim")
		}
		wantName := app.DotExtractName("config", "nvim")
		if got.pickerMembershipName != wantName {
			t.Errorf("pickerMembershipName = %q, want %q", got.pickerMembershipName, wantName)
		}
		if got.pickerMembershipKind != pickerMembershipDot {
			t.Errorf("pickerMembershipKind = %q, want %q", got.pickerMembershipKind, pickerMembershipDot)
		}
	})

	// Called directly (white-box) to verify the seeded state without needing a real app for the child-path branch.
	t.Run("openDotChildExtractPicker seeds extract context directly", func(t *testing.T) {
		m := dotsModelWithChild("nvim", false)
		visible := dotsVisibleRows(m)
		childIdx := -1
		for i, row := range visible {
			if row.isChild {
				childIdx = i
				break
			}
		}
		if childIdx < 0 {
			t.Fatal("expected a child row in visible rows, found none")
		}
		row := visible[childIdx]

		m.openDotChildExtractPicker(row)

		if m.mode != viewGroupMembership {
			t.Fatalf("mode = %v, want viewGroupMembership", m.mode)
		}
		if m.pickerDotExtractParent != "config" {
			t.Errorf("pickerDotExtractParent = %q, want config", m.pickerDotExtractParent)
		}
		if m.pickerDotExtractSub != "nvim" {
			t.Errorf("pickerDotExtractSub = %q, want nvim", m.pickerDotExtractSub)
		}
		wantName := app.DotExtractName("config", "nvim")
		if m.pickerMembershipName != wantName {
			t.Errorf("pickerMembershipName = %q, want %q", m.pickerMembershipName, wantName)
		}
		if m.pickerMembershipKind != pickerMembershipDot {
			t.Errorf("pickerMembershipKind = %q, want pickerMembershipDot", m.pickerMembershipKind)
		}
		if m.pickerOriginalGroups != nil {
			t.Errorf("pickerOriginalGroups = %v, want nil for extract (no prior groups)", m.pickerOriginalGroups)
		}
		if _, hasMembership := m.dotMemberships[wantName]; !hasMembership {
			t.Errorf("phantom dotMemberships[%q] was not seeded", wantName)
		}
	})

	t.Run("g on non-child (top-level) entry with no memberships is a no-op", func(t *testing.T) {
		m := dotsModelWithChildAndApp(t, "nvim", false)
		m.dotsCursor = 0 // top-level "config" row
		visible := dotsVisibleRows(m)
		if visible[0].isChild {
			t.Fatal("expected cursor on top-level row at index 0")
		}
		got := drive(m, pressRune('g'))
		if got.mode == viewGroupMembership {
			t.Error("group picker should not open for top-level entry with no memberships")
		}
	})
}

func TestFlow_UC66_DotsChildExtractable(t *testing.T) {
	t.Parallel()
	t.Run("extractable: isChild=true, not ignored, non-empty RelPath", func(t *testing.T) {
		row := dotsVisibleRow{
			entry:   app.DotStatus{Name: "config"},
			isChild: true,
			child:   app.DotChild{RelPath: "nvim", Ignored: false},
		}
		if !dotsChildExtractable(row) {
			t.Error("expected dotsChildExtractable=true for normal child with non-empty RelPath")
		}
	})

	t.Run("not extractable: isChild=false (top-level row)", func(t *testing.T) {
		row := dotsVisibleRow{
			entry:   app.DotStatus{Name: "config"},
			isChild: false,
		}
		if dotsChildExtractable(row) {
			t.Error("expected dotsChildExtractable=false for top-level row")
		}
	})

	t.Run("not extractable: child is ignored", func(t *testing.T) {
		row := dotsVisibleRow{
			entry:   app.DotStatus{Name: "config"},
			isChild: true,
			child:   app.DotChild{RelPath: "nvim", Ignored: true},
		}
		if dotsChildExtractable(row) {
			t.Error("expected dotsChildExtractable=false for ignored child")
		}
	})

	t.Run("not extractable: empty RelPath", func(t *testing.T) {
		row := dotsVisibleRow{
			entry:   app.DotStatus{Name: "config"},
			isChild: true,
			child:   app.DotChild{RelPath: "", Ignored: false},
		}
		if dotsChildExtractable(row) {
			t.Error("expected dotsChildExtractable=false for child with empty RelPath")
		}
	})

	t.Run("openDotChildExtractPicker is a no-op for an ignored child", func(t *testing.T) {
		m := dotsModelWithChild("nvim", true /* ignored */)
		visible := dotsVisibleRows(m)
		childIdx := -1
		for i, row := range visible {
			if row.isChild {
				childIdx = i
				break
			}
		}
		if childIdx < 0 {
			t.Fatal("expected a child row in visible rows, found none")
		}
		row := visible[childIdx]

		m.openDotChildExtractPicker(row)

		if m.mode == viewGroupMembership {
			t.Error("openDotChildExtractPicker must not open picker for an ignored child row")
		}
		if m.mode != viewDots {
			t.Errorf("mode = %v, want viewDots after no-op on ignored child", m.mode)
		}
	})
}

func TestFlow_UC66_DotChildExtractPickerCancelClears(t *testing.T) {
	t.Parallel()

	t.Run("esc from extract picker clears extract context and phantom membership", func(t *testing.T) {
		m := dotsModelWithChild("nvim", false)
		visible := dotsVisibleRows(m)
		childIdx := -1
		for i, row := range visible {
			if row.isChild {
				childIdx = i
				break
			}
		}
		if childIdx < 0 {
			t.Fatal("expected a child row in visible rows, found none")
		}
		row := visible[childIdx]

		// Open picker directly (bypasses app == nil guard in openDotGroupMembershipPicker).
		m.openDotChildExtractPicker(row)
		if m.mode != viewGroupMembership {
			t.Fatalf("picker did not open: mode = %v", m.mode)
		}
		childName := app.DotExtractName("config", "nvim")
		if _, hasMembership := m.dotMemberships[childName]; !hasMembership {
			t.Fatalf("phantom membership %q not seeded before cancel", childName)
		}

		cancelled := drive(m, pressEsc())

		if cancelled.mode != viewDots {
			t.Errorf("mode = %v, want viewDots after esc", cancelled.mode)
		}
		if cancelled.pickerDotExtractParent != "" {
			t.Errorf("pickerDotExtractParent = %q, want empty after cancel", cancelled.pickerDotExtractParent)
		}
		if cancelled.pickerDotExtractSub != "" {
			t.Errorf("pickerDotExtractSub = %q, want empty after cancel", cancelled.pickerDotExtractSub)
		}
		if _, stillHas := cancelled.dotMemberships[childName]; stillHas {
			t.Errorf("phantom dotMemberships[%q] should be removed after cancel", childName)
		}
	})
}

// baseModel initialises the -1 sentinel fields correctly via New().
func conflictDotsModel(entries []app.DotStatus) Model {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = entries
	return m
}

func twoConflictEntries() []app.DotStatus {
	return []app.DotStatus{
		{Name: "gitconfig", Health: app.HealthConflict, State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
		{Name: "zshrc", Health: app.HealthConflict, State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal, dots.ActionRemove}},
	}
}

// Initialises the -1 sentinel fields modelForCmds omits (dotsOverwriteIdx etc.) so the model behaves like one built via New().
func newDotsModelForCmdsReady(t *testing.T) (Model, string) {
	t.Helper()
	m, repoDir := newDotsModelForCmds(t)
	m.mode = viewDots
	m.dotsLoaded = true
	m.dotsConfirmIdx = -1
	m.dotsOverwriteIdx = -1
	m.dotsLocalIdx = -1
	m.dotsIgnoreIdx = -1
	m.dotsVariantIdx = -1
	m.dangerConfirmRow = -1
	return m, repoDir
}

func TestFlow_UC67_DotForceResolveAll_FirstPressArmsUseRepo(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	if m.dotsForceResolve != "use_repo" {
		t.Errorf("dotsForceResolve = %q, want %q", m.dotsForceResolve, "use_repo")
	}
	if !m.hasActiveConfirmation() {
		t.Error("hasActiveConfirmation() = false, want true after arming")
	}
}

func TestFlow_UC67_DotForceResolveAll_SecondPressFiresUseRepo(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	cmds := m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	if m.dotsForceResolve != "" {
		t.Errorf("dotsForceResolve = %q, want %q after second press", m.dotsForceResolve, "")
	}
	if len(cmds) == 0 {
		t.Error("expected non-empty cmds after second press (run command), got nil")
	}
}

func TestFlow_UC67_DotForceResolveAll_FirstPressArmsUseLocal(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseLocal)
	if m.dotsForceResolve != "use_local" {
		t.Errorf("dotsForceResolve = %q, want %q", m.dotsForceResolve, "use_local")
	}
	if !m.hasActiveConfirmation() {
		t.Error("hasActiveConfirmation() = false, want true after arming with use-local")
	}
}

func TestFlow_UC67_DotForceResolveAll_SwitchStrategyCancelsAndRearms(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseLocal)
	if m.dotsForceResolve != "use_local" {
		t.Fatalf("setup: dotsForceResolve = %q, want use_local", m.dotsForceResolve)
	}
	cmds := m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	if m.dotsForceResolve != "use_repo" {
		t.Errorf("dotsForceResolve = %q, want use_repo after switching strategy", m.dotsForceResolve)
	}
	// Should have returned exactly one arm cmd, not a run cmd (i.e. dotsLoading should stay false)
	if len(cmds) != 1 {
		t.Errorf("expected 1 arm cmd after switching strategy, got %d cmds", len(cmds))
	}
	if m.dotsLoading {
		t.Error("dotsLoading should be false — strategy switch should not fire the operation")
	}
}

func TestFlow_UC67_DotForceResolveAll_NoConflictsIsNoop(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", Health: app.HealthOK, State: dots.StateSynced},
	}

	cmds := m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	if cmds != nil {
		t.Errorf("expected nil cmds for no-conflict model, got %v", cmds)
	}
	if m.dotsForceResolve != "" {
		t.Errorf("dotsForceResolve = %q, want empty for no-conflict model", m.dotsForceResolve)
	}
}

func TestFlow_UC67_DotForceResolveAll_ClearActiveConfirmationClearsArm(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	if m.dotsForceResolve == "" {
		t.Fatal("setup: dotsForceResolve should be set before clear")
	}
	m.clearActiveConfirmation()
	if m.dotsForceResolve != "" {
		t.Errorf("dotsForceResolve = %q, want empty after clearActiveConfirmation", m.dotsForceResolve)
	}
	if m.hasActiveConfirmation() {
		t.Error("hasActiveConfirmation() = true, want false after clearActiveConfirmation")
	}
}

func TestFlow_UC67_DotsConflictHints_BulkKeysVisibleWhenMultipleConflicts(t *testing.T) {
	t.Parallel()
	m := conflictDotsModel(twoConflictEntries())
	hints := dotsConflictHintItems(m)

	repoAllKey := m.keys.DotUseRepoAll.Help().Key
	localAllKey := m.keys.DotUseLocalAll.Help().Key

	var foundRepoAll, foundLocalAll bool
	for _, h := range hints {
		if h.key == repoAllKey {
			foundRepoAll = true
		}
		if h.key == localAllKey {
			foundLocalAll = true
		}
	}
	if !foundRepoAll {
		t.Errorf("DotUseRepoAll hint (%q) missing from dotsConflictHintItems with 2 conflicts; hints = %#v", repoAllKey, hints)
	}
	if !foundLocalAll {
		t.Errorf("DotUseLocalAll hint (%q) missing from dotsConflictHintItems with 2 conflicts; hints = %#v", localAllKey, hints)
	}
}

func TestFlow_UC67_DotsConflictHints_BulkKeysHiddenWithSingleConflict(t *testing.T) {
	t.Parallel()
	m := conflictDotsModel([]app.DotStatus{
		{Name: "gitconfig", Health: app.HealthConflict, State: dots.StateConflict, Actions: []dots.Action{dots.ActionUseRepo, dots.ActionUseLocal}},
	})
	hints := dotsConflictHintItems(m)

	repoAllKey := m.keys.DotUseRepoAll.Help().Key
	localAllKey := m.keys.DotUseLocalAll.Help().Key

	for _, h := range hints {
		if h.key == repoAllKey {
			t.Errorf("DotUseRepoAll hint (%q) should NOT appear with only 1 conflict; hints = %#v", repoAllKey, hints)
		}
		if h.key == localAllKey {
			t.Errorf("DotUseLocalAll hint (%q) should NOT appear with only 1 conflict; hints = %#v", localAllKey, hints)
		}
	}
}

func TestFlow_UC67_DotsConflictHints_BulkKeysHiddenWithNoConflicts(t *testing.T) {
	t.Parallel()
	m := conflictDotsModel([]app.DotStatus{
		{Name: "nvim", Health: app.HealthOK, State: dots.StateSynced},
	})
	// dotsConflictCount guards the bulk hints, so verify they do not appear even when dotsConflictHintItems is called directly.
	hints := dotsConflictHintItems(m)

	repoAllKey := m.keys.DotUseRepoAll.Help().Key
	localAllKey := m.keys.DotUseLocalAll.Help().Key

	for _, h := range hints {
		if h.key == repoAllKey {
			t.Errorf("DotUseRepoAll hint (%q) should NOT appear with 0 conflicts; hints = %#v", repoAllKey, hints)
		}
		if h.key == localAllKey {
			t.Errorf("DotUseLocalAll hint (%q) should NOT appear with 0 conflicts; hints = %#v", localAllKey, hints)
		}
	}
}

func TestFlow_UC67_DotForceResolveAll_AppNilIsNoop(t *testing.T) {
	t.Parallel(
	// baseModel has no app wired; handleDotsForceResolveAllKeyMsg should return nil
	)

	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = twoConflictEntries()
	cmds := m.handleDotsForceResolveAllKeyMsg(app.DotResolveUseRepo)
	if cmds != nil {
		t.Errorf("expected nil cmds when m.app == nil, got %v", cmds)
	}
}

func TestFlow_UC67_DotForceResolveAll_KeyDispatch_UArmsViaUpdate(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	got := drive(m, pressRune('U'))
	if got.dotsForceResolve != "use_repo" {
		t.Errorf("dotsForceResolve = %q, want use_repo after pressing U", got.dotsForceResolve)
	}
	if !got.hasActiveConfirmation() {
		t.Error("hasActiveConfirmation() = false after first U press")
	}
}

func TestFlow_UC67_DotForceResolveAll_KeyDispatch_LArmsViaUpdate(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	got := drive(m, pressRune('L'))
	if got.dotsForceResolve != "use_local" {
		t.Errorf("dotsForceResolve = %q, want use_local after pressing L", got.dotsForceResolve)
	}
}

func TestFlow_UC67_DotForceResolveAll_KeyDispatch_SecondUFires(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	got := drive(m, pressRune('U'), pressRune('U'))
	if got.dotsForceResolve != "" {
		t.Errorf("dotsForceResolve = %q, want empty after second U (operation fired)", got.dotsForceResolve)
	}
	if !got.dotsLoading {
		t.Error("dotsLoading should be true after second U press fires the operation")
	}
}

func TestFlow_UC67_DotForceResolveAll_DotsConflictCount(t *testing.T) {
	t.Parallel()
	t.Run("counts only conflict entries", func(t *testing.T) {
		m := conflictDotsModel([]app.DotStatus{
			{Name: "a", State: dots.StateConflict},
			{Name: "b", State: dots.StateSynced},
			{Name: "c", State: dots.StateConflict},
		})
		if got := dotsConflictCount(m); got != 2 {
			t.Errorf("dotsConflictCount = %d, want 2", got)
		}
	})

	t.Run("zero conflicts", func(t *testing.T) {
		m := conflictDotsModel([]app.DotStatus{
			{Name: "a", State: dots.StateSynced},
		})
		if got := dotsConflictCount(m); got != 0 {
			t.Errorf("dotsConflictCount = %d, want 0", got)
		}
	})

	t.Run("empty entries", func(t *testing.T) {
		m := conflictDotsModel(nil)
		if got := dotsConflictCount(m); got != 0 {
			t.Errorf("dotsConflictCount = %d, want 0 for empty entries", got)
		}
	})
}

func TestFlow_UC67_DotForceResolveAll_KeyDispatch_SwitchRearms(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = twoConflictEntries()

	got := drive(m, pressRune('L'))
	if got.dotsForceResolve != "use_local" {
		t.Fatalf("setup: dotsForceResolve = %q, want use_local", got.dotsForceResolve)
	}
	got = drive(got, pressRune('U'))
	if got.dotsForceResolve != "use_repo" {
		t.Errorf("dotsForceResolve = %q, want use_repo after switching from L to U", got.dotsForceResolve)
	}
	if got.dotsLoading {
		t.Error("dotsLoading should be false — strategy switch should not fire the operation")
	}
}

func TestFlow_UC67_DotForceResolveAll_KeyDispatch_U_NoConflicts(t *testing.T) {
	m, _ := newDotsModelForCmdsReady(t)
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", Health: app.HealthOK, State: dots.StateSynced},
	}

	got := drive(m, pressRune('U'))
	if got.dotsForceResolve != "" {
		t.Errorf("dotsForceResolve = %q, want empty when no conflicts", got.dotsForceResolve)
	}
	if got.dotsLoading {
		t.Error("dotsLoading should be false when no conflicts")
	}
}

func TestFlow_UC66_DotChildExtractHintVisible(t *testing.T) {
	t.Parallel()
	t.Run("g/edit-groups hint appears for extractable child row", func(t *testing.T) {
		m := dotsModelWithChild("nvim", false)
		visible := dotsVisibleRows(m)
		childIdx := -1
		for i, row := range visible {
			if row.isChild {
				childIdx = i
				break
			}
		}
		if childIdx < 0 {
			t.Fatal("expected a child row in visible rows, found none")
		}
		m.dotsCursor = childIdx

		hints := dotsRowHintItems(m)
		moveGroupKey := m.keys.MoveGroup.Help().Key
		found := false
		for _, h := range hints {
			if h.key == moveGroupKey {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("dotsRowHintItems did not include MoveGroup key %q for extractable child; hints = %#v", moveGroupKey, hints)
		}
	})

	t.Run("g/edit-groups hint does NOT appear for ignored child row", func(t *testing.T) {
		m := dotsModelWithChild("nvim", true /* ignored */)
		visible := dotsVisibleRows(m)
		childIdx := -1
		for i, row := range visible {
			if row.isChild {
				childIdx = i
				break
			}
		}
		if childIdx < 0 {
			t.Fatal("expected a child row in visible rows, found none")
		}
		m.dotsCursor = childIdx

		hints := dotsRowHintItems(m)
		moveGroupKey := m.keys.MoveGroup.Help().Key
		for _, h := range hints {
			if h.key == moveGroupKey {
				t.Errorf("dotsRowHintItems should NOT include MoveGroup hint for ignored child; hints = %#v", hints)
				break
			}
		}
	})
}
