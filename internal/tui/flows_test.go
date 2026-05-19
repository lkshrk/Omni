package tui

// flows_test.go — comprehensive flow tests covering every user-facing use case in
// the TUI. Tests drive the model via key presses and messages, then assert on
// model state (mode, cursor, flags, status) rather than rendered string content.
//
// Enumerated use cases (40+):
//
// LIST VIEW — Navigation
//  UC-01  j moves cursor down; clamps at last item
//  UC-02  k moves cursor up; clamps at 0
//  UC-03  home jumps to top; G jumps to bottom
//  UC-04  ctrl+d half-page down; ctrl+u half-page up
//  UC-05  ctrl+f full-page down; ctrl+b full-page up
//  UC-06  Navigation on empty list stays at 0
//
// LIST VIEW — Filtering / Search
//  UC-07  / enters viewSearch; Esc exits viewSearch
//  UC-08  Typing in viewSearch live-filters allTools
//  UC-09  Enter in viewSearch blurs input (stays viewSearch)
//  UC-10  ] / [ cycle provider filter pills
//  UC-11  Esc in viewList clears group/provider filter
//
// LIST VIEW — Tool actions
//  UC-12  Enter on uninstalled tool sets loading
//  UC-13  Enter on installed tool is no-op
//  UC-14  i key installs uninstalled tool (sets loading)
//  UC-15  d key deletes installed tool (sets loading)
//  UC-16  u key upgrades outdated tool (sets upgradingKeys)
//  UC-17  U key upgrade-all with updates (sets wildcard key)
//  UC-18  U key upgrade-all no-op when no updates
//  UC-19  s key triggers sync (sets loading + progress channel)
//  UC-20  R key re-scans providers (populates scanningProviders)
//  UC-21  g key opens group picker; Esc returns to list
//  UC-22  c key opens group picker for orphan tool (claim)
//  UC-23  x key toggles ignore on tool (sets loading)
//  UC-24  o key pins provider for syncWrongProv tool (sets loading)
//  UC-25  r key reinstalls provider for syncWrongProv tool (sets loading + migrating)
//
// GLOBAL
//  UC-26  ? toggles help overlay; Esc/second ? closes it
//  UC-27  q sets confirmQuit; second q quits; other key resets
//  UC-28  Keys ignored while loading=true
//  UC-29  : opens command palette; Esc closes it
//  UC-30  Tab cycles tabs: list→dots→status→hosts→settings→list
//  UC-31  Tab blocked in hostRequired state
//
// SETUP WIZARD
//  UC-32  Setup step 0: y starts config creation (loading=true)
//  UC-33  Setup step 0: n/esc quits
//  UC-34  Setup step 1: space toggles provider; Enter submits (loading=true)
//  UC-35  Setup step 3: empty name triggers exit confirm
//  UC-36  toolsLoadedMsg noConfig sets viewSetup
//  UC-37  toolsLoadedMsg from viewSetup step 0 advances to step 1
//  UC-38  toolsLoadedMsg noHost sets viewSetup step 2
//
// SETTINGS TAB
//  UC-39  j/k navigate settingsCursor; clamps at bounds
//  UC-40  Space/Enter toggles AutoImport on row 0
//  UC-41  Enter on row 7 opens file picker
//  UC-42  Maintenance row 11/12 enter sets dangerConfirmRow
//  UC-43  Esc cancels dangerConfirmRow
//  UC-44  Dots disable keep-local choice: y/n/enter/esc
//  UC-45  Priority editor: j/k/J/K navigation and discard on Esc
//
// PROFILES TAB
//  UC-46  j/k navigate hostCursor; Down enters groupSection
//  UC-47  Esc returns to list (when not hostRequired)
//  UC-48  n opens new-host text input; Esc cancels
//  UC-49  Host delete confirm (D → Enter / Esc)
//  UC-50  Group rename mode (r → type → Enter/Esc)
//  UC-51  Group create mode (n → type → Enter)
//  UC-52  Group delete confirm (D → Enter/Esc in section 1)
//
// GROUP PICKER
//  UC-53  j/k navigate; Enter on real group sets loading
//  UC-54  Esc from picker returns to list
//  UC-55  Sentinel "+ new group…" opens text input; Esc cancels
//
// DOTS TAB
//  UC-56  j/k navigate dotsCursor; clamps at bounds
//  UC-57  d arms delete confirm; Esc cancels
//  UC-58  d twice fires doDotsDelete (loading=true)
//  UC-59  s triggers dots sync (dotsLoading=true)
//  UC-60  Enter on conflict entry arms overwrite; Esc cancels
//
// MESSAGE HANDLERS
//  UC-61  toolsLoadedMsg success populates tools
//  UC-62  opCompleteMsg success sets ✓ status; error sets ✗
//  UC-63  progressDoneMsg clears loading; suppressed when migrating
//  UC-64  providerScannedMsg removes provider from scanningProviders
//  UC-65  allProvidersDoneMsg refreshes tools; suppresses status clear when migrating
//  UC-66  groupChangedMsg success/error updates state
//  UC-67  dangerOpDoneMsg reload=true starts loadTools
//  UC-68  claimDoneMsg removes tool from discoveredTools
//  UC-69  ignoreDoneMsg toggles ignoreSet
//  UC-70  migrateProviderDoneMsg clears loading+migrating
//  UC-71  dotsLoadedMsg/dotsSyncedMsg/dotsPulledMsg/dotsPushedMsg
//  UC-72  dotsDeletedMsg clamps cursor
//  UC-73  dotsFixedMsg clears overwriteIdx

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// oneInstalledOutdated returns a single installed+outdated tool.
func oneInstalledOutdated() []*database.ToolCache {
	return []*database.ToolCache{{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true}}
}

