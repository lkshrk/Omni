package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func assertLinesFitWidth(t *testing.T, out string, width int) {
	t.Helper()
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, got, width, out)
		}
	}
}

// ── view_theme.go ─────────────────────────────────────────────────────────────

func TestBuildPaletteFor_Dark(t *testing.T) {
	// Check that colour tokens differ between dark and light.
	darkP := buildPaletteFor(true)
	lightP := buildPaletteFor(false)
	if darkP.colInstalled == lightP.colInstalled {
		t.Error("expected dark and light installed colours to differ")
	}
}

func TestBuildPaletteFor_Light(t *testing.T) {
	p := buildPaletteFor(false)
	// Verify the light palette is non-zero (colInstalled should differ from zero).
	if p.colInstalled == nil {
		t.Error("expected colInstalled to be set in light palette")
	}
}

func TestDefaultPalette_IsDark(t *testing.T) {
	p := defaultPalette()
	darkP := buildPaletteFor(true)
	// Default palette must match the dark variant (same colour tokens).
	if p.colInstalled != darkP.colInstalled {
		t.Error("defaultPalette should return dark variant")
	}
}

func TestApplyTheme_Dark(t *testing.T) {
	m := baseModel(nil)
	m.applyTheme(true)
	darkP := buildPaletteFor(true)
	if m.palette.colInstalled != darkP.colInstalled {
		t.Error("applyTheme(true) should set dark palette")
	}
}

func TestApplyTheme_Light(t *testing.T) {
	m := baseModel(nil)
	m.applyTheme(false)
	lightP := buildPaletteFor(false)
	if m.palette.colInstalled != lightP.colInstalled {
		t.Error("applyTheme(false) should set light palette")
	}
}

// ── view_scroll.go ────────────────────────────────────────────────────────────

func TestApplyScrollWindow_BasicWindow(t *testing.T) {
	content := "line0\nline1\nline2\nline3\nline4\n"
	// avail=3, cursor=1 → lines 0-2
	got := applyScrollWindow(content, 1, 3)
	if !strings.Contains(got, "line0") {
		t.Errorf("expected 'line0' in windowed output, got: %q", got)
	}
	if strings.Contains(got, "line4") {
		t.Errorf("did not expect 'line4' in windowed output, got: %q", got)
	}
}

func TestApplyScrollWindow_CursorAtBottom(t *testing.T) {
	content := "a\nb\nc\nd\ne\n"
	// cursor at line 4, avail=3 → should show c,d,e
	got := applyScrollWindow(content, 4, 3)
	if !strings.Contains(got, "e") {
		t.Errorf("expected last line 'e' in output, got: %q", got)
	}
}

func TestApplyScrollWindow_EmptyContent(t *testing.T) {
	got := applyScrollWindow("", 0, 5)
	if got != "" {
		t.Errorf("expected empty result for empty content, got: %q", got)
	}
}

func TestApplyScrollWindow_AvailLessThanOne(t *testing.T) {
	content := "line0\nline1\n"
	// avail=0 should be clamped to 1
	got := applyScrollWindow(content, 0, 0)
	if got == "" {
		t.Error("expected non-empty result even with avail=0")
	}
}

func TestApplyScrollWindow_TrailingNewline(t *testing.T) {
	// trailing empty entry from strings.Split should be dropped
	content := "a\nb\nc\n"
	got := applyScrollWindow(content, 0, 10)
	// all three lines should be visible
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") || !strings.Contains(got, "c") {
		t.Errorf("expected all lines, got: %q", got)
	}
}

func TestApplyScrollWindow_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		content string
		cursor  int
		avail   int
		wantIn  string
		wantOut string
	}{
		{
			name:    "cursor at top, enough avail",
			content: "L0\nL1\nL2\nL3\n",
			cursor:  0,
			avail:   4,
			wantIn:  "L0",
		},
		{
			name:    "cursor midway, limited avail",
			content: "L0\nL1\nL2\nL3\nL4\n",
			cursor:  3,
			avail:   2,
			wantIn:  "L3",
			wantOut: "L0",
		},
		{
			name:    "single line",
			content: "only\n",
			cursor:  0,
			avail:   5,
			wantIn:  "only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyScrollWindow(tc.content, tc.cursor, tc.avail)
			if tc.wantIn != "" && !strings.Contains(got, tc.wantIn) {
				t.Errorf("expected %q in output, got: %q", tc.wantIn, got)
			}
			if tc.wantOut != "" && strings.Contains(got, tc.wantOut) {
				t.Errorf("did not expect %q in output, got: %q", tc.wantOut, got)
			}
		})
	}
}

// ── view_header.go ────────────────────────────────────────────────────────────

func TestRenderSetup_Step0(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 0
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=0")
	}
	if !strings.Contains(out, "No settings.json") {
		t.Errorf("expected 'No settings.json' in step 0 output, got:\n%s", out)
	}
}

func TestRenderSetup_Step1(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 1
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system(brew)", enabled: true},
		{name: "node", label: "node(bun)", enabled: false},
	}
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=1")
	}
	// Step 1 renders the provider picker — check for provider labels
	if !strings.Contains(out, "system") {
		t.Errorf("expected provider label in step 1, got:\n%s", out)
	}
}

func TestRenderSetup_Step2(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 2
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system(brew)", enabled: true},
		{name: "python", label: "python(uv)", enabled: true},
	}
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=2")
	}
}

func TestRenderSetup_Step3(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 3
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=3")
	}
	if !strings.Contains(out, "Node") {
		t.Errorf("expected 'Node' in step 3 output, got:\n%s", out)
	}
}

func TestRenderSetup_Step4_Normal(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 4
	m.setupExitConfirm = false
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=4 normal")
	}
	if !strings.Contains(out, "profile") {
		t.Errorf("expected 'profile' in step 4 output, got:\n%s", out)
	}
}

func TestRenderSetup_Step4_ExitConfirm(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 4
	m.setupExitConfirm = true
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=4 exit confirm")
	}
	if !strings.Contains(out, "Close") {
		t.Errorf("expected 'Close' in step 4 exit confirm output, got:\n%s", out)
	}
}

func TestRenderSetup_Step5(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 5
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=5")
	}
	if !strings.Contains(out, "dotfile") {
		t.Errorf("expected 'dotfile' in step 5 output, got:\n%s", out)
	}
}

func TestRenderSetup_Step6(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 6
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=6")
	}
}

func TestRenderSetup_WithLoadingSpinner(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 0
	m.loading = true
	m.statusMsg = "creating…"
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output with loading=true")
	}
}

func TestRenderSetup_AllStepsNoPanic(t *testing.T) {
	for step := 0; step <= 6; step++ {
		step := step
		t.Run("step", func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("renderSetup step %d panicked: %v", step, r)
				}
			}()
			m := baseModel(nil)
			m.mode = viewSetup
			m.setupStep = step
			m.setupProviders = []setupProviderRow{
				{name: "system", label: "system(brew)", enabled: true},
			}
			_ = renderSetup(m)
		})
	}
}

func TestRenderHeader_DefaultMode(t *testing.T) {
	m := baseModel(threeTools())
	out := renderHeader(m)
	if out == "" {
		t.Error("expected non-empty header")
	}
	if strings.Contains(out, "mni") {
		t.Errorf("expected compact globe-only logo in header, got: %q", out)
	}
}

func TestRenderHeader_DotsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsEntries = []app.DotStatus{{Name: "nvim"}}
	out := renderHeader(m)
	if !strings.Contains(out, "entries") {
		t.Errorf("expected 'entries' in dots mode header, got: %q", out)
	}
}

func TestRenderHeader_DotsLoadingKeepsCountSummary(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsLoading = true
	m.dotsEntries = []app.DotStatus{{Name: "nvim"}}
	out := renderHeader(m)
	if strings.Contains(out, "loading") {
		t.Errorf("dots refresh status belongs in footer, got loading header: %q", out)
	}
	if !strings.Contains(out, "1 entries") {
		t.Errorf("expected count summary while dots loading, got: %q", out)
	}
}

func TestRenderHeader_DirtyGitStatus(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.dotsGitStatus = "M .zshrc"
	out := renderHeader(m)
	if !strings.Contains(out, "dirty") {
		t.Errorf("expected 'dirty' when dotsGitStatus is set, got: %q", out)
	}
}

func TestRenderHeader_Searching(t *testing.T) {
	m := baseModel(threeTools())
	m.searching = true
	out := renderHeader(m)
	if !strings.Contains(out, "searching") {
		t.Errorf("expected 'searching' when searching=true, got: %q", out)
	}
}

func TestRenderHeader_ScanningProviders(t *testing.T) {
	m := baseModel(threeTools())
	m.scanningProviders = map[string]bool{"brew": true}
	out := renderHeader(m)
	if !strings.Contains(out, "scanning") {
		t.Errorf("expected 'scanning' when scanningProviders non-empty, got: %q", out)
	}
}

func TestRenderHeader_WithUpdates(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true},
	}
	m := baseModel(tools)
	out := renderHeader(m)
	if !strings.Contains(out, "update") {
		t.Errorf("expected 'update' in header with outdated tools, got: %q", out)
	}
}

func TestRenderHeader_WithSearchTools(t *testing.T) {
	m := baseModel(threeTools())
	m.searchTools = []*database.ToolCache{{Name: "extra-tool", Provider: "brew"}}
	out := renderHeader(m)
	if !strings.Contains(out, "found") {
		t.Errorf("expected 'found' in header with search tools, got: %q", out)
	}
}

func TestRenderHeader_RightEdgeMatchesListRows(t *testing.T) {
	m := baseModel([]*database.ToolCache{{
		Name:      "git",
		Provider:  "brew",
		Installed: true,
		Tracked:   true,
	}})
	m.width = 120
	m.applyFilter()
	header := renderHeader(m)

	cols := colWidths{name: 20, prov: 10, group: 8, screenW: m.width}
	row := listRowPrefix(m.palette, true) + renderToolRow(m.palette, m.allTools[0], cols, "", "dev", "", "", "", false, true, syncOK)
	if got, want := lipgloss.Width(header), lipgloss.Width(row); got != want {
		t.Fatalf("header right edge should align with list row right edge: header=%d row=%d\nheader=%q\nrow=%q", got, want, header, row)
	}
}

func TestRenderHeader_UsesSharedEdgePadding(t *testing.T) {
	m := baseModel(threeTools())
	m.width = 100

	header := renderHeader(m)
	if got := visualColumnOf(header, logoMark); got != screenEdgePadding {
		t.Fatalf("logo column = %d, want shared edge padding %d in %q", got, screenEdgePadding, header)
	}
	if got := m.width - lipgloss.Width(header); got != screenEdgePadding {
		t.Fatalf("header right margin = %d, want shared edge padding %d in %q", got, screenEdgePadding, header)
	}
}

func TestRenderHeader_DoesNotShowHostname(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "deskbox.example.com")
	m := baseModel(threeTools())
	m.width = 100

	header := renderHeader(m)
	for _, unwanted := range []string{"deskbox", "deskbox.example.com"} {
		if strings.Contains(header, unwanted) {
			t.Fatalf("header should not show hostname %q: %q", unwanted, header)
		}
	}
}

func TestRenderStatusBar_UsesSharedEdgePadding(t *testing.T) {
	m := baseModel(nil)
	m.width = 100
	m.statusMsg = "ready"

	status := renderStatusBar(m)
	if got := visualColumnOf(status, "ready"); got != screenEdgePadding {
		t.Fatalf("status column = %d, want shared edge padding %d in %q", got, screenEdgePadding, status)
	}
	if got := m.width - lipgloss.Width(status); got != screenEdgePadding {
		t.Fatalf("status right margin = %d, want shared edge padding %d in %q", got, screenEdgePadding, status)
	}
}

func TestRenderTabs_DefaultMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewList
	out := renderTabs(m)
	if !strings.Contains(out, "Tools") {
		t.Errorf("expected 'Tools' tab active in default mode, got: %q", out)
	}
}

func TestRenderTabs_DotsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	out := renderTabs(m)
	if !strings.Contains(out, "Dots") {
		t.Errorf("expected 'Dots' tab in dots mode output, got: %q", out)
	}
}

func TestRenderTabs_ProfilesMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	out := renderTabs(m)
	if !strings.Contains(out, "Profiles") {
		t.Errorf("expected 'Profiles' in profiles mode output, got: %q", out)
	}
}

func TestRenderTabs_SettingsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderTabs(m)
	if !strings.Contains(out, "Settings") {
		t.Errorf("expected 'Settings' in settings mode output, got: %q", out)
	}
}

func TestRenderTabs_HasStableWidthAcrossModes(t *testing.T) {
	m := baseModel(nil)
	base := lipgloss.Width(renderTabs(m))
	for _, mode := range []viewMode{viewDots, viewProfiles, viewSettings} {
		m.mode = mode
		got := lipgloss.Width(renderTabs(m))
		if got != base {
			t.Fatalf("renderTabs width=%d for %v, want %d in active tab mode", got, mode, base)
		}
	}
}

func TestRenderPopupFrame_TitleDoesNotWrapDividerLine(t *testing.T) {
	m := baseModel(nil)
	m.width = 64
	m.height = 28
	content := "body\nline2\nline3"
	popup := renderPopupFrame(m.palette, content, popupFrame{
		Title:    "Setup mode — this title can be long enough to expose divider wrapping issues",
		Width:    46,
		PaddingY: 1,
		PaddingX: 2,
	})

	for _, line := range strings.Split(popup, "\n") {
		if strings.TrimSpace(line) == "─" {
			t.Fatalf("popup divider wrapped onto a 1-char line:\n%s", popup)
		}
	}
}

func TestRenderPalette_Empty(t *testing.T) {
	m := baseModel(nil)
	m.commandSuggestions = nil
	out := renderPalette(m)
	if !strings.Contains(out, "no matching") {
		t.Errorf("expected 'no matching' for empty suggestions, got: %q", out)
	}
}

func TestRenderPalette_WithSuggestions(t *testing.T) {
	m := baseModel(nil)
	m.commandSuggestions = []palCmd{
		{name: "sync", desc: "sync tools"},
		{name: "dots pull", desc: "git pull"},
	}
	m.commandCursor = 0
	out := renderPalette(m)
	if !strings.Contains(out, "sync") {
		t.Errorf("expected 'sync' in palette output, got: %q", out)
	}
	if !strings.Contains(out, "dots pull") {
		t.Errorf("expected 'dots pull' in palette output, got: %q", out)
	}
}

func TestRenderProviderPickerStep_Step1(t *testing.T) {
	m := baseModel(nil)
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system(brew)", enabled: true},
		{name: "node", label: "node(bun)", enabled: false},
		{name: "python", label: "python(uv)", enabled: true},
	}
	m.setupProviderIdx = 0
	out := renderProviderPickerStep(m, 1)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in provider picker step 1, got:\n%s", out)
	}
	if !strings.Contains(out, "node") {
		t.Errorf("expected 'node' in provider picker step 1, got:\n%s", out)
	}
}

func TestRenderProviderPickerStep_Step2(t *testing.T) {
	m := baseModel(nil)
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system(apt)", enabled: true},
	}
	out := renderProviderPickerStep(m, 2)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in provider picker step 2, got:\n%s", out)
	}
}

func TestRenderProviderPickerStep_CheckboxStates(t *testing.T) {
	m := baseModel(nil)
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system(brew)", enabled: true},
		{name: "node", label: "node(bun)", enabled: false},
	}
	out := renderProviderPickerStep(m, 1)
	// enabled uses [✓], disabled uses [ ]
	if !strings.Contains(out, "✓") {
		t.Errorf("expected checkmark for enabled provider, got:\n%s", out)
	}
}

func TestRenderProviderPickerStep_NoPanic_AllDisabled(t *testing.T) {
	m := baseModel(nil)
	m.setupProviders = []setupProviderRow{
		{name: "system", label: "system", enabled: false},
		{name: "node", label: "node", enabled: false},
		{name: "python", label: "python", enabled: false},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("renderProviderPickerStep panicked: %v", r)
		}
	}()
	_ = renderProviderPickerStep(m, 1)
}

