package tui

import (
	"context"
	sql "database/sql"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/actions"
	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
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

func TestApplyScrollWindow_StartsScrollingAtBottomFifth(t *testing.T) {
	content := strings.Join([]string{
		"L0", "L1", "L2", "L3", "L4",
		"L5", "L6", "L7", "L8", "L9",
		"L10", "L11", "L12", "L13", "L14",
	}, "\n") + "\n"

	before := applyScrollWindow(content, 7, 10)
	if !strings.HasPrefix(before, "L0\n") {
		t.Fatalf("cursor inside bottom comfort margin should not scroll yet, got:\n%s", before)
	}

	after := applyScrollWindow(content, 8, 10)
	if strings.HasPrefix(after, "L0\n") || !strings.HasPrefix(after, "L1\n") {
		t.Fatalf("cursor entering bottom fifth should scroll by one line, got:\n%s", after)
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
	if !strings.Contains(out, "No settings.json was found.") {
		t.Errorf("expected missing-settings copy in step 0 output, got:\n%s", out)
	}
	if !strings.Contains(out, "Import an existing Omni settings file") {
		t.Errorf("expected import prompt in step 0 output, got:\n%s", out)
	}
	choiceLine := renderedLineContaining(out, "import existing")
	if !strings.Contains(choiceLine, "quit") {
		t.Errorf("expected step 0 choices on one row, got:\n%s", out)
	}
	if !strings.Contains(choiceLine, "create new") {
		t.Errorf("expected create-new choice in step 0 choices, got:\n%s", out)
	}
	if got := visualColumnOf(choiceLine, "quit"); got < 0 || got > 8 {
		t.Errorf("expected abort choice near left edge, column=%d:\n%s", got, out)
	}
	if visualColumnOf(choiceLine, "import existing") <= visualColumnOf(choiceLine, "quit") {
		t.Errorf("expected accept choice to the right of abort choice, got:\n%s", out)
	}
}

func TestRenderSetupPopup_UsesSharedCenteredTitle(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 0
	m.width = 72
	frame := setupPopupFrame(m)
	contentWidth := popupInnerContentWidth(frame)
	out := renderPopupFrame(m.palette, renderSetupPopup(m, contentWidth), frame)
	title := logoMark + " Omni - Import settings"
	expectedCol := 1 + frame.PaddingX + (contentWidth-lipgloss.Width(title))/2

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, title) {
			continue
		}
		if got := visualColumnOf(line, title); got != expectedCol {
			t.Fatalf("setup popup title column=%d, want %d:\n%s", got, expectedCol, out)
		}
		return
	}
	t.Fatalf("setup popup title missing:\n%s", out)
}

func TestRenderSetup_Step1(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 1
	m.setupProviders = []app.SetupProviderOption{
		{Name: "system", Label: "system(brew)", Enabled: true},
		{Name: "node", Label: "node(bun)", Enabled: false},
	}
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output for setupStep=1")
	}
	if strings.Contains(out, "settings.json created") {
		t.Fatalf("step 1 should not render stale config-created status copy:\n%s", out)
	}
	// Step 1 renders the provider picker — check for provider labels
	if !strings.Contains(out, "system") {
		t.Errorf("expected provider label in step 1, got:\n%s", out)
	}
	line := renderedLineContaining(out, "save & continue")
	if line == "" {
		t.Fatalf("expected continue action in step 1, got:\n%s", out)
	}
	assertActionRightAligned(t, out, "save & continue", m.width)
	toggleLine := renderedLineContaining(out, "toggle")
	if toggleLine == "" {
		t.Fatalf("expected toggle action in step 1, got:\n%s", out)
	}
	if !strings.Contains(line, "toggle") {
		t.Fatalf("secondary toggle action should share the popup action edge row:\n%s", out)
	}
}

func TestRenderSetup_Step2(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 2
	m.setupProviders = []app.SetupProviderOption{
		{Name: "system", Label: "system(brew)", Enabled: true},
		{Name: "python", Label: "python(uv)", Enabled: true},
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
	// Step 3 is now the provider-priority editor. The background panel shows
	// the "Set provider priority." lead text; the actual editor is an overlay.
	if !strings.Contains(out, "priority") {
		t.Errorf("expected 'priority' in step 3 output, got:\n%s", out)
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
	assertActionLeftAligned(t, out, "skip")
}

func TestRenderSetup_CopyHostAndGroups(t *testing.T) {
	t.Run("copy prompt", func(t *testing.T) {
		m := setupRenderModel(7)
		m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{"laptop": {}}}
		out := renderSetup(m)
		if !strings.Contains(out, "Copy another host") {
			t.Fatalf("expected copy prompt, got:\n%s", out)
		}
		assertActionLeftAligned(t, out, "start fresh")
		assertActionRightAligned(t, out, "copy host", m.width)
	})

	t.Run("host picker", func(t *testing.T) {
		m := setupRenderModel(8)
		m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{"laptop": {Groups: []string{"base"}}}}
		out := renderSetup(m)
		if !strings.Contains(out, "laptop") || !strings.Contains(out, "base") {
			t.Fatalf("expected host picker details, got:\n%s", out)
		}
		assertActionRightAligned(t, out, "copy selected", m.width)
	})

	t.Run("group selection", func(t *testing.T) {
		m := setupRenderModel(9)
		m.groupNames = []string{"base", "work"}
		m.setupGroupDraft = map[string]bool{"base": true}
		out := renderSetup(m)
		if !strings.Contains(out, "base") || !strings.Contains(out, "[x]") || !strings.Contains(out, "work") {
			t.Fatalf("expected group checklist, got:\n%s", out)
		}
		assertActionLeftAligned(t, out, "skip")
		assertActionRightAligned(t, out, "continue", m.width)
	})

	t.Run("bootstrap activation", func(t *testing.T) {
		m := setupRenderModel(10)
		m.hostInfo = &app.HostInfo{Active: "workstation", Hosts: map[string]config.HostAssignment{
			"workstation": {Groups: []string{"base", "work"}},
		}}
		setDotsRepoForTest(&m, "~/dotfiles")
		out := renderSetup(m)
		if !strings.Contains(out, `Host "workstation" is configured`) || !strings.Contains(out, "Sync tools") {
			t.Fatalf("expected bootstrap activation options, got:\n%s", out)
		}
		assertActionLeftAligned(t, out, "review first")
		assertActionRightAligned(t, out, "continue", m.width)
	})
}

func TestRenderSetup_AllActionFootersUsePopupAlignment(t *testing.T) {
	tests := []struct {
		name      string
		model     Model
		left      string
		right     string
		rightOnly string
	}{
		{name: "import settings", model: setupRenderModel(0), left: "quit", right: "import existing"},
		{name: "provider picker", model: setupRenderModel(1), rightOnly: "save & continue"},
		// Step 3 (provider priority) has no inline footer — its actions live inside
		// the editingPriority popup overlay. It is excluded from this footer-alignment
		// suite; the priority editor's own hints are covered by Settings tests.
		{name: "dotfiles decision", model: setupRenderModel(5), left: "skip for now", right: "set up dotfile sync"},
		{name: "dotfiles picker fallback", model: setupRenderModel(6), left: "skip"},
		{name: "copy host", model: setupRenderModel(7), left: "start fresh", right: "copy host"},
		{name: "host picker", model: setupRenderModel(8), left: "start fresh", right: "copy selected"},
		{name: "group selection", model: setupRenderModel(9), left: "skip", right: "continue"},
		{name: "bootstrap host", model: setupRenderModel(10), left: "review first", right: "continue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderSetup(tt.model)
			if tt.left != "" {
				assertActionLeftAligned(t, out, tt.left)
			}
			if tt.right != "" {
				assertActionRightAligned(t, out, tt.right, tt.model.width)
			}
			if tt.rightOnly != "" {
				assertActionRightAligned(t, out, tt.rightOnly, tt.model.width)
			}
		})
	}
}

func setupRenderModel(step int) Model {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = step
	m.setupProviders = []app.SetupProviderOption{
		{Name: "system", Label: "system(brew)", Enabled: true},
		{Name: "node", Label: "node(bun)", Enabled: false},
	}
	return m
}

func assertActionLeftAligned(t *testing.T, out, needle string) {
	t.Helper()
	line := renderedLineContaining(out, needle)
	if line == "" {
		t.Fatalf("expected action %q in output:\n%s", needle, out)
	}
	if got := visualColumnOf(line, needle); got < 0 || got > 8 {
		t.Fatalf("action %q should be left aligned, column=%d:\n%s", needle, got, out)
	}
}

func assertActionRightAligned(t *testing.T, out, needle string, width int) {
	t.Helper()
	line := renderedLineContaining(out, needle)
	if line == "" {
		t.Fatalf("expected action %q in output:\n%s", needle, out)
	}
	if got := visualColumnOf(line, needle); got < width/2 {
		t.Fatalf("action %q should be right aligned, column=%d width=%d:\n%s", needle, got, width, out)
	}
}

func TestRenderSetup_LoadingUsesFooterOnly(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 0
	m.loading = true
	m.statusMsg = "creating…"
	out := renderSetup(m)
	if out == "" {
		t.Error("expected non-empty output with loading=true")
	}
	if strings.Contains(out, "creating…") {
		t.Fatalf("setup popup body should not render activity progress; footer owns setup loading status:\n%s", out)
	}
}

func TestViewString_PostSetupReloadShowsCenteredProgress(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewList
	m.loading = true
	m.setupReloading = true
	m.progressText = "Loading tools..."
	m.allTools = []*database.ToolCache{{Name: "ripgrep", Provider: "brew", Installed: true}}
	m.applyFilter()

	out := m.viewString()
	if strings.Contains(out, "Omni - Loading") {
		t.Fatalf("post-setup reload should not render a framed loading popup, got:\n%s", out)
	}
	if !strings.Contains(out, "Loading tools...") {
		t.Fatalf("post-setup reload should render progress text, got:\n%s", out)
	}
	if !strings.Contains(out, "Tools") {
		t.Fatalf("post-setup reload should keep the default UI shell visible behind the overlay:\n%s", out)
	}
	if strings.Contains(out, "ripgrep") {
		t.Fatalf("post-setup reload should close the globe before showing tool rows:\n%s", out)
	}
	if logoMark == "o" {
		if !strings.Contains(out, "o") {
			t.Fatalf("post-setup reload should render fallback logo mark, got:\n%s", out)
		}
	} else if !strings.Contains(out, "🌍") && !strings.Contains(out, "🌎") && !strings.Contains(out, "🌏") {
		t.Fatalf("post-setup reload should render spinning globe frame, got:\n%s", out)
	}
	line := renderedLineContaining(out, "Loading tools...")
	gotCol := visualColumnOf(line, "Loading tools...")
	wantCol := (m.width - lipgloss.Width("Loading tools...")) / 2
	if absInt(gotCol-wantCol) > 1 {
		t.Fatalf("post-setup progress should be horizontally centered, column=%d want=%d width=%d:\n%s", gotCol, wantCol, m.width, out)
	}
	gotRow := renderedLineIndexContaining(out, "Loading tools...")
	wantRow := (m.height-4)/2 + 2
	if absInt(gotRow-wantRow) > 1 {
		t.Fatalf("post-setup progress should be vertically centered, row=%d want=%d height=%d:\n%s", gotRow, wantRow, m.height, out)
	}
}

func TestViewString_PostSetupReloadEmphasizesProviderRefresh(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewList
	m.loading = true
	m.setupReloading = true
	m.progressText = "Refreshing tools… 0/1: brew"

	out := m.viewString()
	line := renderedLineContaining(out, "Refreshing tools… 0/1: brew")
	if line == "" {
		t.Fatalf("post-setup reload should render provider refresh text:\n%s", out)
	}
	if !strings.Contains(line, "\x1b[1m") {
		t.Fatalf("provider refresh text should be bold:\n%q\n%s", line, out)
	}
}

func TestViewString_PostSetupReloadClosesSetupPopupFirst(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSetup
	m.setupStep = 5
	m.loading = true
	m.setupReloading = true
	m.progressText = "Loading tools..."

	out := m.viewString()
	if strings.Contains(out, "Enable dotfile sync?") {
		t.Fatalf("setup popup should be closed before post-onboarding loading renders:\n%s", out)
	}
	if !strings.Contains(out, "Loading tools...") {
		t.Fatalf("post-setup reload should render centered progress, got:\n%s", out)
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
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.dotsEntries = []app.DotStatus{{
		Name:   "nvim",
		State:  app.DotStateConflict,
		Counts: app.DotFileCounts{Synced: 2, OutOfSync: 1, Ignored: 4},
	}}
	out := renderHeader(m)
	if !strings.Contains(out, "2/3") || !strings.Contains(out, "(4)") {
		t.Errorf("expected dots count summary in header, got: %q", out)
	}
}

func TestRenderHeaderInfo_UsesUniformRegularWeight(t *testing.T) {
	tools := baseModel([]*database.ToolCache{{
		Name:      "git",
		Provider:  "brew",
		Installed: true,
		Outdated:  true,
	}})
	dots := baseModel(nil)
	dots.mode = viewDots
	dots.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	dots.dotsEntries = []app.DotStatus{{
		Name:   "nvim",
		State:  app.DotStateConflict,
		Counts: app.DotFileCounts{Synced: 1, OutOfSync: 1, Ignored: 1},
	}}
	groups := baseModel(nil)
	groups.mode = viewGroups
	groups.groupNames = []string{"dev"}
	settings := baseModel(nil)
	settings.mode = viewSettings
	settings.setSettings(config.Settings{DotsRepo: "~/dotfiles"})

	for name, m := range map[string]Model{
		"tools":    tools,
		"dots":     dots,
		"groups":   groups,
		"settings": settings,
	} {
		out := renderHeaderInfo(m)
		if stripANSIEscapeSequences(out) == "" {
			t.Fatalf("%s header info is empty", name)
		}
		if headerInfoHasBoldANSI(out) {
			t.Fatalf("%s header info should use regular weight: %q", name, out)
		}
	}
}

func headerInfoHasBoldANSI(s string) bool {
	return strings.Contains(s, "\x1b[1m") || strings.Contains(s, "\x1b[1;")
}

func TestRenderHeader_DotsLoadingKeepsCountSummary(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.dotsLoading = true
	m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}
	out := renderHeader(m)
	if strings.Contains(out, "loading") {
		t.Errorf("dots refresh status belongs in footer, got loading header: %q", out)
	}
	if !strings.Contains(out, "1/1") {
		t.Errorf("expected count summary while dots loading, got: %q", out)
	}
}

func TestRenderHeader_DotsDisabledShowsNoInfo(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles", DotsDisabled: config.BoolPtr(true)})
	m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}

	out := stripANSIEscapeSequences(renderHeader(m))
	if strings.Contains(out, "1/1") || strings.Contains(out, "entries") || strings.Contains(out, "dirty") {
		t.Fatalf("dots disabled header should not show dots info: %q", out)
	}
}

func TestRenderHeader_DotsGitStatusShowsDirty(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.dotsGitStatus = "M .zshrc"
	m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}
	out := stripANSIEscapeSequences(renderHeader(m))
	if !strings.Contains(out, "dirty") {
		t.Errorf("expected dirty dots repo in header summary, got: %q", out)
	}
	if !strings.Contains(out, "1/1") {
		t.Errorf("expected synced dots count summary, got: %q", out)
	}
}

func TestRenderHeader_GroupsModeUsesGroupInfo(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewGroups
	m.groupNames = []string{"dev", "ops"}
	m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{
		"work": {},
		"home": {},
	}}

	out := stripANSIEscapeSequences(renderHeader(m))
	if !strings.Contains(out, "3 groups") || !strings.Contains(out, "2 hosts") {
		t.Fatalf("groups header info = %q, want group/host counts", out)
	}
	if strings.Contains(out, "tools") {
		t.Fatalf("groups header should not show tools info: %q", out)
	}
}

func TestRenderHeader_SettingsModeUsesSettingsInfo(t *testing.T) {
	m := baseModel(threeTools())
	m.mode = viewSettings
	m.settings.DisabledProviders = []string{"node", "node", "brew"}

	out := stripANSIEscapeSequences(renderHeader(m))
	if !strings.Contains(out, "2/3 providers") || !strings.Contains(out, "dots unset") {
		t.Fatalf("settings header info = %q, want settings summary", out)
	}
	if strings.Contains(out, "tools") {
		t.Fatalf("settings header should not show tools info: %q", out)
	}
}

func TestRenderHeader_SettingsModeShowsDotsOff(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles", DotsDisabled: config.BoolPtr(true)})

	out := stripANSIEscapeSequences(renderHeader(m))
	if !strings.Contains(out, "3/3 providers") || !strings.Contains(out, "dots off") {
		t.Fatalf("settings header info = %q, want disabled dots summary", out)
	}
}

func TestRenderSettings_ShowsProviderPriorityRow(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.width = 120
	m.height = 50
	settings := tuiSettingsWithPriority("brew", "apt")
	m.setSettings(settings)

	out := stripANSIEscapeSequences(renderSettings(m))
	for _, want := range []string{
		"Provider Priority",
		"brew › apt",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings view missing %q:\n%s", want, out)
		}
	}
	// Removed rows must not appear.
	for _, gone := range []string{"System Provider Order", "Track System", "Track Node", "Track Python", "Node Manager", "Python Manager"} {
		if strings.Contains(out, gone) {
			t.Fatalf("settings view should not contain removed row %q:\n%s", gone, out)
		}
	}
}

func TestRenderHeaderUsesCachedDotsAvailability(t *testing.T) {
	t.Run("settings shows dots on when app is enabled despite stale disabled setting", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
		m.settings = config.Settings{DotsRepo: "/repo/dotfiles", DotsDisabled: config.BoolPtr(true)}

		out := stripANSIEscapeSequences(renderHeader(m))

		if !strings.Contains(out, "dots on") || strings.Contains(out, "dots off") {
			t.Fatalf("settings header should use app-backed enabled state: %q", out)
		}
	})

	t.Run("settings shows dots unset when app is unconfigured despite stale repo", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsSyncAvailCached = app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo}

		out := stripANSIEscapeSequences(renderHeader(m))

		if !strings.Contains(out, "dots unset") || strings.Contains(out, "dots on") {
			t.Fatalf("settings header should use app-backed unconfigured state: %q", out)
		}
	})

	t.Run("dots header keeps counts when app is enabled despite stale disabled setting", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewDots
		m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
		m.settings = config.Settings{DotsRepo: "/repo/dotfiles", DotsDisabled: config.BoolPtr(true)}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}

		out := stripANSIEscapeSequences(renderHeader(m))

		if !strings.Contains(out, "1/1") {
			t.Fatalf("dots header should use app-backed enabled state: %q", out)
		}
	})
}

func TestRenderHeader_Searching(t *testing.T) {
	m := baseModel(threeTools())
	m.searching = true
	out := stripANSIEscapeSequences(renderHeader(m))
	if strings.Contains(out, "searching") {
		t.Errorf("search status belongs in footer, got header: %q", out)
	}
	if !strings.Contains(out, "3 tools") {
		t.Errorf("expected stable tool count while searching, got: %q", out)
	}
}

func TestRenderHeader_ScanningProviders(t *testing.T) {
	m := baseModel(threeTools())
	m.scanningProviders = map[string]bool{"brew": true}
	out := stripANSIEscapeSequences(renderHeader(m))
	if strings.Contains(out, "scanning") {
		t.Errorf("scan status belongs in footer, got header: %q", out)
	}
	if !strings.Contains(out, "3 tools") {
		t.Errorf("expected stable tool count while scanning, got: %q", out)
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

func TestRenderTabs_Order(t *testing.T) {
	out := renderTabs(baseModel(nil))
	assertOrderedSubstrings(t, out, "Dashboard", "Tools", "Dots", "Groups", "Settings")
}

func TestRenderTabs_DotsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	out := renderTabs(m)
	if !strings.Contains(out, "Dots") {
		t.Errorf("expected 'Dots' tab in dots mode output, got: %q", out)
	}
}

func TestRenderTabs_HostsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	out := renderTabs(m)
	if !strings.Contains(out, "Groups") {
		t.Errorf("expected 'Groups' in groups mode output, got: %q", out)
	}
}