// oneInstalled returns a single installed tool.
func oneInstalled() []*database.ToolCache {
	return []*database.ToolCache{{Name: "ripgrep", Provider: "brew", Installed: true, Tracked: true}}
}

// oneMissing returns a single tracked-but-not-installed tool.
func oneMissing() []*database.ToolCache {
	return []*database.ToolCache{{Name: "curl", Provider: "brew", Installed: false, Tracked: true}}
}

// manyTools returns n distinct tools for pagination tests.
func manyTools(n int) []*database.ToolCache {
	out := make([]*database.ToolCache, n)
	for i := range out {
		out[i] = &database.ToolCache{Name: "tool", Provider: "brew"}
	}
	return out
}

// toSettings returns the key sequence that navigates from list to settings tab.
func toSettings() []tea.Msg {
	// Third tab lands on settings with cursor hidden; the extra j reveals cursor at row 0.
	return []tea.Msg{pressTab(), pressTab(), pressTab(), pressRune('j')}
}

// nj returns n 'j' presses.
func nj(n int) []tea.Msg {
	msgs := make([]tea.Msg, n)
	for i := range msgs {
		msgs[i] = pressRune('j')
	}
	return msgs
}

// setupStep1Model builds a model stuck at setup step 1 with two provider rows.
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
		setupProviders: []setupProviderRow{
			{name: "system", label: "system", enabled: true},
			{name: "node", label: "node", enabled: true},
		},
	}
	return m
}

// ── UC-01 Cursor navigation j/k ──────────────────────────────────────────────