// ── view_settings.go ─────────────────────────────────────────────────────────

func TestRenderSettings_Basic(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	if out == "" {
		t.Error("expected non-empty settings output")
	}
	if !strings.Contains(out, "Node Manager") {
		t.Errorf("expected 'Node Manager' in settings, got:\n%s", out)
	}
}

func TestRenderSettings_SectionHeaders(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	sections := []string{"Tools", "Managers", "Dotfiles", "Maintenance"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("expected section %q in settings, got:\n%s", s, out)
		}
	}
}

func TestRenderSettings_LabelOrderAndLegacyNames(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)

	labels := []struct {
		name   string
		header bool
	}{
		{"Tools", true},
		{"Import Installed Tools", false},
		{"System Provider Order", false},
		{"System Provider", false},
		{"Node Provider", false},
		{"Python Provider", false},
		{"Managers", true},
		{"Node Manager", false},
		{"Python Manager", false},
		{"Dotfiles", true},
		{"Repository", false},
		{"Sync on This Machine", false},
		{"Commit Changes", false},
		{"Push Changes", false},
		{"Maintenance", true},
		{"Reset Settings", false},
		{"Reset Cache", false},
	}
	prev := -1
	for _, label := range labels {
		idx := settingsLabelLineIndex(out, label.name, label.header)
		if idx < 0 {
			t.Fatalf("settings label %q missing from output:\n%s", label.name, out)
		}
		if idx <= prev {
			t.Fatalf("settings label %q rendered out of order:\n%s", label.name, out)
		}
		prev = idx
	}

	legacy := []string{
		"Auto Import",
		"System Priority",
		"System Ecosystem",
		"Node Ecosystem",
		"Python Ecosystem",
		"Dots Repo",
		"Dots Sync",
		"Auto Commit",
		"Auto Push",
		"Danger Zone",
		"Ecosystems",
	}
	for _, old := range legacy {
		if strings.Contains(out, old) {
			t.Fatalf("legacy settings label %q should not render:\n%s", old, out)
		}
	}
}

func TestRenderSettings_AutoImportON(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.AutoImport = true
	out := renderSettings(m)
	if !strings.Contains(out, "ON") {
		t.Errorf("expected 'ON' for AutoImport=true, got:\n%s", out)
	}
}

func TestRenderSettings_OnlySelectedRowShowsDetail(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = 5
	out := renderSettings(m)

	if strings.Contains(out, "Add newly installed tools") {
		t.Fatalf("unselected import help should not render in compact settings view:\n%s", out)
	}
	if !strings.Contains(out, "JS package manager") {
		t.Fatalf("selected Node Manager help should render:\n%s", out)
	}
}

func TestRenderSettings_DisabledProvider(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.DisabledProviders = []string{"node"}
	out := renderSettings(m)
	// node provider should show [OFF]
	if !strings.Contains(out, "OFF") {
		t.Errorf("expected 'OFF' for disabled node provider, got:\n%s", out)
	}
}

func TestRenderSettings_NodeManagerSet(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.SetEcosystemManager("node", "bun")
	out := renderSettings(m)
	if !strings.Contains(out, "bun") {
		t.Errorf("expected 'bun' in settings when node manager=bun, got:\n%s", out)
	}
}

func TestRenderSettings_DotsRepo(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.DotsRepo = "/home/user/dotfiles"
	out := renderSettings(m)
	if !strings.Contains(out, "dotfiles") {
		t.Errorf("expected dotfiles path in settings output, got:\n%s", out)
	}
}

func TestRenderSettings_CursorHighlight(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = 0
	out := renderSettings(m)
	// cursor row uses the list cursor marker.
	if !strings.Contains(out, selectedRowMarker) {
		t.Errorf("expected cursor marker %q in settings output, got:\n%s", selectedRowMarker, out)
	}
}

func TestRenderSettings_DangerConfirmRow(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.dangerConfirmRow = 11 // Reset Settings row
	out := renderSettings(m)
	if !strings.Contains(out, "confirm") {
		t.Errorf("expected 'confirm' in danger confirm row output, got:\n%s", out)
	}
	if strings.Contains(out, "press enter to confirm") || strings.Contains(out, "execute") || strings.Contains(out, "cancel") {
		t.Errorf("danger confirm row should render one confirm hint only, got:\n%s", out)
	}
}

func TestRenderSettings_EditingPriority(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = 1
	m.editingPriority = true
	m.priorityDraft = []string{"brew", "apt"}
	out := renderSettings(m)
	if !strings.Contains(out, "editing") {
		t.Errorf("expected 'editing' in priority editing state, got:\n%s", out)
	}
}

func TestRenderSettings_DotsDisableKeepLocalPrompt(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowDotsSync
	m.dangerConfirmRow = settingsRowDotsSync
	out := renderSettings(m)
	for _, want := range []string{"disable dotfile sync", "keep local?", "yes", "no"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dots disable prompt, got:\n%s", want, out)
		}
	}
}

func TestRenderSettings_ProviderPriority(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.SetEcosystemPriority("system", []string{"brew", "pip", "npm", "apt"})
	out := renderSettings(m)
	if !strings.Contains(out, "brew") {
		t.Errorf("expected 'brew' in provider priority row, got:\n%s", out)
	}
	if strings.Contains(out, "pip") || strings.Contains(out, "npm") {
		t.Errorf("system priority row should not render non-system managers, got:\n%s", out)
	}
}

func TestRenderSettings_AutoPushImpliesAutoCommit(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settings.DotsGit.AutoPush = true
	out := renderSettings(m)
	// AutoCommit row should display "──" when AutoPush is on
	if !strings.Contains(out, "──") {
		t.Errorf("expected '──' (disabled indicator) when AutoPush=true, got:\n%s", out)
	}
}

func TestRenderProfiles_Empty(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{},
	}
	out := renderProfiles(m)
	if out == "" {
		t.Error("expected non-empty profiles output")
	}
	if !strings.Contains(out, "No profiles") {
		t.Errorf("expected 'No profiles' when empty, got:\n%s", out)
	}
}

func TestRenderProfiles_WithProfiles(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{"dev"}},
			"home": {Groups: []string{"personal"}},
		},
	}
	m.profileCursor = 0
	out := renderProfiles(m)
	if !strings.Contains(out, "work") {
		t.Errorf("expected 'work' in profiles output, got:\n%s", out)
	}
	if !strings.Contains(out, "home") {
		t.Errorf("expected 'home' in profiles output, got:\n%s", out)
	}
}

func TestRenderProfiles_ProfileGroupsAlphabetical(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{"work", "apps", "base"}},
		},
	}
	out := renderProfiles(m)
	if !strings.Contains(out, "apps, base, work") {
		t.Errorf("expected sorted profile groups in profiles output, got:\n%s", out)
	}
}

func TestRenderProfiles_ActiveProfileMarker(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{"dev"}},
		},
	}
	out := renderProfiles(m)
	if strings.Contains(out, "*") {
		t.Errorf("active profile should not render a second row marker, got:\n%s", out)
	}
}

func TestRenderProfiles_ProfileRequired(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileRequired = true
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{},
	}
	out := renderProfiles(m)
	if !strings.Contains(out, "No active profile") {
		t.Errorf("expected 'No active profile' banner, got:\n%s", out)
	}
}

func TestRenderProfiles_WithGroups(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{},
	}
	m.groupNames = []string{"dev", "personal"}
	m.toolGroups = map[string]string{
		toolKey("git", "brew"): "dev",
	}
	out := renderProfiles(m)
	if !strings.Contains(out, "Groups") {
		t.Errorf("expected 'Groups' section, got:\n%s", out)
	}
}

func TestRenderProfiles_NoBlankLineDirectlyAfterDivider(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work":    {Groups: []string{"dev"}},
			"home":    {Groups: []string{}},
			"acme":    {Groups: []string{"ops"}},
			"profile": {Groups: []string{"team"}},
		},
		Hostnames: map[string]string{
			"laptop": "work",
			"server": "home",
		},
	}
	m.groupNames = []string{"dev", "ops", "team"}
	out := renderProfiles(m)

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.Contains(line, "─") && strings.Contains(line, "Profiles") {
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				t.Fatalf("profile section divider has blank row below it:\n%s", out)
			}
		}
		if strings.Contains(line, "─") && strings.Contains(line, "Groups") {
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				t.Fatalf("groups divider has blank row below it:\n%s", out)
			}
		}
	}
}

func TestRenderProfiles_NilProfileInfo(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = nil
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("renderProfiles panicked with nil profileInfo: %v", r)
		}
	}()
	_ = renderProfiles(m)
}

func TestRenderProfiles_ProfileCreating(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.width = 80
	m.height = 30
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{},
	}
	m.profileCreating = true
	out := m.viewString()
	for _, want := range []string{"New Profile", "profile name", "enter create", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in new profile popup, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Errorf("new profile should render through the shared popup frame, got:\n%s", out)
	}
	if strings.Contains(renderProfiles(m), "New profile —") {
		t.Errorf("new profile input should not render inline:\n%s", renderProfiles(m))
	}
}

func TestRenderProfiles_GroupCreating(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.width = 80
	m.height = 30
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{},
	}
	m.groupCreating = true
	out := m.viewString()
	for _, want := range []string{"New Group", "group name", "enter create", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in new group popup, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Errorf("new group should render through the shared popup frame, got:\n%s", out)
	}
	if strings.Contains(renderProfiles(m), "New group —") {
		t.Errorf("new group input should not render inline:\n%s", renderProfiles(m))
	}
}

func TestPlacePopup_ClampsTallContentToTerminalHeight(t *testing.T) {
	m := baseModel(nil)
	m.width = 80
	m.height = 12
	var body strings.Builder
	for i := 0; i < 30; i++ {
		prefix := "  "
		if i == 18 {
			prefix = "› "
		}
		body.WriteString(fmt.Sprintf("%sitem %02d\n", prefix, i))
	}
	body.WriteString("────────────────────────\n")
	body.WriteString("enter save  ·  esc cancel")

	bg := strings.Repeat("\n", m.height-1)
	out := placePopup(bg, m, body.String(), popupFrame{
		Title:          "Tall Popup",
		PaddingY:       1,
		PaddingX:       2,
		Width:          40,
		NoTitleDivider: true,
	})
	if h := lipgloss.Height(out); h > m.height {
		t.Fatalf("popup output height = %d, want <= %d:\n%s", h, m.height, out)
	}
	for _, want := range []string{"item 18", "enter save"} {
		if !strings.Contains(out, want) {
			t.Fatalf("clamped popup missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "item 00") {
		t.Fatalf("clamped popup should scroll around the selected row, got:\n%s", out)
	}
}

func TestProfileGroupToolsPopup_FilterKeepsDimensions(t *testing.T) {
	m := profilesModel()
	m.mode = viewProfileGroupTools
	m.width = 100
	m.height = 40
	m.groupToolsEditor.group = "work"
	m.effectiveSystemManager = "brew"
	m.effectiveNodeManager = "pnpm"
	m.effectivePythonManager = "uv"
	m.allTools = []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", InstalledWith: "brew", Tracked: true},
		{Name: "fd", Provider: "system", InstalledWith: "brew", Tracked: true},
		{Name: "eslint", Provider: "node", InstalledWith: "pnpm", Tracked: true},
		{Name: "prettier", Provider: "node", InstalledWith: "pnpm", Tracked: true},
		{Name: "ruff", Provider: "python", InstalledWith: "uv", Tracked: true},
	}
	m.groupToolsEditor.membership = map[string]bool{"ripgrep": true}
	m.groupToolsIgnore = map[string]bool{"ruff": true}
	bg := strings.Repeat("\n", m.height-1)
	full := placePopup(bg, m, renderProfileGroupToolsEditor(m), profileGroupToolsPopupFrame(m))

	filtered := m
	filtered.groupToolsProviderIdx = 2 // node
	filtered.groupToolsEditor.search = "eslint"
	narrow := placePopup(bg, filtered, renderProfileGroupToolsEditor(filtered), profileGroupToolsPopupFrame(filtered))
	if lipgloss.Width(narrow) != lipgloss.Width(full) || lipgloss.Height(narrow) != lipgloss.Height(full) {
		t.Fatalf("filtered popup dimensions changed: full=%dx%d filtered=%dx%d\nfull:\n%s\nfiltered:\n%s",
			lipgloss.Width(full), lipgloss.Height(full), lipgloss.Width(narrow), lipgloss.Height(narrow), full, narrow)
	}
}

func TestRenderProfiles_DeleteConfirm(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{}},
		},
	}
	m.profileCursor = 0
	m.profileDeleteConfirm = true
	out := renderProfiles(m)
	if !strings.Contains(out, "press d again to confirm delete") {
		t.Errorf("expected red delete confirmation in output, got:\n%s", out)
	}
	if strings.Contains(out, "esc cancel") || strings.Contains(out, "d confirm delete") {
		t.Errorf("profile delete confirmation should not render confirm/cancel hints, got:\n%s", out)
	}
}

func TestRenderProfiles_HostnameMappingsSectionRemoved(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{}},
		},
		Hostnames: map[string]string{
			"mymachine": "work",
		},
	}
	out := renderProfiles(m)
	if strings.Contains(out, "Hostname Mappings") || strings.Contains(out, "mymachine [this host]") {
		t.Errorf("hostname mappings should not render as a standalone section:\n%s", out)
	}
}

func TestRenderProfiles_ProfileAndGroupSummaries(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "mymachine")
	m := baseModel(nil)
	m.mode = viewProfiles
	m.groupNames = []string{"dev", "personal"}
	m.toolGroups = map[string]string{
		toolKey("git", "system"): "dev",
	}
	m.profileInfo = &app.ProfileInfo{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Groups: []string{"dev"}},
			"home": {Groups: []string{"personal"}},
		},
		Hostnames: map[string]string{
			"mymachine": "work",
			"laptop":    "work",
		},
	}
	m.profileCursor = 1

	out := renderProfiles(m)

	for _, want := range []string{"1 group", "2 hosts", "groups: dev", "1 tool", "0 dotfiles", "hosts: laptop, mymachine"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderProfiles missing %q:\n%s", want, out)
		}
	}
	hostLine := m.palette.styleProvider.Render(textRowContentPrefix() + "hosts: laptop, mymachine")
	if !strings.Contains(out, hostLine) {
		t.Fatalf("selected profile host detail should use host-column style; missing %q in:\n%s", hostLine, out)
	}
}