func TestRenderTabs_StatusMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	out := renderTabs(m)
	if !strings.Contains(out, "Dashboard") {
		t.Errorf("expected 'Dashboard' in status mode output, got: %q", out)
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
	for _, mode := range []viewMode{viewDots, viewStatus, viewGroups, viewSettings} {
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

func TestRenderPopupFrame_CentersTitle(t *testing.T) {
	m := baseModel(nil)
	frame := popupFrame{
		Title:    "Header",
		Width:    34,
		PaddingY: 1,
		PaddingX: 2,
	}
	popup := renderPopupFrame(m.palette, "body", frame)
	expectedCol := 1 + frame.PaddingX + (popupInnerContentWidth(frame)-lipgloss.Width(frame.Title))/2

	for _, line := range strings.Split(popup, "\n") {
		if !strings.Contains(line, frame.Title) {
			continue
		}
		if got := visualColumnOf(line, frame.Title); got != expectedCol {
			t.Fatalf("popup title column=%d, want %d:\n%s", got, expectedCol, popup)
		}
		return
	}
	t.Fatalf("popup title missing:\n%s", popup)
}

func TestRenderPopupFrame_ClampsContentDividers(t *testing.T) {
	m := baseModel(nil)
	frame := popupFrame{
		Title:    "Picker",
		Width:    30,
		PaddingY: 1,
		PaddingX: 2,
	}
	innerWidth := popupInnerContentWidth(frame)
	content := strings.Join([]string{
		"body",
		popupDivider(m.palette, innerWidth+1),
		popupDividerWithStyle(m.palette.styleHelp, innerWidth+1),
	}, "\n")

	popup := renderPopupFrame(m.palette, content, frame)
	assertLinesFitWidth(t, popup, frame.Width)
	for _, line := range strings.Split(popup, "\n") {
		if strings.TrimSpace(line) == "─" {
			t.Fatalf("popup divider wrapped onto a 1-char line:\n%s", popup)
		}
	}
}

func TestRenderPopupFrame_DoesNotPaintDefaultBackgroundAfterStyledContentReset(t *testing.T) {
	m := baseModel(nil)
	m.palette = defaultPalette()
	content := m.palette.styleNormal.Render("Enable dotfile sync?")
	popup := renderPopupFrame(m.palette, content, popupFrame{
		Width:    42,
		PaddingY: 0,
		PaddingX: 2,
	})

	for _, line := range strings.Split(popup, "\n") {
		if !strings.Contains(stripANSIEscapeSequences(line), "Enable dotfile sync?") {
			continue
		}
		if strings.Contains(line, "[48;") {
			t.Fatalf("popup content line should leave the terminal background unpainted:\n%q", line)
		}
		return
	}
	t.Fatalf("popup content missing:\n%s", popup)
}

func TestRenderPopupFrame_DoesNotPaintDefaultFrameBackground(t *testing.T) {
	m := baseModel(nil)
	m.palette = defaultPalette()
	innerWidth := popupInnerContentWidth(popupFrame{Width: 44, PaddingX: 2})
	content := strings.Join([]string{
		m.palette.styleNormal.Render("Body"),
		popupDivider(m.palette, innerWidth),
		renderPopupActionHintText(m.palette, innerWidth, confirmActionItems(m.keys.Confirm, "save", m.keys.Back)),
	}, "\n")

	popup := renderPopupFrame(m.palette, content, popupFrame{
		Title:    "Styled Popup",
		Width:    44,
		PaddingY: 1,
		PaddingX: 2,
	})

	for i, line := range strings.Split(popup, "\n") {
		if strings.Contains(line, "[48;") {
			t.Fatalf("popup line %d should leave the terminal background unpainted:\n%q\n\n%s", i+1, line, popup)
		}
	}
}

func TestRenderPickerHints_DividerUsesPopupBodyWidth(t *testing.T) {
	m := baseModel(nil)
	width := 24
	out := renderPickerHintItems(m, width, confirmActionItems(m.keys.Confirm, "create", m.keys.Back))
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected divider plus hint line, got %q", out)
	}
	if got := lipgloss.Width(lines[0]); got != width {
		t.Fatalf("picker hint divider width=%d, want %d\n%s", got, width, out)
	}
}

func TestRenderPickerHintItems_AlignsAbortLeftPrimaryRight(t *testing.T) {
	m := baseModel(nil)
	width := 36
	out := renderPickerHintItems(m, width, confirmActionItems(m.keys.Confirm, "create", m.keys.Back))
	line := renderedLineContaining(out, "create")
	if line == "" || !strings.Contains(line, "cancel") {
		t.Fatalf("expected cancel and create on action line, got:\n%s", out)
	}
	if got := visualColumnOf(line, "cancel"); got < 0 || got > 5 {
		t.Fatalf("cancel should be left aligned, column=%d:\n%s", got, out)
	}
	createCol := visualColumnOf(line, "create")
	if createCol <= visualColumnOf(line, "cancel") {
		t.Fatalf("create should be right of cancel:\n%s", out)
	}
	if lipgloss.Width(line) != width {
		t.Fatalf("action line width=%d, want %d:\n%s", lipgloss.Width(line), width, out)
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
	m.setupProviders = []app.SetupProviderOption{
		{Name: "system", Label: "system(brew)", Enabled: true},
		{Name: "node", Label: "node(bun)", Enabled: false},
		{Name: "python", Label: "python(uv)", Enabled: true},
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
	m.setupProviders = []app.SetupProviderOption{
		{Name: "system", Label: "system(apt)", Enabled: true},
	}
	out := renderProviderPickerStep(m, 2)
	if !strings.Contains(out, "system") {
		t.Errorf("expected 'system' in provider picker step 2, got:\n%s", out)
	}
}

func TestRenderProviderPickerStep_CheckboxStates(t *testing.T) {
	m := baseModel(nil)
	m.setupProviders = []app.SetupProviderOption{
		{Name: "system", Label: "system(brew)", Enabled: true},
		{Name: "node", Label: "node(bun)", Enabled: false},
	}
	out := renderProviderPickerStep(m, 1)
	if !strings.Contains(out, "[x]") || !strings.Contains(out, "[ ]") {
		t.Errorf("expected enabled and disabled checkbox states, got:\n%s", out)
	}
}

// ── view_settings.go ─────────────────────────────────────────────────────────

func TestRenderSettings_Basic(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	if out == "" {
		t.Error("expected non-empty settings output")
	}
	if !strings.Contains(out, "Provider Priority") {
		t.Errorf("expected 'Provider Priority' in settings, got:\n%s", out)
	}
}

func TestRenderSettings_SectionHeaders(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	sections := []string{"Tools", "Dotfiles", "Maintenance"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("expected section %q in settings, got:\n%s", s, out)
		}
	}
	if strings.Contains(out, "Managers") {
		t.Errorf("'Managers' section should not exist in settings, got:\n%s", out)
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
		{"Provider Priority", false},
		{"Dotfiles", true},
		{"Repository", false},
		{"Dotfile Sync", false},
		{"Reminder Notifications", false},
		{"Reminder Interval", false},
		{"Watch Sync", false},
		{"Watch Debounce", false},
		{"Service Status", false},
		{"Commit Changes", false},
		{"Push Changes", false},
		{"Maintenance", true},
		{"Run Doctor", false},
		{"Run Bootstrap Again", false},
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
		"System Provider Order",
		"Track System",
		"Track Node",
		"Track Python",
		"Node Manager",
		"Python Manager",
		"Managers",
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
	m.settingsCursor = settingsRowProviderPriority
	out := renderSettings(m)

	if strings.Contains(out, "Add newly installed tools") {
		t.Fatalf("unselected import help should not render when priority row is selected:\n%s", out)
	}
	// The priority row is selected; its value bracket should appear.
	if !strings.Contains(out, "Provider Priority") {
		t.Fatalf("selected Provider Priority row should render:\n%s", out)
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
	setDotsRepoForTest(&m, "/home/user/dotfiles")
	out := renderSettings(m)
	if !strings.Contains(out, "dotfiles") {
		t.Errorf("expected dotfiles path in settings output, got:\n%s", out)
	}
}

func TestRenderSettingsUsesCachedDotsAvailability(t *testing.T) {
	t.Run("repo row shows app repo when local settings are stale", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		cacheDotsAvailability(&m, app.DotsSyncAvailability{
			Configured: true,
			Reason:     app.DotsSyncAvailabilityReady,
			RepoPath:   "/repo/current-dotfiles",
		})

		out := stripANSIEscapeSequences(renderSettings(m))
		line := renderedLineContaining(out, "Repository")

		if !strings.Contains(line, "current-dotfiles") || strings.Contains(line, "stale-dotfiles") {
			t.Fatalf("Repository row should use app-backed repo path, line=%q\n%s", line, out)
		}
	})

	t.Run("dotfile sync row shows on when app is enabled despite stale disabled setting", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
		m.settings = config.Settings{DotsRepo: "/repo/dotfiles", DotsDisabled: config.BoolPtr(true)}

		out := stripANSIEscapeSequences(renderSettings(m))
		line := renderedLineContaining(out, "Dotfile Sync")

		if !strings.Contains(line, "[ON]") || strings.Contains(line, "[OFF]") {
			t.Fatalf("Dotfile Sync row should use app-backed enabled state, line=%q\n%s", line, out)
		}
	})

	t.Run("service help asks for repo when app is unconfigured despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewSettings
		m.settingsCursor = settingsRowDotsReminder
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsSyncAvailCached = app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo}

		out := stripANSIEscapeSequences(renderSettings(m))

		if !strings.Contains(out, "Set a dotfiles repository before enabling reminder.") {
			t.Fatalf("settings service help should use app-backed unconfigured state:\n%s", out)
		}
	})
}

func TestRenderSettings_DotsServiceRows(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	setDotsRepoForTest(&m, "/home/user/dotfiles")
	m.dotsReminderService = &app.DotsReminderService{Installed: true, Platform: "systemd", Interval: 12 * time.Hour, Notify: true}
	m.dotsWatchService = &app.DotsWatchService{Installed: false, Platform: "systemd", Debounce: 2 * time.Second}

	out := renderSettings(m)
	for _, want := range []string{"Reminder Notifications", "Reminder Interval", "Watch Sync", "Watch Debounce", "Service Status", "[ON]", "[OFF]", "[12h]", "[2s]", "[1/2 on]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings service row output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSettings_DotsServiceDashboard(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	setDotsRepoForTest(&m, "/home/user/dotfiles")
	m.settingsCursor = settingsRowDotsServices
	m.dotsReminderService = &app.DotsReminderService{Installed: true, Platform: "systemd", Interval: 12 * time.Hour, Notify: true}
	m.dotsWatchService = &app.DotsWatchService{Installed: false, Platform: "systemd", Debounce: 2 * time.Second}

	out := renderSettings(m)
	for _, want := range []string{"Reminder", "Watch", "systemd", "interval 12h", "notify on", "debounce 2s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings service dashboard missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "enter") {
		t.Fatalf("read-only service dashboard should not show an action hint:\n%s", out)
	}
}

func TestRenderSettings_DoctorDashboard(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	m.settingsCursor = settingsRowDoctor
	m.doctorResult = &app.DoctorResult{
		Summary: app.DoctorSummary{OK: 1, Warn: 1, Fail: 1},
		Checks: []app.DoctorCheck{
			{Label: "Config", Status: app.DoctorStatusOK, Message: "settings.json is valid", Details: []string{"/tmp/settings.json", "version 1"}},
			{Label: "Host", Status: app.DoctorStatusWarn, Message: "current host is not configured", Details: []string{"host laptop"}},
			{Label: "Dotfiles", Status: app.DoctorStatusFail, Message: "dots_repo is not accessible", Details: []string{"stow missing"}},
		},
	}

	out := renderSettings(m)
	for _, want := range []string{"Run Doctor", "Summary", "1 ok", "1 warn", "1 fail", "Config", "Host", "Dotfiles", "/tmp/settings.json", "version 1", "host laptop", "stow missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings doctor dashboard missing %q:\n%s", want, out)
		}
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
	m.dangerConfirmRow = settingsRowResetSettings
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
	m.width = 120
	m.height = 50
	// The editor is now a popup; use the full composited view.
	out := m.viewString()
	// Popup title must be present.
	if !strings.Contains(out, "Provider Priority") {
		t.Errorf("expected 'Provider Priority' popup title in priority editing state, got:\n%s", out)
	}
	// Draft providers must appear in the popup body.
	if !strings.Contains(out, "brew") || !strings.Contains(out, "apt") {
		t.Errorf("expected draft providers 'brew' and 'apt' in popup, got:\n%s", out)
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
	// Set a custom provider priority order; the collapsed row should reflect it.
	m.settings.ProviderPriority = []string{"uv", "brew", "apt"}
	out := renderSettings(m)
	if !strings.Contains(out, "uv") || !strings.Contains(out, "brew") {
		t.Errorf("expected saved priority providers in priority row, got:\n%s", out)
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

func TestRenderHosts_Empty(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{},
	}
	out := renderGroups(m)
	if out == "" {
		t.Error("expected non-empty hosts output")
	}
	if !strings.Contains(out, "No host assignments") {
		t.Errorf("expected 'No host assignments' when empty, got:\n%s", out)
	}
}

func TestRenderHosts_WithHosts(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{"dev"}},
			"home": {Groups: []string{"personal"}},
			"solo": {},
		},
	}
	m.hostCursor = 0
	out := renderGroups(m)
	if !strings.Contains(out, "work") {
		t.Errorf("expected 'work' in hosts output, got:\n%s", out)
	}
	if !strings.Contains(out, "home") {
		t.Errorf("expected 'home' in hosts output, got:\n%s", out)
	}
}

func TestRenderHosts_HostGroupsAlphabetical(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{"work", "apps", "base"}},
		},
	}
	out := renderGroups(m)
	if !strings.Contains(out, "apps, base, work") {
		t.Errorf("expected sorted host groups in hosts output, got:\n%s", out)
	}
}

func TestRenderHosts_ActiveHostMarker(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{"dev"}},
		},
	}
	out := renderGroups(m)
	if strings.Contains(out, "*") {
		t.Errorf("active host should not render a second row marker, got:\n%s", out)
	}
}

func TestRenderHosts_HostRequired(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostRequired = true
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{},
	}
	out := renderGroups(m)
	if !strings.Contains(out, "No host configuration") {
		t.Errorf("expected 'No host configuration' banner, got:\n%s", out)
	}
}

func TestRenderHosts_WithGroups(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{},
	}
	m.groupNames = []string{"dev", "personal"}
	m.toolGroups = map[string]string{
		toolKey("git", "brew"): "dev",
	}
	out := renderGroups(m)
	if !strings.Contains(out, "Groups") {
		t.Errorf("expected 'Groups' section, got:\n%s", out)
	}
}

func TestRenderHosts_NoBlankLineDirectlyAfterDivider(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{"dev"}},
			"home": {Groups: []string{}},
			"acme": {Groups: []string{"ops"}},
			"host": {Groups: []string{"team"}},
		},
	}
	m.groupNames = []string{"dev", "ops", "team"}
	out := renderGroups(m)

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.Contains(line, "─") && strings.Contains(line, "Group Assignments") {
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				t.Fatalf("host section divider has blank row below it:\n%s", out)
			}
		}
		if strings.Contains(line, "─") && strings.Contains(line, "Groups") {
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				t.Fatalf("groups divider has blank row below it:\n%s", out)
			}
		}
	}
}

func TestRenderHosts_NilHostInfo(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = nil
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("renderGroups panicked with nil hostInfo: %v", r)
		}
	}()
	_ = renderGroups(m)
}

func TestRenderHosts_GroupCreating(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.width = 80
	m.height = 30
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{},
	}
	m.groupCreating = true
	out := m.viewString()
	for _, want := range []string{"New Group", "group name", "enter create", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in new group popup, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"┌", "└"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("new group popup should not render a nested input border:\n%s", out)
		}
	}
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Errorf("new group should render through the shared popup frame, got:\n%s", out)
	}
	if strings.Contains(renderGroups(m), "New group —") {
		t.Errorf("new group input should not render inline:\n%s", renderGroups(m))
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

func TestHostGroupToolsPopup_FilterKeepsDimensions(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupTools
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
	fullContent, fullFrame := renderHostGroupToolsPopup(m)
	full := placePopup(bg, m, fullContent, fullFrame)

	filtered := m
	filtered.groupToolsProviderIdx = 2 // node
	filtered.groupToolsEditor.search = "eslint"
	filteredContent, filteredFrame := renderHostGroupToolsPopup(filtered)
	narrow := placePopup(bg, filtered, filteredContent, filteredFrame)
	if lipgloss.Width(narrow) != lipgloss.Width(full) || lipgloss.Height(narrow) != lipgloss.Height(full) {
		t.Fatalf("filtered popup dimensions changed: full=%dx%d filtered=%dx%d\nfull:\n%s\nfiltered:\n%s",
			lipgloss.Width(full), lipgloss.Height(full), lipgloss.Width(narrow), lipgloss.Height(narrow), full, narrow)
	}

	baseContent := renderHostGroupToolsEditor(unfilteredHostGroupToolsModel(filtered))
	wantHeight := max(lipgloss.Height(baseContent), lipgloss.Height(filteredContent))
	if filteredFrame.ContentHeight != wantHeight {
		t.Fatalf("filtered tool popup content height = %d, want %d", filteredFrame.ContentHeight, wantHeight)
	}
}

func TestHostGroupDotsPopup_SearchKeepsDimensions(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupDots
	m.width = 100
	m.height = 40
	m.groupDotsEditor.group = "work"
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
		{Name: "tmux", TargetPath: "~/.tmux.conf", Health: app.HealthOK},
		{Name: "zsh", TargetPath: "~/.zshrc", Health: app.HealthMissing},
		{Name: "copilot", TargetPath: "~/.config/copilot", State: app.DotStateIgnored},
	}
	m.groupDotsEditor.membership = map[string]bool{"nvim": true, "tmux": true}
	bg := strings.Repeat("\n", m.height-1)
	fullContent, fullFrame := renderHostGroupDotsPopup(m)
	full := placePopup(bg, m, fullContent, fullFrame)

	filtered := m
	filtered.groupDotsEditor.search = "zsh"
	filteredContent, filteredFrame := renderHostGroupDotsPopup(filtered)
	narrow := placePopup(bg, filtered, filteredContent, filteredFrame)
	if lipgloss.Width(narrow) != lipgloss.Width(full) || lipgloss.Height(narrow) != lipgloss.Height(full) {
		t.Fatalf("filtered dot popup dimensions changed: full=%dx%d filtered=%dx%d\nfull:\n%s\nfiltered:\n%s",
			lipgloss.Width(full), lipgloss.Height(full), lipgloss.Width(narrow), lipgloss.Height(narrow), full, narrow)
	}

	searching := filtered
	searching.groupDotsEditor.searchActive = true
	searchingContent, searchingFrame := renderHostGroupDotsPopup(searching)
	baseContent := renderHostGroupDotsEditor(unfilteredHostGroupDotsModel(searching))
	wantHeight := max(lipgloss.Height(baseContent), lipgloss.Height(searchingContent))
	if searchingFrame.ContentHeight != wantHeight {
		t.Fatalf("searching dot popup content height = %d, want %d", searchingFrame.ContentHeight, wantHeight)
	}
}

func TestHostGroupEditorPopups_DoNotWrapDividersOrFooter(t *testing.T) {
	for _, width := range []int{90, 110} {
		t.Run(fmt.Sprintf("tools width %d", width), func(t *testing.T) {
			m := hostsModel()
			m.mode = viewGroupTools
			m.width = width
			m.height = 34
			m.groupToolsEditor.group = "work"
			m.effectiveSystemManager = "brew"
			m.effectiveNodeManager = "pnpm"
			m.effectivePythonManager = "uv"
			m.allTools = []*database.ToolCache{
				{Name: "@scope/toolkit", Provider: "node", Package: "@scope/toolkit", InstalledWith: "pnpm", Tracked: true},
				{Name: "ripgrep", Provider: "system", InstalledWith: "brew", Tracked: true},
				{Name: "ruff", Provider: "python", InstalledWith: "uv", Tracked: true},
			}
			m.groupToolsEditor.membership = map[string]bool{"@scope/toolkit": true}

			frame := groupToolsPopupFrame(m)
			out := renderPopupFrame(m.palette, renderHostGroupToolsEditor(m), frame)
			assertPopupFrameDoesNotWrap(t, out, frame.Width, []string{"esc", "space", "x", "enter"})
		})

		t.Run(fmt.Sprintf("dots width %d", width), func(t *testing.T) {
			m := hostsModel()
			m.mode = viewGroupDots
			m.width = width
			m.height = 34
			m.groupDotsEditor.group = "work"
			m.dotsEntries = []app.DotStatus{
				{Name: "nvim", TargetPath: "~/.config/nvim", Health: app.HealthOK},
				{Name: "tmux", TargetPath: "~/.tmux.conf", Health: app.HealthMissing},
			}
			m.groupDotsEditor.membership = map[string]bool{"nvim": true}

			frame := groupDotsPopupFrame(m)
			out := renderPopupFrame(m.palette, renderHostGroupDotsEditor(m), frame)
			assertPopupFrameDoesNotWrap(t, out, frame.Width, []string{"esc", "space", "enter"})
		})
	}
}

func assertPopupFrameDoesNotWrap(t *testing.T, out string, width int, footerKeys []string) {
	t.Helper()
	assertLinesFitWidth(t, out, width)
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "─" {
			t.Fatalf("popup divider wrapped onto a 1-char line:\n%s", out)
		}
	}
	footer := renderedLineContaining(out, "enter")
	if footer == "" {
		t.Fatalf("popup footer missing primary action:\n%s", out)
	}
	for _, key := range footerKeys {
		if !strings.Contains(footer, key) {
			t.Fatalf("popup footer key %q wrapped off the action row:\n%s", key, out)
		}
	}
}

func TestRenderHosts_DeleteConfirm(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{}},
		},
	}
	m.hostCursor = 0
	m.hostDeleteConfirm = true
	out := renderGroups(m)
	if !strings.Contains(out, "press ") || !strings.Contains(out, " again to delete") {
		t.Errorf("expected red delete confirmation in output, got:\n%s", out)
	}
	if strings.Contains(out, "esc cancel") || strings.Contains(out, "confirm delete") {
		t.Errorf("host delete confirmation should not render confirm/cancel hints, got:\n%s", out)
	}
}

func TestRenderHosts_LegacyHostnameMappingSectionRemoved(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{}},
		},
	}
	out := renderGroups(m)
	if strings.Contains(out, "Hostname Mappings") || strings.Contains(out, "mymachine [this host]") {
		t.Errorf("legacy hostname mappings should not render as a standalone section:\n%s", out)
	}
}

func TestRenderHosts_HostAndGroupSummaries(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "mymachine")
	m := baseModel(nil)
	m.mode = viewGroups
	m.groupNames = []string{"dev", "personal"}
	m.toolGroups = map[string]string{
		toolKey("git", "system"): "dev",
	}
	m.hostInfo = &app.HostInfo{
		Active: "work",
		Hosts: map[string]config.HostAssignment{
			"work": {Groups: []string{"dev"}},
			"home": {Groups: []string{"personal"}},
		},
	}
	m.hostCursor = 0

	out := renderGroups(m)

	for _, want := range []string{"dev", "this host", "current host: 1 tool, 0 dotfiles", "mymachine (local)", "1 tool", "0 dotfiles"} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderGroups missing %q:\n%s", want, out)
		}
	}
	for _, stale := range []string{"assigned groups:", "host group:", textRowContentPrefix() + "this host"} {
		if strings.Contains(out, stale) {
			t.Fatalf("renderGroups should not include stale host detail %q:\n%s", stale, out)
		}
	}
}