func TestFlow_UC01_CursorNavigation(t *testing.T) {
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

// ── UC-02 Top/Bottom home/G ───────────────────────────────────────────────────

func TestFlow_UC02_TopBottom(t *testing.T) {
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

// ── UC-03 Page navigation ─────────────────────────────────────────────────────

func TestFlow_UC03_PageNavigation(t *testing.T) {
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

// ── UC-04 Empty list navigation ───────────────────────────────────────────────

func TestFlow_UC04_EmptyListNavigation(t *testing.T) {
	m := baseModel(nil)
	msgs := []tea.Msg{pressRune('j'), pressRune('k'), pressHome(), pressRune('G')}
	got := drive(m, msgs...)
	if got.cursor != 0 {
		t.Errorf("cursor = %d on empty list, want 0", got.cursor)
	}
}

// ── UC-05 Search mode enter/exit ─────────────────────────────────────────────

func TestFlow_UC05_SearchMode(t *testing.T) {
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

// ── UC-06 Typing in viewSearch live-filters ──────────────────────────────────

func TestFlow_UC06_LiveFilter(t *testing.T) {
	// 'g' matches only "git" among [git/brew, node/npm, python/pip].
	got := drive(baseModel(threeTools()), pressRune('/'), pressRune('g'))
	if len(got.visibleTools) != 1 {
		t.Errorf("visibleTools = %d, want 1 after live filter", len(got.visibleTools))
	}
	if len(got.visibleTools) > 0 && got.visibleTools[0].Name != "git" {
		t.Errorf("visibleTools[0] = %q, want git", got.visibleTools[0].Name)
	}
}

// ── UC-07 Provider filter pills ──────────────────────────────────────────────

func TestFlow_UC07_ProviderFilterPills(t *testing.T) {
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

// ── UC-08 Esc in viewList clears filters ─────────────────────────────────────

func TestFlow_UC08_EscClearsFilter(t *testing.T) {
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

// ── UC-09 Enter on tool (install / no-op) ────────────────────────────────────

func TestFlow_UC09_EnterOnTool(t *testing.T) {
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

// ── UC-10 i key install ───────────────────────────────────────────────────────

func TestFlow_UC10_InstallKey(t *testing.T) {
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

// ── UC-11 d key delete ─────────────────────────────────────────────────────

func TestFlow_UC11_DeleteKey(t *testing.T) {
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

// ── UC-12 u key upgrade ───────────────────────────────────────────────────────

func TestFlow_UC12_UpgradeKey(t *testing.T) {
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
}

// ── UC-13 U key upgrade-all ───────────────────────────────────────────────────

func TestFlow_UC13_UpgradeAllKey(t *testing.T) {
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

// ── UC-14 S key sync all ─────────────────────────────────────────────────────

func TestFlow_UC14_SyncKey(t *testing.T) {
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
	m := baseModel(nil)
	got := drive(m, pressRune('s'))
	if got.loading {
		t.Error("loading should stay false after lowercase s on tools")
	}
	if got.progressCh != nil {
		t.Error("progressCh should stay nil after lowercase s on tools")
	}
}

// ── UC-15 R key refresh scan ─────────────────────────────────────────────────

func TestFlow_UC15_RefreshKey(t *testing.T) {
	m := baseModel(oneInstalled())
	m.upgradingKeys = make(map[string]bool)
	got := drive(m, pressRune('R'))
	if len(got.scanningProviders) == 0 {
		t.Error("scanningProviders should be populated after R")
	}
}

// ── UC-16 g key opens group membership picker ────────────────────────────────

func TestFlow_UC16_GroupPickerOpen(t *testing.T) {
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

// ── UC-17 c key claim orphan ─────────────────────────────────────────────────

func TestFlow_UC17_ClaimOrphan(t *testing.T) {
	// Build a model with one orphan tool (Tracked=false = discoveredTool).
	orphan := &database.ToolCache{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
	m := baseModel(nil)
	m.discoveredTools = []*database.ToolCache{orphan}
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

// ── UC-18 x key ignore/un-ignore ─────────────────────────────────────────────

func TestFlow_UC18_IgnoreKey(t *testing.T) {
	t.Run("x on tool opens ignore scope picker", func(t *testing.T) {
		m := baseModel(oneInstalled())
		m.upgradingKeys = make(map[string]bool)
		got := drive(m, pressRune('x'))
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

// ── UC-19 p/r key pin/reinstall provider ─────────────────────────────────────

func TestFlow_UC19_PinMigrateProvider(t *testing.T) {
	m := wrongProvModel()

	t.Run("p opens provider scope picker", func(t *testing.T) {
		got := drive(m, pressRune('p'))
		if got.mode != viewProviderScope {
			t.Errorf("mode = %v, want viewProviderScope", got.mode)
		}
	})

	t.Run("r arms reinstall confirmation", func(t *testing.T) {
		got := drive(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
		if got.loading {
			t.Error("loading should stay false before reinstall confirmation")
		}
		if got.listConfirm.action != listConfirmReinstallDefault {
			t.Fatalf("listConfirm.action = %q, want reinstall-default", got.listConfirm.action)
		}
	})

	t.Run("second r confirms reinstall with default (sets loading + migrating)", func(t *testing.T) {
		got := drive(m, tea.KeyPressMsg{Code: 'r', Text: "r"}, tea.KeyPressMsg{Code: 'r', Text: "r"})
		if !got.loading {
			t.Error("loading should be true after confirmed r")
		}
		if !got.migrating {
			t.Error("migrating should be true after confirmed r — regression: was never set")
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

// ── UC-20 Help overlay ───────────────────────────────────────────────────────

func TestFlow_UC20_HelpOverlay(t *testing.T) {
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

// ── UC-21 Confirm quit ────────────────────────────────────────────────────────

func TestFlow_UC21_ConfirmQuit(t *testing.T) {
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

// ── UC-22 Keys ignored while loading ─────────────────────────────────────────

func TestFlow_UC22_KeysIgnoredWhileLoading(t *testing.T) {
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

// ── UC-23 Command palette open/close ─────────────────────────────────────────

func TestFlow_UC23_CommandPalette(t *testing.T) {
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

// ── UC-24 Tab cycles main tabs ───────────────────────────────────────────────

func TestFlow_UC24_TabCycle(t *testing.T) {
	t.Run("dashboard → list", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		got := drive(m, pressTab())
		if got.mode != viewList {
			t.Errorf("mode = %v, want viewList", got.mode)
		}
	})

	t.Run("list → dots", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab())
		if got.mode != viewDots {
			t.Errorf("mode = %v, want viewDots", got.mode)
		}
	})

	t.Run("dots → groups", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab())
		if got.mode != viewGroups {
			t.Errorf("mode = %v, want viewGroups", got.mode)
		}
	})

	t.Run("groups → settings", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab())
		if got.mode != viewSettings {
			t.Errorf("mode = %v, want viewSettings", got.mode)
		}
	})

	t.Run("settings → dashboard", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab(), pressTab())
		if got.mode != viewStatus {
			t.Errorf("mode = %v, want viewStatus", got.mode)
		}
	})

	t.Run("settings → list", func(t *testing.T) {
		got := drive(baseModel(nil), pressTab(), pressTab(), pressTab(), pressTab(), pressTab())
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
	m := baseModel(nil)
	m.mode = viewDots
	m.settings.DotsRepo = ""

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
	m := baseModel(nil)
	m.mode = viewDots
	m.settings.DotsRepo = "/tmp/dots"
	m.dotsLoaded = true

	got := drive(m, pressEnter())
	if got.mode != viewDots {
		t.Fatalf("mode = %v, want viewDots", got.mode)
	}
	if got.loading || got.dotsLoading {
		t.Fatalf("loading=%v dotsLoading=%v, want no onboarding or sync", got.loading, got.dotsLoading)
	}
}

// ── UC-25 Tab blocked when hostRequired ────────────────────────────────────

func TestFlow_UC25_TabBlockedWhenHostRequired(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostRequired = true
	got := drive(m, pressTab())
	// Tab should not advance past hosts when host is required.
	if got.mode != viewGroups {
		t.Errorf("mode = %v, want viewGroups (tab blocked)", got.mode)
	}
}

// ── UC-26 Setup step 0: create config ────────────────────────────────────────

func TestFlow_UC26_SetupStep0(t *testing.T) {
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

// ── UC-27 Setup step 1: provider selection ────────────────────────────────────

func TestFlow_UC27_SetupStep1(t *testing.T) {
	t.Run("space toggles first provider off", func(t *testing.T) {
		got := drive(setupStep1Model(), pressRune(' '))
		if got.setupProviders[0].enabled {
			t.Error("first provider should be toggled off after space")
		}
		if !got.setupProviders[1].enabled {
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

// ── UC-29 toolsLoadedMsg state transitions ────────────────────────────────────

func TestFlow_UC29_ToolsLoadedMsg(t *testing.T) {
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

	t.Run("configured host first launch enters bootstrap activation", func(t *testing.T) {
		got := drive(loadingModel(), toolsLoadedMsg{
			tools:             threeTools(),
			bootstrapRequired: true,
			hostInfo: &app.HostInfo{Active: "testhost", Hosts: map[string]config.HostAssignment{
				"testhost": {},
			}},
		})
		if got.mode != viewSetup {
			t.Errorf("mode = %v, want viewSetup", got.mode)
		}
		if got.setupStep != 10 {
			t.Errorf("setupStep = %d, want 10", got.setupStep)
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

// ── UC-30 Settings tab: j/k navigation ──────────────────────────────────────

func TestFlow_UC30_SettingsNavigation(t *testing.T) {
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

// ── UC-31 Settings tab: toggle AutoImport row 0 ──────────────────────────────

func TestFlow_UC31_SettingsToggleAutoImport(t *testing.T) {
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

// ── UC-32 Settings tab: row 7 opens file picker ──────────────────────────────

func TestFlow_UC32_SettingsOpenFilePicker(t *testing.T) {
	msgs := append(toSettings(), nj(7)...)
	msgs = append(msgs, pressEnter())
	got := drive(baseModel(nil), msgs...)
	if !got.showFilePicker {
		t.Error("showFilePicker should be true after enter on dots repo row (row 7)")
	}
}

// ── UC-33 Settings tab: Esc from file picker closes it ───────────────────────

func TestFlow_UC33_FilePickerEscCloses(t *testing.T) {
	m := baseModel(nil)
	m.settings.DotsRepo = "~/dotfiles"
	msgs := append(toSettings(), nj(7)...)
	msgs = append(msgs, pressEnter(), pressEsc())
	got := drive(m, msgs...)
	if got.showFilePicker {
		t.Error("showFilePicker should be false after esc")
	}
	if got.settings.DotsRepo != "~/dotfiles" {
		t.Errorf("DotsRepo changed to %q, want unchanged", got.settings.DotsRepo)
	}
}

// ── UC-34 Maintenance: dangerConfirmRow set/cancelled ────────────────────────

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
		base := baseModel(nil)
		base.settings.DotsRepo = "~/dotfiles"
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressEnter())
		got := drive(base, msgs...)
		if got.dangerConfirmRow != settingsRowDotsSync {
			t.Errorf("dangerConfirmRow = %d, want %d", got.dangerConfirmRow, settingsRowDotsSync)
		}
	})
}

// ── UC-35 Maintenance: dots disable keep-local choice ─────────────────────────

func TestFlow_UC35_DangerDotsDisableKeepLocalChoice(t *testing.T) {
	openChoice := func() Model {
		base := baseModel(nil)
		base.settings.DotsRepo = "~/dotfiles"
		msgs := append(toSettings(), nj(settingsRowDotsSync)...)
		msgs = append(msgs, pressEnter())
		return drive(base, msgs...)
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

// ── UC-36 Priority editor ─────────────────────────────────────────────────────

func TestFlow_UC36_PriorityEditor(t *testing.T) {
	// Navigate to settings and to the provider-order row.
	toPriority := func() []tea.Msg {
		return append(toSettings(), nj(settingsRowSystemPriority)...)
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

	t.Run("J swaps item down", func(t *testing.T) {
		msgs := append(toPriority(), pressEnter(), pressRune('J'))
		got := drive(baseModel(nil), msgs...)
		// Default draft is the concrete system provider priority; J swaps apt↓apk.
		if len(got.priorityDraft) < 2 || got.priorityDraft[0] != "apk" || got.priorityDraft[1] != "apt" {
			t.Errorf("priorityDraft = %v after J, want apk first", got.priorityDraft)
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
		if priority := got.settings.EcosystemPriority("system"); len(priority) == 0 || priority[0] != "brew" {
			t.Errorf("system priority = %v after esc, want original order", priority)
		}
	})

	t.Run("Enter saves reordered priority", func(t *testing.T) {
		msgs := append(toPriority(), pressEnter(), pressRune('J'), pressEnter())
		got := drive(baseModel(nil), msgs...)
		if got.editingPriority {
			t.Error("editingPriority should be false after enter")
		}
		if priority := got.settings.EcosystemPriority("system"); len(priority) == 0 || priority[0] != "apk" {
			t.Errorf("system priority = %v after enter, want reordered", priority)
		}
	})
}

// ── UC-37 Hosts tab navigation ────────────────────────────────────────────

func TestFlow_UC37_GroupsNavigation(t *testing.T) {
	toHosts := func() []tea.Msg { return []tea.Msg{pressTab(), pressTab()} }

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
		// hostCursor advances OR assignmentSection advances.
		if got.hostCursor < 1 && got.assignmentSection < 1 {
			t.Error("j in hosts tab should move cursor or section")
		}
	})
}

// ── UC-38 Host creation is onboarding/CLI only ────────────────────────────────

func TestFlow_UC38_NewHostRemovedFromHostsTab(t *testing.T) {
	toHosts := func() []tea.Msg { return []tea.Msg{pressTab(), pressTab()} }

	t.Run("p does not open host creation", func(t *testing.T) {
		got := drive(baseModel(nil), append(toHosts(), pressRune('p'))...)
		if got.groupCreating || got.hostRenameMode || got.hostEditMode != 0 {
			t.Fatalf("p should not start a Hosts tab edit mode: groupCreating=%v hostRename=%v hostEditMode=%d", got.groupCreating, got.hostRenameMode, got.hostEditMode)
		}
	})

	t.Run("n still opens reusable group creation", func(t *testing.T) {
		got := drive(baseModel(nil), append(toHosts(), pressRune('n'))...)
		if !got.groupCreating {
			t.Error("groupCreating should be true after n in hosts tab")
		}
	})
}

// ── UC-39 Host delete confirm ─────────────────────────────────────────────

func TestFlow_UC39_HostDeleteConfirm(t *testing.T) {
	toHosts := func() []tea.Msg { return []tea.Msg{pressTab(), pressTab()} }

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

// ── UC-40 Group picker: j/k/Enter/Esc ────────────────────────────────────────

func TestFlow_UC40_GroupPickerNav(t *testing.T) {
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

// ── UC-41 Group picker: sentinel "+ new group…" ──────────────────────────────

func TestFlow_UC41_GroupPickerSentinel(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 0
	m.upgradingKeys = make(map[string]bool)

	t.Run("Enter on sentinel opens text input", func(t *testing.T) {
		// Move to sentinel (index 1) then Enter.
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

// ── UC-42 Dots tab: navigation ───────────────────────────────────────────────

func TestFlow_UC42_DotsNavigation(t *testing.T) {
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

// ── UC-43 Dots tab: delete confirm ───────────────────────────────────────────

func TestFlow_UC43_DotsDeleteConfirm(t *testing.T) {
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
		m.settings.DotsRepo = "/repo"
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

// ── UC-44 Dots tab: s syncs dots ─────────────────────────────────────────────

func TestFlow_UC44_DotsSyncKey(t *testing.T) {
	m := dotsModel()
	m.dotsCursor = 1
	got := drive(m, pressRune('s'))
	if !got.dotsLoading {
		t.Error("dotsLoading should be true after s on syncable dots row")
	}
}

func TestFlow_DotsDisabledBlocksMutatingKeys(t *testing.T) {
	disabledModel := func() Model {
		m := dotsModel()
		m.settings.DotsDisabled = config.BoolPtr(true)
		m.dotMemberships = map[string][]string{"gitconfig": {"default"}}
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
			got := drive(disabledModel(), tc.msg)
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
}

func TestRender_DotsDisabledHidesMutatingHints(t *testing.T) {
	m := dotsModel()
	m.settings.DotsDisabled = config.BoolPtr(true)
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
	help := renderHelpPopup(m)
	for _, blocked := range []string{"discover", "sync all", "variant", "delete", "ignore", "use repo", "use local"} {
		if strings.Contains(help, blocked) {
			t.Fatalf("disabled dots help includes %q: %q", blocked, help)
		}
	}
	if !strings.Contains(help, "enable dots") {
		t.Fatalf("disabled dots help should keep enable path: %q", help)
	}
}

// ── UC-45 Dots tab: explicit conflict choice ──────────────────────────────────

func TestFlow_UC45_DotsConflictOverwrite(t *testing.T) {
	conflictModel := func() Model {
		m := baseModel(nil)
		m.mode = viewDots
		m.dotsLoaded = true
		m.settings.DotsRepo = "/repo"
		m.dotsEntries = []app.DotStatus{
			{Name: "gitconfig", Health: app.HealthConflict, State: app.DotStateConflict, Actions: []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove}},
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
		m.settings.DotsRepo = "/repo"
		m.dotsEntries = []app.DotStatus{
			{Name: "claude", State: app.DotStateUntrackedConflict, Actions: []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionIgnore}},
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
		m.settings.DotsRepo = "/repo"
		m.dotsEntries = []app.DotStatus{{Name: "nvim", Health: app.HealthOK, State: app.DotStateSynced}}
		got := drive(m, pressRune('u'))
		if got.dotsOverwriteIdx != -1 {
			t.Errorf("dotsOverwriteIdx = %d, want -1 for non-conflict entry", got.dotsOverwriteIdx)
		}
	})
}

func TestFlow_DotsSynthesizedIgnoredChildUnignore(t *testing.T) {
	// Merged ignored-child entries with Children expand instead of toggling
	// the whole entry; individual children can then be unignored.
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		ConfigPath: "~/.config/nvim",
		Health:     app.HealthOK,
		State:      app.DotStateIgnored,
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
	// Merged ignored entries expand/collapse with space like synced entries.
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsEntries = []app.DotStatus{
		{
			Name:   "nvim",
			State:  app.DotStateSynced,
			Health: app.HealthOK,
			Counts: app.DotFileCounts{Synced: 2},
		},
		{
			Name:       "nvim",
			TargetPath: "~/.config/nvim",
			State:      app.DotStateIgnored,
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

	// Cursor starts on first entry (synced nvim). Move to ignored nvim.
	toIgnored := drive(m, pressRune('j'))

	// Space expands the merged ignored entry.
	expanded := drive(toIgnored, pressRune(' '))
	if expanded.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want nvim after space on merged ignored entry", expanded.dotsExpandedName)
	}
	rows := dotsVisibleRows(expanded)
	if len(rows) != 4 { // synced nvim + ignored nvim + node_modules + auth.json
		t.Fatalf("visible rows = %d, want 4 (synced + ignored parent + 2 children)", len(rows))
	}

	// Space again collapses.
	collapsed := drive(expanded, pressRune(' '))
	if collapsed.dotsExpandedName != "" {
		t.Fatalf("dotsExpandedName = %q, want empty after collapsing", collapsed.dotsExpandedName)
	}
	if rows := dotsVisibleRows(collapsed); len(rows) != 2 {
		t.Fatalf("visible rows after collapse = %d, want 2", len(rows))
	}
}

func TestFlow_DotsExpandIgnoredDoesNotExpandSyncedSameName(t *testing.T) {
	// Regression: expanding an ignored entry must not also expand a synced entry
	// with the same name. The fix introduced dotsExpandedState to scope expansion
	// to the correct section (DotStateSynced vs DotStateIgnored).
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"

	syncedEntry := app.DotStatus{
		Name:   "nvim",
		State:  app.DotStateSynced,
		Health: app.HealthOK,
		Counts: app.DotFileCounts{Synced: 2},
	}
	ignoredEntry := app.DotStatus{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateIgnored,
		Health:     app.HealthOK,
		IsDir:      true,
		Children: []app.DotChild{
			{Name: "node_modules", RelPath: "node_modules", Path: "~/.config/nvim/node_modules", IsDir: true, Depth: 1, Ignored: true},
			{Name: "auth.json", RelPath: "auth.json", Path: "~/.config/nvim/auth.json", Depth: 1, Ignored: true},
		},
	}
	m.dotsEntries = []app.DotStatus{syncedEntry, ignoredEntry}

	// Cursor starts on synced nvim (index 0). Move down to ignored nvim.
	m = drive(m, pressRune('j'))

	// Expand the ignored nvim with space.
	m = drive(m, pressRune(' '))

	// 1. dotsExpandedState must be DotStateIgnored.
	if m.dotsExpandedState != app.DotStateIgnored {
		t.Fatalf("dotsExpandedState = %v, want DotStateIgnored", m.dotsExpandedState)
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
	// Expanding a child directory inside a merged ignored entry works.
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateIgnored,
		Health:     app.HealthOK,
		IsDir:      true,
		Children: []app.DotChild{
			{Name: "lua", RelPath: "lua", Path: "~/.config/nvim/lua", IsDir: true, Depth: 1, Ignored: true, Children: []app.DotChild{
				{Name: "config.lua", RelPath: "lua/config.lua", Path: "~/.config/nvim/lua/config.lua", Depth: 2, Ignored: true},
			}},
			{Name: "init.vim", RelPath: "init.vim", Path: "~/.config/nvim/init.vim", Depth: 1, Ignored: true},
		},
	}}

	// Expand top-level entry.
	expanded := drive(m, pressRune(' '))
	if expanded.dotsExpandedName != "nvim" {
		t.Fatalf("dotsExpandedName = %q, want nvim", expanded.dotsExpandedName)
	}
	rows := dotsVisibleRows(expanded)
	if len(rows) != 3 { // nvim + lua + init.vim
		t.Fatalf("visible rows = %d, want 3", len(rows))
	}

	// Move to lua child and expand it.
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
	// Pressing x on an ignored child within a merged entry dispatches
	// the ignore-pattern removal, not the whole-entry toggle.
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsExpandedName = "nvim"
	m.dotsExpandedState = app.DotStateIgnored
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateIgnored,
		Health:     app.HealthOK,
		IsDir:      true,
		Children: []app.DotChild{
			{Name: "node_modules", RelPath: "node_modules", Path: "~/.config/nvim/node_modules", IsDir: true, Depth: 1, Ignored: true},
		},
	}}

	// j moves to child row, x opens confirmation.
	got := drive(m, pressRune('j'), pressRune('x'))
	if got.dotsIgnoreIdx != 1 {
		t.Fatalf("dotsIgnoreIdx = %d, want 1 (child row confirmation)", got.dotsIgnoreIdx)
	}
}

func TestFlow_DotsChildRowsCanBeIgnored(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsExpandedName = "nvim"
	m.dotsExpandedState = app.DotStateSynced
	m.dotsEntries = []app.DotStatus{{
		Name:    "nvim",
		Health:  app.HealthOK,
		State:   app.DotStateSynced,
		Actions: []app.DotAction{app.DotActionRemove, app.DotActionIgnore},
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
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsEntries = []app.DotStatus{
		{
			Name:   "alpha",
			Health: app.HealthOK,
			State:  app.DotStateSynced,
			Children: []app.DotChild{
				{Name: "one", RelPath: "one", Path: "~/.config/alpha/one"},
				{Name: "two", RelPath: "two", Path: "~/.config/alpha/two"},
			},
		},
		{
			Name:   "beta",
			Health: app.HealthOK,
			State:  app.DotStateSynced,
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
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsEntries = []app.DotStatus{{
		Name:    "nvim",
		State:   app.DotStateConflict,
		Actions: []app.DotAction{app.DotActionUseRepo, app.DotActionUseLocal, app.DotActionRemove, app.DotActionIgnore},
		Children: []app.DotChild{{
			Name:    "init.lua",
			RelPath: "init.lua",
			Path:    "~/.config/nvim/init.lua",
			State:   app.DotStateSynced,
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
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoaded = true
	m.settings.DotsRepo = "/repo"
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateConflict,
		IsDir:      true,
		Children: []app.DotChild{{
			Name:      "lua",
			RelPath:   "lua",
			Path:      "~/.config/nvim/lua",
			State:     app.DotStateConflict,
			IsDir:     true,
			FileCount: 1,
			Children: []app.DotChild{{
				Name:    "config.lua",
				RelPath: "lua/config.lua",
				Path:    "~/.config/nvim/lua/config.lua",
				State:   app.DotStateMissing,
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

// ── UC-46 opCompleteMsg ───────────────────────────────────────────────────────

func TestFlow_UC46_OpCompleteMsg(t *testing.T) {
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

// ── UC-47 progressDoneMsg ─────────────────────────────────────────────────────

func TestFlow_UC47_ProgressDoneMsg(t *testing.T) {
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

// ── UC-48 providerScannedMsg / allProvidersDoneMsg ───────────────────────────

func TestFlow_UC48_ProviderScanMsgs(t *testing.T) {
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

	t.Run("providerScannedMsg shows scan error", func(t *testing.T) {
		m := baseModel(nil)
		m.scanningProviders = map[string]bool{"brew": true}
		got := drive(m, providerScannedMsg{provider: "brew", err: errors.New("db write failed")})
		if !got.statusIsErr {
			t.Fatal("statusIsErr = false, want true")
		}
		if got.statusMsg == "" {
			t.Fatal("statusMsg empty, want scan failure")
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

// ── UC-49 groupChangedMsg ─────────────────────────────────────────────────────

func TestFlow_UC49_GroupChangedMsg(t *testing.T) {
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

// ── UC-50 dangerOpDoneMsg ─────────────────────────────────────────────────────

func TestFlow_UC50_DangerOpDoneMsg(t *testing.T) {
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

// ── UC-51 claimDoneMsg ────────────────────────────────────────────────────────

func TestFlow_UC51_ClaimDoneMsg(t *testing.T) {
	orphan := &database.ToolCache{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
	m := baseModel(nil)
	m.discoveredTools = []*database.ToolCache{orphan}
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

// ── UC-52 ignoreDoneMsg ───────────────────────────────────────────────────────

func TestFlow_UC52_IgnoreDoneMsg(t *testing.T) {
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
}

func TestFlow_SyncAllDoneRemovesClaimedDiscovered(t *testing.T) {
	orphan := &database.ToolCache{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
	m := baseModel(nil)
	m.loading = true
	m.discoveredTools = []*database.ToolCache{orphan}
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

// ── UC-53 migrateProviderDoneMsg ─────────────────────────────────────────────

func TestFlow_UC53_MigrateProviderDoneMsg(t *testing.T) {
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

// ── UC-54 dots tab message handlers ──────────────────────────────────────────

func TestFlow_UC54_DotsMsgs(t *testing.T) {
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
		entries := []app.DotStatus{{Name: "refreshed", Health: app.HealthConflict, State: app.DotStateConflict}}
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

	t.Run("dotsSyncedMsg error still updates conflict entries", func(t *testing.T) {
		entries := []app.DotStatus{{Name: "nvim", Health: app.HealthConflict, State: app.DotStateConflict}}
		got := drive(baseModel(nil), dotsSyncedMsg{entries: entries, err: errors.New("dots sync: nvim: requires choosing use repo version or use local version")})
		if !got.dotsLoaded {
			t.Fatal("dotsLoaded should be true after failed sync refresh")
		}
		if len(got.dotsEntries) != 1 || got.dotsEntries[0].State != app.DotStateConflict {
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

// ── UC-55 dotsDeletedMsg cursor clamp ────────────────────────────────────────

func TestFlow_UC55_DotsDeletedMsg(t *testing.T) {
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

// ── UC-56 dotsFixedMsg ────────────────────────────────────────────────────────

func TestFlow_UC56_DotsFixedMsg(t *testing.T) {
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

// ── UC-57 Migration race guards ───────────────────────────────────────────────

func TestFlow_UC57_MigrationRaceGuards(t *testing.T) {
	// Regression: progressDoneMsg cleared loading mid-migration.
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

// ── UC-58 settingsSavedMsg ────────────────────────────────────────────────────

func TestFlow_UC58_SettingsSavedMsg(t *testing.T) {
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

// ── UC-59 progressMsg ─────────────────────────────────────────────────────────

func TestFlow_UC59_ProgressMsg(t *testing.T) {
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

// ── UC-60 clearStatusMsg ──────────────────────────────────────────────────────

func TestFlow_UC60_ClearStatusMsg(t *testing.T) {
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

// ── UC-61 Command palette execution ──────────────────────────────────────────

func TestFlow_UC61_PaletteExecution(t *testing.T) {
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
			commandCursor: -1,
			upgradingKeys: make(map[string]bool),
		}
		m.commandInput.Focus()
		// Type "zzz" (matches no command) then Enter.
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

// ── UC-62 discoveredRefreshedMsg ─────────────────────────────────────────────

func TestFlow_UC62_DiscoveredRefreshedMsg(t *testing.T) {
	t.Run("success updates discoveredTools", func(t *testing.T) {
		orphan := &database.ToolCache{Name: "fzf", Provider: "brew", Installed: true, Tracked: false}
		got := drive(baseModel(nil), discoveredRefreshedMsg{discovered: []*database.ToolCache{orphan}})
		if len(got.discoveredTools) != 1 {
			t.Errorf("discoveredTools = %d, want 1", len(got.discoveredTools))
		}
	})

	t.Run("success with missing descriptions queues refresh", func(t *testing.T) {
		orphan := &database.ToolCache{Name: "playwright", Provider: "node", Installed: true, Tracked: false}
		m := baseModel(nil)
		cmd := m.handleDiscoveredRefreshedMsg(discoveredRefreshedMsg{discovered: []*database.ToolCache{orphan}})
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
}

// ── UC-63 descRefreshDoneMsg ──────────────────────────────────────────────────

func TestFlow_UC63_DescRefreshDoneMsg(t *testing.T) {
	refreshedDiscovered := []*database.ToolCache{{
		Name:        "playwright",
		Provider:    "node",
		Installed:   true,
		Tracked:     false,
		Description: sql.NullString{String: "browser automation", Valid: true},
	}}
	got := drive(baseModel(nil), descRefreshDoneMsg{tools: threeTools(), discovered: refreshedDiscovered})
	if len(got.allTools) != 3 {
		t.Errorf("allTools = %d, want 3 after descRefreshDoneMsg", len(got.allTools))
	}
	if len(got.discoveredTools) != 1 || got.discoveredTools[0].Description.String != "browser automation" {
		t.Fatalf("discoveredTools = %+v, want refreshed discovered descriptions", got.discoveredTools)
	}

	m := baseModel([]*database.ToolCache{{Name: "fresh", Provider: "brew"}})
	m.discoveredTools = []*database.ToolCache{{Name: "old-orphan", Provider: "brew"}}
	m.descRefreshGen = 2
	got = drive(m, descRefreshDoneMsg{gen: 1, tools: threeTools(), discovered: refreshedDiscovered})
	if len(got.allTools) != 1 || got.allTools[0].Name != "fresh" {
		t.Fatalf("stale descRefreshDoneMsg replaced allTools with %+v", got.allTools)
	}
	if len(got.discoveredTools) != 1 || got.discoveredTools[0].Name != "old-orphan" {
		t.Fatalf("stale descRefreshDoneMsg replaced discoveredTools with %+v", got.discoveredTools)
	}

	m = baseModel([]*database.ToolCache{{Name: "fresh", Provider: "brew"}})
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

// ── UC-64 Mouse wheel scrolling ──────────────────────────────────────────────

func TestFlow_UC64_MouseWheelScroll(t *testing.T) {
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

	t.Run("wheel up at top stays at 0", func(t *testing.T) {
		got := drive(m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
		if got.cursor != 0 {
			t.Errorf("cursor = %d, want 0 (clamped)", got.cursor)
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

// ── UC-65 WindowSizeMsg updates dimensions ────────────────────────────────────

func TestFlow_UC65_WindowSize(t *testing.T) {
	m := baseModel(nil)
	got := drive(m, tea.WindowSizeMsg{Width: 200, Height: 60})
	if got.width != 200 {
		t.Errorf("width = %d, want 200", got.width)
	}
	if got.height != 60 {
		t.Errorf("height = %d, want 60", got.height)
	}
}