func TestRenderProfiles_ProfileAndGroupColumnsAlign(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{
			"long-profile": {Groups: []string{"short"}},
		},
		Hostnames: map[string]string{"host": "long-profile"},
	}
	m.groupNames = []string{"very-long-group"}
	m.toolGroups = map[string]string{toolKey("git", "system"): "very-long-group"}

	out := renderProfiles(m)
	profileLine := renderedLineContaining(out, "long-profile")
	groupLine := renderedLineContaining(out, "very-long-group")
	if profileLine == "" || groupLine == "" {
		t.Fatalf("missing profile/group rows:\n%s", out)
	}
	profileSecondCol := visualColumnOf(profileLine, "1 group")
	groupSecondCol := visualColumnOf(groupLine, "1 tool")
	profileThirdCol := visualColumnOf(profileLine, "1 host")
	groupThirdCol := visualColumnOf(groupLine, "0 dotfiles")
	if profileSecondCol < 0 || groupSecondCol < 0 || profileThirdCol < 0 || groupThirdCol < 0 {
		t.Fatalf("missing expected count columns:\nprofile=%q\ngroup=%q", profileLine, groupLine)
	}
	if profileSecondCol != groupSecondCol || profileThirdCol != groupThirdCol {
		t.Fatalf("profile/group columns not aligned:\nprofile=%q\ngroup=%q", profileLine, groupLine)
	}
	wantSecondCol := rowMarkerWidth + colsNameWidthForProfileTest(m) + firstColumnGap
	if profileSecondCol != wantSecondCol {
		t.Fatalf("profile second column = %d, want fixed left layout column %d:\n%s", profileSecondCol, wantSecondCol, profileLine)
	}
	if profileSecondCol >= m.width/2 {
		t.Fatalf("profile summary columns should not be pulled to the right edge:\n%s", profileLine)
	}
	secondToThirdGap := profileThirdCol - profileSecondCol - lipgloss.Width("1 group")
	if secondToThirdGap < listColumnGap {
		t.Fatalf("second and third columns too tight: gap=%d line=%q", secondToThirdGap, profileLine)
	}

	activeGroupCount := listRowColumnStyle(true, m.palette.styleHelp).Render("1 group")
	activeHostCount := listRowColumnStyle(true, m.palette.styleProvider).Render("1 host")
	if !strings.Contains(profileLine, activeGroupCount) || !strings.Contains(profileLine, activeHostCount) {
		t.Fatalf("selected profile count columns should use active weight:\n%s", profileLine)
	}

	m.profileSection = 1
	for i, group := range buildAllGroupNames(m.groupNames) {
		if group == "very-long-group" {
			m.groupCursor = i
			break
		}
	}
	out = renderProfiles(m)
	groupLine = renderedLineContaining(out, "very-long-group")
	activeToolCount := listRowColumnStyle(true, m.palette.styleHelp).Render("1 tool")
	activeDotCount := listRowColumnStyle(true, m.palette.styleProvider).Render("0 dotfiles")
	if !strings.Contains(groupLine, activeToolCount) || !strings.Contains(groupLine, activeDotCount) {
		t.Fatalf("selected group count columns should use active weight:\n%s", groupLine)
	}
}

func visualColumnOf(line, needle string) int {
	idx := strings.Index(line, needle)
	if idx < 0 {
		return -1
	}
	return lipgloss.Width(line[:idx])
}

func colsNameWidthForProfileTest(m Model) int {
	names := sortedProfileNames(m.profileInfo)
	allGroupNames := buildAllGroupNames(m.groupNames)
	hostCounts := profileHostCounts(m.profileInfo)
	groupCounts := make(map[string]int, len(allGroupNames))
	for _, gn := range m.toolGroups {
		groupCounts[gn]++
	}
	groupDots := make(map[string]int, len(allGroupNames))
	for _, groups := range m.dotMemberships {
		for _, gn := range groups {
			groupDots[gn]++
		}
	}
	return profileTableColumnWidths(names, m.profileInfo, hostCounts, allGroupNames, groupCounts, groupDots).name
}

func TestRenderProfiles_ProfileActionsAndRename(t *testing.T) {
	m := profilesModel()
	m.profileCursor = 0
	out := renderProfiles(m)
	for _, want := range []string{"space activate profile", "r rename", "g edit groups", "h edit hosts", "d delete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("profile row missing action %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "set default") {
		t.Fatalf("profile row should not offer set default:\n%s", out)
	}

	m.profileRenameMode = true
	m.settingsInput.SetValue("alpha")
	out = renderProfiles(m)
	if !strings.Contains(out, "Rename:") || !strings.Contains(out, "enter save") {
		t.Fatalf("profile rename mode missing input/save hint:\n%s", out)
	}
}

func TestRenderProfiles_GroupActions(t *testing.T) {
	m := profilesModel()
	m.profileSection = 1
	for i, group := range buildAllGroupNames(m.groupNames) {
		if group == "work" {
			m.groupCursor = i
			break
		}
	}
	out := renderProfiles(m)
	for _, want := range []string{"r rename", "t edit tools", "f edit dotfiles", "d delete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group row missing action %q:\n%s", want, out)
		}
	}
	assertOrderedSubstrings(t, out, "r rename", "t edit tools", "f edit dotfiles", "d delete")
	for _, old := range []string{"D delete"} {
		if strings.Contains(out, old) {
			t.Fatalf("group row should not show old action %q:\n%s", old, out)
		}
	}

	m.groupCursor = 0 // base
	out = renderProfiles(m)
	for _, disallowed := range []string{"r rename", "d delete"} {
		if strings.Contains(out, disallowed) {
			t.Fatalf("base group should not show disabled action %q:\n%s", disallowed, out)
		}
	}
}

func assertOrderedSubstrings(t *testing.T, out string, wants ...string) {
	t.Helper()
	previous := -1
	for _, want := range wants {
		idx := strings.Index(out, want)
		if idx < 0 {
			t.Fatalf("missing %q:\n%s", want, out)
		}
		if idx <= previous {
			t.Fatalf("%q should appear after previous action in %v:\n%s", want, wants, out)
		}
		previous = idx
	}
}

func TestRenderProfileGroupToolsEditor(t *testing.T) {
	m := profilesModel()
	m.mode = viewProfileGroupTools
	m.groupToolsEditor.group = "work"
	m.effectiveSystemManager = "brew"
	m.effectiveNodeManager = "pnpm"
	m.allTools = []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true},
		{Name: "eslint", Provider: "node", Package: "eslint", Installed: true, InstalledWith: "pnpm", Tracked: true},
		{Name: "ruff", Provider: "python", Installed: false, Tracked: true},
	}
	m.toolMemberships = map[string][]string{
		toolKey("ripgrep", "system"): []string{"work"},
	}
	m.groupToolsEditor.membership = map[string]bool{"ripgrep": true, "eslint": false, "ruff": false}
	m.groupToolsEditor.originalMembership = copyBoolMap(m.groupToolsEditor.membership)
	m.groupToolsIgnore = map[string]bool{"ruff": true}
	m.groupToolsOriginalIgnore = copyBoolMap(m.groupToolsIgnore)
	m.groupToolsEditor.cursor = 0

	out := renderProfileGroupToolsEditor(m)
	for _, want := range []string{"[all]", "system", "node", "python", "enabled", "disabled", "ignored", "[x]", "ripgrep", "system(", "brew", "[ ]", "eslint", "node(", "pnpm", "ruff", "ignored", "space disable", "x ignore", "/ search", "[] filter", "enter save", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group tools editor missing %q:\n%s", want, out)
		}
	}

	m.groupToolsProviderIdx = 2
	out = renderProfileGroupToolsEditor(m)
	if !strings.Contains(out, "[node]") || strings.Contains(out, "provider:") || strings.Contains(out, "ripgrep") {
		t.Fatalf("provider filter should narrow rows to node tools:\n%s", out)
	}
}

func TestRenderProfileGroupDotsEditor(t *testing.T) {
	m := profilesModel()
	m.mode = viewProfileGroupDots
	m.groupDotsEditor.group = "work"
	m.dotMemberships = map[string][]string{
		"nvim": {"work"},
		"zsh":  {"base"},
	}
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
		{Name: "zsh", TargetPath: "~/.zshrc", Health: app.HealthMissing},
	}
	m.groupDotsEditor.membership = map[string]bool{"nvim": true, "zsh": false}
	m.groupDotsEditor.originalMembership = copyBoolMap(m.groupDotsEditor.membership)

	out := renderProfileGroupDotsEditor(m)
	for _, want := range []string{"enabled", "disabled", "[x]", "nvim", "~/.config/nvim", "[ ]", "zsh", "space disable", "/ search", "enter save", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group dots editor missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"ok", "missing"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("group dots editor should no longer render health column %q:\n%s", unwanted, out)
		}
	}

	m.groupDotsEditor.search = "zsh"
	out = renderProfileGroupDotsEditor(m)
	if strings.Contains(out, "nvim") || !strings.Contains(out, "zsh") {
		t.Fatalf("search should narrow rows to zsh:\n%s", out)
	}
}

func TestRenderProfileGroupDotsEditor_GroupsIgnoredSeparately(t *testing.T) {
	m := profilesModel()
	m.mode = viewProfileGroupDots
	m.groupDotsEditor.group = "work"
	m.dotMemberships = map[string][]string{
		"nvim":    {"work"},
		"copilot": {"work"},
	}
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced},
		{Name: "copilot", TargetPath: "~/.config/copilot", State: app.DotStateIgnored},
	}
	m.groupDotsEditor.membership = map[string]bool{"nvim": true, "copilot": true}
	m.groupDotsEditor.originalMembership = copyBoolMap(m.groupDotsEditor.membership)

	out := renderProfileGroupDotsEditor(m)
	enabledIdx := strings.Index(out, "enabled")
	ignoredIdx := strings.Index(out, "ignored")
	if enabledIdx < 0 || ignoredIdx < 0 {
		t.Fatalf("missing enabled/ignored section labels:\n%s", out)
	}
	if ignoredIdx < enabledIdx {
		t.Fatalf("ignored section should come after enabled:\n%s", out)
	}
	copilotIdx := strings.Index(out, "copilot")
	if copilotIdx < ignoredIdx {
		t.Fatalf("copilot row should appear under the ignored section:\n%s", out)
	}
}

func TestRenderProfileGroupEditor(t *testing.T) {
	m := profilesModel()
	m.profileEditMode = 1
	m.profileEditName = "alpha"
	m.profileGroupPicker = []string{"base", "work", groupPickerNewSentinel}
	m.profileGroupDraft = []string{"work"}
	m.profileGroupIdx = 1
	out := renderProfileGroupEditor(m)
	for _, want := range []string{"[x]", "work", "[ ]", "base", "+ new group", "space toggle", "enter save"} {
		if !strings.Contains(out, want) {
			t.Fatalf("profile group editor missing %q:\n%s", want, out)
		}
	}
}

func TestRenderProfileHostEditor(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "myhost")
	m := profilesModel()
	m.profileEditMode = 2
	m.profileEditName = "alpha"
	m.profileHostPicker = []string{"myhost", "otherhost"}
	m.profileHostDraft = map[string]string{"myhost": "alpha", "otherhost": "beta"}
	m.profileCursor = 0
	out := renderProfileHostEditor(m)
	for _, want := range []string{"[x]", "myhost", "alpha · this host", "[ ]", "otherhost", "beta", "space toggle", "enter save"} {
		if !strings.Contains(out, want) {
			t.Fatalf("profile host editor missing %q:\n%s", want, out)
		}
	}
}

func TestRenderProfileHostEditorUsesCapturedProfileAfterCursorMoves(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "myhost")
	m := profilesModel()
	m.profileEditMode = 2
	m.profileEditName = "alpha"
	m.profileHostPicker = []string{"myhost", "otherhost"}
	m.profileHostDraft = map[string]string{"myhost": "alpha", "otherhost": "beta"}
	m.profileCursor = 1

	out := renderProfileHostEditor(m)
	if !strings.Contains(out, "[x]") || !strings.Contains(out, "myhost") {
		t.Fatalf("captured alpha host should remain checked after cursor move:\n%s", out)
	}
	if !strings.Contains(out, "[ ]") || !strings.Contains(out, "otherhost") {
		t.Fatalf("beta host should not be checked while editing captured alpha:\n%s", out)
	}
}

func TestViewProfileEditorTitlesUseCapturedProfileAfterCursorMoves(t *testing.T) {
	m := profilesModel()
	m.width = 100
	m.height = 40
	m.profileEditMode = 1
	m.profileEditName = "alpha"
	m.profileGroupPicker = []string{"base", "work"}
	m.profileCursor = 1

	out := m.viewString()
	if !strings.Contains(out, "Edit Groups: alpha") {
		t.Fatalf("profile group editor title should use captured profile:\n%s", out)
	}
	if strings.Contains(out, "Edit Groups: beta") {
		t.Fatalf("profile group editor title used live cursor:\n%s", out)
	}

	m.profileEditMode = 2
	m.profileHostPicker = []string{"myhost"}
	m.profileHostDraft = map[string]string{"myhost": "alpha"}
	out = m.viewString()
	if !strings.Contains(out, "Edit Hosts: alpha") {
		t.Fatalf("profile host editor title should use captured profile:\n%s", out)
	}
	if strings.Contains(out, "Edit Hosts: beta") {
		t.Fatalf("profile host editor title used live cursor:\n%s", out)
	}
}

func TestToggleProvider_AddNew(t *testing.T) {
	disabled := []string{"node"}
	result := toggleProvider(disabled, "python")
	found := false
	for _, d := range result {
		if d == "python" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'python' to be added to disabled list")
	}
}

func TestToggleProvider_RemoveExisting(t *testing.T) {
	disabled := []string{"node", "python"}
	result := toggleProvider(disabled, "node")
	for _, d := range result {
		if d == "node" {
			t.Error("expected 'node' to be removed from disabled list")
		}
	}
}

func TestToggleProvider_EmptyList(t *testing.T) {
	result := toggleProvider(nil, "system")
	if len(result) != 1 || result[0] != "system" {
		t.Errorf("expected [system], got %v", result)
	}
}

func TestRenderGroupPicker_NoToolSelected(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroupPicker
	// cursor=-1 → no tool selected
	m.cursor = -1
	out := renderGroupPicker(m)
	if !strings.Contains(out, "no tool") {
		t.Errorf("expected 'no tool' when no tool selected, got: %q", out)
	}
}

func TestRenderGroupPicker_WithTool(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerGroups = []string{"dev", "personal", "+ new group…"}
	m.pickerCursor = 0
	out := renderGroupPicker(m)
	if !strings.Contains(out, "dev") {
		t.Errorf("expected 'dev' in group picker, got: %q", out)
	}
}

func TestRenderGroupPicker_SeparatesActiveAndInactiveGroups(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerGroups = []string{"base", "work", "personal", "+ new group…"}
	m.profileInfo = &app.ProfileInfo{
		Active: "main",
		Profiles: map[string]config.Profile{
			"main": {Groups: []string{"base", "work"}},
		},
	}
	out := renderGroupPicker(m)
	for _, want := range []string{"current profile", "inactive groups", "base", "work", "personal"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group picker missing %q:\n%s", want, out)
		}
	}
}

func TestRenderGroupMembershipPicker_SeparatesActiveAndInactiveGroups(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupMembership
	m.cursor = 0
	m.pickerGroups = []string{"base", "work", "personal"}
	m.toolMemberships = map[string][]string{
		toolMembershipKey(m.selectedTool()): []string{"base", "personal"},
	}
	m.profileInfo = &app.ProfileInfo{
		Active: "main",
		Profiles: map[string]config.Profile{
			"main": {Groups: []string{"base", "work"}},
		},
	}
	out := renderGroupMembershipPicker(m)
	for _, want := range []string{"current profile", "inactive groups", "base", "work", "personal", "space", "enter", "save"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group membership picker missing %q:\n%s", want, out)
		}
	}
}

func TestRenderGroupPicker_ClaimPurpose(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerPurposeClaim = true
	m.pickerGroups = []string{"base"}
	out := m.viewString()
	if !strings.Contains(out, "Add To Config: git") {
		t.Errorf("expected add-to-config wording in picker, got: %q", out)
	}
}

func TestRenderGroupPicker_CreatingGroup(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 1
	m.pickerCreatingGroup = true
	out := renderGroupPicker(m)
	// When creating, settingsInput view is shown instead of sentinel
	if out == "" {
		t.Error("expected non-empty output when creating group")
	}
}

func TestRenderGroupPicker_CreatingGroupKeepsSize(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerGroups = []string{"base", "+ new group…"}
	m.pickerCursor = 1

	before := renderGroupPicker(m)

	m.pickerCreatingGroup = true
	m.settingsInput.SetValue("")
	m.settingsInput.Placeholder = "new group name…"
	m.settingsInput.Focus()
	after := renderGroupPicker(m)

	if lipgloss.Width(after) != lipgloss.Width(before) {
		t.Fatalf("width changed after entering create mode: before=%d after=%d", lipgloss.Width(before), lipgloss.Width(after))
	}
	if lipgloss.Height(after) != lipgloss.Height(before) {
		t.Fatalf("height changed after entering create mode: before=%d after=%d", lipgloss.Height(before), lipgloss.Height(after))
	}
}

func TestViewString_GroupPickerTitle(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerGroups = []string{"base", "+ new group…"}

	out := m.viewString()
	if !strings.Contains(out, "Choose Group: git") {
		t.Errorf("expected group picker title in overlay, got: %q", out)
	}
}