func TestRenderHosts_CurrentHostSummaryAggregatesAssignedGroups(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "mymachine")
	m := baseModel(nil)
	m.mode = viewGroups
	m.groupNames = []string{"dev", "ops"}
	m.toolMemberships = map[string][]string{
		toolKey("git", "system"): {"mymachine"},
		toolKey("fd", "system"):  {"dev"},
		toolKey("rg", "system"):  {"dev", "ops"},
	}
	m.dotMemberships = map[string][]string{
		"zsh":  {"mymachine"},
		"nvim": {"dev"},
		"git":  {"dev", "ops"},
	}
	m.hostInfo = &app.HostInfo{
		Active: "mymachine",
		Hosts: map[string]config.HostAssignment{
			"mymachine": {Groups: []string{"dev", "ops"}},
		},
	}
	m.hostCursor = 0

	out := renderGroups(m)
	if !strings.Contains(out, "current host: 3 tools, 3 dotfiles") {
		t.Fatalf("host summary should aggregate the host group plus assigned groups without double-counting:\n%s", out)
	}
}

func TestRenderHosts_HostAndGroupColumnsAlign(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Active: "long-host",
		Hosts: map[string]config.HostAssignment{
			"long-host": {Groups: []string{"short"}},
		},
	}
	m.groupNames = []string{"very-long-group"}
	m.toolGroups = map[string]string{toolKey("git", "system"): "very-long-group"}

	out := renderGroups(m)
	hostLine := renderedLineContaining(out, "long-host")
	groupLine := renderedLineContaining(out, "very-long-group")
	if hostLine == "" || groupLine == "" {
		t.Fatalf("missing host/group rows:\n%s", out)
	}
	hostSecondCol := visualColumnOf(hostLine, "long-host (local)")
	groupSecondCol := visualColumnOf(groupLine, "1 tool")
	hostThirdCol := visualColumnOf(hostLine, "this host")
	groupThirdCol := visualColumnOf(groupLine, "0 dotfiles")
	if hostSecondCol < 0 || groupSecondCol < 0 || hostThirdCol < 0 || groupThirdCol < 0 {
		t.Fatalf("missing expected count columns:\nhost=%q\ngroup=%q", hostLine, groupLine)
	}
	cols := groupAssignmentTableColumnWidths(app.PrioritizedHostSummaries(m.hostInfo), buildAllGroupNames(m.groupNames), map[string]int{
		"very-long-group": 1,
	}, map[string]int{})
	wantSecondCol := rowMarkerWidth + rowAvailableWidth(m.width) - (cols.mid + listColumnGap + cols.tail)
	wantGroupSecondCol := wantSecondCol + cols.mid - lipgloss.Width("1 tool")
	wantGroupThirdCol := wantSecondCol + cols.mid + listColumnGap + cols.tail - lipgloss.Width("0 dotfiles")
	if hostSecondCol != wantSecondCol {
		t.Fatalf("host second column = %d, want responsive right layout column %d:\n%s", hostSecondCol, wantSecondCol, hostLine)
	}
	if groupSecondCol != wantGroupSecondCol || groupThirdCol != wantGroupThirdCol {
		t.Fatalf("group count columns should right-align within shared column bounds:\nhost=%q\ngroup=%q", hostLine, groupLine)
	}
	if hostSecondCol <= m.width/2 {
		t.Fatalf("host summary columns should use available horizontal space:\n%s", hostLine)
	}
	secondToThirdGap := hostThirdCol - hostSecondCol - lipgloss.Width("long-host (local), short")
	if secondToThirdGap < listColumnGap {
		t.Fatalf("second and third columns too tight: gap=%d line=%q", secondToThirdGap, hostLine)
	}

	activeGroupCount := listRowColumnStyle(true, m.palette.styleHelp).Render("long-host (local), short")
	activeHostCount := listRowColumnStyle(true, m.palette.styleProvider).Render("this host")
	if !strings.Contains(hostLine, activeGroupCount) || !strings.Contains(hostLine, activeHostCount) {
		t.Fatalf("selected host count columns should use active weight:\n%s", hostLine)
	}

	m.assignmentSection = 1
	for i, group := range buildAllGroupNames(m.groupNames) {
		if group == "very-long-group" {
			m.groupCursor = i
			break
		}
	}
	out = renderGroups(m)
	hostLine = renderedLineContaining(out, "long-host")
	groupLine = renderedLineContaining(out, "very-long-group")
	if strings.Contains(hostLine, ">") {
		t.Fatalf("host row should not keep a static cursor while groups are focused:\n%s", hostLine)
	}
	if !strings.Contains(groupLine, ">") {
		t.Fatalf("focused group row should own the cursor:\n%s", groupLine)
	}
	activeToolCount := listRowColumnStyle(true, m.palette.styleHelp).Render("1 tool")
	activeDotCount := listRowColumnStyle(true, m.palette.styleProvider).Render("0 dotfiles")
	if !strings.Contains(groupLine, activeToolCount) || !strings.Contains(groupLine, activeDotCount) {
		t.Fatalf("selected group count columns should use active weight:\n%s", groupLine)
	}
}

func TestRenderHosts_ProtectedGroupDetail(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "mymachine")
	m := baseModel(nil)
	m.mode = viewGroups
	m.assignmentSection = 1
	m.hostInfo = &app.HostInfo{
		Active: "mymachine",
		Hosts: map[string]config.HostAssignment{
			"mymachine": {Groups: nil},
		},
	}
	m.groupNames = []string{"dev"}
	for i, group := range buildAllGroupNames(m.groupNames) {
		if group == "mymachine" {
			m.groupCursor = i
			break
		}
	}

	out := renderGroups(m)
	if !strings.Contains(out, "host bound group") {
		t.Fatalf("protected host group should describe host binding:\n%s", out)
	}
	if strings.Contains(out, "local tools for this host") {
		t.Fatalf("protected host group should not use stale local-tools copy:\n%s", out)
	}
}

func TestRenderHosts_GroupCountsRightAlign(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{Hosts: map[string]config.HostAssignment{}}
	m.groupNames = []string{"small", "large"}
	m.toolGroups = map[string]string{
		toolKey("one", "system"): "small",
	}
	for i := 0; i < 12; i++ {
		m.toolGroups[toolKey(fmt.Sprintf("many-%02d", i), "system")] = "large"
	}
	m.dotMemberships = map[string][]string{
		"dot-one": {"small"},
	}
	for i := 0; i < 12; i++ {
		m.dotMemberships[fmt.Sprintf("dot-many-%02d", i)] = []string{"large"}
	}

	out := renderGroups(m)
	smallLine := renderedLineContaining(out, "small")
	largeLine := renderedLineContaining(out, "large")
	if smallLine == "" || largeLine == "" {
		t.Fatalf("missing group rows:\n%s", out)
	}

	toolRightSmall := visualColumnOf(smallLine, "1 tool") + lipgloss.Width("1 tool")
	toolRightLarge := visualColumnOf(largeLine, "12 tools") + lipgloss.Width("12 tools")
	if toolRightSmall != toolRightLarge {
		t.Fatalf("tool counts should right-align:\nsmall=%q\nlarge=%q", smallLine, largeLine)
	}

	dotRightSmall := visualColumnOf(smallLine, "1 dotfile") + lipgloss.Width("1 dotfile")
	dotRightLarge := visualColumnOf(largeLine, "12 dotfiles") + lipgloss.Width("12 dotfiles")
	if dotRightSmall != dotRightLarge {
		t.Fatalf("dotfile counts should right-align:\nsmall=%q\nlarge=%q", smallLine, largeLine)
	}
}

func visualColumnOf(line, needle string) int {
	idx := strings.Index(line, needle)
	if idx < 0 {
		return -1
	}
	return lipgloss.Width(line[:idx])
}

func colsNameWidthForHostTest(m Model) int {
	allGroupNames := buildAllGroupNames(m.groupNames)
	return groupAssignmentTableColumnWidths(app.PrioritizedHostSummaries(m.hostInfo), allGroupNames, toolCountsByGroup(m), dotCountsByGroup(m)).name
}

func TestRenderHosts_HostActionsAndRename(t *testing.T) {
	m := hostsModel()
	m.hostCursor = 0
	out := renderGroups(m)
	for _, want := range []string{"space copy groups", "r rename", "g edit groups", "d delete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("host row missing action %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "set default") {
		t.Fatalf("host row should not offer set default:\n%s", out)
	}

	m.hostRenameMode = true
	m.settingsInput.SetValue("alpha")
	out = renderGroups(m)
	if !strings.Contains(out, "Rename:") || !strings.Contains(out, "enter save") {
		t.Fatalf("host rename mode missing input/save hint:\n%s", out)
	}
}

func TestRenderHosts_CurrentHostDoesNotOfferCopyGroups(t *testing.T) {
	m := hostsModel()
	m.hostInfo.Active = "alpha"
	m.hostCursor = 0

	out := renderGroups(m)
	if strings.Contains(out, "copy groups") {
		t.Fatalf("current host row should not offer copy groups:\n%s", out)
	}
	for _, want := range []string{"r rename", "g edit groups", "d delete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("current host row missing action %q:\n%s", want, out)
		}
	}
}

func TestRenderHostGroupEditor_LocalHostGroupLocked(t *testing.T) {
	m := hostsModel()
	m.hostInfo.Active = "alpha"
	m.hostCursor = 0
	var cmds []tea.Cmd
	m.startHostGroupEdit(&cmds)

	out := renderHostGroupEditor(m)
	if !strings.Contains(out, "alpha (local)") || !strings.Contains(out, "[x]") {
		t.Fatalf("host assignment picker should show locked local host group checked:\n%s", out)
	}
	before := append([]string(nil), m.hostGroupDraft...)
	m.toggleHostGroupDraft()
	if !slices.Equal(m.hostGroupDraft, before) {
		t.Fatalf("local host group should not be removable, before=%v after=%v", before, m.hostGroupDraft)
	}
}

func TestRenderHosts_GroupActions(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 1
	for i, group := range buildAllGroupNames(m.groupNames) {
		if group == "work" {
			m.groupCursor = i
			break
		}
	}
	out := renderGroups(m)
	for _, want := range []string{"r rename", "t edit tools", "f edit dotfiles", "d delete"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group row missing action %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "hosts: alpha") {
		t.Fatalf("group row should label host usage as host context:\n%s", out)
	}
	legacyUsageLabel := "pro" + "files:"
	if strings.Contains(out, legacyUsageLabel) {
		t.Fatalf("group row should not label usage with the old term:\n%s", out)
	}
	assertOrderedSubstrings(t, out, "r rename", "t edit tools", "f edit dotfiles", "d delete")
	for _, old := range []string{"D delete"} {
		if strings.Contains(out, old) {
			t.Fatalf("group row should not show old action %q:\n%s", old, out)
		}
	}

	m.groupCursor = 0 // base
	out = renderGroups(m)
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

func TestRenderHostGroupToolsEditor(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupTools
	m.groupToolsEditor.group = "work"
	m.effectiveSystemManager = "brew"
	m.effectiveNodeManager = "pnpm"
	m.allTools = []*database.ToolCache{
		{Name: "ripgrep", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: true},
		{Name: "eslint", Provider: "node", Package: "eslint", Installed: true, InstalledWith: "pnpm", Tracked: true},
		{Name: "ruff", Provider: "python", Installed: false, Tracked: true},
	}
	m.toolMemberships = map[string][]string{
		toolKey("ripgrep", "system"): {"work"},
	}
	m.groupToolsEditor.membership = map[string]bool{"ripgrep": true, "eslint": false, "ruff": false}
	m.groupToolsEditor.originalMembership = copyBoolMap(m.groupToolsEditor.membership)
	m.groupToolsIgnore = map[string]bool{"ruff": true}
	m.groupToolsOriginalIgnore = copyBoolMap(m.groupToolsIgnore)
	m.groupToolsEditor.cursor = 0

	out := renderHostGroupToolsEditor(m)
	for _, want := range []string{"[all]", "system", "node", "python", "enabled", "disabled", "ignored", "[x]", "ripgrep", "brew", "[ ]", "eslint", "pnpm", "ruff", "ignored", "space toggle", "x ignore", "/ search", "[] filter", "enter save", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group tools editor missing %q:\n%s", want, out)
		}
	}
	ripgrepLine := renderedLineContaining(out, "ripgrep")
	providerCol := visualColumnOf(ripgrepLine, "brew")
	wantProviderCol := groupToolsContentWidth(m) - lipgloss.Width("brew")
	if providerCol != wantProviderCol {
		t.Fatalf("tool provider column = %d, want right-aligned %d:\n%s", providerCol, wantProviderCol, ripgrepLine)
	}

	m.groupToolsProviderIdx = 2
	out = renderHostGroupToolsEditor(m)
	if !strings.Contains(out, "[node]") || strings.Contains(out, "provider:") || strings.Contains(out, "ripgrep") {
		t.Fatalf("provider filter should narrow rows to node tools:\n%s", out)
	}

	m.groupToolsProviderIdx = 0
	for i, row := range groupToolRows(m) {
		if row.tool.Name == "ruff" {
			m.groupToolsEditor.cursor = i
			break
		}
	}
	out = renderHostGroupToolsEditor(m)
	if !strings.Contains(out, "x unignore") {
		t.Fatalf("group-ignored selected tool should hint unignore:\n%s", out)
	}
	if strings.Contains(out, "x ignore") {
		t.Fatalf("group-ignored selected tool should not hint ignore:\n%s", out)
	}
}

func TestRenderHostGroupDotsEditor(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupDots
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

	out := renderHostGroupDotsEditor(m)
	for _, want := range []string{"enabled", "disabled", "[x]", "nvim", "~/.config/nvim", "[ ]", "zsh", "space toggle", "/ search", "enter save", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group dots editor missing %q:\n%s", want, out)
		}
	}
	nvimLine := renderedLineContaining(out, "nvim")
	targetCol := visualColumnOf(nvimLine, "~/.config/nvim")
	wantTargetCol := groupDotsContentWidth(m) - lipgloss.Width("~/.config/nvim")
	if targetCol != wantTargetCol {
		t.Fatalf("dot target column = %d, want right-aligned %d:\n%s", targetCol, wantTargetCol, nvimLine)
	}
	for _, unwanted := range []string{"ok", "missing"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("group dots editor should no longer render health column %q:\n%s", unwanted, out)
		}
	}

	m.groupDotsEditor.search = "zsh"
	out = renderHostGroupDotsEditor(m)
	if strings.Contains(out, "nvim") || !strings.Contains(out, "zsh") {
		t.Fatalf("search should narrow rows to zsh:\n%s", out)
	}
}

func TestRenderHostGroupDotsEditor_GroupsIgnoredSeparately(t *testing.T) {
	m := hostsModel()
	m.mode = viewGroupDots
	m.groupDotsEditor.group = "work"
	m.dotMemberships = map[string][]string{
		"nvim":    {"work"},
		"copilot": {"work"},
	}
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced},
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateIgnored, Counts: app.DotFileCounts{Ignored: 2}},
		{Name: "copilot", TargetPath: "~/.config/copilot", State: app.DotStateIgnored},
	}
	m.groupDotsEditor.membership = map[string]bool{"nvim": true, "copilot": true}
	m.groupDotsEditor.originalMembership = copyBoolMap(m.groupDotsEditor.membership)

	out := renderHostGroupDotsEditor(m)
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
	nvimIdx := strings.Index(out, "nvim")
	if nvimIdx < enabledIdx || nvimIdx > ignoredIdx {
		t.Fatalf("synced nvim row should stay in enabled section despite ignored-child status:\n%s", out)
	}
}

func TestRenderHostGroupDotsEditor_AppGeneratedIgnoredChildSummary(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "dotfiles-repo")
	cfgPath := filepath.Join(dir, "settings.json")
	target := filepath.Join(home, ".config", "nvim")
	source := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(filepath.Join(source, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "init.lua"), []byte("-- cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "node_modules", "pkg", "mod.js"), []byte("module"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if err := saveTUIConfig(t, cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Groups: []*config.GroupConfig{
			{Name: "testhost", Special: "host"},
			{Name: "work", Dots: []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}}},
		},
		Hosts: map[string][]string{"testhost": {"work"}},
	}); err != nil {
		t.Fatal(err)
	}
	a := app.New(cfgPath)
	a.CacheDir = dir
	if err := a.InitTestMode(context.Background()); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	result, err := a.DiscoverDotsStatus(context.Background())
	if err != nil {
		t.Fatalf("DiscoverDotsStatus: %v", err)
	}
	var syncedNvim, ignoredNvim bool
	for _, entry := range result.Entries {
		if entry.Name == "nvim" && entry.State == app.DotStateSynced {
			syncedNvim = true
		}
		if entry.Name == "nvim" && entry.State == app.DotStateIgnored {
			ignoredNvim = true
		}
	}
	if !syncedNvim || !ignoredNvim {
		t.Fatalf("DiscoverDotsStatus entries = %#v, want synced and ignored nvim rows", result.Entries)
	}
	memberships, err := a.DotMembershipMap(context.Background())
	if err != nil {
		t.Fatalf("DotMembershipMap: %v", err)
	}

	m := hostsModel()
	m.mode = viewGroupDots
	m.groupDotsEditor.group = "work"
	m.dotMemberships = memberships
	m.dotsEntries = result.Entries
	m.groupDotsEditor.start("work", groupDotNames(m), func(name string) bool {
		return slices.Contains(m.dotMemberships[name], "work")
	})

	for _, tc := range []struct {
		name         string
		search       string
		searchActive bool
	}{
		{name: "normal"},
		{name: "search", search: "nvim", searchActive: true},
	} {
		m.groupDotsEditor.search = tc.search
		m.groupDotsEditor.searchActive = tc.searchActive
		out := renderHostGroupDotsEditor(m)
		if !strings.Contains(out, "enabled") || strings.Contains(out, "ignored") {
			t.Fatalf("synced nvim with ignored children should render only as enabled (%s):\n%s", tc.name, out)
		}
	}
}

func TestRenderHostGroupEditor(t *testing.T) {
	m := hostsModel()
	m.hostEditMode = 1
	m.hostEditName = "alpha"
	m.hostGroupPicker = []string{"base", "work", groupPickerNewSentinel}
	m.hostGroupDraft = []string{"work"}
	m.hostGroupIdx = 1
	out := renderHostGroupEditor(m)
	for _, want := range []string{"[x]", "work", "[ ]", "base", "+ new group", "space toggle", "enter save"} {
		if !strings.Contains(out, want) {
			t.Fatalf("host group editor missing %q:\n%s", want, out)
		}
	}
}

func TestHostGroupEditorPopupFrameMatchesBodyWidth(t *testing.T) {
	m := hostsModel()
	m.width = 100
	m.height = 30
	m.hostEditMode = 1
	m.hostEditName = "helmfile"
	m.hostGroupPicker = []string{"Topaz", "infra", groupPickerNewSentinel}
	m.hostGroupDraft = []string{"infra"}
	m.hostGroupIdx = 1

	frame := groupEditorPopupFrame(m)
	contentWidth := popupInnerContentWidth(frame)
	if got, want := contentWidth, groupEditorContentWidth(m); got != want {
		t.Fatalf("host group editor frame content width=%d, want body width %d", got, want)
	}
	out := renderPopupFrame(m.palette, renderHostGroupEditor(m), frame)
	assertLinesFitWidth(t, out, frame.Width)
	for _, line := range strings.Split(renderHostGroupEditor(m), "\n") {
		if strings.Contains(line, "─") && lipgloss.Width(line) != contentWidth {
			t.Fatalf("host group editor divider width=%d, want %d:\n%s", lipgloss.Width(line), contentWidth, out)
		}
	}
}

func TestHostGroupEditorPopupUsesTerminalDefaultBackground(t *testing.T) {
	m := hostsModel()
	m.width = 100
	m.height = 30
	m = drive(m, tea.BackgroundColorMsg{Color: color.RGBA{R: 12, G: 13, B: 14, A: 255}})
	m.hostEditMode = 1
	m.hostEditName = "helmfile"
	m.hostGroupPicker = []string{"Topaz", "infra", groupPickerNewSentinel}
	m.hostGroupDraft = []string{"infra"}
	m.hostGroupIdx = 1

	frame := groupEditorPopupFrame(m)
	out := renderPopupFrame(m.palette, renderHostGroupEditor(m), frame)
	if strings.Contains(out, "48;2;12;13;14") {
		t.Fatalf("popup should not repaint the terminal background color:\n%s", out)
	}
}

func TestViewDoesNotForceCanvasToPopupSurfaceColor(t *testing.T) {
	m := hostsModel()
	m.width = 100
	m.height = 30
	m = drive(m, tea.BackgroundColorMsg{Color: color.RGBA{R: 12, G: 13, B: 14, A: 255}})
	m.mode = viewGroups

	out := m.View().Content
	if strings.Contains(out, "48;2;12;13;14") {
		t.Fatalf("normal view should not repaint the whole canvas with the terminal background:\n%s", out)
	}
}

func TestRenderHostGroupEditorCreatingGroupPlaceholder(t *testing.T) {
	m := hostsModel()
	m.hostEditMode = 1
	m.hostEditName = "alpha"
	m.hostGroupPicker = []string{"base", groupPickerNewSentinel}
	m.hostGroupIdx = 1
	m.pickerCreatingGroup = true
	m.settingsInput.SetValue("")
	m.settingsInput.Placeholder = "new group name…"
	m.settingsInput.Focus()

	out := renderHostGroupEditor(m)
	line := renderedLineContaining(out, "new group name…")
	if line == "" {
		t.Fatalf("new group placeholder missing:\n%s", out)
	}
	if strings.Contains(line, "nnew group name") {
		t.Fatalf("new group placeholder first character rendered twice:\n%s", out)
	}
}

func TestRenderFocusedEmptyInputsDoNotDuplicatePlaceholderFirstRune(t *testing.T) {
	cases := []struct {
		name        string
		placeholder string
		render      func(string) string
	}{
		{
			name:        "tools search",
			placeholder: "search…",
			render: func(placeholder string) string {
				m := baseModel(nil)
				m.mode = viewSearch
				m.filter.Placeholder = placeholder
				m.filter.SetValue("")
				m.filter.Focus()
				return m.viewString()
			},
		},
		{
			name:        "dots search",
			placeholder: "search dotfiles…",
			render: func(placeholder string) string {
				m := baseModel(nil)
				m.mode = viewDots
				setDotsRepoForTest(&m, "~/dotfiles")
				m.dotsEntries = []app.DotStatus{{Name: "nvim", TargetPath: "~/.config/nvim"}}
				m.dotsSearchActive = true
				m.filter.Placeholder = placeholder
				m.filter.SetValue("")
				m.filter.Focus()
				return m.viewString()
			},
		},
		{
			name:        "command palette",
			placeholder: "type a command…",
			render: func(placeholder string) string {
				m := baseModel(nil)
				m.mode = viewCommand
				m.commandInput.Placeholder = placeholder
				m.commandInput.SetValue("")
				m.commandInput.Focus()
				return m.viewString()
			},
		},
		{
			name:        "group tools search",
			placeholder: "search tools…",
			render: func(placeholder string) string {
				m := hostsModel()
				m.mode = viewGroupTools
				m.groupToolsEditor.group = "work"
				m.groupToolsEditor.searchActive = true
				m.settingsInput.Placeholder = placeholder
				m.settingsInput.SetValue("")
				m.settingsInput.Focus()
				return renderHostGroupToolsEditor(m)
			},
		},
		{
			name:        "group dots search",
			placeholder: "search dotfiles…",
			render: func(placeholder string) string {
				m := hostsModel()
				m.mode = viewGroupDots
				m.groupDotsEditor.group = "work"
				m.groupDotsEditor.searchActive = true
				m.settingsInput.Placeholder = placeholder
				m.settingsInput.SetValue("")
				m.settingsInput.Focus()
				return renderHostGroupDotsEditor(m)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.render(tc.placeholder)
			if !strings.Contains(out, tc.placeholder) {
				t.Fatalf("placeholder %q missing:\n%s", tc.placeholder, out)
			}
			first := string([]rune(tc.placeholder)[0])
			if strings.Contains(out, first+tc.placeholder) {
				t.Fatalf("placeholder first character rendered twice:\n%s", out)
			}
		})
	}
}

func TestViewHostEditorTitlesUseCapturedHostAfterCursorMoves(t *testing.T) {
	m := hostsModel()
	m.width = 100
	m.height = 40
	m.hostEditMode = 1
	m.hostEditName = "alpha"
	m.hostGroupPicker = []string{"base", "work"}
	m.hostCursor = 1

	out := m.viewString()
	if !strings.Contains(out, "Edit Groups: alpha") {
		t.Fatalf("host group editor title should use captured host:\n%s", out)
	}
	if strings.Contains(out, "Edit Groups: beta") {
		t.Fatalf("host group editor title used live cursor:\n%s", out)
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
	m.hostInfo = &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"base", "work"}},
		},
	}
	out := renderGroupPicker(m)
	for _, want := range []string{"current host", "inactive groups", "base", "work", "personal"} {
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
		toolMembershipKey(m.selectedTool()): {"base", "personal"},
	}
	m.hostInfo = &app.HostInfo{
		Active: "main",
		Hosts: map[string]config.HostAssignment{
			"main": {Groups: []string{"base", "work"}},
		},
	}
	out := renderGroupMembershipPicker(m)
	for _, want := range []string{"current host", "inactive groups", "base", "work", "personal", "space", "select", "esc", "cancel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("group membership picker missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "enter save") {
		t.Fatalf("single-membership picker should not require a second save key:\n%s", out)
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
	if !strings.Contains(out, "Move Group: ripgrep") {
		t.Fatalf("membership picker title should use captured tool:\n%s", out)
	}
	if strings.Contains(out, "Move Group: zoxide") {
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

func TestRenderScopePicker_ProviderLabelsFitWithShortDetails(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "npm", Provider: "node", Installed: true, InstalledWith: "npm", Tracked: true},
	})
	m.openProviderScopePicker(m.selectedTool())

	out := renderScopePicker(m)
	for _, want := range []string{
		"this tool on this host",
		"this tool everywhere",
		"node manager on this host",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("provider scope picker should show full label %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "…") {
		t.Fatalf("provider scope picker should not truncate short rows:\n%s", out)
	}
	if !strings.Contains(out, "space select") || strings.Contains(out, "enter save") {
		t.Fatalf("provider scope picker should commit selected row with space only:\n%s", out)
	}
}

func TestScopePickerPopupFrameFitsContent(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "npm", Provider: "node", Installed: true, InstalledWith: "npm", Tracked: true},
	})
	m.openProviderScopePicker(m.selectedTool())

	frame := scopePickerPopupFrame(m, popupTitleForScopeTool(m, "Pin Provider"))
	if got, want := popupInnerContentWidth(frame), scopePickerContentWidth(m); got != want {
		t.Fatalf("scope popup inner width = %d, want content width %d", got, want)
	}
	if frame.Width >= 64 {
		t.Fatalf("scope popup should fit its compact content instead of using default width, got %d", frame.Width)
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

func TestRenderSettings_RowLabelUsesSharedListEdge(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	line := renderedLineContaining(out, "Import Installed Tools")
	if line == "" {
		t.Fatalf("settings row missing from output:\n%s", out)
	}
	want := rowMarkerWidth
	if got := visualColumnOf(line, "Import Installed Tools"); got != want {
		t.Fatalf("settings row label column = %d, want shared list edge %d in %q", got, want, line)
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
		{"priority", settingsRowProviderPriority, "edit"},
		{"dots repo", settingsRowDotsRepo, "edit"},
		{"disable dots", settingsRowDotsSync, "disable"},
		{"reminder interval", settingsRowDotsReminderInterval, "set"},
		{"watch debounce", settingsRowDotsWatchDebounce, "set"},
		{"reset settings", settingsRowResetSettings, "confirm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.settingsCursor = tc.row
			out := renderSettings(m)
			line := renderedLineContaining(out, "enter")
			if line == "" {
				t.Fatalf("%s row hint missing from output:\n%s", tc.name, out)
			}
			if !strings.Contains(line, tc.word) || strings.Contains(line, "space") || strings.Contains(line, "cancel") {
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
	m.width = 120
	m.height = 50
	// The editor is now a popup; hints appear in the composited full view.
	// The footer renders across two lines: one for cancel, one for enter save.
	out := stripANSIEscapeSequences(m.viewString())
	if renderedLineContaining(out, "cancel") == "" {
		t.Fatalf("priority edit cancel hint missing from output:\n%s", out)
	}
	if renderedLineContaining(out, "save") == "" {
		t.Fatalf("priority edit save hint missing from output:\n%s", out)
	}
	// "enter" key label must appear somewhere in the popup footer.
	if !strings.Contains(out, "enter") {
		t.Fatalf("priority edit should show enter key hint, got:\n%s", out)
	}
}

func TestRenderSettings_StateColumnUsesResponsiveRightEdge(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSettings
	out := renderSettings(m)
	line := renderedLineContaining(out, "Import Installed Tools")
	if line == "" || !strings.Contains(line, "[OFF]") {
		t.Fatalf("settings row should contain label and value:\n%s", out)
	}
	valueCol := visualColumnOf(line, "[OFF]")
	wantCol := rowMarkerWidth + rowAvailableWidth(m.width) - lipgloss.Width("[OFF]")
	if valueCol != wantCol {
		t.Fatalf("settings value column = %d, want responsive right-aligned column %d in %q", valueCol, wantCol, line)
	}
	if valueCol <= m.width/2 {
		t.Fatalf("settings value should use available horizontal space: %q", line)
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
	setDotsRepoForTest(&dotsNoFilters, "/repo")
	dotsNoFilters.dotsLoaded = true
	dotsNoFilters.dotsEntries = []app.DotStatus{{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced}}

	dotsWithControls := baseModel(nil)
	setDotsRepoForTest(&dotsWithControls, "/repo")
	dotsWithControls.dotsLoaded = true
	dotsWithControls.dotsSearchActive = true
	dotsWithControls.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced, Group: "config"},
		{Name: "zsh", TargetPath: "~/.zshrc", State: app.DotStateSynced, Group: "home"},
	}

	settings := baseModel(nil)
	settings.mode = viewSettings

	hosts := baseModel(nil)
	hosts.mode = viewGroups
	hosts.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{"default": {}},
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
		{"hosts", renderGroups(hosts), "Group Assignments"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := 1
			// Dots with search control has an extra line for the search bar.
			if tc.name == "dots controls" {
				want = 2
			}
			if got := sectionLineIndex(tc.out, tc.label); got != want {
				t.Fatalf("first section line = %d, want %d for %s:\n%s", got, want, tc.name, tc.out)
			}
		})
	}
}

func TestRenderHosts_InlineHintsUseSharedIndent(t *testing.T) {
	m := hostsModel()
	m.assignmentSection = 0
	out := renderGroups(m)
	line := renderedLineContaining(out, "copy groups")
	if line == "" {
		t.Fatalf("host hints missing from output:\n%s", out)
	}
	if !strings.HasPrefix(line, textRowHintPrefix()) {
		t.Fatalf("host hint line should use shared indent %q, got %q", textRowHintPrefix(), line)
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

func renderedLineIndexContaining(out, needle string) int {
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
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

func TestRenderStatusBar_StatusMsgHidesFooterHintsWhenCrowded(t *testing.T) {
	m := baseModel(threeTools())
	m.width = 96
	m.mode = viewList
	m.help = newHelp()
	m.help.SetWidth(m.width)

	m.statusMsg = "Installing fd…"
	withStatus := renderStatusBar(m)
	if !strings.Contains(withStatus, "Installing fd") {
		t.Fatalf("status missing from footer: %q", withStatus)
	}
	for _, hidden := range []string{"upgrade all", "sync all", "refresh", "help", "quit"} {
		if strings.Contains(withStatus, hidden) {
			t.Fatalf("crowded status should hide footer hint %q, got: %q", hidden, withStatus)
		}
	}
	if got, want := lipgloss.Width(withStatus), screenContentWidth(m.width); got != want {
		t.Fatalf("status-only footer width = %d, want %d: %q", got, want, withStatus)
	}
}

func TestRenderStatusBar_LongStatusMsgHidesFooterHints(t *testing.T) {
	m := baseModel(threeTools())
	m.width = 72
	m.mode = viewList
	m.help = newHelp()
	m.help.SetWidth(m.width)
	m.statusMsg = "Installing very-long-tool-name-with-extra-provider-detail-and-extra-context…"

	out := renderStatusBar(m)
	if !strings.Contains(out, "Installing") {
		t.Fatalf("expected status message in footer, got: %q", out)
	}
	for _, hidden := range []string{"upgrade all", "sync all", "refresh", "help", "quit"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("long status should hide footer hint %q, got: %q", hidden, out)
		}
	}
	if got, want := lipgloss.Width(out), screenContentWidth(m.width); got != want {
		t.Fatalf("status-only footer width = %d, want %d: %q", got, want, out)
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
	if !strings.Contains(out, "press ") || !strings.Contains(out, " again to sync all") {
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
	for _, unwanted := range []string{"sync all", "pull", "add", "variant", "search"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("dots row confirmation should hide footer hints; found %q in %q", unwanted, out)
		}
	}
}

func TestRenderStatusBar_FilterActiveShowsClearHint(t *testing.T) {
	m := baseModel(nil)
	out := renderStatusBar(m)
	if strings.Contains(out, "clear") {
		t.Fatalf("inactive filters should not show clear hint, got: %q", out)
	}

	m.filter.SetValue("rip")
	m.applyFilter()
	out = renderStatusBar(m)
	if !strings.Contains(out, "esc") || !strings.Contains(out, "clear") {
		t.Fatalf("active filter should show esc clear hint, got: %q", out)
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
			name: "dots variant create",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewDots
				m.dotsVariantIdx = 0
				m.dotsVariantMode = dotsVariantCreate
				return m
			}(),
			want: "again to create variant",
		},
		{
			name: "dots variant remove",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewDots
				m.dotsVariantIdx = 0
				m.dotsVariantMode = dotsVariantRemove
				return m
			}(),
			want: "again to remove variant",
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
			name: "host delete",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewGroups
				m.hostDeleteConfirm = true
				return m
			}(),
			want: "again to delete",
		},
		{
			name: "group delete",
			m: func() Model {
				m := baseModel(nil)
				m.mode = viewGroups
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

			help := renderHelpPopupWithWidth(tc.m, helpPopupContentWidth(tc.m))
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
	m.statusMsg = "previous status should not cover quit confirm"
	out := renderStatusBar(m)
	if !strings.Contains(out, "press ") || !strings.Contains(out, "ctrl+c") || !strings.Contains(out, "again to quit") {
		t.Fatalf("quit confirmation should show triggering key and confirm text, got: %q", out)
	}
	if strings.Contains(out, "previous status") {
		t.Fatalf("quit confirmation should not be covered by stale status, got: %q", out)
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
	m.refreshToolTotal = 4
	got := activityLabel(m)
	if got != "Refreshing tools… 0/4: brew, npm" {
		t.Errorf("activity label = %q, want sorted provider refresh status", got)
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

func TestTabKeyMap_ShortHelp_StatusMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	got := strings.Join(bindingHelpDescs(tabKeyMap{&m}.ShortHelp()), ",")
	if !strings.Contains(got, "refresh dashboard") {
		t.Errorf("dashboard footer missing refresh action, got %q", got)
	}
	for _, unwanted := range []string{"reconcile all", "upgrade all tools", "open/fix selected"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("dashboard footer should hide inactive or inline action %q, got %q", unwanted, got)
		}
	}

	m = baseModel([]*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
	})
	m.mode = viewStatus
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
	got = strings.Join(bindingHelpDescs(tabKeyMap{&m}.ShortHelp()), ",")
	for _, want := range []string{"reconcile all", "refresh dashboard"} {
		if !strings.Contains(got, want) {
			t.Errorf("dashboard footer missing %q, got %q", want, got)
		}
	}
	if strings.Index(got, "reconcile all") > strings.Index(got, "refresh dashboard") {
		t.Errorf("dashboard footer should put reconcile before refresh, got %q", got)
	}
	for _, unwanted := range []string{"open/fix selected", "upgrade all tools"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("dashboard footer should not duplicate row action %q, got %q", unwanted, got)
		}
	}
}

func TestTabKeyMap_ShortHelp_HostsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	km := tabKeyMap{&m}
	bindings := km.ShortHelp()
	if len(bindings) == 0 {
		t.Error("expected non-empty ShortHelp for hosts mode")
	}
	if got := strings.Join(bindingHelpDescs(bindings), ","); !strings.Contains(got, "new group") {
		t.Errorf("hosts footer should include new group, got %v", got)
	}
	if got := strings.Join(bindingHelpDescs(bindings), ","); strings.Contains(got, "new host") {
		t.Errorf("hosts footer should not include new host, got %v", got)
	}
}

func TestTabKeyMap_ShortHelp_HostsGroupSection(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.assignmentSection = 1
	got := strings.Join(bindingHelpDescs(tabKeyMap{&m}.ShortHelp()), ",")
	if !strings.Contains(got, "new group") {
		t.Errorf("hosts footer should include new group in group section, got %v", got)
	}
	if strings.Contains(got, "new host") {
		t.Errorf("hosts footer should not include new host in group section, got %v", got)
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
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	got := bindingHelpKeys(tabKeyMap{&m}.ShortHelp())
	want := []string{
		toolActionTUIKey(t, actions.DotsAdd),
		toolActionTUIKey(t, actions.DotsRefresh),
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
		{"status", viewStatus},
		{"settings", viewSettings},
		{"hosts", viewGroups},
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

	out := renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
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
		{"dots", viewDots, []string{"refresh", "conflict", "no source", "host variant", "child"}},
		{"status", viewStatus, []string{"upgrade all tools", "reconcile all", "refresh dashboard", iconInstalled, "healthy", iconPending, "working", iconFailed, "warning", iconMissing, "failure", iconIgnored, "quiet"}},
		{"settings", viewSettings, []string{"change toggle or option", "[ON]", "[OFF]"}},
		{"hosts", viewGroups, []string{"new group", "(local)", "[x]", "[ ]", "may need sudo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.mode = tc.mode
			if tc.mode == viewDots {
				setDotsRepoForTest(&m, "/repo/dotfiles")
			}
			if tc.mode == viewStatus {
				m.allTools = []*database.ToolCache{
					{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
					{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
				}
				setDotsRepoForTest(&m, "/repo/dotfiles")
				m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
			}
			out := renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("help popup missing %q:\n%s", want, out)
				}
			}
			if tc.mode == viewStatus && strings.Contains(out, "open/fix selected") {
				t.Errorf("dashboard help should show the selected row action, not generic open/fix copy:\n%s", out)
			}
		})
	}
}

func TestRenderHelpPopup_DashboardSelectedRowActions(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(Model) Model
		want     []string
		unwanted []string
	}{
		{
			name: "updates",
			setup: func(m Model) Model {
				m.allTools = []*database.ToolCache{{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true}}
				m.statusCursor = statusRowIndex(statusRows(m), "Tool Updates")
				return m
			},
			want:     []string{"Row", "U", "upgrade all tools", "Bulk", "A", "reconcile all", "R", "refresh dashboard"},
			unwanted: []string{"enter upgrade all tools", "open/fix selected"},
		},
		{
			name: "tool sync",
			setup: func(m Model) Model {
				m.allTools = []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}}
				m.statusCursor = statusRowIndex(statusRows(m), "Tool Sync")
				return m
			},
			want:     []string{"Row", "S", "sync tools", "Bulk", "A", "reconcile all", "R", "refresh dashboard"},
			unwanted: []string{"enter sync tools", "open/fix selected"},
		},
		{
			name: "dotfiles sync",
			setup: func(m Model) Model {
				m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
				m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
				m.statusCursor = statusRowIndex(statusRows(m), "Dotfiles")
				return m
			},
			want:     []string{"Row", "S", "sync dotfiles", "Bulk", "A", "reconcile all", "R", "refresh dashboard"},
			unwanted: []string{"enter sync dotfiles", "open/fix selected"},
		},
		{
			name: "services",
			setup: func(m Model) Model {
				m.dotsReminderServiceErr = "read service file: denied"
				m.statusCursor = statusRowIndex(statusRows(m), "Services")
				return m
			},
			want:     []string{"Row", "enter", "open service settings", "Bulk", "R", "refresh dashboard"},
			unwanted: []string{"open/fix selected"},
		},
		{
			name: "all clear",
			setup: func(m Model) Model {
				m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
				m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}
				m.statusCursor = statusRowIndex(statusRows(m), "All Clear")
				return m
			},
			want:     []string{"Bulk", "R", "refresh dashboard"},
			unwanted: []string{"Row", "reconcile all", "open/fix selected"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.mode = viewStatus
			m = tc.setup(m)
			out := renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("dashboard help missing %q:\n%s", want, out)
				}
			}
			for _, unwanted := range tc.unwanted {
				if strings.Contains(out, unwanted) {
					t.Fatalf("dashboard help should not contain %q:\n%s", unwanted, out)
				}
			}
		})
	}
}