func TestViewString_GroupPickerTitleUsesCapturedToolAfterCursorMoves(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: true},
		{Name: "zoxide", Provider: "system", Installed: true},
	})
	m.openGroupPicker(true)
	m.cursor = 1

	out := m.viewString()
	if !strings.Contains(out, "Add To Config: ripgrep") {
		t.Fatalf("group picker title should use captured tool:\n%s", out)
	}
	if strings.Contains(out, "Add To Config: zoxide") {
		t.Fatalf("group picker title used live cursor:\n%s", out)
	}
}

func TestViewString_GroupMembershipTitleUsesCapturedToolAfterCursorMoves(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Tracked: true},
		{Name: "zoxide", Provider: "system", Tracked: true},
	})
	m.openGroupMembershipPicker()
	m.cursor = 1

	out := m.viewString()
	if !strings.Contains(out, "Edit Groups: ripgrep") {
		t.Fatalf("membership picker title should use captured tool:\n%s", out)
	}
	if strings.Contains(out, "Edit Groups: zoxide") {
		t.Fatalf("membership picker title used live cursor:\n%s", out)
	}
}

func TestViewString_ProviderScopeTitleIncludesTool(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewProviderScope
	m.cursor = 0
	m.scopeOptions = providerScopeOptions(m.selectedTool())

	out := m.viewString()
	if !strings.Contains(out, "Pin Provider: git") {
		t.Errorf("expected provider scope title in overlay, got: %q", out)
	}
	if strings.Contains(renderScopePicker(m), "git") {
		t.Errorf("scope picker content should not render a second tool headline, got: %q", renderScopePicker(m))
	}
}

func TestViewString_ScopeTitleUsesCapturedToolAfterCursorMoves(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true},
		{Name: "zoxide", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true},
	})
	m.openProviderScopePicker(m.selectedTool())
	m.cursor = 1

	out := m.viewString()
	if !strings.Contains(out, "Pin Provider: ripgrep") {
		t.Fatalf("scope picker title should use captured tool:\n%s", out)
	}
	if strings.Contains(out, "Pin Provider: zoxide") {
		t.Fatalf("scope picker title used live cursor:\n%s", out)
	}
}

func TestRenderSettings_InlineHintsUseSharedIndent(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	line := renderedLineContaining(out, "change")
	if line == "" {
		t.Fatalf("settings hints missing from output:\n%s", out)
	}
	if !strings.HasPrefix(line, textRowHintPrefix()) {
		t.Fatalf("settings hint line should use shared indent %q, got %q", textRowHintPrefix(), line)
	}
	if strings.Contains(line, "back") || strings.Contains(line, "cancel") {
		t.Fatalf("default toggle settings hint should not show back/cancel, got %q", line)
	}
}

func TestSelectedRowDetailPrefixesUseSmallInset(t *testing.T) {
	listBase := rowMarkerWidth + listIconWidth + listIconGapWidth
	textBase := rowMarkerWidth + 2

	if got := lipgloss.Width(listTextPrefix()); got != listBase+listDetailExtraIndent {
		t.Fatalf("list detail prefix width = %d, want %d", got, listBase+listDetailExtraIndent)
	}
	if got := lipgloss.Width(textRowContentPrefix()); got != textBase+listDetailExtraIndent {
		t.Fatalf("text detail prefix width = %d, want %d", got, textBase+listDetailExtraIndent)
	}
	if got, want := lipgloss.Width(listHintPrefix())-lipgloss.Width(listTextPrefix()), listHintExtraIndent; got != want {
		t.Fatalf("list hint extra indent = %d, want %d", got, want)
	}
	if got, want := lipgloss.Width(textRowHintPrefix())-lipgloss.Width(textRowContentPrefix()), listHintExtraIndent; got != want {
		t.Fatalf("text hint extra indent = %d, want %d", got, want)
	}
}

func TestRenderSettings_ExpandableRowsUseEnterHint(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	for _, tc := range []struct {
		name string
		row  int
		word string
	}{
		{"priority", 1, "edit"},
		{"dots repo", 7, "edit"},
		{"disable dots", settingsRowDotsSync, "disable"},
		{"reset settings", settingsRowResetSettings, "confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.settingsCursor = tc.row
			out := renderSettings(m)
			line := renderedLineContaining(out, tc.word)
			if line == "" {
				t.Fatalf("%s row hint missing from output:\n%s", tc.name, out)
			}
			if !strings.Contains(line, "enter") || strings.Contains(line, "space") || strings.Contains(line, "cancel") {
				t.Fatalf("%s row should hint enter %s without space/cancel, got %q", tc.name, tc.word, line)
			}
		})
	}
}

func TestRenderSettings_EditModeShowsCancelHint(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = 1
	m.editingPriority = true
	m.priorityDraft = []string{"brew", "apt"}
	out := renderSettings(m)
	line := renderedLineContaining(out, "cancel")
	if line == "" {
		t.Fatalf("priority edit cancel hint missing from output:\n%s", out)
	}
	if !strings.Contains(line, "enter") || !strings.Contains(line, "save") {
		t.Fatalf("priority edit should hint enter save and cancel, got %q", line)
	}
}

func TestRenderSettings_StateColumnUsesFixedFirstGap(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	line := renderedLineContaining(out, "Import Installed Tools")
	if line == "" || !strings.Contains(line, "[OFF]") {
		t.Fatalf("settings row should contain label and value:\n%s", out)
	}
	valueCol := visualColumnOf(line, "[OFF]")
	wantCol := rowMarkerWidth + lipgloss.Width(rowContentInset()) + settingLabelWidth + firstColumnGap
	if valueCol != wantCol {
		t.Fatalf("settings value column = %d, want fixed left layout column %d in %q", valueCol, wantCol, line)
	}
	if valueCol >= m.width/2 {
		t.Fatalf("settings value should not be pulled to the right edge: %q", line)
	}
}

func TestMainTabs_FirstSectionStartsAtSharedRow(t *testing.T) {
	tool := &database.ToolCache{Name: "git", Provider: "system", Installed: true, Tracked: true}
	toolsNoFilters := baseModel([]*database.ToolCache{tool})
	toolsNoFilters.providerNames = nil
	toolsNoFilters.groupNames = nil

	toolsWithFilters := baseModel([]*database.ToolCache{tool})
	toolsWithFilters.providerNames = []string{"all", "system"}
	toolsWithFilters.providerTabIdx = 0

	dotsNoFilters := baseModel(nil)
	dotsNoFilters.settings.DotsRepo = "/repo"
	dotsNoFilters.dotsLoaded = true
	dotsNoFilters.dotsEntries = []app.DotStatus{{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced}}

	dotsWithControls := baseModel(nil)
	dotsWithControls.settings.DotsRepo = "/repo"
	dotsWithControls.dotsLoaded = true
	dotsWithControls.dotsSearchActive = true
	dotsWithControls.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced, Group: "config"},
		{Name: "zsh", TargetPath: "~/.zshrc", State: app.DotStateSynced, Group: "home"},
	}

	settings := baseModel(nil)
	settings.mode = viewSettings

	profiles := baseModel(nil)
	profiles.mode = viewProfiles
	profiles.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{"default": {}},
	}

	cases := []struct {
		name  string
		out   string
		label string
	}{
		{"tools no filters", renderList(toolsNoFilters), "Installed"},
		{"tools filters", renderList(toolsWithFilters), "Installed"},
		{"dots no filters", renderDots(dotsNoFilters), "Synced"},
		{"dots controls", renderDots(dotsWithControls), "Synced"},
		{"settings", renderSettings(settings), "Tools"},
		{"profiles", renderProfiles(profiles), "Profiles"},
	}

	want := 1
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionLineIndex(tc.out, tc.label); got != want {
				t.Fatalf("first section line = %d, want %d for %s:\n%s", got, want, tc.name, tc.out)
			}
		})
	}
}

func TestRenderProfiles_InlineHintsUseSharedIndent(t *testing.T) {
	m := profilesModel()
	m.profileSection = 0
	out := renderProfiles(m)
	line := renderedLineContaining(out, "activate profile")
	if line == "" {
		t.Fatalf("profile hints missing from output:\n%s", out)
	}
	if !strings.HasPrefix(line, textRowHintPrefix()) {
		t.Fatalf("profile hint line should use shared indent %q, got %q", textRowHintPrefix(), line)
	}
}

func renderedLineContaining(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func sectionLineIndex(out, label string) int {
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, label) && strings.Contains(line, "─") {
			return i
		}
	}
	return -1
}

func settingsLabelLineIndex(out, label string, header bool) int {
	if header {
		return sectionLineIndex(out, label)
	}
	needle := formatSettingLabel(label)
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

// ── view_statusbar.go ─────────────────────────────────────────────────────────

func TestRenderStatusBar_Idle(t *testing.T) {
	m := baseModel(nil)
	out := renderStatusBar(m)
	if out == "" {
		t.Error("expected non-empty status bar even when idle")
	}
}

func TestRenderStatusBar_WithStatusMsg(t *testing.T) {
	m := baseModel(nil)
	m.statusMsg = "sync complete"
	out := renderStatusBar(m)
	if !strings.Contains(out, "sync complete") {
		t.Errorf("expected status message in status bar, got: %q", out)
	}
}

func TestRenderStatusBar_SuccessMsgUsesGreenStyle(t *testing.T) {
	m := baseModel(nil)
	m.statusMsg = "✓ sync complete"
	out := renderStatusBar(m)
	want := m.palette.styleInstalled.Bold(true).Render(m.statusMsg)
	if !strings.Contains(out, want) {
		t.Errorf("expected success message to use installed style, got: %q", out)
	}
}

func TestRenderStatusBar_SyncAllConfirmUsesFooterOnly(t *testing.T) {
	m := baseModel(nil)
	m.armListConfirmation(listConfirmSyncAll, nil)
	out := renderStatusBar(m)
	if strings.Contains(out, "Press") {
		t.Fatalf("sync-all confirmation should not render as status text, got: %q", out)
	}
	if want := "press " + m.keys.SyncAll.Help().Key + " again to sync all"; !strings.Contains(out, want) {
		t.Fatalf("sync-all confirmation should replace footer hints, got: %q", out)
	}
	if strings.Contains(out, toolActionConfirmDesc(t, actions.ToolSyncAll)) {
		t.Fatalf("sync-all footer prompt should not use confirm wording, got: %q", out)
	}
}

func TestRenderStatusBar_RowConfirmHidesFooterHints(t *testing.T) {
	tool := &database.ToolCache{Name: "bat", Provider: "brew", Installed: true, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.armListConfirmation(listConfirmDelete, tool)
	out := renderStatusBar(m)
	for _, unwanted := range []string{"upgrade all", "sync all", "refresh", "search", "filter"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("tool row confirmation should hide footer hints; found %q in %q", unwanted, out)
		}
	}

	m = baseModel(nil)
	m.mode = viewDots
	m.dotsConfirmIdx = 0
	out = renderStatusBar(m)
	for _, unwanted := range []string{"sync all", "pull", "add", "search"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("dots row confirmation should hide footer hints; found %q in %q", unwanted, out)
		}
	}
}

func TestActiveConfirmationsUseSingleHelpHint(t *testing.T) {
	tool := &database.ToolCache{Name: "bat", Provider: "brew", Installed: true, Tracked: true}
	cases := []struct {
		name string
		m    Model
		want string
	}{
		{
			name: "tool delete",
			m: func() Model {
				m := baseModel([]*database.ToolCache{tool})
				m.armListConfirmation(listConfirmDelete, tool)
				return m
			}(),
			want: "confirm delete",
		},
		{
			name: "dots delete",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewDots
				m.dotsConfirmIdx = 0
				return m
			}(),
			want: "yes",
		},
		{
			name: "settings danger",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewSettings
				m.dangerConfirmRow = settingsRowResetCache
				return m
			}(),
			want: "confirm",
		},
		{
			name: "settings dots disable",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewSettings
				m.dangerConfirmRow = settingsRowDotsSync
				return m
			}(),
			want: "yes",
		},
		{
			name: "profile delete",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewProfiles
				m.profileDeleteConfirm = true
				return m
			}(),
			want: "press d again to confirm delete",
		},
		{
			name: "group delete",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewProfiles
				m.groupDeleteConfirm = true
				return m
			}(),
			want: "confirm delete",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := renderStatusBar(tc.m)
			for _, unwanted := range []string{"sync all", "search", "filter", "tab", "help", "quit"} {
				if strings.Contains(status, unwanted) {
					t.Fatalf("confirmation should suppress footer hints; found %q in %q", unwanted, status)
				}
			}

			help := renderHelpPopup(tc.m)
			if !strings.Contains(help, tc.want) {
				t.Fatalf("help popup missing single confirmation %q:\n%s", tc.want, help)
			}
			for _, unwanted := range []string{"Current Tab Actions", "Navigation", "cancel", "close", "search", "switch tab", "quit omni"} {
				if strings.Contains(help, unwanted) {
					t.Fatalf("help popup should show only current confirmation; found %q in:\n%s", unwanted, help)
				}
			}
		})
	}
}

func TestRenderStatusBar_QuitConfirmReplacesFooterHints(t *testing.T) {
	m := baseModel(nil)
	m.confirmQuit = true
	m.quitConfirmKey = "ctrl+c"
	out := renderStatusBar(m)
	if !strings.Contains(out, "ctrl+c") || !strings.Contains(out, "press ctrl+c again to quit") {
		t.Fatalf("quit confirmation should show triggering key and confirm text, got: %q", out)
	}
	if strings.Contains(out, "confirm quit") {
		t.Fatalf("quit confirmation should not use confirm wording, got: %q", out)
	}
	if strings.Contains(out, "switch tab") || strings.Contains(out, "help") {
		t.Fatalf("quit confirmation should replace default footer hints, got: %q", out)
	}
}

func TestRenderStatusBar_ErrorMsg(t *testing.T) {
	m := baseModel(nil)
	m.statusMsg = "something went wrong"
	m.statusIsErr = true
	out := renderStatusBar(m)
	if !strings.Contains(out, "wrong") {
		t.Errorf("expected error message in status bar, got: %q", out)
	}
}

func TestRenderStatusBar_Loading(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	out := renderStatusBar(m)
	if out == "" {
		t.Error("expected non-empty status bar when loading")
	}
}

func TestViewString_LoadingListUsesFooterOnly(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.mode = viewList

	out := m.viewString()
	if got := strings.Count(out, "Loading"); got != 1 {
		t.Fatalf("viewString should render loading only in the footer, got %d occurrences:\n%s", got, out)
	}
}

func TestRenderStatusBar_ProgressText(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.progressText = "installing git…"
	out := renderStatusBar(m)
	if !strings.Contains(out, "git") {
		t.Errorf("expected progress text 'git' in status bar, got: %q", out)
	}
}

func TestRenderStatusBar_ToolsFooterShowsCombinedFilter(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewList
	m.help = newHelp()
	m.help.SetWidth(m.width)

	out := renderStatusBar(m)
	if !strings.Contains(out, "[],{}") || !strings.Contains(out, "filter") {
		t.Errorf("status bar missing combined filter hint:\n%s", out)
	}
}

func TestActivityLabel_Searching(t *testing.T) {
	m := baseModel(nil)
	m.searching = true
	got := activityLabel(m)
	if !strings.Contains(got, "Search") {
		t.Errorf("expected 'Search' in activity label, got: %q", got)
	}
}

func TestActivityLabel_Scanning(t *testing.T) {
	m := baseModel(nil)
	m.scanningProviders = map[string]bool{"brew": true, "npm": true}
	got := activityLabel(m)
	if !strings.Contains(got, "Scan") {
		t.Errorf("expected 'Scan' in activity label, got: %q", got)
	}
}

func TestActivityLabel_DotsLoading(t *testing.T) {
	m := baseModel(nil)
	m.dotsLoading = true
	got := activityLabel(m)
	if !strings.Contains(got, "dots") {
		t.Errorf("expected 'dots' in activity label, got: %q", got)
	}
}