func TestRenderHelpPopup_DotsLegendOmitsTreeKindIcons(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	setDotsRepoForTest(&m, "/repo/dotfiles")
	out := renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
	for _, unwanted := range []string{dotKindFolderCollapsedIcon, dotKindFolderExpandedIcon, dotKindFolderEmptyIcon} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("dots help legend should not include tree kind icon %q:\n%s", unwanted, out)
		}
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
		renderHelpPopupWithWidth(m, helpPopupContentWidth(m)),
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

	for _, mode := range []viewMode{viewList, viewDots, viewStatus, viewGroups, viewSettings} {
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
			before: []string{"install", "upgrade", "move group", "ignore"},
		},
		{
			name:   "dots",
			mode:   viewDots,
			before: []string{"add", "refresh", "move group", "ignore"},
		},
		{
			name:   "groups",
			mode:   viewGroups,
			before: []string{"new group", "rename", "edit groups", "edit tools"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel(nil)
			m.mode = tc.mode
			if tc.mode == viewDots {
				m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
			}
			out := renderHelpPopupWithWidth(m, helpPopupContentWidth(m))
			deleteIdx := strings.Index(out, "delete")
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

func TestProviderLabel_TableDriven(t *testing.T) {
	cases := []struct {
		raw, installedWith, systemBin, pythonBin, nodeBin, want string
	}{
		{"brew", "", "", "", "", "brew"},
		{"apt", "", "", "", "", "apt"},
		{"system", "brew", "", "", "", "brew"},
		{"system", "", "brew", "", "", "brew"},
		{"system", "", "", "", "", "system"},
		{"python", "", "", "uv", "", "uv"},
		{"python", "", "", "", "", "python"},
		{"pip", "", "", "", "", "pip3"},
		{"pip3", "", "", "", "", "pip3"},
		{"uv", "", "", "", "", "uv"},
		{"node", "", "", "", "bun", "bun"},
		{"node", "", "", "", "", "node"},
		{"npm", "", "", "", "", "npm"},
		{"bun", "", "", "", "", "bun"},
		{"pnpm", "", "", "", "", "pnpm"},
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

func TestProviderLabelForToolWithPinMarksExplicitOverride(t *testing.T) {
	cases := []struct {
		name, pin, systemBin, pythonBin, nodeBin, want string
		tool                                           *database.ToolCache
	}{
		{
			name:    "installed pinned node manager",
			pin:     "npm",
			nodeBin: "bun",
			tool:    &database.ToolCache{Name: "typescript", Provider: "node", Installed: true, InstalledWith: "npm", Tracked: true},
			want:    "npm",
		},
		{
			name:      "missing pinned python manager",
			pin:       "pip3",
			pythonBin: "uv",
			tool:      &database.ToolCache{Name: "ruff", Provider: "python", Installed: false, Tracked: true},
			want:      "pip3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := providerLabelForToolWithPin(tc.tool, tc.pin, "", tc.systemBin, tc.pythonBin, tc.nodeBin)
			if got != tc.want {
				t.Fatalf("providerLabelForToolWithPin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInlineDetailLines_WrongProviderShowsActualAndExpected(t *testing.T) {
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
	m.applyFilter()

	cols := newColWidthsWithProviderPins(m.visibleTools, nil, nil, m.toolProviderPins, nil, "", "", m.effectiveNodeManager, 100)
	lines := inlineDetailLines(m, 100, cols)
	got := stripANSIEscapeSequences(strings.Join(lines, "\n"))
	want := "wrong provider: installed with bun, expected configured npm"
	if !strings.Contains(got, want) {
		t.Fatalf("inline detail = %q, want %q", got, want)
	}
}

func TestInlineDetailLines_ConfiguredProviderCandidates(t *testing.T) {
	tool := &database.ToolCache{Name: "prettier", Provider: "", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolProviderCandidates = map[string][]config.ToolInstallSpec{
		"prettier": {
			{Provider: "npm", Package: "prettier"},
			{Provider: "brew", Package: "prettier"},
		},
	}
	m.applyFilter()

	cols := newColWidthsWithProviderPins(m.visibleTools, nil, nil, m.toolProviderPins, nil, "", "", "", 120)
	lines := inlineDetailLines(m, 120, cols)
	got := stripANSIEscapeSequences(strings.Join(lines, "\n"))
	for _, want := range []string{"available providers", "npm/prettier", "brew/prettier"} {
		if !strings.Contains(got, want) {
			t.Fatalf("inline detail = %q, want %q", got, want)
		}
	}
}

func TestInlineDetailLines_ConfiguredProviderCandidatesHiddenWhenNotActionable(t *testing.T) {
	tests := []struct {
		name       string
		tool       *database.ToolCache
		candidates []config.ToolInstallSpec
	}{
		{
			name:       "single candidate",
			tool:       &database.ToolCache{Name: "prettier", Provider: "npm", Installed: false, Tracked: true},
			candidates: []config.ToolInstallSpec{{Provider: "npm", Package: "prettier"}},
		},
		{
			name: "installed row",
			tool: &database.ToolCache{Name: "prettier", Provider: "npm", Installed: true, Tracked: true},
			candidates: []config.ToolInstallSpec{
				{Provider: "npm", Package: "prettier"},
				{Provider: "brew", Package: "prettier"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel([]*database.ToolCache{tc.tool})
			m.toolProviderCandidates = map[string][]config.ToolInstallSpec{"prettier": tc.candidates}
			m.applyFilter()

			cols := newColWidthsWithProviderPins(m.visibleTools, nil, nil, m.toolProviderPins, nil, "", "", "", 120)
			lines := inlineDetailLines(m, 120, cols)
			got := stripANSIEscapeSequences(strings.Join(lines, "\n"))
			if strings.Contains(got, "available providers") {
				t.Fatalf("inline detail = %q, want provider candidates hidden", got)
			}
		})
	}
}

func TestToolInlineHints_PinnedProviderOffersRemoveOverride(t *testing.T) {
	tool := &database.ToolCache{Name: "typescript", Provider: "node", Installed: true, InstalledWith: "npm", Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolProviderPins = map[string]string{"typescript": "npm"}
	m.effectiveNodeManager = "bun"

	hints := toolInlineHints(m, tool)
	if len(hints) == 0 {
		t.Fatal("expected inline hints")
	}
	if hints[0].key != m.keys.PinProvider.Help().Key || hints[0].desc != "remove override" {
		t.Fatalf("first hint = %+v, want p remove override", hints[0])
	}
}

func TestToolInlineHints_FallbackEligibleSystemToolOffersFallback(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})

	hints := toolInlineHints(m, tool)
	if !hasHint(hints, m.keys.Fallback.Help().Key, actions.MustTUILabel(actions.ToolFallback)) {
		t.Fatalf("hints = %+v, want fallback action", hints)
	}
}

func TestToolInlineHints_FallbackEligibleConcreteProviderToolOffersFallback(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "apt", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolFallbacks = map[string]config.FallbackSpec{
		"rg": {
			Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
			Status: config.FallbackStatusUnverified,
		},
	}

	hints := toolInlineHints(m, tool)
	if !hasHint(hints, m.keys.Fallback.Help().Key, actions.MustTUILabel(actions.ToolFallback)) {
		t.Fatalf("hints = %+v, want fallback action", hints)
	}
}

func TestToolInlineHints_NativeInstalledSystemToolHidesFallback(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: true, InstalledWith: "apt", Tracked: true}
	m := baseModel([]*database.ToolCache{tool})

	hints := toolInlineHints(m, tool)
	if hasHintKey(hints, m.keys.Fallback.Help().Key) {
		t.Fatalf("hints = %+v, did not expect fallback action for native installed tool", hints)
	}
}

func TestToolInlineHints_NativeInstalledConcreteProviderToolHidesFallback(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "apt", Installed: true, InstalledWith: "apt", Tracked: true}
	m := baseModel([]*database.ToolCache{tool})

	hints := toolInlineHints(m, tool)
	if hasHintKey(hints, m.keys.Fallback.Help().Key) {
		t.Fatalf("hints = %+v, did not expect fallback action", hints)
	}
}

func TestFallbackKey_NativeInstalledSystemToolNoOp(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: true, InstalledWith: "apt", Tracked: true}
	m := baseModel([]*database.ToolCache{tool})

	got := drive(m, pressRune('f'))
	if got.mode == viewFallbackEditor || got.fallbackTargetSet {
		t.Fatalf("fallback editor opened for native-installed tool: mode=%v targetSet=%v", got.mode, got.fallbackTargetSet)
	}
	if got.loading {
		t.Fatal("fallback key should not start an operation for native-installed tool")
	}
}

func TestFallbackKey_GitHubInstalledSystemToolOpensEditor(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: true, InstalledWith: "gh", Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolFallbacks = map[string]config.FallbackSpec{
		"rg": {
			Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
			Status: config.FallbackStatusVerified,
		},
	}

	got := drive(m, pressRune('f'))
	if got.mode != viewFallbackEditor || !got.fallbackTargetSet {
		t.Fatalf("fallback editor state = mode:%v targetSet:%v, want editor open", got.mode, got.fallbackTargetSet)
	}
	if got.fallbackEditor.fields[fallbackFieldRepo] != "BurntSushi/ripgrep" {
		t.Fatalf("repo field = %q, want existing fallback repo", got.fallbackEditor.fields[fallbackFieldRepo])
	}
}

func TestRenderList_ConfiguredGitHubFallbackShowsGHStatus(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		status   string
		want     string
	}{
		{name: "verified system", provider: "system", status: config.FallbackStatusVerified, want: "gh"},
		{name: "unverified concrete", provider: "apt", status: config.FallbackStatusUnverified, want: "gh?"},
		{name: "unresolved system", provider: "system", status: config.FallbackStatusUnresolved, want: "gh!"},
		{name: "unsupported system", provider: "system", status: config.FallbackStatusUnsupported, want: "gh!"},
		{name: "failed system", provider: "system", status: config.FallbackStatusFailed, want: "gh!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &database.ToolCache{Name: "rg", Provider: tt.provider, Installed: false, Tracked: true}
			m := baseModel([]*database.ToolCache{tool})
			m.toolFallbacks = map[string]config.FallbackSpec{
				"rg": {
					Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
					Status: tt.status,
				},
			}

			out := stripANSIEscapeSequences(renderList(m))
			if !strings.Contains(out, tt.want) {
				t.Fatalf("rendered list = %q, want %s", out, tt.want)
			}
		})
	}
}

func TestRenderList_ConfiguredGitHubFallbackHidesGHStatusForNativeInstalledTool(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: true, InstalledWith: "apt", Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolFallbacks = map[string]config.FallbackSpec{
		"rg": {
			Source: config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
			Status: config.FallbackStatusVerified,
		},
	}

	out := stripANSIEscapeSequences(renderList(m))
	if strings.Contains(out, "gh") {
		t.Fatalf("rendered list = %q, want native installed provider without gh fallback status", out)
	}
	if !strings.Contains(out, "apt") {
		t.Fatalf("rendered list = %q, want native installed concrete provider label", out)
	}
}

func TestRenderFallbackEditorPopup_ShowsStructuredFallbackFields(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.mode = viewFallbackEditor
	m.fallbackTarget = *tool
	m.fallbackTargetSet = true
	m.fallbackEditor = fallbackEditorState{
		fields: map[fallbackEditorFieldID]string{
			fallbackFieldRepo:           "BurntSushi/ripgrep",
			fallbackFieldBinary:         "rg",
			fallbackFieldBinDir:         "~/.local/share/omni/fallback/bin",
			fallbackFieldAssetPattern:   "ripgrep-{version}-{os}-{arch}.tar.gz",
			fallbackFieldInstallCommand: "install rg",
			fallbackFieldCheckCommand:   "command -v rg",
			fallbackFieldUninstall:      "",
			fallbackFieldUpgrade:        "",
			fallbackFieldVersion:        "rg --version",
			fallbackFieldReleaseChannel: "stable",
		},
	}

	out := stripANSIEscapeSequences(renderFallbackEditorPopup(m))
	for _, want := range []string{
		"status",
		"gh?",
		"repo",
		"BurntSushi/ripgrep",
		"binary",
		"rg",
		"bin dir",
		"asset",
		"install",
		"check",
		"uninstall",
		"upgrade",
		"version",
		"channel",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("popup = %q, want %q", out, want)
		}
	}
}

func TestOpenFallbackEditor_PrefillsExistingRecipe(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolFallbacks = map[string]config.FallbackSpec{
		"rg": {
			Source:         config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "BurntSushi", Repo: "ripgrep"},
			Status:         config.FallbackStatusUnverified,
			Binary:         "rg",
			BinDir:         "~/.local/share/omni/fallback/bin",
			ReleaseChannel: "stable",
			Recipe:         config.FallbackRecipe{Type: config.FallbackRecipeGitHubReleaseAsset, AssetPattern: "ripgrep-{version}-{os}-{arch}.tar.gz"},
			Commands: config.FallbackCommands{
				Install:   "install rg",
				Check:     "test -x {{bin_dir}}/{{binary}}",
				Uninstall: "rm -f {{bin_dir}}/{{binary}}",
				Upgrade:   "upgrade rg",
				Version:   "{{binary}} --version",
			},
		},
	}

	if cmd := m.openFallbackEditor(tool); cmd == nil {
		t.Fatal("openFallbackEditor returned nil command")
	}
	if m.mode != viewFallbackEditor {
		t.Fatalf("mode = %v, want fallback editor", m.mode)
	}
	fields := m.fallbackEditor.fields
	if fields[fallbackFieldRepo] != "BurntSushi/ripgrep" ||
		fields[fallbackFieldInstallCommand] != "install rg" ||
		fields[fallbackFieldCheckCommand] != "test -x {{bin_dir}}/{{binary}}" ||
		fields[fallbackFieldUninstall] != "rm -f {{bin_dir}}/{{binary}}" ||
		fields[fallbackFieldReleaseChannel] != "stable" {
		t.Fatalf("fallback editor fields = %#v, want existing recipe values", fields)
	}
}

func TestOpenFallbackEditor_PrefillsUnsupportedFallback(t *testing.T) {
	tool := &database.ToolCache{Name: "gh", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolFallbacks = map[string]config.FallbackSpec{
		"gh": {
			Source:         config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "cli", Repo: "cli"},
			Status:         config.FallbackStatusUnsupported,
			Binary:         "gh",
			ReleaseChannel: "stable",
			Recipe: config.FallbackRecipe{
				ReleaseID:   "330388700",
				TagName:     "v2.93.0",
				PublishedAt: "2026-05-27T17:47:41Z",
			},
		},
	}

	if cmd := m.openFallbackEditor(tool); cmd == nil {
		t.Fatal("openFallbackEditor returned nil command")
	}
	if m.mode != viewFallbackEditor {
		t.Fatalf("mode = %v, want fallback editor", m.mode)
	}
	fields := m.fallbackEditor.fields
	if fields[fallbackFieldRepo] != "cli/cli" ||
		fields[fallbackFieldBinary] != "gh" ||
		fields[fallbackFieldInstallCommand] != "" ||
		fields[fallbackFieldCheckCommand] != "" ||
		fields[fallbackFieldReleaseChannel] != "stable" {
		t.Fatalf("fallback editor fields = %#v, want unsupported draft values without commands", fields)
	}
	out := stripANSIEscapeSequences(renderFallbackEditorPopup(m))
	if !strings.Contains(out, "gh!") {
		t.Fatalf("popup = %q, want unsupported gh! status", out)
	}
}

func TestOpenFallbackEditor_PrefillsConfiguredGit(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.toolGit = map[string]string{"rg": "https://github.com/BurntSushi/ripgrep"}

	if cmd := m.openFallbackEditor(tool); cmd == nil {
		t.Fatal("openFallbackEditor returned nil command")
	}
	if m.mode != viewFallbackEditor {
		t.Fatalf("mode = %v, want fallback editor", m.mode)
	}
	if got := m.fallbackEditor.fields[fallbackFieldRepo]; got != "BurntSushi/ripgrep" {
		t.Fatalf("repo field = %q, want configured git repo", got)
	}
	if got := fallbackConcreteForTool(tool, m.toolFallbacks); got != "" {
		t.Fatalf("fallback label = %q, want no gh status until fallback is configured", got)
	}
}

func TestFallbackRepoFromToolGit(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "https://github.com/cli/cli", want: "cli/cli"},
		{in: "https://github.com/cli/cli.git", want: "cli/cli"},
		{in: "github.com/cli/cli", want: "cli/cli"},
		{in: "git@github.com:cli/cli.git", want: "cli/cli"},
		{in: "https://www.github.com/cli/cli", want: "cli/cli"},
		{in: "https://gitlab.com/cli/cli", want: ""},
		{in: "https://github.com/cli/cli/releases", want: ""},
		{in: "https://github.com/cli/cli?tab=readme", want: ""},
		{in: "https://github.com/cli/cli#readme", want: ""},
		{in: "git@github.com:cli/cli.git?ref=main", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := fallbackRepoFromToolGit(tt.in); got != tt.want {
				t.Fatalf("fallbackRepoFromToolGit(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFallbackEditorKeyboardNavigationPersistsActiveField(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	if cmd := m.openFallbackEditor(tool); cmd == nil {
		t.Fatal("openFallbackEditor returned nil command")
	}
	m.settingsInput.SetValue("BurntSushi/ripgrep")

	var tm tea.Model = m
	tm, _ = tm.Update(pressTab())
	got := tm.(Model)
	if got.mode != viewFallbackEditor {
		t.Fatalf("mode after tab = %v, want fallback editor", got.mode)
	}
	if got.fallbackEditor.cursor != 1 {
		t.Fatalf("cursor after tab = %d, want binary field", got.fallbackEditor.cursor)
	}
	if got.fallbackEditor.fields[fallbackFieldRepo] != "BurntSushi/ripgrep" {
		t.Fatalf("repo field = %q, want persisted active field", got.fallbackEditor.fields[fallbackFieldRepo])
	}
	if got.settingsInput.Value() != "rg" {
		t.Fatalf("binary input = %q, want rg", got.settingsInput.Value())
	}

	got.settingsInput.SetValue("ripgrep")
	tm = got
	tm, _ = tm.Update(pressShiftTab())
	got = tm.(Model)
	if got.fallbackEditor.cursor != 0 {
		t.Fatalf("cursor after shift+tab = %d, want repo field", got.fallbackEditor.cursor)
	}
	if got.fallbackEditor.fields[fallbackFieldBinary] != "ripgrep" {
		t.Fatalf("binary field = %q, want persisted edited binary", got.fallbackEditor.fields[fallbackFieldBinary])
	}
	if got.settingsInput.Value() != "BurntSushi/ripgrep" {
		t.Fatalf("repo input = %q, want previous repo", got.settingsInput.Value())
	}
}

func TestFallbackEditorEnterWithEmptyRepoStaysOpen(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	if cmd := m.openFallbackEditor(tool); cmd == nil {
		t.Fatal("openFallbackEditor returned nil command")
	}
	m.settingsInput.SetValue("")

	var tm tea.Model = m
	tm, cmd := tm.Update(pressEnter())
	got := tm.(Model)
	if cmd != nil {
		t.Fatal("enter with empty repo returned command, want no save")
	}
	if got.mode != viewFallbackEditor {
		t.Fatalf("mode = %v, want fallback editor", got.mode)
	}
	if got.loading {
		t.Fatal("loading = true, want no save started")
	}
}

func TestFallbackEditorPastePersistsActiveField(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	if cmd := m.openFallbackEditor(tool); cmd == nil {
		t.Fatal("openFallbackEditor returned nil command")
	}

	var tm tea.Model = m
	tm, _ = tm.Update(tea.PasteMsg{Content: "BurntSushi/ripgrep"})
	got := tm.(Model)
	if got.fallbackEditor.fields[fallbackFieldRepo] != "BurntSushi/ripgrep" {
		t.Fatalf("repo field = %q, want pasted repo", got.fallbackEditor.fields[fallbackFieldRepo])
	}
}

func TestRenderFallbackEditorPopup_LongCommandsFitNarrowFrame(t *testing.T) {
	tool := &database.ToolCache{Name: "rg", Provider: "system", Installed: false, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.width = 52
	m.mode = viewFallbackEditor
	m.fallbackTarget = *tool
	m.fallbackTargetSet = true
	m.fallbackEditor = fallbackEditorState{
		fields: map[fallbackEditorFieldID]string{
			fallbackFieldRepo:           "BurntSushi/ripgrep",
			fallbackFieldBinary:         "rg",
			fallbackFieldBinDir:         "~/.local/share/omni/fallback/bin",
			fallbackFieldAssetPattern:   "ripgrep-{version}-{os}-{arch}.tar.gz",
			fallbackFieldInstallCommand: "curl -fsSL https://github.com/BurntSushi/ripgrep/releases/download/{{version}}/ripgrep.tar.gz | tar -xz -C {{bin_dir}}",
			fallbackFieldCheckCommand:   "test -x {{bin_dir}}/{{binary}} && {{bin_dir}}/{{binary}} --version",
			fallbackFieldUninstall:      "rm -f {{bin_dir}}/{{binary}}",
			fallbackFieldUpgrade:        "curl -fsSL https://github.com/BurntSushi/ripgrep/releases/latest/download/ripgrep.tar.gz | tar -xz -C {{bin_dir}}",
			fallbackFieldVersion:        "{{bin_dir}}/{{binary}} --version",
			fallbackFieldReleaseChannel: "stable",
		},
	}

	frame := fallbackEditorPopupFrame(m)
	out := stripANSIEscapeSequences(renderPopupFrame(m.palette, renderFallbackEditorPopup(m), frame))
	for _, line := range strings.Split(out, "\n") {
		if width := lipgloss.Width(line); width > frame.Width {
			t.Fatalf("line width = %d > frame width %d:\n%s", width, frame.Width, out)
		}
	}
}

func hasHint(hints []hintItem, key, desc string) bool {
	for _, hint := range hints {
		if hint.key == key && hint.desc == desc {
			return true
		}
	}
	return false
}

func hasHintKey(hints []hintItem, key string) bool {
	for _, hint := range hints {
		if hint.key == key {
			return true
		}
	}
	return false
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
	if cols.group != len("[dev]") {
		t.Fatalf("group column = %d, want %d; ignore details belong in selected-row detail, not the group column", cols.group, len("[dev]"))
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

func TestRenderToolRow_StatusColorStaysOnIcon(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "git", Provider: "brew", Installed: true}
	cols := colWidths{name: 20, prov: 10, screenW: 120}

	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncOK)

	if !strings.Contains(out, p.styleInstalled.Render(iconInstalled)) {
		t.Fatalf("row should color installed icon, got: %q", out)
	}
	if strings.Contains(out, p.styleInstalled.Render("git")) {
		t.Fatalf("row should not color installed tool name with installed status, got: %q", out)
	}
	if !strings.Contains(out, p.styleNormal.Render("git")) {
		t.Fatalf("row should render installed tool name with normal text style, got: %q", out)
	}
}

func TestRenderToolRow_OrphanUsesOrphanIconColor(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "utm", Provider: "system", Installed: true, InstalledWith: "brew", Tracked: false}
	cols := colWidths{name: 20, prov: 10, screenW: 120}

	out := renderToolRow(p, tool, cols, "", "", "brew", "", "", false, false, syncOrphan)

	if !strings.Contains(out, p.styleOrphan.Render(iconOrphan)) {
		t.Fatalf("orphan row should color orphan icon, got: %q", out)
	}
	if strings.Contains(out, p.styleInstalled.Render(iconInstalled)) {
		t.Fatalf("orphan row should not render as a normal installed row, got: %q", out)
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

func TestRenderToolRow_PrivilegeMarkerUsesOwnColumn(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "editor", Provider: "apt", Package: "neovim", Installed: true, Tracked: true}
	cols := colWidths{name: 24, priv: lipgloss.Width(iconPrivileged), prov: 14, ver: 8, screenW: 120}

	out := renderToolRow(p, tool, cols, "", "", "", "", "", false, false, syncOK)

	if strings.Contains(out, "editor "+iconPrivileged) || strings.Contains(out, "{neovim} "+iconPrivileged) {
		t.Fatalf("row = %q, privilege marker should not be in name column", out)
	}
	markerIdx := strings.Index(out, iconPrivileged)
	providerIdx := strings.Index(out, "apt")
	if markerIdx < 0 || providerIdx < 0 || markerIdx > providerIdx {
		t.Fatalf("row = %q, want privilege marker before provider label", out)
	}
	markerCol := visualColumnOf(out, iconPrivileged)
	providerCol := visualColumnOf(out, "apt")
	if gap := providerCol - markerCol - lipgloss.Width(iconPrivileged); gap != toolPrivilegeProviderGap {
		t.Fatalf("row = %q, privilege-provider gap = %d, want %d", out, gap, toolPrivilegeProviderGap)
	}
	providerCell := renderProviderCol(p, "apt", "", "", "", "", "apt", 14, false, false)
	if strings.Contains(providerCell, iconPrivileged) {
		t.Fatalf("provider cell = %q, privilege marker should be rendered separately", providerCell)
	}
}

func TestRenderToolRow_SystemBrewDoesNotShowPrivilegeMarker(t *testing.T) {
	p := defaultPalette()
	tool := &database.ToolCache{Name: "ripgrep", Provider: "system", Package: "ripgrep", Installed: false, Tracked: true}
	cols := colWidths{name: 24, prov: 14, ver: 8, screenW: 120}

	out := renderToolRow(p, tool, cols, "", "", "brew", "", "", false, false, syncMissing)

	if strings.Contains(out, iconPrivileged) {
		t.Fatalf("row = %q, system rows resolved to brew should not show privilege marker", out)
	}
}

func TestRenderList_SearchResultPrivilegeMarker(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSearch
	m.filter.SetValue("parsec")
	m.searchTools = []*database.ToolCache{{
		Name:          "parsec",
		Provider:      "system",
		InstalledWith: "brew",
		Description:   sql.NullString{String: "remote desktop", Valid: true},
		Privilege:     string(provider.PrivilegeMaybe),
	}}
	m.applyFilter()

	out := renderList(m)
	plain := stripANSIEscapeSequences(out)

	if !strings.Contains(out, iconPrivileged) {
		t.Fatalf("row = %q, want privilege marker for privileged search result", out)
	}
	if !strings.Contains(plain, "brew") {
		t.Fatalf("row = %q, want brew-backed provider label", out)
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
	if !strings.Contains(out, "brew") {
		t.Fatalf("orphan row = %q, want concrete provider label 'brew'", out)
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

func TestRenderToolRow_InstalledEcosystemProviderWithoutInstalledWithDoesNotGuessManager(t *testing.T) {
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

func TestRenderToolRow_EmptyGroupCellKeepsColumnsAligned(t *testing.T) {
	p := defaultPalette()
	grouped := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	grouped.Version.Valid = true
	grouped.Version.String = "2.40.0"
	ungrouped := &database.ToolCache{Name: "fd", Provider: "brew", Installed: true, Tracked: false}
	ungrouped.Version.Valid = true
	ungrouped.Version.String = "9.10.0"
	cols := colWidths{name: 20, prov: 10, ver: 8, group: 8, screenW: 80}

	withGroup := renderToolRow(p, grouped, cols, "", "dev", "", "", "", false, false, syncOK)
	withoutGroup := renderToolRow(p, ungrouped, cols, "", "", "", "", "", false, false, syncOrphan)
	if strings.Contains(withoutGroup, "[base]") || strings.Contains(withoutGroup, "[dev]") {
		t.Fatalf("ungrouped row should reserve an empty group cell, not render a badge: %q", withoutGroup)
	}
	for _, tc := range []struct {
		label string
		a     string
		b     string
	}{
		{label: "provider", a: "brew", b: "brew"},
		{label: "version", a: "2.40.0", b: "9.10.0"},
	} {
		if got, want := visualColumnOf(withoutGroup, tc.b), visualColumnOf(withGroup, tc.a); got != want {
			t.Fatalf("%s column shifted without group: got %d want %d\nwith group: %q\nwithout: %q", tc.label, got, want, withGroup, withoutGroup)
		}
	}
}

func TestRenderDotsRow_UsesCompactSpacing(t *testing.T) {
	const name = "abcdefghijkl"
	const target = "~/dot-target"

	m := baseModel(nil)
	m.mode = viewDots
	setDotsRepoForTest(&m, "/repo")
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
	displayName := dotKindFileIcon + " " + name
	nameCol := visualColumnOf(row, displayName)
	targetCol := visualColumnOf(row, target)
	if iconCol < 0 || nameCol < 0 || targetCol < 0 {
		t.Fatalf("failed to locate dots row fragments in row: %q", row)
	}

	if got, want := nameCol-iconCol-1, dotsIconNameGapW; got != want {
		t.Fatalf("icon-to-name gap = %d, want %d in row: %q", got, want, row)
	}
	if got, want := targetCol-nameCol-lipgloss.Width(displayName), dotsGapW; got != want {
		t.Fatalf("name-to-target gap = %d, want %d in row: %q", got, want, row)
	}
}

func TestRenderDotsRow_ShowsVariantMarker(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	setDotsRepoForTest(&m, "/repo")
	m.width = 100
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", Package: "nvim@laptop", Variant: true, TargetPath: "~/.config/nvim", State: app.DotStateSynced},
		{Name: "zshrc", Package: "zshrc", TargetPath: "~/.zshrc", State: app.DotStateSynced},
	}

	out := renderDots(m)
	if !strings.Contains(out, "nvim "+dotVariantIcon) {
		t.Fatalf("variant row should include marker %q, got:\n%s", dotVariantIcon, out)
	}
	if strings.Contains(out, "zshrc "+dotVariantIcon) {
		t.Fatalf("default row should not include variant marker, got:\n%s", out)
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

func TestRenderFilterBar_GroupsAlphabeticalAfterHost(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "host")
	m := baseModel(threeTools())
	m.groupNames = []string{"work", "apps", "personal"}
	out := renderFilterBar(m)
	hostIdx := strings.Index(out, "host")
	appsIdx := strings.Index(out, "apps")
	personalIdx := strings.Index(out, "personal")
	workIdx := strings.Index(out, "work")
	if hostIdx < 0 || appsIdx < 0 || personalIdx < 0 || workIdx < 0 {
		t.Fatalf("filter bar missing expected groups: %q", out)
	}
	if !(hostIdx < appsIdx && appsIdx < personalIdx && personalIdx < workIdx) {
		t.Fatalf("filter bar groups not host-first/alphabetical: %q", out)
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
	if !strings.Contains(out, "no tools yet") {
		t.Errorf("expected TUI no-tools copy for empty list, got: %q", out)
	}
	for _, cliCopy := range []string{"omni sync", "omni add", "run "} {
		if strings.Contains(out, cliCopy) {
			t.Fatalf("TUI empty state should not render CLI copy %q, got: %q", cliCopy, out)
		}
	}
}

func TestRenderList_SearchEmptyPrompt(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSearch
	m.filter.SetValue("r")
	m.applyFilter()

	out := renderList(m)
	if strings.Contains(out, "no tools") {
		t.Fatalf("search empty state should not reuse no-tools copy, got: %q", out)
	}
	if !strings.Contains(out, "type at least 2 characters") {
		t.Fatalf("expected search prompt, got: %q", out)
	}
}

func TestRenderList_SearchNoResults(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSearch
	m.filter.SetValue("zzzz")
	m.providerNames = []string{"brew"}
	m.providerTabIdx = 1
	m.applyFilter()

	out := renderList(m)
	if strings.Contains(out, "no tools") {
		t.Fatalf("search no-results state should not reuse no-tools copy, got: %q", out)
	}
	if !strings.Contains(out, "no search results for 'zzzz' in brew") {
		t.Fatalf("expected search no-results copy, got: %q", out)
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
	if !strings.Contains(combined, "ctrl+c") || !strings.Contains(combined, "cancel") {
		t.Fatalf("row operation should show ctrl+c cancel hint, got:\n%s", combined)
	}
	if strings.Contains(combined, "move group") || strings.Contains(combined, "delete") {
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

func TestRenderList_RowActionErrorStaysSingleLine(t *testing.T) {
	tool := &database.ToolCache{Name: "pip", Provider: "python", Installed: true, Outdated: true, Tracked: true}
	other := &database.ToolCache{Name: "git", Provider: "brew", Installed: true, Tracked: true}
	m := baseModel([]*database.ToolCache{tool, other})
	m.width = 120
	m.cursor = 1
	m.setToolActionError(toolKey("pip", "python"), "\x1b[31mpip install --upgrade pip: exit status 1\x1b[0m (stderr: error: externally-managed-environment\n\n× This environment is externally managed\n\tThe PyPA recommended tool for installing Python packages)")

	out := renderList(m)
	if !strings.Contains(out, "externally-managed") {
		t.Fatalf("row action error should keep the useful failure summary, got:\n%s", out)
	}
	if strings.Contains(out, "\n× This environment") || strings.Contains(out, "\n\tThe PyPA") {
		t.Fatalf("row action error leaked multiline stderr into the table, got:\n%s", out)
	}
}

func TestRowErrorSummaryNormalizesUnsafeOutput(t *testing.T) {
	got := rowErrorSummary("\x1b[31mfailed\x1b[0m\n\tbecause package manager wrote multiline stderr")
	if got != "failed because package manager wrote multiline stderr" {
		t.Fatalf("rowErrorSummary() = %q", got)
	}
}

func TestInlineDetailLines_RowActionErrorShowsProviderSolution(t *testing.T) {
	tool := &database.ToolCache{Name: "pip", Provider: "python", Installed: true, Outdated: true, Tracked: true}
	tool.Description.Valid = true
	tool.Description.String = "The PyPA recommended tool for installing Python packages."

	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	err := provider.NewExternallyManagedPythonError("pip3", "upgrade", provider.Tool{Name: "pip", Provider: "python"}, nil, "externally-managed-environment", []provider.ErrorSolution{
		{
			Label:          "Reinstall this tool with uv",
			Command:        "omni switch pip --from python --to uv",
			Detail:         "uv installs Python CLI tools into isolated tool environments.",
			Action:         provider.ErrorSolutionActionSwitchProvider,
			TargetProvider: "uv",
		},
	})
	m.setToolActionError(toolKey("pip", "python"), err.Error(), err)

	cols := colWidths{name: 20, prov: 10, screenW: 120}
	lines := inlineDetailLines(m, 120, cols)
	combined := strings.Join(lines, "\n")
	if !strings.Contains(combined, "proposal:") || !strings.Contains(combined, "Reinstall this tool with uv") {
		t.Fatalf("selected row should show provider remedy proposal, got:\n%s", combined)
	}
	if !strings.Contains(combined, "a apply fix") {
		t.Fatalf("selected row should show apply-fix shortcut, got:\n%s", combined)
	}
	if strings.Contains(combined, "omni switch pip --from python --to uv") {
		t.Fatalf("selected row should not render CLI command for applicable TUI remedy, got:\n%s", combined)
	}
	if !strings.Contains(combined, "isolated tool environments") {
		t.Fatalf("selected row should show provider remedy detail, got:\n%s", combined)
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

func TestRenderDots_BulkPendingUsesWaitingIcon(t *testing.T) {
	m := baseModel(nil)
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateMissing,
		Counts:     app.DotFileCounts{OutOfSync: 1},
	}}
	m.dotsPendingNames = map[string]bool{"nvim": true}

	out := renderDots(m)
	if !strings.Contains(out, m.palette.styleStatus.Render(iconPending)) {
		t.Fatalf("bulk pending dots row should use waiting icon, got:\n%s", out)
	}
}

func TestRenderDots_DoesNotRenderGroupPillBar(t *testing.T) {
	m := baseModel(nil)
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced, Group: "config", Counts: app.DotFileCounts{Synced: 1}},
		{Name: "zsh", TargetPath: "~/.zshrc", State: app.DotStateSynced, Group: "home", Counts: app.DotFileCounts{Synced: 1}},
	}

	out := stripANSIEscapeSequences(renderDots(m))
	first := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			first = line
			break
		}
	}
	if !strings.Contains(first, "/repo") {
		t.Fatalf("first visible dots line = %q, want repo status line instead of group controls\n%s", first, out)
	}
}

func TestRenderList_RowSpinnerKeepsNameColumn(t *testing.T) {
	tool := &database.ToolCache{Name: "curl", Provider: "brew", Installed: true, Tracked: true}
	m := baseModel([]*database.ToolCache{tool})
	m.cursor = 0
	normalLine := renderedLineContaining(renderList(m), "curl")

	active := m
	active.startRowOperation("curl", "brew", "Installing curl…")
	activeLine := renderedLineContaining(renderList(active), "curl")

	if got, want := visualColumnOf(activeLine, "curl"), visualColumnOf(normalLine, "curl"); got != want {
		t.Fatalf("tool row spinner shifted name column: got %d want %d\nnormal: %q\nactive: %q", got, want, normalLine, activeLine)
	}
}

func TestRenderDots_RowSpinnerKeepsNameColumn(t *testing.T) {
	m := baseModel(nil)
	setDotsRepoForTest(&m, "/repo")
	m.dotsCursor = 0
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateSynced,
		Counts:     app.DotFileCounts{Synced: 1},
	}}
	normalLine := renderedLineContaining(renderDots(m), "nvim")

	active := m
	active.dotsActiveName = "nvim"
	activeLine := renderedLineContaining(renderDots(active), "nvim")

	if got, want := visualColumnOf(activeLine, "nvim"), visualColumnOf(normalLine, "nvim"); got != want {
		t.Fatalf("dots row spinner shifted name column: got %d want %d\nnormal: %q\nactive: %q", got, want, normalLine, activeLine)
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

func TestRenderDots_RowOperationUsesSpinnerIcon(t *testing.T) {
	m := baseModel(nil)
	setDotsRepoForTest(&m, "/repo")
	m.dotsEntries = []app.DotStatus{{
		Name:       "nvim",
		TargetPath: "~/.config/nvim",
		State:      app.DotStateMissing,
		Counts:     app.DotFileCounts{OutOfSync: 1},
	}}
	m.dotsActiveName = "nvim"

	out := renderDots(m)
	if spin := m.spinner.View(); spin != "" && !strings.Contains(out, spin) {
		t.Fatalf("rendered dots should show row spinner %q, got:\n%s", spin, out)
	}
}

func TestRenderDots_NoExtraBlankLineAtTop(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	setDotsRepoForTest(&m, "/repo")
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced},
		{Name: "zsh", TargetPath: "~/.zshrc", State: app.DotStateSynced},
	}
	// dotsSearchActive is false by default — the removed else-branch would have
	// emitted a blank line here; verify the first line is not empty.
	out := stripANSIEscapeSequences(renderDots(m))
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatal("renderDots returned empty output")
	}
	if strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("renderDots emitted a leading blank line when search is not active; first line = %q", lines[0])
	}
}

func TestRenderDots_SearchActiveHasControlLine(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewDots
	setDotsRepoForTest(&m, "/repo")
	m.dotsLoaded = true
	m.dotsEntries = []app.DotStatus{
		{Name: "nvim", TargetPath: "~/.config/nvim", State: app.DotStateSynced},
		{Name: "zsh", TargetPath: "~/.zshrc", State: app.DotStateSynced},
	}
	m.dotsSearchActive = true

	out := stripANSIEscapeSequences(renderDots(m))
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		t.Fatal("renderDots returned empty output")
	}
	// The search control line contains "/" as the search prompt prefix.
	if !strings.Contains(lines[0], "/") {
		t.Fatalf("renderDots first line should contain search control when dotsSearchActive=true; first line = %q\nfull output:\n%s", lines[0], out)
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
	// system meta with brew concrete — label is just the concrete name
	out := renderProviderCol(p, "system", "brew", "", "", "", "brew", 14, false, false)
	if !strings.Contains(out, "brew") {
		t.Errorf("expected 'brew' in provider col, got: %q", out)
	}
}

func TestRenderProviderCol_Selected(t *testing.T) {
	p := defaultPalette()
	out := renderProviderCol(p, "system", "brew", "", "", "", "brew", 14, true, false)
	if !strings.Contains(out, "brew") {
		t.Errorf("expected 'brew' in selected provider col, got: %q", out)
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

	pressAgainRendered := renderPressAgainActionHint(m.palette, "", m.keys.SyncAll.Help().Key, "sync all")
	for _, want := range []string{
		m.palette.styleDangerLabel.Bold(true).Render("press "),
		m.palette.styleDangerSection.Bold(true).Render(m.keys.SyncAll.Help().Key),
		m.palette.styleDangerLabel.Bold(true).Render(" again to sync all"),
	} {
		if !strings.Contains(pressAgainRendered, want) {
			t.Fatalf("press-again confirmation should use danger style for %q, got: %q", want, pressAgainRendered)
		}
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
		{viewStatus, "status"},
		{viewGroups, "groups"},
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
	if !strings.Contains(out, "No settings.json was found.") {
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
	setDotsRepoForTest(&m, "")

	out := m.viewString()
	if !strings.Contains(out, "Enable dotfile sync?") {
		t.Errorf("expected setup popup content, got:\n%s", out)
	}
	if !strings.Contains(out, "No dotfiles repo configured yet.") {
		t.Errorf("expected dots tab background, got:\n%s", out)
	}
}

func TestViewString_SetupDefaultBackgroundIsDashboard(t *testing.T) {
	t.Run("zero-value setupBackgroundMode resolves to Dashboard", func(t *testing.T) {
		var m Model
		// Zero-value viewMode should be viewStatus (Dashboard), not viewList.
		if m.setupBackgroundMode != viewStatus {
			t.Fatalf("zero-value setupBackgroundMode = %v, want viewStatus", m.setupBackgroundMode)
		}
	})

	t.Run("noConfig setup shows Dashboard background", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		got := drive(m, toolsLoadedMsg{noConfig: true})
		if got.mode != viewSetup {
			t.Fatalf("mode = %v, want viewSetup", got.mode)
		}
		if got.setupBackgroundMode != viewStatus {
			t.Fatalf("setupBackgroundMode = %v, want viewStatus (Dashboard)", got.setupBackgroundMode)
		}
	})

	t.Run("noHost setup shows Dashboard background", func(t *testing.T) {
		m := baseModel(nil)
		m.loading = true
		got := drive(m, toolsLoadedMsg{noHost: true})
		if got.setupBackgroundMode != viewStatus {
			t.Fatalf("setupBackgroundMode = %v, want viewStatus (Dashboard)", got.setupBackgroundMode)
		}
	})

	t.Run("view.go fallback renders Dashboard not Tools", func(t *testing.T) {
		m := baseModel(threeTools())
		m.mode = viewSetup
		m.setupBackgroundMode = viewSetup // triggers the fallback branch

		out := stripANSIEscapeSequences(m.viewString())
		// Health Check is a Dashboard-only section header.
		if !strings.Contains(out, "Health Check") {
			t.Errorf("fallback background should be Dashboard (expected 'Health Check'), got:\n%s", out)
		}
	})
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
	if !strings.Contains(out, "Provider Priority") {
		t.Errorf("expected settings content in viewString, got:\n%s", out)
	}
}

func TestViewString_StatusMode(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
		{Name: "certifi", Provider: "python", Installed: true, Tracked: true},
	})
	m.mode = viewStatus
	m.ignoreSet = map[string]bool{"certifi": true}
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.doctorResult = &app.DoctorResult{
		Checks: []app.DoctorCheck{
			{ID: "config", Label: "Config", Status: app.DoctorStatusOK, Message: "settings.json is valid"},
			{ID: "dots", Label: "Dotfiles", Status: app.DoctorStatusWarn, Message: "dotfiles need attention"},
		},
		Summary: app.DoctorSummary{OK: 1, Warn: 1},
	}
	m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateConflict, Health: app.HealthConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
	out := m.viewString()
	for _, want := range []string{"Health Check", "Data", "Quiet", "Tool Updates", "Tool Sync", "Dotfiles", "Services", "Ignored Tools"} {
		if !strings.Contains(out, want) {
			t.Errorf("status view missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Inventory", "OK Checks", "Watch Sync"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("status view should not render old dashboard row %q:\n%s", unwanted, out)
		}
	}
}

func TestDashboardAttentionRowsCollapseOKDoctorChecks(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	m.allTools = []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}}
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Health: app.HealthOK}}
	m.doctorResult = &app.DoctorResult{
		Checks: []app.DoctorCheck{
			{ID: "config", Label: "Config", Status: app.DoctorStatusOK, Message: "settings.json is valid"},
			{ID: "cache", Label: "Cache", Status: app.DoctorStatusOK, Message: "cache database is readable"},
			{ID: "dots", Label: "Dotfiles", Status: app.DoctorStatusWarn, Message: "dotfiles need attention"},
		},
		Summary: app.DoctorSummary{OK: 2, Warn: 1},
	}

	rows := statusRows(m)
	if statusRowIndex(rows, "Config") >= 0 || statusRowIndex(rows, "Cache") >= 0 {
		t.Fatalf("OK doctor checks should be collapsed into one row: %#v", rows)
	}
	if statusRowIndex(rows, "OK Checks") >= 0 {
		t.Fatalf("OK doctor checks should not render a dashboard row: %#v", rows)
	}
	if idx := statusRowIndex(rows, "Doctor"); idx < 0 || rows[idx].action.kind != statusActionOpenDotsIssue {
		t.Fatalf("single dotfiles health warning should open dotfile issues, idx=%d row=%#v", idx, rows)
	}
}

func TestDashboardDoctorIgnoreWarningOffersFix(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	m.doctorResult = &app.DoctorResult{
		Checks: []app.DoctorCheck{
			{ID: "dots.ignore", Label: "Dots ignore", Status: app.DoctorStatusWarn, Message: "1 pattern issue(s) across dot entries"},
		},
		Summary: app.DoctorSummary{Warn: 1},
	}

	rows := statusRows(m)
	idx := statusRowIndex(rows, "Doctor")
	if idx < 0 {
		t.Fatalf("missing Doctor row: %#v", rows)
	}
	if rows[idx].action.kind != statusActionFixIgnore || rows[idx].action.desc != "fix ignore patterns" {
		t.Fatalf("dots.ignore row action = %#v, want fix ignore patterns", rows[idx].action)
	}
}

func TestStatusSelectedRowExpandsDetails(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	m.allTools = []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}}
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Health: app.HealthOK}}
	m.doctorResult = &app.DoctorResult{
		Checks: []app.DoctorCheck{
			{ID: "dots", Label: "Dotfiles", Status: app.DoctorStatusWarn, Message: "dotfiles need attention"},
			{ID: "config", Label: "Config", Status: app.DoctorStatusOK, Message: "settings.json is valid"},
		},
		Summary: app.DoctorSummary{OK: 1, Warn: 1},
	}

	m.statusCursor = statusRowIndex(statusRows(m), "Doctor")
	out := renderStatus(m)
	plain := stripANSIEscapeSequences(out)
	if !strings.Contains(plain, "Cause: 1 warn: Dotfiles") || !strings.Contains(plain, "Dotfiles: dotfiles need attention") {
		t.Fatalf("selected health row should show actionable details:\n%s", out)
	}
	if !strings.Contains(plain, "Action:") || !strings.Contains(plain, "enter") || !strings.Contains(plain, "open dotfiles") {
		t.Fatalf("selected health row should show a labeled action hint:\n%s", out)
	}
	if strings.Contains(out, "reconcile all") {
		t.Fatalf("bulk dashboard action should stay in the footer, not row details:\n%s", out)
	}
	if strings.Contains(out, "settings.json is valid") {
		t.Fatalf("passed health checks should stay out of the dashboard details:\n%s", out)
	}

	m.statusCursor = statusRowIndex(statusRows(m), "Tools")
	out = renderStatus(m)
	if strings.Contains(out, "open dotfiles") {
		t.Fatalf("unselected health row action should be collapsed:\n%s", out)
	}
}