func TestActivityLabel_DefaultFallback(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	got := activityLabel(m)
	if !strings.Contains(got, "Load") {
		t.Errorf("expected 'Load' in default activity label, got: %q", got)
	}
}

func TestTabKeyMap_ShortHelp_DotsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	km := tabKeyMap{&m}
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty ShortHelp for dots mode")
	}
}

func TestTabKeyMap_ShortHelp_SettingsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	km := tabKeyMap{&m}
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty ShortHelp for settings mode")
	}
}

func TestTabKeyMap_ShortHelp_ProfilesMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	km := tabKeyMap{&m}
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty ShortHelp for profiles mode")
	}
	if got := strings.Join(bindingHelpDescs(bindings), ","); !strings.Contains(got, "new profile") {
		t.Errorf("profiles footer should include new profile, got %v", got)
	}
	if got := strings.Join(bindingHelpDescs(bindings), ","); !strings.Contains(got, "new group") {
		t.Errorf("profiles footer should include new group, got %v", got)
	}
}

func TestTabKeyMap_ShortHelp_ProfilesGroupSection(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileSection = 1
	got := strings.Join(bindingHelpDescs(tabKeyMap{&m}.ShortHelp()), ",")
	if !strings.Contains(got, "new profile") {
		t.Errorf("profiles footer should keep new profile in group section, got %v", got)
	}
	if !strings.Contains(got, "new group") {
		t.Errorf("profiles footer should include new group in group section, got %v", got)
	}
}

func TestTabKeyMap_ShortHelp_DefaultWithGroups(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewList
	m.groupNames = []string{"dev"}
	km := tabKeyMap{&m}
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty ShortHelp with groups")
	}
}

func TestTabKeyMap_ShortHelp_DefaultNoGroups(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewList
	m.groupNames = nil
	km := tabKeyMap{&m}
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty ShortHelp without groups")
	}
	got := strings.Join(bindingHelpKeys(bindings), ",")
	if !strings.Contains(got, "[],{}") {
		t.Errorf("ShortHelp missing combined provider/group filter without groups: %v", got)
	}
}

func TestTabKeyMap_ShortHelp_ListOrder(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewList
	m.groupNames = []string{"dev"}
	got := bindingHelpKeys(tabKeyMap{&m}.ShortHelp())
	want := []string{
		toolActionTUIKey(t, actions.ToolUpdateAll),
		toolActionTUIKey(t, actions.ToolSyncAll),
		toolActionTUIKey(t, actions.ToolRefresh),
		"/", "[],{}", "tab", "?", "q",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ShortHelp order = %v, want %v", got, want)
	}
}

func TestTabKeyMap_ShortHelp_DotsOrder(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewDots
	got := bindingHelpKeys(tabKeyMap{&m}.ShortHelp())
	want := []string{
		toolActionTUIKey(t, actions.DotsAdd),
		toolActionTUIKey(t, actions.DotsDiscover),
		m.keys.SyncAll.Help().Key,
		"/", "tab", "?", "q",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Dots ShortHelp order = %v, want %v", got, want)
	}
}

func TestFooterFilterBinding_DerivesCombinedLabel(t *testing.T) {
	k := DefaultKeyMap()
	got := footerFilterBinding(k, true).Help()
	if got.Key != "[],{}" {
		t.Errorf("filter key label = %q, want %q", got.Key, "[],{}")
	}
	if got.Desc != k.PrevTab.Help().Desc {
		t.Errorf("filter desc = %q, want %q", got.Desc, k.PrevTab.Help().Desc)
	}
}

func TestTabKeyMap_ShortHelp_OmitsRowActions(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewList
	got := strings.Join(bindingHelpKeys(tabKeyMap{&m}.ShortHelp()), ",")
	for _, rowKey := range []string{
		toolActionTUIKey(t, actions.ToolInstall),
		toolActionTUIKey(t, actions.ToolUpdate),
		toolActionTUIKey(t, actions.ToolDelete),
		toolActionTUIKey(t, actions.ToolChangeGroup),
		toolActionTUIKey(t, actions.ToolPinProvider),
		toolActionTUIKey(t, actions.ToolReinstallDefault),
	} {
		if strings.Contains(got, rowKey) {
			t.Errorf("ShortHelp contains row action %q in %v", rowKey, got)
		}
	}
}

func TestTabKeyMap_ShortHelp_StaticSuffix(t *testing.T) {
	cases := []struct {
		name string
		mode viewMode
	}{
		{"dots", viewDots},
		{"settings", viewSettings},
		{"profiles", viewProfiles},
		{"list", viewList},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(threeTools())
			m.mode = tc.mode
			got := bindingHelpKeys(tabKeyMap{&m}.ShortHelp())
			if len(got) < 3 {
				t.Fatalf("ShortHelp too short: %v", got)
			}
			suffix := got[len(got)-3:]
			want := []string{"tab", "?", "q"}
			if strings.Join(suffix, ",") != strings.Join(want, ",") {
				t.Errorf("ShortHelp static suffix = %v, want %v", suffix, want)
			}
		})
	}
}

func TestTabKeyMap_FullHelp(t *testing.T) {
	m := baseModel(nil)
	km := tabKeyMap{&m}
	bindings := km.FullHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty FullHelp")
	}
}

func TestRenderHelpPopup_ToolsSectionsUseCompactDescriptions(t *testing.T) {
	m := baseModel(nil)
	m.groupNames = []string{"base", "work"}

	out := renderHelpPopup(m)
	for _, want := range []string{
		"Current Tab Actions",
		"Row",
		"Bulk",
		"Navigation",
		"add to config",
		"reinstall with default",
		"add discovered and install missing",
		"[],{}",
		"wrong provider",
		"──",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help popup missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Legend") {
		t.Errorf("help popup should not title the icon legend:\n%s", out)
	}
}

func TestRenderHelpPopup_TabSpecificActionsAndLegend(t *testing.T) {
	cases := []struct {
		name string
		mode viewMode
		want []string
	}{
		{"dots", viewDots, []string{"discover", "conflict", "no source"}},
		{"settings", viewSettings, []string{"change toggle or option", "[ON]", "[OFF]"}},
		{"profiles", viewProfiles, []string{"new profile", "active profile"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.mode = tc.mode
			out := renderHelpPopup(m)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("help popup missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestHelpPopup_DividerRendersAsSingleLineInPopupFrame(t *testing.T) {
	m := baseModel(nil)
	m.width = 80
	m.height = 34
	m.help.ShowAll = true
	m.mode = viewList

	popup := renderPopupFrame(
		m.palette,
		renderHelpPopup(m),
		popupFrame{
			Title:    helpPopupTitle(m),
			PaddingY: 1,
			PaddingX: 3,
		},
	)
	barCount := 0
	for _, line := range strings.Split(popup, "\n") {
		trimmed := strings.TrimSpace(strings.Trim(line, "│ "))
		if trimmed != "" && strings.Trim(trimmed, "─") == "" {
			barCount++
		}
	}
	if barCount != 2 {
		t.Fatalf("expected exactly two divider bar lines in help popup, got %d:\n%s", barCount, popup)
	}
}

func TestRenderHelpPopup_ContentLinesFitAvailableWidth(t *testing.T) {
	m := baseModel(nil)
	m.width = 90
	m.height = 34
	m.help.ShowAll = true
	helpWidth := helpPopupContentWidth(m)

	for _, mode := range []viewMode{viewList, viewDots, viewProfiles, viewSettings} {
		m.mode = mode
		content := renderHelpPopupWithWidth(m, helpWidth)
		for i, line := range strings.Split(content, "\n") {
			if got, want := lipgloss.Width(line), helpWidth; got > want {
				t.Fatalf("help popup line %d too wide in mode %v: got %d > %d:\n%s", i+1, mode, got, want, line)
			}
		}
	}
}

func TestRenderHelpPopup_ActionOrderKeepsDeleteLast(t *testing.T) {
	cases := []struct {
		name   string
		mode   viewMode
		before []string
	}{
		{
			name:   "tools",
			mode:   viewList,
			before: []string{"i     install", "u     upgrade", "g     edit groups", "x     ignore"},
		},
		{
			name:   "dots",
			mode:   viewDots,
			before: []string{"a     add", "D     discover", "g     edit groups", "x     ignore"},
		},
		{
			name:   "profiles",
			mode:   viewProfiles,
			before: []string{"p      new profile", "n      new group", "space  activate profile", "r      rename", "g      edit groups", "h      edit hosts", "t      edit tools"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.mode = tc.mode
			out := renderHelpPopup(m)
			deleteIdx := strings.Index(out, "d     delete")
			if deleteIdx < 0 {
				deleteIdx = strings.Index(out, "d      delete")
			}
			if deleteIdx < 0 {
				t.Fatalf("help popup missing canonical delete action:\n%s", out)
			}
			for _, before := range tc.before {
				idx := strings.Index(out, before)
				if idx < 0 {
					t.Fatalf("help popup missing %q:\n%s", before, out)
				}
				if idx > deleteIdx {
					t.Fatalf("%q should be listed before delete:\n%s", before, out)
				}
			}
		})
	}
}

func bindingHelpKeys(bindings []key.Binding) []string {
	keys := make([]string, 0, len(bindings))
	for _, b := range bindings {
		keys = append(keys, b.Help().Key)
	}
	return keys
}

func bindingHelpDescs(bindings []key.Binding) []string {
	descs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		descs = append(descs, b.Help().Desc)
	}
	return descs
}

// ── view_list.go ──────────────────────────────────────────────────────────────

func TestProviderEcosystem_TableDriven(t *testing.T) {
	cases := []struct{ input, want string }{
		{"system", "system"},
		{"brew", "system"},
		{"apt", "system"},
		{"apk", "system"},
		{"dnf", "system"},
		{"pacman", "system"},
		{"zypper", "system"},
		{"python", "python"},
		{"pip", "python"},
		{"pip3", "python"},
		{"uv", "python"},
		{"node", "node"},
		{"npm", "node"},
		{"bun", "node"},
		{"pnpm", "node"},
		{"cargo", "cargo"}, // unknown → raw
		{"unknown", "unknown"},
	}
	for _, tc := range cases {
		got := providerEcosystem(tc.input)
		if got != tc.want {
			t.Errorf("providerEcosystem(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestProviderLabel_TableDriven(t *testing.T) {
	cases := []struct {
		raw, installedWith, systemBin, pythonBin, nodeBin, want string
	}{
		{"brew", "", "", "", "", "system(brew!)"},
		{"apt", "", "", "", "", "system(apt!)"},
		{"system", "brew", "", "", "", "system(brew)"},
		{"system", "", "brew", "", "", "system(brew)"},
		{"system", "", "", "", "", "system"},
		{"python", "", "", "uv", "", "python(uv)"},
		{"python", "", "", "", "", "python"},
		{"pip", "", "", "", "", "python(pip3!)"},
		{"pip3", "", "", "", "", "python(pip3!)"},
		{"uv", "", "", "", "", "python(uv!)"},
		{"node", "", "", "", "bun", "node(bun)"},
		{"node", "", "", "", "", "node"},
		{"npm", "", "", "", "", "node(npm!)"},
		{"bun", "", "", "", "", "node(bun!)"},
		{"pnpm", "", "", "", "", "node(pnpm!)"},
		{"cargo", "", "", "", "", "cargo"},
	}
	for _, tc := range cases {
		got := providerLabel(tc.raw, tc.installedWith, tc.systemBin, tc.pythonBin, tc.nodeBin)
		if got != tc.want {
			t.Errorf("providerLabel(%q, %q, %q, %q, %q) = %q, want %q",
				tc.raw, tc.installedWith, tc.systemBin, tc.pythonBin, tc.nodeBin, got, tc.want)
		}
	}
}

func TestProviderParts_System(t *testing.T) {
	meta, concrete, isOverride := providerParts("system", "brew", "", "", "")
	if meta != "system" {
		t.Errorf("expected meta=system, got %q", meta)
	}
	if concrete != "brew" {
		t.Errorf("expected concrete=brew, got %q", concrete)
	}
	if isOverride {
		t.Error("expected isOverride=false for system ecosystem provider")
	}
}

func TestProviderParts_Brew(t *testing.T) {
	meta, concrete, isOverride := providerParts("brew", "", "", "", "")
	if meta != "system" {
		t.Errorf("expected meta=system, got %q", meta)
	}
	if concrete != "brew" {
		t.Errorf("expected concrete=brew, got %q", concrete)
	}
	if !isOverride {
		t.Error("expected isOverride=true for brew")
	}
}

func TestProviderParts_Python(t *testing.T) {
	meta, _, _ := providerParts("python", "uv", "", "uv", "")
	if meta != "python" {
		t.Errorf("expected meta=python, got %q", meta)
	}
}

func TestProviderParts_Node(t *testing.T) {
	meta, concrete, _ := providerParts("node", "", "", "", "bun")
	if meta != "node" {
		t.Errorf("expected meta=node, got %q", meta)
	}
	if concrete != "bun" {
		t.Errorf("expected concrete=bun, got %q", concrete)
	}
}

func TestProviderParts_Unknown(t *testing.T) {
	meta, concrete, isOverride := providerParts("cargo", "", "", "", "")
	if meta != "cargo" {
		t.Errorf("expected meta=cargo for unknown, got %q", meta)
	}
	if concrete != "" {
		t.Errorf("expected empty concrete for unknown, got %q", concrete)
	}
	if isOverride {
		t.Error("expected isOverride=false for unknown provider")
	}
}

func TestNewColWidths_BasicTools(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "brew"},
		{Name: "a-very-long-tool-name", Provider: "npm"},
	}
	cols := newColWidths(tools, nil, nil, "", "", "", 120)
	if cols.name < len("a-very-long-tool-name") {
		t.Errorf("name column %d should be >= longest tool name (%d)", cols.name, len("a-very-long-tool-name"))
	}
	if cols.prov < 8 {
		t.Errorf("provider column %d should be >= floor of 8", cols.prov)
	}
}

func TestNewColWidths_WithGroups(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "brew"},
	}
	groups := []string{"dev"}
	cols := newColWidths(tools, map[string]string{
		toolKey("git", "brew"): "dev",
	}, groups, "", "", "", 120)
	if cols.group == 0 {
		t.Error("expected non-zero group column width when groups exist")
	}
}

func TestNewColWidths_IgnoreLabelsDoNotInflateGroupColumn(t *testing.T) {
	tools := []*database.ToolCache{
		{Name: "git", Provider: "brew"},
	}
	cols := newColWidths(tools, map[string]string{
		toolKey("git", "brew"): "dev",
	}, []string{"dev"}, "", "", "", 120)
	if cols.group != len("[base]") {
		t.Fatalf("group column = %d, want %d; ignore details belong in selected-row detail, not the group column", cols.group, len("[base]"))
	}
}

func TestNewColWidths_NoTools(t *testing.T) {
	cols := newColWidths(nil, nil, nil, "", "", "", 120)
	if cols.name < 20 {
		t.Errorf("name column %d should be >= floor of 20", cols.name)
	}
}

func TestNewColWidths_UsesCompactVersionWidth(t *testing.T) {
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true}
	tool.Version.Valid = true
	tool.Version.String = "2.40.0, abc1234567890"

	cols := newColWidths([]*database.ToolCache{tool}, nil, nil, "", "", "", 80)
	wantName := 20
	if cols.name != wantName {
		t.Errorf("name column = %d, want %d with split row width", cols.name, wantName)
	}
}

func TestRenderToolRow_InstalledNormal(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true}
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncOK)
	if !strings.Contains(out, "git") {
		t.Errorf("expected 'git' in tool row, got: %q", out)
	}
}