func TestDashboardServicesRowShowsActionableDetails(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	setDotsRepoForTest(&m, "/repo/dotfiles")
	m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Health: app.HealthOK}}
	m.doctorResult = &app.DoctorResult{
		Checks:  []app.DoctorCheck{{ID: "config", Label: "Config", Status: app.DoctorStatusOK, Message: "settings.json is valid"}},
		Summary: app.DoctorSummary{OK: 1},
	}
	m.dotsReminderService = &app.DotsReminderService{
		Installed: true,
		Platform:  "launchd",
		Interval:  12 * time.Hour,
		Notify:    true,
		Files:     []string{"~/Library/LaunchAgents/com.lkshrk.omni.dots-reminder.plist"},
	}
	m.dotsWatchService = &app.DotsWatchService{
		Installed: true,
		Platform:  "systemd",
		Debounce:  5 * time.Second,
		Files:     []string{"~/.config/systemd/user/omni-dots-watch.service"},
	}

	m.statusCursor = statusRowIndex(statusRows(m), "Services")
	out := renderStatus(m)
	for _, want := range []string{"Services", "Reminder [ON]", "Watch [ON]", "launchd every 12h notify on", "systemd debounce 5s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("services row should show compact service state, missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "LaunchAgents") || strings.Contains(out, "journalctl") || strings.Contains(out, "systemd/user") {
		t.Fatalf("services row should not expose native service files or logs:\n%s", out)
	}

	m.dotsReminderServiceErr = "read service file: denied"
	rows := statusRows(m)
	idx := statusRowIndex(rows, "Services")
	if idx < 0 || rows[idx].section != statusSectionAttention {
		t.Fatalf("service warning should promote services to attention, idx=%d rows=%#v", idx, rows)
	}
	m.statusCursor = idx
	out = renderStatus(m)
	if !strings.Contains(out, "Reminder [WARN]") || !strings.Contains(out, "open service settings") {
		t.Fatalf("selected services warning should explain and route to settings:\n%s", out)
	}
}

func TestDashboardDotfilesRowShowsRecentHistory(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewStatus
	m.width = 72
	m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
	m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 2}}}
	m.dotsHistory = []app.DotsHistoryEntry{{
		Operation: "sync",
		Status:    "success",
		Summary:   "sync completed: no changes, 2 dotfiles unchanged",
		Time:      time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local),
	}}
	m.statusCursor = statusRowIndex(statusRows(m), "Dotfiles")

	out := renderStatus(m)
	plain := stripANSIEscapeSequences(out)
	for _, want := range []string{"last 01-02 03:04 sync: success", "no changes"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("selected dotfiles row should show recent history, missing %q:\n%s", want, out)
		}
	}
	assertLinesFitWidth(t, out, m.width)
}

func TestStatusNavigationAndEnterActions(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
	})
	m.mode = viewStatus
	got := drive(m, pressRune('j'))
	if got.statusCursor != 1 {
		t.Fatalf("statusCursor = %d, want 1 after j", got.statusCursor)
	}

	m.statusCursor = statusRowIndex(statusRows(m), "Tool Updates")
	out := renderStatus(m)
	if !strings.Contains(out, "U") || !strings.Contains(out, "upgrade all tools") || strings.Contains(out, "enter upgrade all tools") {
		t.Fatalf("updates row should use the Tools tab upgrade-all key inline:\n%s", out)
	}
	got = drive(m, pressEnter())
	if got.loading || got.upgradingKeys["*"] {
		t.Fatalf("enter should not trigger the updates row action, loading=%v upgrading=%v", got.loading, got.upgradingKeys)
	}
	got = drive(m, pressRune('U'))
	if !got.loading || !got.upgradingKeys["*"] {
		t.Fatalf("U on updates should start upgrade all, loading=%v upgrading=%v", got.loading, got.upgradingKeys)
	}

	m = baseModel([]*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}})
	m.mode = viewStatus
	m.statusCursor = statusRowIndex(statusRows(m), "Tool Sync")
	out = renderStatus(m)
	if !strings.Contains(out, "S") || !strings.Contains(out, "sync tools") || strings.Contains(out, "enter sync tools") {
		t.Fatalf("tool sync row should use the Tools tab sync-all key inline:\n%s", out)
	}
	got = drive(m, pressRune('S'))
	if !got.loading || !got.bulkPendingKeys[toolKey("fd", "brew")] {
		t.Fatalf("S on tool sync should start sync-all with row progress, loading=%v pending=%v", got.loading, got.bulkPendingKeys)
	}

	m, _ = dashboardDotsAppModel(t, nil)
	m.mode = viewStatus
	m.doctorResult = &app.DoctorResult{
		Checks:  []app.DoctorCheck{{ID: "dots", Label: "Dotfiles", Status: app.DoctorStatusWarn, Message: "dotfiles need attention"}},
		Summary: app.DoctorSummary{Warn: 1},
	}
	m.dotsEntries = []app.DotStatus{
		{Name: "zsh", State: app.DotStateSynced},
		{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}},
	}
	m.statusCursor = statusRowIndex(statusRows(m), "Dotfiles")
	out = renderStatus(m)
	if !strings.Contains(out, "S") || !strings.Contains(out, "sync dotfiles") || strings.Contains(out, "enter sync dotfiles") {
		t.Fatalf("dotfiles sync row should use the Dots tab sync-all key inline:\n%s", out)
	}
	got = drive(m, pressRune('S'))
	if !got.dotsLoading || !got.dotsPendingNames["nvim"] {
		t.Fatalf("S on dotfiles warning should start sync, loading=%v pending=%v", got.dotsLoading, got.dotsPendingNames)
	}

	m, _ = dashboardDotsAppModel(t, nil)
	m.mode = viewStatus
	m.dotsGitStatus = " M dotfiles/zsh/.zshrc"
	m.statusCursor = statusRowIndex(statusRows(m), "Dotfiles")
	out = renderStatus(m)
	if !strings.Contains(out, "enter") || !strings.Contains(out, "commit dotfiles") {
		t.Fatalf("dirty dotfiles row should use the Dots commit key inline:\n%s", out)
	}
	got = drive(m, pressEnter())
	if !got.dotsLoading || got.statusMsg != "Committing dots…" {
		t.Fatalf("enter on dirty dotfiles should commit, loading=%v status=%q", got.dotsLoading, got.statusMsg)
	}

	m = baseModel(nil)
	m.mode = viewStatus
	m.statusCursor = statusRowIndex(statusRows(m), "Services")
	out = renderStatus(m)
	if !strings.Contains(out, "enter") || !strings.Contains(out, "open service settings") {
		t.Fatalf("services row should keep the Settings navigation key inline:\n%s", out)
	}
	got = drive(m, pressEnter())
	if got.mode != viewSettings || got.settingsCursor != settingsRowDotsServices {
		t.Fatalf("enter on services should open service settings, mode=%v settingsCursor=%d", got.mode, got.settingsCursor)
	}
}

func dashboardDotsAppModel(t *testing.T, tools []*database.ToolCache) (Model, string) {
	t.Helper()
	appModel, repoDir := newDotsModelForCmds(t)
	m := baseModel(tools)
	m.app = appModel.app
	m.ctx = appModel.ctx
	m.setSettings(config.Settings{DotsRepo: repoDir})
	return m, repoDir
}

func TestDashboardReconcilePlanUsesCachedDotsAvailability(t *testing.T) {
	t.Run("includes dot steps when app is configured despite stale empty settings", func(t *testing.T) {
		m, _ := newDotsModelForCmds(t)
		m.settings = config.Settings{}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}

		steps := dashboardReconcilePlanItems(m)

		if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepSyncDots) {
			t.Fatalf("missing sync-dots step from app-backed configuration: %#v", steps)
		}
	})

	t.Run("omits dot steps when app is unconfigured despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		m.dotsGitStatus = " M dotfiles/nvim/init.lua"

		steps := dashboardReconcilePlanItems(m)

		for _, id := range []app.DashboardReconcileStepID{app.ReconcileStepSyncDots, app.ReconcileStepCommitDots} {
			if app.DashboardReconcilePlanHasStep(steps, id) {
				t.Fatalf("unexpected %s step from stale local settings: %#v", id, steps)
			}
		}
	})

	t.Run("includes dot steps when app is enabled despite stale local disabled flag", func(t *testing.T) {
		m, repoDir := newDotsModelForCmds(t)
		m.settings = config.Settings{DotsRepo: repoDir, DotsDisabled: config.BoolPtr(true)}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}

		steps := dashboardReconcilePlanItems(m)

		if !app.DashboardReconcilePlanHasStep(steps, app.ReconcileStepSyncDots) {
			t.Fatalf("missing sync-dots step from app-backed enabled state: %#v", steps)
		}
	})
}

func TestDashboardAutomationWarningsUseCachedDotsAvailability(t *testing.T) {
	t.Run("does not warn when app is configured despite stale empty settings", func(t *testing.T) {
		m, _ := newDotsModelForCmds(t)
		m.settings = config.Settings{}
		m.dotsReminderService = &app.DotsReminderService{Installed: true}

		warnings := dashboardDotsAutomationStatus(m).ReadinessWarnings

		if len(warnings) != 0 {
			t.Fatalf("readiness warnings = %v, want none from app-backed configuration", warnings)
		}
	})

	t.Run("warns when app is unconfigured despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsReminderService = &app.DotsReminderService{Installed: true}

		warnings := dashboardDotsAutomationStatus(m).ReadinessWarnings

		if want := []string{"Blocked: dots_repo is not configured."}; !slices.Equal(warnings, want) {
			t.Fatalf("readiness warnings = %v, want %v", warnings, want)
		}
	})

	t.Run("does not warn disabled when app is enabled despite stale local disabled flag", func(t *testing.T) {
		m, repoDir := newDotsModelForCmds(t)
		m.settings = config.Settings{DotsRepo: repoDir, DotsDisabled: config.BoolPtr(true)}
		m.dotsReminderService = &app.DotsReminderService{Installed: true}

		warnings := dashboardDotsAutomationStatus(m).ReadinessWarnings

		if len(warnings) != 0 {
			t.Fatalf("readiness warnings = %v, want none from app-backed enabled state", warnings)
		}
	})
}

func TestDashboardDotfileRowsUseCachedDotsAvailability(t *testing.T) {
	t.Run("syncs when app is configured despite stale empty settings", func(t *testing.T) {
		m, _ := newDotsModelForCmds(t)
		m.mode = viewStatus
		m.settings = config.Settings{}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}

		row, ok := statusDotfilesAttentionRow(m)
		if !ok {
			t.Fatal("expected dotfiles attention row")
		}
		if row.action.kind != statusActionSyncDots {
			t.Fatalf("attention action = %v, want sync dots from app-backed availability", row.action)
		}
		if strings.Contains(stripANSIEscapeSequences(row.value), "not set") {
			t.Fatalf("attention value should not use stale empty settings: %#v", row)
		}

		overview := statusDotfilesOverviewRow(m)
		if overview.action.kind != statusActionOpenDots {
			t.Fatalf("overview action = %v, want open dots", overview.action)
		}
	})

	t.Run("prompts configuration when app is unconfigured despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsSyncAvailCached = app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		m.dotsGitStatus = " M dotfiles/nvim/init.lua"

		row, ok := statusDotfilesAttentionRow(m)
		if !ok {
			t.Fatal("expected dotfiles attention row")
		}
		if row.action.kind != statusActionOpenSettings || row.action.settingsRow != settingsRowDotsRepo {
			t.Fatalf("attention action = %v, want dots repo settings", row.action)
		}
		if !strings.Contains(stripANSIEscapeSequences(row.value), "not set") {
			t.Fatalf("attention value should use app-backed unconfigured state: %#v", row)
		}
	})

	t.Run("syncs when app is enabled despite stale local disabled flag", func(t *testing.T) {
		m, repoDir := newDotsModelForCmds(t)
		m.mode = viewStatus
		m.settings = config.Settings{DotsRepo: repoDir, DotsDisabled: config.BoolPtr(true)}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}

		row, ok := statusDotfilesAttentionRow(m)
		if !ok {
			t.Fatal("expected dotfiles attention row")
		}
		if row.action.kind != statusActionSyncDots {
			t.Fatalf("attention action = %v, want sync dots from app-backed enabled state", row.action)
		}
		if strings.Contains(stripANSIEscapeSequences(row.value), "disabled") {
			t.Fatalf("attention value should not use stale disabled setting: %#v", row)
		}
	})

	t.Run("details render repo path from app-backed availability despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.width = 100
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsSyncAvailCached = app.DotsSyncAvailability{Configured: true, Reason: app.DotsSyncAvailabilityReady, RepoPath: "/repo/current-dotfiles"}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}
		m.statusCursor = statusRowIndex(statusRows(m), "Dotfiles")

		out := stripANSIEscapeSequences(renderStatus(m))

		if !strings.Contains(out, "repo /repo/current-dotfiles") || strings.Contains(out, "/tmp/stale-dotfiles") {
			t.Fatalf("dashboard details should show app-backed repo path, got:\n%s", out)
		}
	})

	t.Run("prompts configuration when app availability is unknown despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.app = &app.App{}
		m.mode = viewStatus
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		m.dotsGitStatus = " M dotfiles/nvim/init.lua"

		row, ok := statusDotfilesAttentionRow(m)
		if !ok {
			t.Fatal("expected dotfiles attention row")
		}
		if row.action.kind != statusActionOpenSettings || row.action.settingsRow != settingsRowDotsRepo {
			t.Fatalf("attention action = %v, want dots repo settings", row.action)
		}
		if !strings.Contains(stripANSIEscapeSequences(row.value), "not set") {
			t.Fatalf("attention value should not derive availability from stale local settings: %#v", row)
		}
	})

	t.Run("prompts configuration without app cache despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		m.dotsGitStatus = " M dotfiles/nvim/init.lua"

		row, ok := statusDotfilesAttentionRow(m)
		if !ok {
			t.Fatal("expected dotfiles attention row")
		}
		if row.action.kind != statusActionOpenSettings || row.action.settingsRow != settingsRowDotsRepo {
			t.Fatalf("attention action = %v, want dots repo settings", row.action)
		}
		if !strings.Contains(stripANSIEscapeSequences(row.value), "not set") {
			t.Fatalf("attention value should not derive availability from stale local settings without cache: %#v", row)
		}
	})
}

func TestDashboardAutomationIconUsesCachedDotsAvailability(t *testing.T) {
	t.Run("healthy when app is configured despite stale empty settings", func(t *testing.T) {
		m, _ := newDotsModelForCmds(t)
		m.settings = config.Settings{}
		m.dotsReminderService = &app.DotsReminderService{Installed: true}

		icon, _ := statusAutomationIcon(m)

		if icon != iconInstalled {
			t.Fatalf("automation icon = %q, want healthy from app-backed availability", icon)
		}
	})

	t.Run("warns when app is unconfigured despite stale local repo", func(t *testing.T) {
		m := baseModel(nil)
		m.settings = config.Settings{DotsRepo: "/tmp/stale-dotfiles"}
		m.dotsSyncAvailCached = app.DotsSyncAvailability{Reason: app.DotsSyncAvailabilityNoRepo}
		m.dotsReminderService = &app.DotsReminderService{Installed: true}

		icon, _ := statusAutomationIcon(m)

		if icon != iconFailed {
			t.Fatalf("automation icon = %q, want warning from app-backed unconfigured state", icon)
		}
	})
}

func TestDashboardRowsUseMiddleSummaryColumn(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
		{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
	})
	m.mode = viewStatus
	m.width = 140

	out := renderStatus(m)
	for _, want := range []string{"git", "fd missing", "1 installed locally"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard should render row summaries in the main row, missing %q:\n%s", want, out)
		}
	}
}

func TestDashboardSelectedUpdateDoesNotDuplicateSummary(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true, Version: sql.NullString{String: "2.45", Valid: true}, LatestVersion: sql.NullString{String: "2.46", Valid: true}},
	})
	m.mode = viewStatus
	m.statusCursor = statusRowIndex(statusRows(m), "Tool Updates")

	out := renderStatus(m)
	plain := stripANSIEscapeSequences(out)
	if got := strings.Count(plain, "Cause: git (2.46)"); got != 1 {
		t.Fatalf("selected update summary should move into one cause detail, count=%d:\n%s", got, out)
	}
	if got := strings.Count(plain, "git (2.46)"); got != 1 {
		t.Fatalf("selected update summary should not be repeated in details, count=%d:\n%s", got, out)
	}
}

func TestDashboardUpdatesShowPendingProgress(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true, Version: sql.NullString{String: "2.45", Valid: true}, LatestVersion: sql.NullString{String: "2.46", Valid: true}},
		{Name: "fd", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
	})
	m.mode = viewStatus
	m.upgradingKeys = map[string]bool{"*": true}
	m.bulkPendingKeys = map[string]bool{
		toolKey("git", "brew"): true,
		toolKey("fd", "brew"):  true,
	}
	m.statusCursor = statusRowIndex(statusRows(m), "Tool Updates")

	out := renderStatus(m)
	for _, want := range []string{iconPending, "2 queued", "Upgrading tools…", "ctrl+c", "queued:", "git (2.46)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard update row should show pending progress, missing %q:\n%s", want, out)
		}
	}
}

func TestDashboardToolSyncShowsPendingProgress(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
		{Name: "git", Provider: "brew", Installed: true, Tracked: true},
	})
	m.mode = viewStatus
	m.loading = true
	m.progressText = "Syncing tools 1/1"
	m.bulkPendingKeys = map[string]bool{
		toolKey("fd", "brew"): true,
	}
	m.statusCursor = statusRowIndex(statusRows(m), "Tool Sync")

	out := renderStatus(m)
	for _, want := range []string{iconPending, "syncing", "Syncing tools 1/1", "ctrl+c", "queued:", "fd missing"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard tool sync row should show pending progress, missing %q:\n%s", want, out)
		}
	}
}