func TestRenderToolRow_ShowsPackageAliasAfterLogicalName(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "editor", Provider: "system", Package: "neovim", Installed: true, Tracked: true}
	cols := colWidths{name: 24, prov: 14, ver: 8, screenW: 120}

	out := renderToolRow(p, tool, cols, "", "", "brew", "", "", false, false, syncOK)

	if !strings.Contains(out, "editor") || !strings.Contains(out, "{neovim}") {
		t.Fatalf("row = %q, want logical name and package alias", out)
	}
	if !strings.Contains(out, p.styleHelp.Render(" {neovim}")) {
		t.Fatalf("row = %q, want package alias rendered with help style", out)
	}
}

func TestRenderToolRow_MissingTool(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "missing-tool", Provider: "brew", Installed: false}
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncMissing)
	if !strings.Contains(out, "missing-tool") {
		t.Errorf("expected tool name in missing tool row, got: %q", out)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("expected 'missing' text in missing tool row, got: %q", out)
	}
}

func TestRenderToolRow_OrphanDoesNotShowBaseGroup(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "fzf", Provider: "system", InstalledWith: "brew", Installed: true, Tracked: false}
	cols := colWidths{name: 20, prov: 12, ver: 8, group: len("[base]"), screenW: 120}

	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncOrphan)
	if strings.Contains(out, "[base]") {
		t.Fatalf("orphan row = %q, should not show base group", out)
	}
	if !strings.Contains(out, "system(") || !strings.Contains(out, "brew") {
		t.Fatalf("orphan row = %q, want concrete provider label", out)
	}
}

func TestRenderToolRow_OutdatedTool(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{
		Name:      "git",
		Provider:  "brew",
		Installed: true,
		Outdated:  true,
	}
	tool.Version.Valid = true
	tool.Version.String = "2.40.0"
	tool.LatestVersion.Valid = true
	tool.LatestVersion.String = "2.41.0"
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncOK)
	if !strings.Contains(out, "2.40.0") {
		t.Errorf("expected version in outdated tool row, got: %q", out)
	}
}

func TestRenderToolRow_CompactsCommaVersionSuffix(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{
		Name:      "git",
		Provider:  "brew",
		Installed: true,
	}
	tool.Version.Valid = true
	tool.Version.String = "2.40.0, abc123"
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncOK)
	if !strings.Contains(out, "2.40.0") {
		t.Errorf("expected compact version in tool row, got: %q", out)
	}
	if strings.Contains(out, "abc123") {
		t.Errorf("expected comma suffix hidden in tool row, got: %q", out)
	}
}

func TestRenderToolRow_IgnoredTool(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "ignored-pkg", Provider: "pip", Installed: false, Tracked: true}
	cols := colWidths{name: 20, prov: 14, group: len("[work]"), screenW: 120}
	out := renderToolRow(p, tool, cols, "", "work", "", "", "", true, false, syncOK)
	if !strings.Contains(out, "ignored-pkg") {
		t.Errorf("expected tool name in ignored row, got: %q", out)
	}
	if !strings.Contains(out, "[work]") {
		t.Errorf("expected group badge in ignored row, got: %q", out)
	}
	if strings.Contains(out, "ignored: work") || strings.Contains(out, "[ignore:work]") {
		t.Errorf("ignore source should not render in the group column, got: %q", out)
	}
}

func TestRenderToolRow_OneGapBetweenIconAndName(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "rg", Provider: "brew", Installed: true, Tracked: true}
	tool.Version.Valid = true
	tool.Version.String = "1.0.0"
	cols := colWidths{name: 20, prov: 10, group: 0, screenW: 120}

	out := renderToolRow(p, tool, cols, "", "base", "", "", "", false, false, syncOK)
	icon := p.styleInstalled.Render(iconInstalled)
	name := renderNameCell(p, p.styleNormal, tool, "", cols.name, false)
	if !strings.Contains(out, icon+" "+name) {
		t.Fatalf("tool row should render exactly one space between icon and name, got: %q", out)
	}
	if !strings.Contains(out, icon+" "+name) || strings.Contains(out, icon+"  "+name) {
		t.Fatalf("tool row should use a single spacer between icon and name, got: %q", out)
	}
}

func TestRenderToolRow_InstalledMetaProviderWithoutInstalledWithDoesNotGuessManager(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "typescript", Provider: "node", Installed: true, Tracked: true}
	cols := colWidths{name: 20, prov: 10, group: 8, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "dev", "", "", "pnpm", false, false, syncOK)
	if strings.Contains(out, "node(pnpm)") {
		t.Fatalf("installed row without installed_with should not guess effective node manager, got: %q", out)
	}
	if !strings.Contains(out, "node") {
		t.Fatalf("expected ecosystem provider label, got: %q", out)
	}
}

func TestRenderToolRow_SelectedTool(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "selected", Provider: "brew", Installed: true, Tracked: true}
	tool.Version.Valid = true
	tool.Version.String = "1.2.3"
	cols := colWidths{name: 20, prov: 10, group: 8, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "dev", "", "", "", false, true, syncOK)
	if !strings.Contains(out, "selected") {
		t.Errorf("expected 'selected' in selected tool row, got: %q", out)
	}
	for _, want := range []string{
		p.styleInstalled.Bold(true).Render(iconInstalled),
		p.styleVersionMuted.Bold(true).Render("1.2.3"),
		p.styleHelp.Bold(true).Render("[dev]"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("selected row should emphasize all columns; missing %q in %q", want, out)
		}
	}
}

func TestRenderToolRow_WithSpinner(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "upgrading", Provider: "brew", Installed: true}
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	out := renderToolRow(p, tool, cols, "⠋", "", "", "", "", false, false, syncOK)
	if !strings.Contains(out, "upgrading") {
		t.Errorf("expected tool name with spinner view, got: %q", out)
	}
}

func TestRenderToolRow_WithGroup(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	cols := colWidths{name: 20, prov: 10, group: 8, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "dev", "", "", "", false, false, syncOK)
	if !strings.Contains(out, "dev") {
		t.Errorf("expected group badge 'dev' in tool row, got: %q", out)
	}
}

func TestRenderToolRow_RightAlignsGroupBadge(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	cols := colWidths{name: 20, prov: 10, group: 8, screenW: 120}
	out := renderToolRow(p, tool, cols, "", "dev", "", "", "", false, false, syncOK)
	if !strings.Contains(out, p.styleHelp.Render("[dev]")) {
		t.Errorf("expected group badge in right group, got: %q", out)
	}
}

func TestRenderToolRow_ProviderVersionAndGroupShareRightGroup(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	tool.Version.Valid = true
	tool.Version.String = "2.40.0"
	cols := colWidths{name: 20, prov: 10, ver: 8, group: 8, screenW: 80}

	out := renderToolRow(p, tool, cols, "", "dev", "", "", "", false, false, syncOK)
	providerCol := visualColumnOf(out, "brew")
	versionCol := visualColumnOf(out, "2.40.0")
	groupCol := visualColumnOf(out, "[dev]")
	if providerCol < cols.name+listIconWidth+listIconGapWidth+listColumnGap+6 {
		t.Fatalf("provider should be in the right group after the flexible gap, got: %q", out)
	}
	if !(providerCol < versionCol && versionCol < groupCol) {
		t.Fatalf("right group should order provider, version, group; got: %q", out)
	}
}

func TestRenderDotsRow_UsesCompactSpacing(t *testing.T) {
	const name = "abcdefghijkl"
	const target = "~/dot-target"

	m := baseModel(nil)
	m.mode = viewDots
	m.settings.DotsRepo = "/repo"
	m.width = 120
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: name, TargetPath: target, State: app.DotStateSynced},
	}

	out := renderDots(m)
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name) && strings.Contains(line, target) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("expected dots row for test data, got: %q", out)
	}

	iconCol := visualColumnOf(row, "✓")
	nameCol := visualColumnOf(row, name)
	targetCol := visualColumnOf(row, target)
	if iconCol < 0 || nameCol < 0 || targetCol < 0 {
		t.Fatalf("failed to locate dots row fragments in row: %q", row)
	}

	if got, want := nameCol-iconCol-1, dotsIconNameGapW; got != want {
		t.Fatalf("icon-to-name gap = %d, want %d in row: %q", got, want, row)
	}
	if got, want := targetCol-nameCol-lipgloss.Width(name), dotsGapW; got != want {
		t.Fatalf("name-to-target gap = %d, want %d in row: %q", got, want, row)
	}
}

func TestRenderToolRow_LongUpgradeVersionDoesNotPushGroupBadge(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "typescript", Provider: "node", Installed: true, Outdated: true, Tracked: true}
	tool.Version.Valid = true
	tool.Version.String = "5.8.3"
	tool.LatestVersion.Valid = true
	tool.LatestVersion.String = "5.9.0-next.20260501+very-long-build-metadata"
	screenW := 80
	cols := newColWidths(
		[]*database.ToolCache{tool},
		map[string]string{toolKey("typescript", "node"): "dev"},
		[]string{"dev"},
		"",
		"",
		"pnpm",
		screenW,
	)

	out := screenEdgeInset() + renderToolRow(p, tool, cols, "", "dev", "", "", "pnpm", false, false, syncOK)
	if got := lipgloss.Width(out); got > screenContentWidth(screenW) {
		t.Fatalf("row width = %d, want <= %d; row: %q", got, screenContentWidth(screenW), out)
	}
	if !strings.Contains(out, "…") {
		t.Fatalf("expected long latest version to be truncated, got: %q", out)
	}
}

func TestRenderFilterBar_Empty(t *testing.T) {
	m := baseModel(threeTools())
	m.groupNames = nil
	m.providerNames = nil
	out := renderFilterBar(m)
	if out != "" {
		t.Errorf("expected empty filter bar with no groups/providers, got: %q", out)
	}
}

func TestRenderFilterBar_WithGroups(t *testing.T) {
	m := baseModel(threeTools())
	m.groupNames = []string{"dev", "personal"}
	m.groupTabIdx = 0
	out := renderFilterBar(m)
	if !strings.Contains(out, "dev") {
		t.Errorf("expected 'dev' in filter bar with groups, got: %q", out)
	}
}

func TestRenderFilterBar_GroupsAlphabeticalIncludingBase(t *testing.T) {
	m := baseModel(threeTools())
	m.groupNames = []string{"work", "apps", "personal"}
	out := renderFilterBar(m)
	appsIdx := strings.Index(out, "apps")
	baseIdx := strings.Index(out, "base")
	personalIdx := strings.Index(out, "personal")
	workIdx := strings.Index(out, "work")
	if appsIdx < 0 || baseIdx < 0 || personalIdx < 0 || workIdx < 0 {
		t.Fatalf("filter bar missing expected groups: %q", out)
	}
	if !(appsIdx < baseIdx && baseIdx < personalIdx && personalIdx < workIdx) {
		t.Fatalf("filter bar groups not alphabetical: %q", out)
	}
}

func TestRenderFilterBar_WithProviders(t *testing.T) {
	m := baseModel(threeTools())
	m.providerNames = []string{"system", "node"}
	m.providerTabIdx = 0
	out := renderFilterBar(m)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in filter bar with providers, got: %q", out)
	}
}

func TestRenderFilterBar_WithSingleProvider(t *testing.T) {
	m := baseModel(threeTools())
	m.providerNames = []string{"system"}
	out := renderFilterBar(m)
	for _, want := range []string{"all", "system"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in single-provider filter bar, got: %q", want, out)
		}
	}
}

func TestRenderList_NarrowWidthFitsRows(t *testing.T) {
	tool := &database.ToolCache{
		Name:          "very-long-tool-name-that-needs-cutting",
		Provider:      "node",
		Installed:     true,
		InstalledWith: "pnpm",
		Tracked:       true,
		Package:       "very-long-package-name-that-also-needs-cutting",
	}
	tool.Version.Valid = true
	tool.Version.String = "1.2.3-beta-with-extra-build-metadata"

	m := baseModel([]*database.ToolCache{tool})
	m.width = 44
	m.height = 12
	m.toolGroups = map[string]string{toolKey(tool.Name, tool.Provider): "very-long-group-name"}
	m.groupNames = []string{"very-long-group-name"}
	m.applyFilter()

	assertLinesFitWidth(t, renderList(m), m.width)
}

func TestApplyFilter_UsesEcosystemProviderFilters(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "ripgrep", Provider: "system", Tracked: true}})
	m.providerNames = nil
	m.applyFilter()
	for _, want := range []string{"system", "node", "python"} {
		if !slices.Contains(m.providerNames, want) {
			t.Fatalf("providerNames = %v, want ecosystem provider %q", m.providerNames, want)
		}
	}
}

func TestRenderList_Empty(t *testing.T) {
	m := baseModel(nil)
	out := renderList(m)
	if !strings.Contains(out, "no tools") {
		t.Errorf("expected 'no tools' for empty list, got: %q", out)
	}
}

func TestRenderList_WithTools(t *testing.T) {
	m := baseModel(threeTools())
	out := renderList(m)
	for _, want := range []string{"all", "system", "node", "python"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected provider filter %q in list output, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "git") {
		t.Errorf("expected 'git' in list output, got:\n%s", out)
	}
	if !strings.Contains(out, "node") {
		t.Errorf("expected 'node' in list output, got:\n%s", out)
	}
}

func TestRenderList_NoPanicWithCursor(t *testing.T) {
	m := baseModel(threeTools())
	m.cursor = 1
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("renderList panicked: %v", r)
		}
	}()
	_ = renderList(m)
}

func TestRenderList_LoadingState(t *testing.T) {
	m := baseModel(nil)
	m.loading = true
	m.scanningProviders = map[string]bool{"brew": true}
	out := renderList(m)
	for _, unwanted := range []string{activityLabel(m), m.spinner.View(), "no tools"} {
		if unwanted != "" && strings.Contains(out, unwanted) {
			t.Fatalf("renderList should leave launch/update status to footer, found %q in %q", unwanted, out)
		}
	}
}

func TestInlineDetailLines_NoTool(t *testing.T) {
	m := baseModel(nil)
	m.cursor = -1
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	if lines != nil {
		t.Errorf("expected nil detail lines when no tool selected, got: %v", lines)
	}
}

func TestInlineDetailLines_WithDescription(t *testing.T) {
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true}
	tool.Description.Valid = true
	tool.Description.String = "the fast version control system"
	tool.Tracked = true

	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	if len(lines) == 0 {
		t.Error("expected detail lines for tool with description")
	}
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "version control") {
		t.Errorf("expected description in detail lines, got:\n%s", combined)
	}
}

func TestToolDetailWrapWidth_ReachesProviderBoundary(t *testing.T) {
	cols := colWidths{name: 20, prov: 10, ver: 8, group: 8, screenW: 100}
	prefixW := lipgloss.Width(listTextPrefix())
	got := toolDetailWrapWidth(100, cols, prefixW)
	oldConservativeWidth := cols.name + listColumnGap + cols.prov
	if got <= oldConservativeWidth {
		t.Fatalf("wrap width = %d, want wider than old conservative width %d", got, oldConservativeWidth)
	}

	rightStart := rowMarkerWidth + rowAvailableWidth(100) - toolRightGroupWidth(cols)
	if prefixW+got > rightStart-listColumnGap {
		t.Fatalf("wrapped detail reaches provider metadata: prefix+width=%d boundary=%d", prefixW+got, rightStart-listColumnGap)
	}
}

func TestInlineDetailLines_ConfirmationOnlyReplacesHints(t *testing.T) {
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	tool.Description.Valid = true
	tool.Description.String = "the fast version control system"
	tool.Version.Valid = true
	tool.Version.String = "2.40.0, abc123"

	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	m.armListConfirmation(listConfirmDelete, tool)

	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "version control") {
		t.Fatalf("confirmation should keep description detail, got:\n%s", combined)
	}
	if !strings.Contains(combined, "2.40.0, abc123") {
		t.Fatalf("confirmation should keep full version detail, got:\n%s", combined)
	}
	if !strings.Contains(combined, "confirm delete") {
		t.Fatalf("confirmation should replace action hints, got:\n%s", combined)
	}
	if strings.Contains(combined, "cancel") || strings.Contains(combined, "esc") {
		t.Fatalf("destructive confirmation should not show cancel hint, got:\n%s", combined)
	}
	if strings.Contains(combined, "⚠") {
		t.Fatalf("confirmation should not replace detail content with a warning line, got:\n%s", combined)
	}
}