func TestDashboardDataRowsShowLoadingWithCurrentSnapshot(t *testing.T) {
	t.Run("tools", func(t *testing.T) {
		m := baseModel([]*database.ToolCache{{Name: "git", Provider: "brew", Installed: true, Tracked: true}})
		m.mode = viewStatus
		m.scanningProviders = map[string]bool{"brew": true}
		m.refreshToolTotal = 1
		m.progressText = "Refreshing tools… 0/1: brew"

		out := renderStatus(m)
		for _, want := range []string{iconPending, "Refreshing tools… 0/1: brew", "1 installed locally"} {
			if !strings.Contains(out, want) {
				t.Fatalf("dashboard should keep tool data visible while refreshing, missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("dotfiles", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
		m.dotsLoading = true
		m.progressText = "Syncing dots 1/2"
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 3}}}

		out := renderStatus(m)
		for _, want := range []string{iconPending, "Syncing dots 1/2", "3/3 managed"} {
			if !strings.Contains(out, want) {
				t.Fatalf("dashboard should keep dotfile data visible while syncing, missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("services", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.dotsServicesRefreshing = true
		m.dotsReminderService = &app.DotsReminderService{Installed: true, Interval: 12 * time.Hour, Notify: true}

		out := renderStatus(m)
		for _, want := range []string{iconPending, "Refreshing service status…", "Reminder [ON]"} {
			if !strings.Contains(out, want) {
				t.Fatalf("dashboard should keep service data visible while refreshing, missing %q:\n%s", want, out)
			}
		}
	})
}

func TestDashboardIgnoredToolsAreMuted(t *testing.T) {
	m := baseModel([]*database.ToolCache{
		{Name: "certifi", Provider: "python", Installed: true, Tracked: true},
	})
	m.ignoreSet = map[string]bool{"certifi": true}

	rows := statusRows(m)
	idx := statusRowIndex(rows, "Ignored Tools")
	if idx < 0 {
		t.Fatalf("missing ignored row: %#v", rows)
	}
	if rows[idx].section != statusSectionQuiet {
		t.Fatalf("ignored tools should be quiet, row=%#v", rows[idx])
	}
	if got, want := rows[idx].value, m.palette.styleHelp.Render("1 ignored"); got != want {
		t.Fatalf("ignored value = %q, want muted %q", got, want)
	}
	if statusRowIndex(rows, "Inventory") >= 0 {
		t.Fatalf("dashboard should not render the old inventory row: %#v", rows)
	}
}

func TestDashboardRowsUseSharedStatusIcons(t *testing.T) {
	t.Run("attention rows", func(t *testing.T) {
		m := baseModel([]*database.ToolCache{
			{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
			{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
			{Name: "certifi", Provider: "python", Installed: true, Tracked: true},
		})
		m.mode = viewStatus
		setDotsRepoForTest(&m, "/repo/dotfiles")
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		m.dotsReminderServiceErr = "read service file: denied"
		m.ignoreSet = map[string]bool{"certifi": true}

		rows := statusRows(m)
		want := map[string]string{
			"Tool Updates":  iconFailed,
			"Tool Sync":     iconFailed,
			"Dotfiles":      iconFailed,
			"Services":      iconFailed,
			"Ignored Tools": iconIgnored,
		}
		for label, icon := range want {
			idx := statusRowIndex(rows, label)
			if idx < 0 {
				t.Fatalf("missing dashboard row %q: %#v", label, rows)
			}
			if rows[idx].icon != icon {
				t.Fatalf("%s icon = %q, want %q in rows %#v", label, rows[idx].icon, icon, rows)
			}
		}
	})

	t.Run("all clear", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		m.setSettings(config.Settings{DotsRepo: "/repo/dotfiles"})
		m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}

		rows := statusRows(m)
		idx := statusRowIndex(rows, "All Clear")
		if idx < 0 || rows[idx].icon != iconInstalled {
			t.Fatalf("all-clear row should use healthy icon, idx=%d rows=%#v", idx, rows)
		}
	})
}

func TestDashboardFooterBulkActions(t *testing.T) {
	t.Run("refresh starts dashboard refresh", func(t *testing.T) {
		m := baseModel(nil)
		m.mode = viewStatus
		got := drive(m, pressRune('R'))
		if !got.doctorRunning || !strings.Contains(got.statusMsg, "Refreshing dashboard") {
			t.Fatalf("refresh should start dashboard refresh, running=%v status=%q", got.doctorRunning, got.statusMsg)
		}
	})

	t.Run("reconcile opens planned operations", func(t *testing.T) {
		m, _ := dashboardDotsAppModel(t, []*database.ToolCache{
			{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
			{Name: "fd", Provider: "brew", Installed: false, Tracked: true},
		})
		m.mode = viewStatus
		m.dotsGitStatus = " M dotfiles/zsh/.zshrc"
		got := drive(m, pressRune('A'))
		if !got.dashboardReconcilePlanOpen || !got.dashboardReconcilePlanSelected[dashboardReconcilePlanSyncTools] {
			t.Fatalf("reconcile should open a selected plan, open=%v selected=%v", got.dashboardReconcilePlanOpen, got.dashboardReconcilePlanSelected)
		}
		out := got.viewString()
		for _, want := range []string{"Reconcile Plan", "Sync tools", "install 1 missing tool", "Upgrade tools", "Commit dotfiles"} {
			if !strings.Contains(out, want) {
				t.Fatalf("reconcile plan missing %q:\n%s", want, out)
			}
		}
		plain := stripANSIEscapeSequences(got.viewString())
		actionLine := renderedLineContaining(plain, "reconcile selected")
		if actionLine == "" || !strings.Contains(actionLine, "esc") || !strings.Contains(actionLine, "space") || !strings.Contains(actionLine, "enter") {
			t.Fatalf("reconcile popup should show cancel/select/primary actions:\n%s", plain)
		}
		if visualColumnOf(actionLine, "cancel") > visualColumnOf(actionLine, "select") || visualColumnOf(actionLine, "select") > visualColumnOf(actionLine, "reconcile selected") {
			t.Fatalf("reconcile popup actions should keep the usual cancel/select/primary order:\n%s", actionLine)
		}
	})

	t.Run("lowercase apply fix does not trigger dashboard reconcile", func(t *testing.T) {
		m := baseModel([]*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}})
		m.mode = viewStatus
		got := drive(m, pressRune('a'))
		if got.dashboardReconcilePlanOpen || got.loading {
			t.Fatalf("lowercase apply-fix key should not trigger dashboard reconcile, open=%v loading=%v", got.dashboardReconcilePlanOpen, got.loading)
		}
	})

	t.Run("reconcile ignores non-auto-fixable provider mismatch", func(t *testing.T) {
		m := baseModel([]*database.ToolCache{{Name: "fd", Provider: "system", Installed: true, InstalledWith: "apt", Tracked: true}})
		m.mode = viewStatus
		m.effectiveSystemManager = "brew"
		got := drive(m, pressRune('A'))
		if got.dashboardReconcilePlanOpen {
			t.Fatalf("reconcile should not open a plan for provider mismatch only: selected=%v", got.dashboardReconcilePlanSelected)
		}
		if got.statusMsg != "nothing to reconcile" {
			t.Fatalf("status = %q, want nothing to reconcile", got.statusMsg)
		}
		rows := statusRows(got)
		idx := statusRowIndex(rows, "Tool Sync")
		if idx < 0 || rows[idx].action.kind != statusActionOpenToolsSection {
			t.Fatalf("provider mismatch should route to sync issues, idx=%d row=%#v", idx, rows)
		}
	})

	t.Run("confirmed reconcile starts first operation and queues the rest", func(t *testing.T) {
		m, _ := dashboardDotsAppModel(t, []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}})
		m.mode = viewStatus
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		got := drive(m, pressRune('A'), pressEnter())
		if !got.loading || !got.bulkPendingKeys[toolKey("fd", "brew")] {
			t.Fatalf("tool sync should start and mark pending tools, loading=%v pending=%v", got.loading, got.bulkPendingKeys)
		}
		if got.dotsLoading {
			t.Fatalf("dot sync should wait for the tool step, dotsLoading=%v", got.dotsLoading)
		}
		if len(got.dashboardReconcileQueue) != 1 || got.dashboardReconcileQueue[0] != dashboardReconcilePlanSyncDots {
			t.Fatalf("reconcile queue = %v, want sync dots", got.dashboardReconcileQueue)
		}
		gen := got.progressGen
		got = drive(got, progressDoneMsg{gen: gen, message: "sync complete", tools: got.allTools})
		if !got.dotsLoading || !got.dotsPendingNames["nvim"] {
			t.Fatalf("dot sync should start after tool sync completes, loading=%v pending=%v", got.dotsLoading, got.dotsPendingNames)
		}
	})

	t.Run("reconcile skips queued operations that became clean", func(t *testing.T) {
		m, _ := dashboardDotsAppModel(t, []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}})
		m.mode = viewStatus
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		got := drive(m, pressRune('A'), pressEnter())
		got.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}
		got = drive(got, progressDoneMsg{gen: got.progressGen, message: "sync complete", tools: got.allTools})
		if got.dashboardReconcileRunning || got.dotsLoading {
			t.Fatalf("clean queued dot sync should be skipped, reconcileRunning=%v dotsLoading=%v", got.dashboardReconcileRunning, got.dotsLoading)
		}
		if got.statusMsg != "✓ reconciled" {
			t.Fatalf("status = %q, want reconciled", got.statusMsg)
		}
	})

	t.Run("deselected reconcile operation is skipped", func(t *testing.T) {
		m, _ := dashboardDotsAppModel(t, []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: false, Tracked: true}})
		m.mode = viewStatus
		m.dotsEntries = []app.DotStatus{{Name: "nvim", State: app.DotStateConflict, Counts: app.DotFileCounts{OutOfSync: 1}}}
		got := drive(m, pressRune('A'), pressRune(' '), pressEnter())
		if got.loading || got.bulkPendingKeys[toolKey("fd", "brew")] {
			t.Fatalf("deselected tool sync should not start, loading=%v pending=%v", got.loading, got.bulkPendingKeys)
		}
		if !got.dotsLoading || !got.dotsPendingNames["nvim"] {
			t.Fatalf("selected dot sync should start, loading=%v pending=%v", got.dotsLoading, got.dotsPendingNames)
		}
	})

	t.Run("dirty dotfiles row can start commit", func(t *testing.T) {
		m, _ := dashboardDotsAppModel(t, nil)
		m.mode = viewStatus
		m.dotsEntries = []app.DotStatus{{Name: "zsh", State: app.DotStateSynced, Counts: app.DotFileCounts{Synced: 1}}}
		m.dotsGitStatus = " M zsh/.zshrc"
		rows := statusRows(m)
		m.statusCursor = statusRowIndex(rows, "Dotfiles")
		if m.statusCursor < 0 || rows[m.statusCursor].action.kind != statusActionCommitDots {
			t.Fatalf("dirty dotfiles should expose commit action, cursor=%d row=%#v", m.statusCursor, rows)
		}
		got := drive(m, pressEnter())
		if !got.dotsLoading || got.progressText != "Committing dots…" {
			t.Fatalf("commit action should start dots commit, loading=%v progress=%q", got.dotsLoading, got.progressText)
		}
	})

	t.Run("upgrade tools key is row scoped", func(t *testing.T) {
		m := baseModel([]*database.ToolCache{{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true}})
		m.mode = viewStatus
		m.sectionCounts = map[section]int{}
		m.statusCursor = statusRowIndex(statusRows(m), "Tools")
		got := drive(m, pressRune('U'))
		if got.loading || got.upgradingKeys["*"] {
			t.Fatalf("U should not be a dashboard footer action, loading=%v upgrading=%v", got.loading, got.upgradingKeys)
		}
		m.statusCursor = statusRowIndex(statusRows(m), "Tool Updates")
		got = drive(m, pressRune('U'))
		if !got.loading || !got.upgradingKeys["*"] {
			t.Fatalf("U on updates row should start upgrade all, loading=%v upgrading=%v", got.loading, got.upgradingKeys)
		}
	})
}

func statusRowIndex(rows []statusListRow, label string) int {
	for i, row := range rows {
		if row.label == label {
			return i
		}
	}
	return -1
}

func TestStatusToolCountsIncludesDiscoveredTools(t *testing.T) {
	m := baseModel([]*database.ToolCache{{Name: "git", Provider: "brew", Installed: true, Outdated: true, Tracked: true}})
	m.discoveredTools = []*database.ToolCache{{Name: "fd", Provider: "brew", Installed: true}}
	m.rebuildDiscoveredKeys()

	counts := statusToolCounts(m)
	if counts.Updates != 1 {
		t.Fatalf("updates = %d, want 1", counts.Updates)
	}
	if counts.OutOfSync != 1 {
		t.Fatalf("outOfSync = %d, want 1 discovered orphan", counts.OutOfSync)
	}
	if counts.Installed != 2 {
		t.Fatalf("installed = %d, want 2", counts.Installed)
	}
}

func TestStatusToolsOverviewValue_UpdateCountUsesOutdatedStyle(t *testing.T) {
	// Case 1: outdated tool → value contains "update", not "tracked"
	m := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: true, Tracked: true},
	})
	counts := statusToolCounts(m)
	value := statusToolsOverviewValue(m, counts)
	if !strings.Contains(value, "update") {
		t.Errorf("outdated: value = %q, want it to contain \"update\"", value)
	}
	if strings.Contains(value, "tracked") {
		t.Errorf("outdated: value = %q, must not contain \"tracked\"", value)
	}

	// Case 2: no outdated tools → value contains "tracked", not "update"
	m2 := baseModel([]*database.ToolCache{
		{Name: "ripgrep", Provider: "brew", Installed: true, Outdated: false, Tracked: true},
	})
	counts2 := statusToolCounts(m2)
	value2 := statusToolsOverviewValue(m2, counts2)
	if !strings.Contains(value2, "tracked") {
		t.Errorf("no updates: value = %q, want it to contain \"tracked\"", value2)
	}
	if strings.Contains(value2, "update") {
		t.Errorf("no updates: value = %q, must not contain \"update\"", value2)
	}

	// Case 3: outdated tool → summary does NOT contain "update"
	summary := statusToolsOverviewSummary(counts)
	if strings.Contains(summary, "update") {
		t.Errorf("summary = %q, must not contain \"update\"", summary)
	}
}

func TestViewString_HostsMode(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewGroups
	m.hostInfo = &app.HostInfo{
		Hosts: map[string]config.HostAssignment{},
	}
	out := m.viewString()
	if !strings.Contains(out, "Groups") {
		t.Errorf("expected 'Groups' in groups viewString, got:\n%s", out)
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
	if !strings.Contains(out, "enter") || !strings.Contains(out, "pick") || !strings.Contains(out, "esc") || !strings.Contains(out, "close") {
		t.Errorf("file picker overlay should render text key labels, got: %q", out)
	}
	actionLine := renderedLineContaining(out, "pick")
	for _, unwanted := range []string{"type", "path", "tab", "complete"} {
		if strings.Contains(actionLine, unwanted) {
			t.Errorf("file picker overlay action line should not render redundant %q hint, got: %q", unwanted, actionLine)
		}
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
	for _, want := range []string{"enter", "pick", "esc", "close"} {
		if !strings.Contains(out, want) {
			t.Errorf("file picker popup missing %q in hints, got: %q", want, out)
		}
	}
	actionLine := renderedLineContaining(out, "pick")
	for _, unwanted := range []string{"type", "path", "tab", "complete"} {
		if strings.Contains(actionLine, unwanted) {
			t.Errorf("file picker action line should not render redundant %q hint, got: %q", unwanted, actionLine)
		}
	}
	if visualColumnOf(actionLine, "close") >= visualColumnOf(actionLine, "pick") {
		t.Errorf("file picker primary action should be right of close action, got: %q", out)
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
	for _, unwanted := range []string{"type", "tab", "complete"} {
		if strings.Contains(popup, unwanted) {
			t.Fatalf("file picker popup should not render redundant %q hint:\n%s", unwanted, popup)
		}
	}
	if !strings.Contains(popup, "enter") || !strings.Contains(popup, "esc") {
		t.Fatalf("file picker popup missing footer hints:\n%s", popup)
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

func TestTruncatedGitStatus(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		maxLines     int
		wantLines    []string
		wantOverflow string
	}{
		{
			name:         "empty",
			status:       "",
			maxLines:     3,
			wantLines:    nil,
			wantOverflow: "",
		},
		{
			name:         "under limit",
			status:       " M settings.json\n M dots.go\n",
			maxLines:     3,
			wantLines:    []string{" M settings.json", " M dots.go"},
			wantOverflow: "",
		},
		{
			name:         "at limit",
			status:       " M a\n M b\n M c\n",
			maxLines:     3,
			wantLines:    []string{" M a", " M b", " M c"},
			wantOverflow: "",
		},
		{
			name:         "over limit",
			status:       " M a\n M b\n M c\n M d\n M e\n",
			maxLines:     3,
			wantLines:    []string{" M a", " M b", " M c"},
			wantOverflow: "+2 more repo change(s)",
		},
		{
			name:         "filters empty lines",
			status:       " M a\n\n M b\n  \n M c\n M d\n",
			maxLines:     3,
			wantLines:    []string{" M a", " M b", " M c"},
			wantOverflow: "+1 more repo change(s)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines, overflow := truncatedGitStatus(tt.status, tt.maxLines)
			if len(lines) == 0 && len(tt.wantLines) == 0 {
				// both nil/empty — ok
			} else if len(lines) != len(tt.wantLines) {
				t.Fatalf("lines = %v, want %v", lines, tt.wantLines)
			} else {
				for i := range lines {
					if lines[i] != tt.wantLines[i] {
						t.Fatalf("lines[%d] = %q, want %q", i, lines[i], tt.wantLines[i])
					}
				}
			}
			if overflow != tt.wantOverflow {
				t.Fatalf("overflow = %q, want %q", overflow, tt.wantOverflow)
			}
		})
	}
}

// ── renderSetupOptions ────────────────────────────────────────────────────────

func TestRenderSetupOptions_DescriptionAlignment(t *testing.T) {
	// Labels of different lengths: descriptions must all start at the same column.
	m := baseModel(nil)
	m.width = 80
	options := []setupOption{
		{Label: "short", Detail: "detail A", Selected: false},
		{Label: "much-longer-label", Detail: "detail B", Selected: false},
		{Label: "mid", Detail: "detail C", Selected: true},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))

	// Collect the column position of each detail string.
	cols := make(map[string]int)
	for _, line := range strings.Split(out, "\n") {
		for _, detail := range []string{"detail A", "detail B", "detail C"} {
			if strings.Contains(line, detail) {
				cols[detail] = visualColumnOf(line, detail)
			}
		}
	}
	for _, detail := range []string{"detail A", "detail B", "detail C"} {
		if _, ok := cols[detail]; !ok {
			t.Fatalf("detail %q not found in output:\n%s", detail, out)
		}
	}
	if cols["detail A"] != cols["detail B"] || cols["detail B"] != cols["detail C"] {
		t.Fatalf("description columns should be equal across rows: detailA=%d detailB=%d detailC=%d\n%s",
			cols["detail A"], cols["detail B"], cols["detail C"], out)
	}
}

func TestRenderSetupOptions_DescriptionColumnMatchesFormula(t *testing.T) {
	// Verify that the desc column equals prefixW + maxLabelW + detailGap (2).
	m := baseModel(nil)
	m.width = 80
	options := []setupOption{
		{Label: "abc", Detail: "x", Selected: false},
		{Label: "abcdefgh", Detail: "y", Selected: false}, // widest: 8 runes
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))

	// prefixW=2 (cursor), maxLabelW=8, detailGap=2 → descCol=12
	wantDescCol := 2 + 8 + 2 // 12
	for _, line := range strings.Split(out, "\n") {
		for _, detail := range []string{"x", "y"} {
			if strings.Contains(line, detail) {
				if got := visualColumnOf(line, detail); got != wantDescCol {
					t.Fatalf("detail %q at column %d, want %d:\n%s", detail, got, wantDescCol, out)
				}
			}
		}
	}
}

func TestRenderSetupOptions_DescriptionColumnWithCheckbox(t *testing.T) {
	// When all options have Checked fields, prefix grows by 4 ("[ ] " or "[x] ").
	// All descriptions must start at the same column.
	m := baseModel(nil)
	m.width = 80
	checkedTrue := true
	checkedFalse := false
	options := []setupOption{
		{Label: "brew", Detail: "system packages", Selected: false, Checked: &checkedTrue},
		{Label: "node", Detail: "js packages", Selected: false, Checked: &checkedFalse},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))

	// prefixW=2+4=6, maxLabelW=4 ("brew"/"node"), detailGap=2 → descCol=12
	wantDescCol := 6 + 4 + 2 // 12
	colA, colB := -1, -1
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "system packages") {
			colA = visualColumnOf(line, "system packages")
		}
		if strings.Contains(line, "js packages") {
			colB = visualColumnOf(line, "js packages")
		}
	}
	if colA < 0 || colB < 0 {
		t.Fatalf("details not found in output:\n%s", out)
	}
	if colA != wantDescCol {
		t.Fatalf("\"system packages\" column=%d, want %d:\n%s", colA, wantDescCol, out)
	}
	if colA != colB {
		t.Fatalf("description columns should match: %d vs %d:\n%s", colA, colB, out)
	}
}

func TestRenderSetupOptions_LineWrapping(t *testing.T) {
	// A detail longer than availW should wrap to a continuation line.
	m := baseModel(nil)
	// prefixW=2, maxLabelW=4 ("opt1"), detailGap=2 → descCol=8; availW=m.width-8
	m.width = 30 // availW = 30-8 = 22
	longDetail := "this is a fairly long detail text that must wrap"
	options := []setupOption{
		{Label: "opt1", Detail: longDetail, Selected: false},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines for wrapped detail, got:\n%s", out)
	}
	// Every continuation line must be indented by descCol (8 spaces).
	indent := strings.Repeat(" ", 8)
	foundContinuation := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, indent) {
			t.Fatalf("continuation line missing %d-space indent:\n%q\nfull output:\n%s", 8, line, out)
		}
		foundContinuation = true
	}
	if !foundContinuation {
		t.Fatalf("expected at least one continuation/wrapped line:\n%s", out)
	}
}

func TestRenderSetupOptions_NoDetail_NoTrailingGap(t *testing.T) {
	// When Detail is empty, the line should contain only prefix + label with no trailing padding.
	m := baseModel(nil)
	m.width = 80
	options := []setupOption{
		{Label: "brew", Detail: "", Selected: false},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "brew") {
			// Right-trimmed line must end right after "brew".
			trimmed := strings.TrimRight(line, " ")
			if !strings.HasSuffix(trimmed, "brew") {
				t.Fatalf("line with empty detail should not have trailing content after label, got:\n%q", line)
			}
			return
		}
	}
	t.Fatalf("label not found in output:\n%s", out)
}

func TestRenderSetupOptions_SelectedRowCursorDiffers(t *testing.T) {
	// The selected row's cursor character must differ from the unselected cursor.
	m := baseModel(nil)
	m.width = 80
	options := []setupOption{
		{Label: "first", Detail: "d1", Selected: false},
		{Label: "second", Detail: "d2", Selected: true},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got:\n%s", out)
	}
	firstCursor := string([]rune(lines[0])[0])
	secondCursor := string([]rune(lines[1])[0])
	if firstCursor == secondCursor {
		t.Fatalf("selected and unselected rows should have different cursor characters: both=%q", firstCursor)
	}
}

func TestRenderSetupOptions_CheckboxState(t *testing.T) {
	// Checked and unchecked options must render "[x]" and "[ ]" respectively.
	m := baseModel(nil)
	m.width = 80
	checkedTrue := true
	checkedFalse := false
	options := []setupOption{
		{Label: "brew", Detail: "", Selected: false, Checked: &checkedTrue},
		{Label: "node", Detail: "", Selected: false, Checked: &checkedFalse},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))
	if !strings.Contains(out, "[x]") {
		t.Fatalf("expected [x] for checked option:\n%s", out)
	}
	if !strings.Contains(out, "[ ]") {
		t.Fatalf("expected [ ] for unchecked option:\n%s", out)
	}
}

func TestRenderSetupOptions_MultipleOptions_LineCount(t *testing.T) {
	// n options with single-line details → exactly n lines.
	m := baseModel(nil)
	m.width = 80
	options := []setupOption{
		{Label: "a", Detail: "alpha", Selected: false},
		{Label: "b", Detail: "beta", Selected: false},
		{Label: "c", Detail: "gamma", Selected: false},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for 3 short-detail options, got %d:\n%s", len(lines), out)
	}
}

func TestRenderSetupOptions_WrapIndentMatchesDescCol(t *testing.T) {
	// Continuation lines from wrapping must be indented by exactly descCol spaces.
	m := baseModel(nil)
	// prefixW=2, labels "go"(2)/"rust"(4) → maxLabelW=4, detailGap=2 → descCol=8
	m.width = 20 // availW = 20-8 = 12
	longDetail := "some fairly long text here for wrapping test"
	options := []setupOption{
		{Label: "go", Detail: longDetail, Selected: false},
		{Label: "rust", Detail: "short", Selected: false},
	}
	out := stripANSIEscapeSequences(renderSetupOptions(m, options))
	lines := strings.Split(out, "\n")

	firstDetailLine := -1
	for i, line := range lines {
		if strings.Contains(line, "go") && !strings.Contains(line, "rust") {
			firstDetailLine = i
			break
		}
	}
	if firstDetailLine < 0 {
		t.Fatalf("could not find 'go' option line:\n%s", out)
	}
	wantIndent := strings.Repeat(" ", 8)
	for _, line := range lines[firstDetailLine+1:] {
		if strings.Contains(line, "rust") {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, wantIndent) {
			t.Fatalf("continuation line should start with 8 spaces, got:\n%q\nfull output:\n%s", line, out)
		}
		stripped := strings.TrimLeft(line, " ")
		actualIndent := len(line) - len(stripped)
		if actualIndent != 8 {
			t.Fatalf("continuation line indent = %d, want exactly 8:\n%q\nfull output:\n%s", actualIndent, line, out)
		}
	}
}