func TestInlineDetailLines_RowOperationReplacesHints(t *testing.T) {
	tool := &database.ToolCache{Name: "curl", Provider: "brew", Installed: false, Tracked: true}
	tool.Description.Valid = true
	tool.Description.String = "transfer data with URLs"

	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	m.startRowOperation("curl", "brew", "Installing curl…")

	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "transfer data") {
		t.Fatalf("row operation should keep description detail, got:\n%s", combined)
	}
	if !strings.Contains(combined, "Installing curl…") {
		t.Fatalf("row operation should show current status in hint slot, got:\n%s", combined)
	}
	if strings.Contains(combined, "edit groups") || strings.Contains(combined, "delete") {
		t.Fatalf("row operation should replace normal action hints, got:\n%s", combined)
	}
	if strings.Index(combined, "Installing curl…") < strings.Index(combined, "transfer data") {
		t.Fatalf("row operation status should replace the hint line after details, got:\n%s", combined)
	}
}

func TestRenderList_RowActionErrorShowsBehindToolName(t *testing.T) {
	tool := &database.ToolCache{Name: "curl", Provider: "brew", Installed: false, Tracked: true}
	tool.Description.Valid = true
	tool.Description.String = "transfer data with URLs"
	other := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}

	m := baseModel([]*database.ToolCache{tool, other})
	m.cursor = 1
	m.setToolActionError(toolKey("curl", "brew"), "provider not found")

	out := renderList(m)
	if !strings.Contains(out, "provider not found") {
		t.Fatalf("row action error should render on the tool row even when not selected, got:\n%s", out)
	}
	if !strings.Contains(out, m.palette.styleErr.Render(iconFailed)) {
		t.Fatalf("row action error should use error icon, got:\n%s", out)
	}
}

func TestRenderList_BulkPendingUsesWaitingIcon(t *testing.T) {
	tool := &database.ToolCache{Name: "curl", Provider: "brew", Installed: true, Outdated: true, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	m.upgradingKeys = make(map[string]bool)
	m.upgradingKeys["*"] = true
	m.bulkPendingKeys = map[string]bool{toolKey("curl", "brew"): true}

	out := renderList(m)
	if !strings.Contains(out, m.palette.styleStatus.Render(iconPending)) {
		t.Fatalf("bulk pending row should use waiting icon, got:\n%s", out)
	}
}

func TestRenderList_RowOperationUsesSpinnerIcon(t *testing.T) {
	tool := &database.ToolCache{Name: "curl", Provider: "brew", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	m.startRowOperation("curl", "brew", "Installing curl…")

	out := renderList(m)
	if spin := m.spinner.View(); spin != "" && !strings.Contains(out, spin) {
		t.Fatalf("rendered list should show row spinner %q, got:\n%s", spin, out)
	}
	if !strings.Contains(out, "Installing curl…") {
		t.Fatalf("rendered list should show row operation status, got:\n%s", out)
	}
}

func TestInlineDetailLines_ShowsFullCommaVersionSuffix(t *testing.T) {
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	tool.Version.Valid = true
	tool.Version.String = "2.40.0, abc123"

	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "2.40.0, abc123") {
		t.Errorf("expected full version in selected-row detail, got:\n%s", combined)
	}
}

func TestInlineDetailLines_ShowsIgnoreSource(t *testing.T) {
	tool := &database.ToolCache{Name: "ignored-pkg", Provider: "python", Installed: false, Tracked: true}
	tool.Description.Valid = true
	tool.Description.String = "ignored test package"

	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	m.ignoreLabels = map[string]string{"ignored-pkg": "group work"}
	cols := colWidths{name: 20, prov: 10, screenW: 120}

	lines := inlineDetailLines(m, 120, cols)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "ignored by") || !strings.Contains(combined, "group work") {
		t.Fatalf("selected-row detail should show ignore source, got:\n%s", combined)
	}
}

func TestInlineDetailLines_NoDescription(t *testing.T) {
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	tool.Description.Valid = false
	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	if len(lines) == 0 {
		t.Error("expected at least the 'no description' fallback line")
	}
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "no description") {
		t.Errorf("expected 'no description' fallback, got:\n%s", combined)
	}
}

func TestWrapText_Basic(t *testing.T) {
	cases := []struct {
		input string
		width int
		check func([]string) bool
	}{
		{"hello world", 20, func(s []string) bool { return len(s) == 1 }},
		{"hello world", 5, func(s []string) bool { return len(s) >= 2 }},
		{"", 10, func(s []string) bool { return len(s) == 1 && s[0] == "" }},
	}
	for _, tc := range cases {
		got := wrapText(tc.input, tc.width)
		if !tc.check(got) {
			t.Errorf("wrapText(%q, %d) = %v, check failed", tc.input, tc.width, got)
		}
	}
}

func TestRenderProviderCol_NoConcreteProvider(t *testing.T) {
	p := defaultPalette()
	out := renderProviderCol(p, "cargo", "", "", "", "", "cargo", 10, false, false)
	if !strings.Contains(out, "cargo") {
		t.Errorf("expected 'cargo' in provider col, got: %q", out)
	}
}

func TestRenderProviderCol_WithConcrete(t *testing.T) {
	p := defaultPalette()
	// system meta with brew concrete — no override
	out := renderProviderCol(p, "system", "brew", "", "", "", "system(brew)", 14, false, false)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in provider col, got: %q", out)
	}
}

func TestRenderProviderCol_Selected(t *testing.T) {
	p := defaultPalette()
	out := renderProviderCol(p, "system", "brew", "", "", "", "system(brew)", 14, true, false)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in selected provider col, got: %q", out)
	}
}

// ── view_helpers.go ───────────────────────────────────────────────────────────

func TestRenderHRule_NonEmpty(t *testing.T) {
	p := defaultPalette()
	out := renderHRule(p, 80)
	if out == "" {
		t.Error("expected non-empty horizontal rule")
	}
}

func TestRenderHRule_SmallWidth(t *testing.T) {
	p := defaultPalette()
	out := renderHRule(p, 10)
	if out == "" {
		t.Error("expected non-empty rule for small width")
	}
	if got := lipgloss.Width(out); got != 10 {
		t.Fatalf("small horizontal rule width = %d, want 10", got)
	}
}

func TestSelectedRowPrefix_UsesMarker(t *testing.T) {
	p := defaultPalette()
	if got := selectedRowPrefix(p); !strings.Contains(got, selectedRowMarker) {
		t.Fatalf("selected row prefix should use marker %q, got %q", selectedRowMarker, got)
	}
}

func TestPopupFrame_ClampsToWindow(t *testing.T) {
	m := baseModel(nil)
	m.width = 38
	m.height = 12
	bg := strings.Repeat(" ", m.width)
	content := "this is intentionally long popup content that should wrap or clip inside the resized terminal"
	out := placePopup(bg, m, content, popupFrame{Title: "A very long popup title that must not widen the frame", PaddingY: 1, PaddingX: 2, Width: 80})
	assertLinesFitWidth(t, out, m.width)
}

func TestPopupDefaultWidth_DependsOnWindowNotContent(t *testing.T) {
	m := baseModel(nil)
	m.width = 100
	shortFrame := fitPopupFrameToWindow(m, popupFrame{})
	longFrame := fitPopupFrameToWindow(m, popupFrame{Title: strings.Repeat("title ", 20)})
	if shortFrame.Width != longFrame.Width {
		t.Fatalf("default popup width changed by content/title: short=%d long=%d", shortFrame.Width, longFrame.Width)
	}

	narrow := m
	narrow.width = 42
	narrowFrame := fitPopupFrameToWindow(narrow, popupFrame{})
	if narrowFrame.Width >= shortFrame.Width {
		t.Fatalf("popup width did not shrink on resize: narrow=%d wide=%d", narrowFrame.Width, shortFrame.Width)
	}
}

func TestAlignLR_Basic(t *testing.T) {
	out := alignLR("left", "right", 20, 1)
	if !strings.Contains(out, "left") {
		t.Errorf("expected 'left' in alignLR output, got: %q", out)
	}
	if !strings.Contains(out, "right") {
		t.Errorf("expected 'right' in alignLR output, got: %q", out)
	}
}

func TestAlignLR_MinGap(t *testing.T) {
	// When totalWidth is smaller than content, minGap should be used.
	out := alignLR("left", "right", 1, 2)
	if !strings.Contains(out, "left") || !strings.Contains(out, "right") {
		t.Errorf("expected both parts in alignLR output, got: %q", out)
	}
}

func TestRenderSplitRow_MaximizesGapBetweenGroups(t *testing.T) {
	out := renderSplitRow(
		[]rowCell{leftCell("name", 8), leftCell("provider", 8)},
		[]rowCell{rightCell("[dev]", 8)},
		40,
		2,
		listColumnGap,
	)
	if !strings.Contains(out, "name") || !strings.Contains(out, "provider") || !strings.Contains(out, "[dev]") {
		t.Fatalf("split row missing columns: %q", out)
	}
	if got := lipgloss.Width(out); got != 40 {
		t.Fatalf("split row width = %d, want 40: %q", got, out)
	}
	if visualColumnOf(out, "[dev]") <= visualColumnOf(out, "provider")+lipgloss.Width("provider") {
		t.Fatalf("right group should appear after left group: %q", out)
	}
}

func TestRenderSectionHeader_NonEmpty(t *testing.T) {
	p := defaultPalette()
	out := renderSectionHeader(p, "Test Section", 80)
	if !strings.Contains(out, "Test Section") {
		t.Errorf("expected 'Test Section' in header, got: %q", out)
	}
}

func TestRenderSectionHeaderDanger_NonEmpty(t *testing.T) {
	p := defaultPalette()
	out := renderSectionHeaderDanger(p, "Maintenance", 80)
	if !strings.Contains(out, "Maintenance") {
		t.Errorf("expected 'Maintenance' in danger header, got: %q", out)
	}
}

func TestRenderPillBar_Basic(t *testing.T) {
	p := defaultPalette()
	out := renderPillBar(p, []string{"system", "node"}, 0)
	if !strings.Contains(out, "all") {
		t.Errorf("expected 'all' in pill bar, got: %q", out)
	}
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in pill bar, got: %q", out)
	}
}

func TestRenderPillBar_ActiveNonZero(t *testing.T) {
	p := defaultPalette()
	out := renderPillBar(p, []string{"system", "node"}, 1)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in pill bar with activeIdx=1, got: %q", out)
	}
}

func TestRenderInlineHints_Empty(t *testing.T) {
	p := defaultPalette()
	out := renderInlineHints(p, nil, "  ")
	if out != "" {
		t.Errorf("expected empty string for empty hints, got: %q", out)
	}
}

func TestRenderInlineHints_WithHints(t *testing.T) {
	p := defaultPalette()
	hints := []hintItem{
		rawHint("enter", "confirm"),
		rawHint("esc", "cancel"),
	}
	out := renderInlineHints(p, hints, "  ")
	if !strings.Contains(out, "confirm") {
		t.Errorf("expected 'confirm' in inline hints, got: %q", out)
	}
	if !strings.Contains(out, "cancel") {
		t.Errorf("expected 'cancel' in inline hints, got: %q", out)
	}
}

func TestActionHintBuilders_RenderSharedConfirmAndPressAgain(t *testing.T) {
	m := baseModel(nil)
	confirm := renderConfirmActionHints(m, "  ", m.keys.Delete, "confirm delete")
	for _, want := range []string{m.keys.Delete.Help().Key, "confirm delete"} {
		if !strings.Contains(confirm, want) {
			t.Fatalf("shared confirm hint missing %q: %q", want, confirm)
		}
	}
	if strings.Contains(confirm, "cancel") || strings.Contains(confirm, m.keys.Back.Help().Key) {
		t.Fatalf("shared destructive confirm hint should not include cancel: %q", confirm)
	}
	if want := m.palette.styleDangerSection.Render(m.keys.Delete.Help().Key); !strings.Contains(confirm, want) {
		t.Fatalf("shared confirm key should use danger style, got: %q", confirm)
	}
	if want := m.palette.styleDangerLabel.Bold(true).Render(" confirm delete"); !strings.Contains(confirm, want) {
		t.Fatalf("shared confirm message should use danger style, got: %q", confirm)
	}

	pressAgain := pressAgainBinding(m.keys.SyncAll, "sync all").Help()
	if pressAgain.Desc != "press "+pressAgain.Key+" again to sync all" {
		t.Fatalf("press-again binding desc = %q", pressAgain.Desc)
	}
	pressAgainRendered := renderPressAgainActionHint(m.palette, "", m.keys.SyncAll.Help().Key, "sync all")
	if want := m.palette.styleDangerLabel.Bold(true).Render(" press " + m.keys.SyncAll.Help().Key + " again to sync all"); !strings.Contains(pressAgainRendered, want) {
		t.Fatalf("press-again confirmation should use danger style, got: %q", pressAgainRendered)
	}
}

func TestContextConfirmHintsUseDangerStyle(t *testing.T) {
	m := baseModel(nil)
	cases := []struct {
		name string
		ctx  hintContext
		word string
	}{
		{"dots delete", hintCtxDotsDeleteConfirm, "no"},
		{"dots use repo", hintCtxDotsRepoConfirm, "confirm use repo"},
		{"dots use local", hintCtxDotsLocalConfirm, "confirm use local"},
		{"settings danger", hintCtxSettingsDanger, "confirm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderContextHints(m, tc.ctx, "")
			if want := m.palette.styleDangerLabel.Bold(true).Render(" " + tc.word); !strings.Contains(out, want) {
				t.Fatalf("%s hint should use danger style, got: %q", tc.name, out)
			}
			if strings.Contains(out, "cancel") || strings.Contains(out, m.keys.Back.Help().Key) {
				t.Fatalf("%s destructive hint should not include cancel: %q", tc.name, out)
			}
		})
	}
}

func TestToolInlineHints_OutdatedWrongProviderStartsWithUpgrade(t *testing.T) {
	tool := wrongProvTool()
	tool.Outdated = true
	m := wrongProvModel()
	m.allTools = []*database.ToolCache{tool}
	m.visibleTools = []*database.ToolCache{tool}

	hints := toolInlineHints(m, tool)
	if len(hints) == 0 {
		t.Fatal("expected inline hints")
	}

	got := hintKeys(hints)
	want := []string{
		m.keys.Upgrade.Help().Key,
		m.keys.PinProvider.Help().Key,
		m.keys.MigrateProvider.Help().Key,
		m.keys.MoveGroup.Help().Key,
		m.keys.Ignore.Help().Key,
		m.keys.Delete.Help().Key,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hint key order = %v, want %v", got, want)
	}
}

func TestToolInlineHints_DefaultActionOrder(t *testing.T) {
	tool := &database.ToolCache{Name: "ripgrep", Provider: "system", Installed: true, Tracked: true, Outdated: true}
	m := baseModel([]*database.ToolCache{tool})

	got := hintKeys(toolInlineHints(m, tool))
	want := []string{
		m.keys.Upgrade.Help().Key,
		m.keys.MoveGroup.Help().Key,
		m.keys.Ignore.Help().Key,
		m.keys.Delete.Help().Key,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hint key order = %v, want %v", got, want)
	}
}

func TestToolInlineHints_MissingConfiguredToolCanDelete(t *testing.T) {
	tool := &database.ToolCache{Name: "ripgrep", Provider: "brew", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})

	hints := toolInlineHints(m, tool)
	got := hintKeys(hints)
	want := []string{
		m.keys.Install.Help().Key,
		m.keys.MoveGroup.Help().Key,
		m.keys.Ignore.Help().Key,
		m.keys.Delete.Help().Key,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hint key order = %v, want %v", got, want)
	}
}

func TestListConfirmationDetailLine_MissingToolConfirmsDelete(t *testing.T) {
	tool := &database.ToolCache{Name: "ripgrep", Provider: "brew", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.armListConfirmation(listConfirmDelete, tool)

	out := listConfirmationHintsLine(m, tool, "")
	if !strings.Contains(out, "confirm delete") {
		t.Fatalf("confirmation line = %q, want delete wording", out)
	}
}

func TestToolInlineHints_PinnedProviderDoesNotExposeLegacyUnpin(t *testing.T) {
	tool := &database.ToolCache{Name: "typescript", Provider: "pnpm", Installed: true, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})

	hints := toolInlineHints(m, tool)
	got := hintKeys(hints)
	want := []string{
		m.keys.MoveGroup.Help().Key,
		m.keys.Ignore.Help().Key,
		m.keys.Delete.Help().Key,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hint key order = %v, want %v", got, want)
	}
}

func TestScrollBuf_WriteMark(t *testing.T) {
	var buf scrollBuf
	buf.write("line1\n")
	buf.write("line2\n")
	buf.markCursor()
	buf.write("line3\n")
	// cursor should be at line 2 (0-indexed after 2 newlines)
	if buf.cursorLine != 2 {
		t.Errorf("expected cursorLine=2, got %d", buf.cursorLine)
	}
}

func TestScrollBuf_Render(t *testing.T) {
	var buf scrollBuf
	for i := 0; i < 10; i++ {
		buf.write("line\n")
	}
	buf.cursorLine = 5
	out := buf.render(3)
	if out == "" {
		t.Error("expected non-empty render output")
	}
}

// ── view.go remaining functions ───────────────────────────────────────────────

func TestWindowTitle_AllModes(t *testing.T) {
	cases := []struct {
		mode viewMode
		want string
	}{
		{viewDots, "dots"},
		{viewProfiles, "profiles"},
		{viewSettings, "settings"},
		{viewSetup, "setup"},
		{viewList, "omni"},
		{viewSearch, "omni"},
		{viewCommand, "omni"},
	}
	for _, tc := range cases {
		m := baseModel(nil)
		m.mode = tc.mode
		got := m.windowTitle()
		if !strings.Contains(got, tc.want) {
			t.Errorf("windowTitle for mode %d = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

func TestListAvailableHeight_Default(t *testing.T) {
	m := baseModel(nil)
	m.height = 40
	h := listAvailableHeight(m)
	if h < 1 {
		t.Errorf("expected positive listAvailableHeight, got %d", h)
	}
	// default mode: height - 2 (header) - 2 (footer) = 36
	if h != 36 {
		t.Errorf("expected listAvailableHeight=36, got %d", h)
	}
}

func TestListAvailableHeight_SearchMode(t *testing.T) {
	m := baseModel(nil)
	m.height = 40
	m.mode = viewSearch
	h := listAvailableHeight(m)
	// search mode adds 2 more fixed lines
	if h != 34 {
		t.Errorf("expected listAvailableHeight=34 in search mode, got %d", h)
	}
}

func TestListAvailableHeight_CommandMode(t *testing.T) {
	m := baseModel(nil)
	m.height = 40
	m.mode = viewCommand
	h := listAvailableHeight(m)
	if h != 34 {
		t.Errorf("expected listAvailableHeight=34 in command mode, got %d", h)
	}
}

func TestListAvailableHeight_TooSmall(t *testing.T) {
	m := baseModel(nil)
	m.height = 1
	h := listAvailableHeight(m)
	if h < 1 {
		t.Errorf("expected listAvailableHeight >= 1 even for tiny terminal, got %d", h)
	}
}

func TestViewString_DefaultMode(t *testing.T) {
	m := baseModel(threeTools())
	out := m.viewString()
	if out == "" {
		t.Error("expected non-empty viewString output")
	}
}

func TestViewString_SetupMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 0
	out := m.viewString()
	if out == "" {
		t.Error("expected non-empty viewString for setup mode")
	}
	if !strings.Contains(out, "No settings.json") {
		t.Errorf("expected setup content in viewString, got:\n%s", out)
	}
	if !strings.Contains(out, "Tools") {
		t.Errorf("expected setup to render over the normal tools UI, got:\n%s", out)
	}
}

func TestViewString_SetupModeCanRenderOverDotsTab(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupBackgroundMode = viewDots
	m.setupStep = 5
	m.settings.DotsRepo = ""

	out := m.viewString()
	if !strings.Contains(out, "Enable dotfile sync?") {
		t.Errorf("expected setup popup content, got:\n%s", out)
	}
	if !strings.Contains(out, "No dotfiles repo configured yet.") {
		t.Errorf("expected dots tab background, got:\n%s", out)
	}
}

func TestViewString_WithError(t *testing.T) {
	m := baseModel(nil)
	m.err = errForTest("something failed")
	out := m.viewString()
	if !strings.Contains(out, "Error:") {
		t.Errorf("expected 'Error:' in error view, got:\n%s", out)
	}
}

func TestViewString_GroupPickerMode(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroupPicker
	m.cursor = 0
	m.pickerGroups = []string{"base"}
	out := m.viewString()
	if out == "" {
		t.Error("expected non-empty viewString for group picker mode")
	}
}

func TestViewString_SettingsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := m.viewString()
	if !strings.Contains(out, "Node Manager") {
		t.Errorf("expected settings content in viewString, got:\n%s", out)
	}
}

func TestViewString_ProfilesMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewProfiles
	m.profileInfo = &app.ProfileInfo{
		Profiles: map[string]config.Profile{},
	}
	out := m.viewString()
	if !strings.Contains(out, "Profiles") {
		t.Errorf("expected 'Profiles' in profiles viewString, got:\n%s", out)
	}
}

func TestViewString_DotsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	out := m.viewString()
	if out == "" {
		t.Error("expected non-empty viewString for dots mode")
	}
}

func TestViewString_CommandMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewCommand
	out := m.viewString()
	if out == "" {
		t.Error("expected non-empty viewString for command mode")
	}
}

func TestViewString_SearchMode(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewSearch
	out := m.viewString()
	if out == "" {
		t.Error("expected non-empty viewString for search mode")
	}
}

func TestViewString_HelpOverlay(t *testing.T) {
	m := baseModel(nil)
	m.help.ShowAll = true
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("viewString panicked with help overlay: %v", r)
		}
	}()
	out := m.viewString()
	if !strings.Contains(out, "Help") {
		t.Errorf("expected help title in overlay, got: %q", out)
	}
}

func TestViewString_HelpOverlay_StableOnNarrowWidths(t *testing.T) {
	widths := []int{40, 58, 70, 90, 120}
	for _, w := range widths {
		t.Run(fmt.Sprintf("width=%d", w), func(t *testing.T) {
			m := baseModel(nil)
			m.width = w
			m.height = 34
			m.help.ShowAll = true
			const helpPopupPaddingW = 3
			helpContentWidth := helpPopupContentWidth(m)
			helpFrame := popupFrame{
				Title:    helpPopupTitle(m),
				PaddingY: 1,
				PaddingX: helpPopupPaddingW,
				Width:    helpContentWidth + helpPopupPaddingW*2 + 2,
			}
			helpFrame = fitPopupFrameToWindow(m, helpFrame)
			helpContentWidth = max(popupInnerContentWidth(helpFrame), 1)
			out := renderPopupFrame(m.palette, renderHelpPopupWithWidth(m, helpContentWidth), helpFrame)
			dividerFound := false

			for i, line := range strings.Split(out, "\n") {
				if lipgloss.Width(line) > w {
					t.Fatalf("help overlay clipped line %d: width=%d > %d\n%s", i+1, lipgloss.Width(line), w, line)
				}
				trimmed := strings.TrimSpace(strings.Trim(line, "│ "))
				if trimmed != "" && strings.Trim(trimmed, "─") == "" {
					dividerFound = true
					if lipgloss.Width(trimmed) != helpContentWidth {
						t.Fatalf("help overlay divider width wrong at width=%d: got=%d want=%d\n%s", w, lipgloss.Width(trimmed), helpContentWidth, line)
					}
					if lipgloss.Width(trimmed) == 1 {
						t.Fatalf("help overlay divider wrapped to single-char line at width=%d:\n%s", w, out)
					}
				}
				if trimmed == "─" {
					t.Fatalf("help overlay divider wrapped to single-char line at width=%d:\n%s", w, out)
				}
			}
			if !dividerFound {
				t.Fatalf("help overlay expected divider for width=%d", w)
			}
		})
	}
}

func TestViewString_HelpOverlay_DividerLineUsesPopupInnerWidth(t *testing.T) {
	const helpPopupPaddingW = 3
	m := baseModel(nil)
	m.width = 72
	m.height = 34
	m.help.ShowAll = true

	helpContentWidth := helpPopupContentWidth(m)
	helpFrame := popupFrame{
		Title:    helpPopupTitle(m),
		PaddingY: 1,
		PaddingX: helpPopupPaddingW,
		Width:    helpContentWidth + helpPopupPaddingW*2 + 2,
	}
	helpFrame = fitPopupFrameToWindow(m, helpFrame)
	helpContentWidth = max(popupInnerContentWidth(helpFrame), 1)
	out := renderPopupFrame(m.palette, renderHelpPopupWithWidth(m, helpContentWidth), helpFrame)

	found := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(strings.Trim(line, "│ "))
		if trimmed != "" && strings.Trim(trimmed, "─") == "" {
			found = true
			if lipgloss.Width(trimmed) != helpContentWidth {
				t.Fatalf("help divider width=%d, want=%d\n%s", lipgloss.Width(trimmed), helpContentWidth, out)
			}
		}
	}
	if !found {
		t.Fatal("expected divider line in help overlay")
	}
}

func TestViewString_FilePickerOverlay(t *testing.T) {
	m := baseModel(nil)
	m.showFilePicker = true
	m.filePickerTitle = "Select dotfiles repo"
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("viewString panicked with file picker overlay: %v", r)
		}
	}()
	out := m.viewString()
	if count := strings.Count(out, "Select dotfiles repo"); count != 1 {
		t.Errorf("file picker overlay should render one shared title, got %d in %q", count, out)
	}
	if strings.Contains(out, "→") || strings.Contains(out, "←") {
		t.Errorf("file picker overlay hints should not use arrow glyphs, got: %q", out)
	}
	if !strings.Contains(out, "tab") || !strings.Contains(out, "bs") {
		t.Errorf("file picker overlay should render text key labels, got: %q", out)
	}
}

func TestOpenFilePicker_UsesPathInput(t *testing.T) {
	m := baseModel(nil)
	m.openFilePicker("Select dotfiles repo", "", false)

	if m.dotsFilePicker.AutoHeight {
		t.Fatal("file picker should use TUI-managed bounded height, not AutoHeight")
	}
	if got, want := m.dotsFilePicker.Height(), filePickerListHeight(m); got != want {
		t.Fatalf("file picker height = %d, want %d", got, want)
	}
	if m.dotsFilePicker.Cursor != "›" {
		t.Fatalf("file picker cursor = %q, want shared row cursor", m.dotsFilePicker.Cursor)
	}
	if m.dotsFilePicker.input.Value() == "" {
		t.Fatal("file picker input should be prefilled with a starting path")
	}
}

func TestRenderFilePickerPopup_BrowsingMode(t *testing.T) {
	m := baseModel(nil)
	m.showFilePicker = true
	m.filePickerTitle = "Select dotfiles repo"
	out := renderPopupFrame(m.palette, renderFilePickerPopup(m), filePickerPopupFrame(m))
	if !strings.Contains(out, "Select dotfiles repo") {
		t.Errorf("expected title in file picker popup, got: %q", out)
	}
	if count := strings.Count(out, "Select dotfiles repo"); count != 1 {
		t.Errorf("file picker popup should render one title, got %d in %q", count, out)
	}
	if strings.Contains(out, "→") || strings.Contains(out, "←") {
		t.Errorf("file picker hints should not use arrow glyphs, got: %q", out)
	}
	for _, want := range []string{"tab", "complete", "enter", "pick", "bs", "parent", "esc", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("file picker popup missing %q in hints, got: %q", want, out)
		}
	}
	parentIdx := strings.Index(out, "parent")
	pickIdx := strings.Index(out, "pick")
	if parentIdx < 0 || pickIdx < 0 || parentIdx > pickIdx {
		t.Errorf("file picker hints should show parent before pick, got: %q", out)
	}
}

func TestRenderFilePickerPopup_BoundedBrowsingMode(t *testing.T) {
	tmp := t.TempDir()
	for i := range 32 {
		if err := os.Mkdir(filepath.Join(tmp, fmt.Sprintf("dir-%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := baseModel(nil)
	m.width = 80
	m.height = 42
	cmd := m.openFilePicker("Dots repo path", tmp, false)
	if cmd != nil {
		for _, next := range m.updateActiveFilePicker(cmd()) {
			if next != nil {
				_ = next()
			}
		}
	}

	popup := renderPopupFrame(m.palette, renderFilePickerPopup(m), filePickerPopupFrame(m))
	if h := lipgloss.Height(popup); h > 34 {
		t.Fatalf("file picker popup height = %d, want bounded <= 34:\n%s", h, popup)
	}
	if m.dotsFilePicker.Height() > 16 {
		t.Fatalf("file picker list height = %d, want <= 16", m.dotsFilePicker.Height())
	}
	for _, line := range strings.Split(popup, "\n") {
		if strings.TrimSpace(line) == "close" {
			t.Fatalf("file picker footer wrapped close onto its own line:\n%s", popup)
		}
		if strings.TrimSpace(line) == "─" {
			t.Fatalf("file picker divider wrapped onto its own line:\n%s", popup)
		}
	}
	if !strings.Contains(popup, "tab") || !strings.Contains(popup, "enter") || !strings.Contains(popup, "esc") {
		t.Fatalf("file picker popup missing footer hints:\n%s", popup)
	}
}

// ── model.go utility functions ────────────────────────────────────────────────

func TestBuildSetupProviders_WithDisabled(t *testing.T) {
	rows := buildSetupProvidersFromManagers(
		map[string]string{},
		nil,
		nil,
		config.Settings{DisabledProviders: []string{"node"}},
	)
	for _, r := range rows {
		if r.name == "node" && r.enabled {
			t.Error("expected 'node' to be disabled")
		}
	}
}

func TestBuildSetupProviders_LabelsReflectManagers(t *testing.T) {
	rows := buildSetupProvidersFromManagers(
		map[string]string{"system": "apt"},
		[]string{"uv", "pip3"},
		[]string{"bun", "pnpm"},
		config.Settings{},
	)
	for _, r := range rows {
		switch r.name {
		case "system":
			if !strings.Contains(r.label, "apt") {
				t.Errorf("expected 'apt' in system label, got: %q", r.label)
			}
		case "node":
			if !strings.Contains(r.label, "bun") {
				t.Errorf("expected 'bun' in node label, got: %q", r.label)
			}
		case "python":
			if !strings.Contains(r.label, "uv") {
				t.Errorf("expected 'uv' in python label, got: %q", r.label)
			}
		}
	}
}

func TestIsNodeProviderEnabled_True(t *testing.T) {
	rows := []setupProviderRow{
		{name: "system", enabled: true},
		{name: "node", enabled: true},
	}
	if !isNodeProviderEnabled(rows) {
		t.Error("expected isNodeProviderEnabled=true when node is enabled")
	}
}

func TestIsNodeProviderEnabled_False(t *testing.T) {
	rows := []setupProviderRow{
		{name: "system", enabled: true},
		{name: "node", enabled: false},
	}
	if isNodeProviderEnabled(rows) {
		t.Error("expected isNodeProviderEnabled=false when node is disabled")
	}
}

func TestIsNodeProviderEnabled_NoNodeRow(t *testing.T) {
	rows := []setupProviderRow{
		{name: "system", enabled: true},
	}
	if isNodeProviderEnabled(rows) {
		t.Error("expected isNodeProviderEnabled=false when no node row")
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

func hintKeys(hints []hintItem) []string {
	keys := make([]string, len(hints))
	for i, h := range hints {
		keys[i] = h.key
	}
	return keys
}

// errForTest is a simple error type used in viewString error tests.
type errForTest string

func (e errForTest) Error() string { return string(e) }
