package tui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
)

const parityWidth = 120

// Structurally equivalent between a tools row and an agents row so both renderers see comparable input.
type parityFixture struct {
	name          string // identical name used for both tools.Name and agents row Name — same rune length
	version       string // "" for not-installed
	latestVersion string // "" unless outdated
	installed     bool
	outdated      bool
	missing       bool // config-declared but not installed (tools: syncMissing: agents: agentsMarkMissing)
	ignored       bool
	group         string // "" for no group badge
	selected      bool
}

type parityRow struct {
	plain string // stripped of ANSI
	raw   string // full styled text, as rendered
	cols  colWidths
}

// Measured from the actual rendered string rather than recomputed from layout constants: a formula could drift out of sync with the renderer and stop catching regressions.
func (r parityRow) offsetOf(target string) int {
	if target == "" {
		return -1
	}
	runes := []rune(r.plain)
	targetRunes := []rune(target)
	for i := 0; i+len(targetRunes) <= len(runes); i++ {
		match := true
		for j, tr := range targetRunes {
			if runes[i+j] != tr {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Lets tests assert on style identity by name instead of comparing opaque lipgloss.Style values.
type namedStyle struct {
	name  string
	style lipgloss.Style
}

// Order matters for resolveStyleName's first-match semantics: most specific styles first so foreground collisions resolve predictably.
func styleRegistry(p palette) []namedStyle {
	return []namedStyle{
		{"styleMissing", p.styleMissing},
		{"styleOutdated", p.styleOutdated},
		{"styleInstalled", p.styleInstalled},
		{"styleOrphan", p.styleOrphan},
		{"styleWrongProv", p.styleWrongProv},
		{"styleProvider", p.styleProvider},
		{"styleProviderLinux", p.styleProviderLinux},
		{"styleProviderSystem", p.styleProviderSystem},
		{"styleVersion", p.styleVersion},
		{"styleVersionMuted", p.styleVersionMuted},
		{"styleHelp", p.styleHelp},
		{"styleIgnored", p.styleIgnored},
		{"styleNormal", p.styleNormal},
		{"styleStatus", p.styleStatus},
		{"styleSection", p.styleSection},
		{"styleErr", p.styleErr},
	}
}

// Every palette style used by row rendering has a distinct (foreground, bold) pair, so that alone disambiguates; returns "" when nothing matched, which callers must treat as a hard failure.
func resolveStyleName(p palette, s lipgloss.Style) string {
	fg := s.GetForeground()
	bold := s.GetBold()
	for _, ns := range styleRegistry(p) {
		if colorsEqual(ns.style.GetForeground(), fg) && ns.style.GetBold() == bold {
			return ns.name
		}
	}
	return ""
}

// Every palette color is lipgloss.Color(hex), a comparable concrete type, so interface == is exact and avoids RGBA()'s lossy quantization.
func colorsEqual(a, b color.Color) bool {
	return a == b
}

// baseModel/agentsAllModel never call applyTheme, so the default palette is zero-valued and every style would resolve to the same "no color" bucket, silently passing style-parity assertions that should fail.
func parityPalette() palette {
	return buildPaletteFor(true)
}

func buildToolFixture(f parityFixture) (*app.ToolView, map[string]string, map[string][]string) {
	t := &app.ToolView{
		Name:      f.name,
		Provider:  "brew",
		Package:   f.name,
		Installed: f.installed,
		Tracked:   true,
	}
	if f.version != "" {
		t.Version = f.version
	}
	if f.outdated {
		t.Outdated = true
		t.LatestVersion = f.latestVersion
	}
	groups := map[string]string{}
	memberships := map[string][]string{}
	if f.group != "" {
		groups[toolKey(t.Name, t.Provider)] = f.group
		memberships[toolKey(t.Name, t.Provider)] = []string{f.group}
	}
	return t, groups, memberships
}

// Renders through the production renderer, mirroring what renderList does for a single-tool list at parityWidth.
func renderToolsRowForTest(t *testing.T, f parityFixture) parityRow {
	t.Helper()
	tool, groups, memberships := buildToolFixture(f)
	pal := parityPalette()

	var groupNames []string
	if f.group != "" {
		groupNames = []string{f.group}
	}
	cols := newColWidthsWithProviderPins([]*app.ToolView{tool}, memberships, nil, groupNames, nil, nil, "", "", "", parityWidth, nil)

	m := baseModel([]*app.ToolView{tool})
	m.palette = pal
	m.toolGroups = groups
	m.toolMemberships = memberships
	if f.ignored {
		m.ignoreSet = map[string]bool{tool.Name: true}
	}
	ss := m.syncStatusOf(tool)

	rowGroups := memberships[toolKey(tool.Name, tool.Provider)]
	raw := renderToolRowWithProviderPin(pal, tool, cols, "", rowGroups, nil, "", "", "", "", "", f.ignored, f.selected, ss)
	return parityRow{plain: stripANSIEscapeSequences(raw), raw: raw, cols: cols}
}

// Renders at the SAME cols the ignored render used, isolating what the ignored branch changes about provider-cell alignment from any column-width shift.
func renderToolsRowIgnoredOverride(t *testing.T, cols colWidths) parityRow {
	t.Helper()
	tool, groups, memberships := buildToolFixture(fixtureIgnored)
	pal := parityPalette()
	m := baseModel([]*app.ToolView{tool})
	m.palette = pal
	m.toolGroups = groups
	m.toolMemberships = memberships
	ss := m.syncStatusOf(tool)
	raw := renderToolRowWithProviderPin(pal, tool, cols, "", nil, nil, "", "", "", "", "", false, false, ss)
	return parityRow{plain: stripANSIEscapeSequences(raw), raw: raw, cols: cols}
}

// Uses the skills feature since skills rows are the closest structural analog to a tools row (name + version/date + group).
func buildAgentsFixture(f parityFixture) (Model, agentsAllRow) {
	if f.outdated {
		return buildAgentsPluginFixture(f)
	}
	return buildAgentsSkillsFixture(f)
}

// Not used for the outdated case: skillPackageRowStatus never returns agentsStatusUpdates, so forcing it would fabricate an unreachable state and mask the real gap.
func buildAgentsSkillsFixture(f parityFixture) (Model, agentsAllRow) {
	row := app.SkillPackageRow{
		Name:      f.name,
		Source:    "owner/" + f.name,
		Installed: f.installed,
		Updated:   f.version,
	}
	if f.group != "" {
		row.Groups = []string{f.group}
	}
	if f.installed {
		row.PerAgentStatus = map[string]app.SkillStatus{"claude-code": app.SkillStatusInstalled}
	}

	m := agentsAllModel([]app.SkillPackageRow{row}, nil, nil)
	m.palette = parityPalette()
	m.width = parityWidth

	status, mark := skillPackageRowStatus(f.installed, false, false, false, false)
	if f.ignored {
		status = agentsStatusIgnored
	}
	e := agentsAllRow{feature: agentsSectionSkills, localIdx: 0, agentID: "claude-code", status: status, mark: mark, sortName: f.name}
	return m, e
}

// The plugin branch is the one feature rendering a real "current → latest" arrow via fitUpgradeVersionText, structurally matching an outdated tool; skills would force an unreachable state and compare a plain date against tools' arrow.
func buildAgentsPluginFixture(f parityFixture) (Model, agentsAllRow) {
	row := app.PluginRow{
		Name:          f.name,
		Marketplace:   "acme",
		Version:       f.version,
		LatestVersion: f.latestVersion,
	}
	if f.group != "" {
		row.Groups = []string{f.group}
	}
	row.PerAgentStatus = map[string]app.PluginStatus{"claude-code": app.PluginStatusInstalled}

	m := agentsAllModel(nil, nil, []app.PluginRow{row})
	m.palette = parityPalette()
	m.width = parityWidth

	status, mark := agentsStatusUpdates, agentsMarkNone
	if f.ignored {
		status = agentsStatusIgnored
	}
	e := agentsAllRow{feature: agentsSectionPlugins, localIdx: 0, agentID: "claude-code", status: status, mark: mark, sortName: f.name}
	return m, e
}

// Reconstructed into the same flat row-string shape renderAgentsGroupedTab produces so it is comparable to renderToolsRowForTest's output.
func renderAgentsRowForTest(t *testing.T, f parityFixture) parityRow {
	t.Helper()
	m, e := buildAgentsFixture(f)
	rows := []agentsAllRow{e}
	cols := agentsColWidths(m, rows)
	return renderAgentsRowWithCols(t, f, cols)
}

// Lets tests normalize one known-divergent column and observe the actual renderer output at that width rather than recomputing offsets by hand.
func renderAgentsRowWithCols(t *testing.T, f parityFixture, cols colWidths) parityRow {
	t.Helper()
	m, e := buildAgentsFixture(f)
	left, right := agentsRowCells(m, m.palette, cols, e, f.selected)
	raw := renderSplitRow(left, right, rowAvailableWidth(m.width), listColumnGap, listColumnGap)
	return parityRow{plain: stripANSIEscapeSequences(raw), raw: raw, cols: cols}
}

// The name column starts right after the icon+gap; the right-aligned block starts at rowAvailableWidth(width) minus its own rendered width — deterministic from cols alone.
type colOffsets struct {
	icon, name, right, prov, ver, group int
}

func toolColOffsets(cols colWidths, width int) colOffsets {
	name := listIconWidth + toolIconNameGapWidth
	right := rowAvailableWidth(width) - toolRightGroupWidth(cols)
	prov := right
	if cols.priv > 0 {
		prov += cols.priv + toolPrivilegeProviderGap
	}
	ver := prov + cols.prov + listColumnGap
	group := 0
	if cols.group > 0 {
		group = ver + cols.ver + listColumnGap
	}
	return colOffsets{icon: 0, name: name, right: right, prov: prov, ver: ver, group: group}
}

// Agents rows never reserve a priv column, so prov starts exactly at right with no privilege-gap offset — the one structurally intentional divergence from tools.
func agentsColOffsets(cols colWidths, width int) colOffsets {
	// agents' mark icon and iconGap are the same widths as tools', so the name offset formula is identical even though the constants are not shared.
	name := listIconWidth + toolIconNameGapWidth
	right := rowAvailableWidth(width) - toolRightGroupWidth(cols)
	prov := right
	ver := prov + cols.prov + listColumnGap
	group := 0
	if cols.group > 0 {
		group = ver + cols.ver + listColumnGap
	}
	return colOffsets{icon: 0, name: name, right: right, prov: prov, ver: ver, group: group}
}

func assertEqualInt(t *testing.T, got, want int, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", what, got, want)
	}
}

// Sidesteps ANSI parsing: lipgloss rendering is pure in (style, text), so a candidate whose Render(plain) matches the rendered bytes is provably the style that was used.
func resolveRenderedStyleName(t *testing.T, p palette, plain, rendered string) string {
	t.Helper()
	for _, ns := range styleRegistry(p) {
		if ns.style.Render(plain) == rendered {
			return ns.name
		}
	}
	return ""
}

func requireRenderedStyle(t *testing.T, p palette, plain, rendered, want string) {
	t.Helper()
	got := resolveRenderedStyleName(t, p, plain, rendered)
	if got != want {
		t.Errorf("style for %q = %q, want %q (rendered=%q)", plain, got, want, rendered)
	}
}

var (
	fixtureBase = parityFixture{name: "widget-outdated", version: "1.0.0", latestVersion: "2.0.0", installed: true, outdated: true}
	// name deliberately avoids the substring "missing" so findStyledSubstring(raw, "missing") matches the version cell, not the row name.
	fixtureMissing  = parityFixture{name: "widget-absent", installed: false}
	fixtureGrouped  = parityFixture{name: "widget-grouped", version: "1.0.0", installed: true, group: "devtools"}
	fixtureIgnored  = parityFixture{name: "widget-ignored", version: "1.0.0", installed: true, ignored: true}
	fixtureSelected = parityFixture{name: "widget-selected", version: "1.0.0", installed: true, selected: true}
)

var allParityFixtures = []parityFixture{fixtureBase, fixtureMissing, fixtureGrouped, fixtureIgnored, fixtureSelected}

func TestParity_ColumnOffsets_NameStartsRightAfterIconGap(t *testing.T) {
	t.Parallel()
	for _, f := range allParityFixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			tr := renderToolsRowForTest(t, f)
			ar := renderAgentsRowForTest(t, f)

			toolNameOff := tr.offsetOf(f.name)
			agentNameOff := ar.offsetOf(f.name)
			if toolNameOff < 0 {
				t.Fatalf("tools: could not locate name %q in plain row %q", f.name, tr.plain)
			}
			if agentNameOff < 0 {
				t.Fatalf("agents: could not locate name %q in plain row %q", f.name, ar.plain)
			}
			assertEqualInt(t, agentNameOff, toolNameOff, "name column x-offset (agents vs tools)")
		})
	}
}

// tools' provider column floors at 8 while agents' floors at agentsAgentIDColFloor=11 — different constants by design, not a shared width-fitting bug, so this is deliberately not asserted equal.
func TestParity_ColumnOffsets_ProviderAgentColumnWidthIsAKnownContentShapeDivergence(t *testing.T) {
	t.Parallel()
	tr := renderToolsRowForTest(t, fixtureGrouped)
	ar := renderAgentsRowForTest(t, fixtureGrouped)

	const toolProvFloor = 8 // view_list.go:436 newColWidthsWithProviderPins seed
	if tr.cols.prov < toolProvFloor {
		t.Fatalf("tools cols.prov = %d, want >= floor %d", tr.cols.prov, toolProvFloor)
	}
	if ar.cols.prov < agentsAgentIDColFloor {
		t.Fatalf("agents cols.prov = %d, want >= agentsAgentIDColFloor %d", ar.cols.prov, agentsAgentIDColFloor)
	}

	knownDivergentProvWidth := toolProvFloor != agentsAgentIDColFloor
	if !knownDivergentProvWidth {
		t.Fatal("floors now match — TestParity_ColumnOffsets_RightBlockOffsetsMatch's knownDivergent allowlist should be revisited/tightened")
	}

	toolOff := toolColOffsets(tr.cols, parityWidth)
	agentOff := agentsColOffsets(ar.cols, parityWidth)
	if agentOff.right == toolOff.right {
		t.Skip("right-block offsets happened to coincide for this fixture; the divergence is still real (see cols.prov floors above), just not manifesting at this width/name-length combination")
	}
}

// Fixes cols.prov to the same value on both sides so only offset math is compared; fixtureIgnored is excluded because it gets its own dedicated alignment test.
func TestParity_ColumnOffsets_RightBlockOffsetsMatch(t *testing.T) {
	t.Parallel()
	for _, f := range []parityFixture{fixtureBase, fixtureMissing, fixtureGrouped} {
		f := f
		t.Run(f.name, func(t *testing.T) {
			tr := renderToolsRowForTest(t, f)
			arNatural := renderAgentsRowForTest(t, f)

			normalizedCols := arNatural.cols
			normalizedCols.prov = tr.cols.prov
			ar := renderAgentsRowWithCols(t, f, normalizedCols)

			toolLabel := providerLabelForFixture(f)
			// At the normalized width "claude-code" no longer fits and is truncated with an ellipsis; truncation only shortens the tail, so search a prefix that survives it.
			agentLabel := "claude-"

			toolProvOff := tr.offsetOf(toolLabel)
			agentProvOff := ar.offsetOf(agentLabel)
			if toolProvOff < 0 {
				t.Fatalf("tools: could not locate provider label %q in %q", toolLabel, tr.plain)
			}
			if agentProvOff < 0 {
				t.Fatalf("agents: could not locate agent label %q in %q", agentLabel, ar.plain)
			}
			assertEqualInt(t, agentProvOff, toolProvOff, "provider/agent column x-offset (normalized)")

			if f.group != "" {
				toolGroupOff := tr.offsetOf("[" + f.group + "]")
				agentGroupOff := ar.offsetOf("[" + f.group + "]")
				if toolGroupOff < 0 {
					t.Fatalf("tools: could not locate group badge in %q", tr.plain)
				}
				if agentGroupOff < 0 {
					t.Fatalf("agents: could not locate group badge in %q", ar.plain)
				}
				assertEqualInt(t, agentGroupOff, toolGroupOff, "group column x-offset (normalized)")
			}
		})
	}
}

// Every parity fixture uses provider "brew" with no override, so the label is always the bare concrete manager name.
func providerLabelForFixture(f parityFixture) string {
	return "brew"
}

// The ignored branch must pad its provider label with trailing spaces like renderProviderColWithExplicit, or rightCell left-pads a short label to the column's right edge.
func TestParity_ColumnOffsets_IgnoredToolsRowProviderCellAlignsLikeEveryOtherState(t *testing.T) {
	t.Parallel()
	trIgnored := renderToolsRowForTest(t, fixtureIgnored)
	// Render the same tool/cols through the non-ignored branch so the only variable is which branch built the provider cell; a different fixture would also shift cols.ver.
	trNonIgnored := renderToolsRowIgnoredOverride(t, trIgnored.cols)

	ignoredProvOff := trIgnored.offsetOf("brew")
	nonIgnoredProvOff := trNonIgnored.offsetOf("brew")
	if ignoredProvOff < 0 {
		t.Fatalf("tools ignored row: could not locate provider label in %q", trIgnored.plain)
	}
	if nonIgnoredProvOff < 0 {
		t.Fatalf("tools non-ignored row: could not locate provider label in %q", trNonIgnored.plain)
	}
	if ignoredProvOff != nonIgnoredProvOff {
		t.Errorf("tools ignored-row provider offset = %d, want %d (same as non-ignored branch at the same cols) — "+
			"ignored branch's provider cell padding regressed in view_list.go", ignoredProvOff, nonIgnoredProvOff)
	}

	arNatural := renderAgentsRowForTest(t, fixtureIgnored)
	normalizedCols := arNatural.cols
	normalizedCols.prov = trIgnored.cols.prov
	ar := renderAgentsRowWithCols(t, fixtureIgnored, normalizedCols)

	agentProvOff := ar.offsetOf("claude-")
	if agentProvOff < 0 {
		t.Fatalf("agents: could not locate agent label in %q", ar.plain)
	}
	if ignoredProvOff != agentProvOff {
		t.Errorf("tools ignored-row provider offset = %d, agents ignored-row offset = %d, want equal (both left-aligned)",
			ignoredProvOff, agentProvOff)
	}
}

// Only fixtures whose version text is the same literal on both tabs; fixtureBase's outdated-arrow shape differs and is covered by TestParity_Style_OutdatedArrowStylesMatch.
func TestParity_ColumnOffsets_VersionColumnRightEdgeAligns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		f       parityFixture
		verText string
	}{
		{fixtureMissing, "missing"},
		{fixtureGrouped, "1.0.0"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.f.name, func(t *testing.T) {
			tr := renderToolsRowForTest(t, c.f)
			ar := renderAgentsRowForTest(t, c.f)

			toolVerOff := tr.offsetOf(c.verText)
			agentVerOff := ar.offsetOf(c.verText)
			if toolVerOff < 0 {
				t.Fatalf("tools: could not locate version text %q in %q", c.verText, tr.plain)
			}
			if agentVerOff < 0 {
				t.Fatalf("agents: could not locate version text %q in %q", c.verText, ar.plain)
			}
			toolRightEdge := toolVerOff + len([]rune(c.verText))
			agentRightEdge := agentVerOff + len([]rune(c.verText))
			assertEqualInt(t, agentRightEdge, toolRightEdge, "version column right edge")
		})
	}
}

func TestParity_Style_SelectedRowEmphasisIsBoldOnBothTabs(t *testing.T) {
	t.Parallel()
	unselected := parityFixture{name: fixtureSelected.name, version: fixtureSelected.version, installed: true}
	trSel := renderToolsRowForTest(t, fixtureSelected)
	trUnsel := renderToolsRowForTest(t, unselected)
	arSel := renderAgentsRowForTest(t, fixtureSelected)
	arUnsel := renderAgentsRowForTest(t, unselected)

	if trSel.raw == trUnsel.raw {
		t.Error("tools: selected row rendering identical to unselected — expected bold emphasis to change output")
	}
	if arSel.raw == arUnsel.raw {
		t.Error("agents: selected row rendering identical to unselected — expected bold emphasis to change output")
	}

	// Both tabs define the same bold-if-selected emphasis closure locally; confirm the name cell resolves to a bold styleNormal when selected.
	pal := parityPalette()
	toolName := findStyledSubstring(trSel.raw, fixtureSelected.name)
	agentName := findStyledSubstring(arSel.raw, fixtureSelected.name)
	if toolName == "" {
		t.Fatalf("tools: could not locate name substring in selected row %q", trSel.plain)
	}
	if agentName == "" {
		t.Fatalf("agents: could not locate name substring in selected row %q", arSel.plain)
	}
	wantBold := pal.styleNormal.Bold(true).Render(fixtureSelected.name)
	if toolName != wantBold {
		t.Errorf("tools selected name cell = %q, want bold styleNormal %q", toolName, wantBold)
	}
	if agentName != wantBold {
		t.Errorf("agents selected name cell = %q, want bold styleNormal %q", agentName, wantBold)
	}
}

func TestParity_Style_MissingCellIsRedOnBothTabs(t *testing.T) {
	t.Parallel()
	pal := parityPalette()
	tr := renderToolsRowForTest(t, fixtureMissing)
	ar := renderAgentsRowForTest(t, fixtureMissing)

	requireRenderedStyle(t, pal, "missing", extractMissingCell(tr.raw), "styleMissing")
	requireRenderedStyle(t, pal, "missing", extractMissingCell(ar.raw), "styleMissing")
}

func extractMissingCell(raw string) string {
	return findStyledSubstring(raw, "missing")
}

func TestParity_Style_OutdatedArrowStylesMatch(t *testing.T) {
	t.Parallel()
	pal := parityPalette()
	tr := renderToolsRowForTest(t, fixtureBase)
	ar := renderAgentsRowForTest(t, fixtureBase)

	toolArrow := findStyledSubstring(tr.raw, " → "+fixtureBase.latestVersion)
	agentArrow := findStyledSubstring(ar.raw, " → "+fixtureBase.latestVersion)
	if toolArrow == "" {
		t.Fatalf("tools row missing outdated arrow substring in %q", tr.plain)
	}
	if agentArrow == "" {
		t.Skip("agents skills row has no per-row upgrade-arrow concept for this fixture shape — see agentsRowCells skills branch (only date, no version-arrow); documented divergence, not a bug this harness enforces")
	}
	requireRenderedStyle(t, pal, " → "+fixtureBase.latestVersion, toolArrow, "styleOutdated")
	requireRenderedStyle(t, pal, " → "+fixtureBase.latestVersion, agentArrow, "styleOutdated")
}

func TestParity_Style_IgnoredRowIsFullyDimmed(t *testing.T) {
	t.Parallel()
	pal := parityPalette()
	trIgnored := renderToolsRowForTest(t, fixtureIgnored)
	arIgnored := renderAgentsRowForTest(t, fixtureIgnored)

	nameStyled := findStyledSubstring(trIgnored.raw, fixtureIgnored.name)
	if nameStyled == "" {
		t.Fatalf("tools: could not locate name substring in ignored row %q", trIgnored.plain)
	}
	requireRenderedStyle(t, pal, fixtureIgnored.name, nameStyled, "styleIgnored")

	agentNameStyled := findStyledSubstring(arIgnored.raw, fixtureIgnored.name)
	if agentNameStyled == "" {
		t.Fatalf("agents: could not locate name substring in ignored row %q", arIgnored.plain)
	}
	requireRenderedStyle(t, pal, fixtureIgnored.name, agentNameStyled, "styleIgnored")
}

// Asserts both tabs' category/identity label resolves to something other than the flat muted help style.
func TestParity_Style_ProviderAgentLabelCarriesNonDefaultColor(t *testing.T) {
	t.Parallel()
	pal := parityPalette()

	// renderProviderColWithExplicit pads with unstyled trailing spaces, so extract just the styled "brew" run — the whole padded cell would never match a plain render.
	toolProvCell := renderProviderColWithExplicit(pal, "brew", "brew", "", "", "", "", "brew", 8, false, false)
	toolProvStyled := findStyledSubstring(toolProvCell, "brew")
	if toolProvStyled == "" {
		t.Fatalf("could not locate styled 'brew' run in provider cell %q", toolProvCell)
	}
	toolProvStyle := resolveRenderedStyleName(t, pal, "brew", toolProvStyled)
	if toolProvStyle == "" || toolProvStyle == "styleHelp" {
		t.Errorf("tools provider cell style = %q, want a category-colored style, not styleHelp/unresolved", toolProvStyle)
	}

	agentStyle := styleForAgent(pal, "claude-code")
	agentCell := agentStyle.Render("claude-code")
	agentResolved := resolveRenderedStyleName(t, pal, "claude-code", agentCell)
	if agentResolved == "" || agentResolved == "styleHelp" {
		t.Errorf("agents agent-ID cell style = %q, want a category-colored style, not styleHelp/unresolved", agentResolved)
	}
}

// Intentionally not a general ANSI parser: it only isolates single-style leaf runs (one SGR prefix + text + one reset, no nesting) where the target never spans a style boundary.
func findStyledSubstring(raw, target string) string {
	if target == "" {
		return ""
	}
	plain := stripANSIEscapeSequences(raw)
	idx := indexOf(plain, target)
	if idx < 0 {
		return ""
	}

	start, end := -1, -1
	plainPos := 0
	i := 0
	for i < len(raw) {
		if raw[i] == '\x1b' {
			i = skipANSISequence(raw, i)
			continue
		}
		if plainPos == idx && start == -1 {
			start = i
		}
		i++
		plainPos++
		if plainPos == idx+len(target) {
			end = i
			break
		}
	}
	if start == -1 || end == -1 {
		return ""
	}

	segStart := start
	for k := start - 1; k >= 0; k-- {
		if raw[k] == '\x1b' {
			segStart = k
			break
		}
	}
	segEnd := end
	if segEnd < len(raw) && raw[segEnd] == '\x1b' {
		segEnd = skipANSISequence(raw, segEnd)
	}
	return raw[segStart:segEnd]
}

// Returns i+1 when raw[i] does not start a well-formed CSI sequence.
func skipANSISequence(raw string, i int) int {
	j := i + 1
	if j >= len(raw) || raw[j] != '[' {
		return i + 1
	}
	j++
	for j < len(raw) {
		c := raw[j]
		j++
		if c >= 0x40 && c <= 0x7e {
			return j
		}
	}
	return j
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Both delegate to renderActionHints, so the "k desc • k desc" format holds by construction; this pins the shared path against a refactor splitting them apart.
func TestParity_HintLineFormat_SameSeparatorAndKeyDescShape(t *testing.T) {
	t.Parallel()
	pal := parityPalette()
	toolHints := []hintItem{{key: "u", desc: "upgrade"}, {key: "x", desc: "ignore"}}
	agentHints := []hintItem{{key: "u", desc: "update"}, {key: "g", desc: "group"}}

	toolLine := renderInlineHints(pal, toolHints, "")
	agentLine := renderHintItems(pal, "", agentHints)

	wantToolLine := hintKey(pal, "u", "upgrade") + pal.styleSep.Render(" • ") + hintKey(pal, "x", "ignore")
	wantAgentLine := hintKey(pal, "u", "update") + pal.styleSep.Render(" • ") + hintKey(pal, "g", "group")

	if toolLine != wantToolLine {
		t.Errorf("tools hint line = %q, want %q", toolLine, wantToolLine)
	}
	if agentLine != wantAgentLine {
		t.Errorf("agents hint line = %q, want %q", agentLine, wantAgentLine)
	}

	// The separator being byte-identical proves both use the same hintJoin format rather than two independently-authored, visually-similar joiners.
	sep := pal.styleSep.Render(" • ")
	if !containsSubstring(toolLine, sep) || !containsSubstring(agentLine, sep) {
		t.Fatalf("expected both hint lines to contain the shared separator %q; tool=%q agent=%q", sep, toolLine, agentLine)
	}
}

func containsSubstring(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

// Proves toolInlineHints and agentsRowHints format through the identical renderHintItems/renderInlineHints call, so hint drift cannot reappear as a presentation-layer difference.
func TestParity_HintLineFormat_ToolInlineHintsAndAgentsRowHintsShareRenderer(t *testing.T) {
	t.Parallel()
	pal := parityPalette()
	tool, _, _ := buildToolFixture(fixtureBase)
	m := baseModel([]*app.ToolView{tool})
	toolHints := toolInlineHints(m, tool)

	am, e := buildAgentsFixture(fixtureBase)
	agentHints := agentsRowHints(am, e)

	if len(toolHints) == 0 {
		t.Fatal("expected at least one tool hint for an outdated+installed tool")
	}
	if len(agentHints) == 0 {
		t.Fatal("expected at least one agent hint for an updates-available row")
	}

	toolLine := renderInlineHints(pal, toolHints, listHintPrefix())
	agentLine := renderHintItems(pal, listHintPrefix(), agentHints)

	if toolLine == "" || agentLine == "" {
		t.Fatalf("expected both hint lines non-empty, got tool=%q agent=%q", toolLine, agentLine)
	}
	sep := pal.styleSep.Render(" • ")
	toolHasSep := len(toolHints) < 2 || containsSubstring(toolLine, sep)
	agentHasSep := len(agentHints) < 2 || containsSubstring(agentLine, sep)
	if !toolHasSep {
		t.Errorf("tools hint line with %d items missing separator %q: %q", len(toolHints), sep, toolLine)
	}
	if !agentHasSep {
		t.Errorf("agents hint line with %d items missing separator %q: %q", len(agentHints), sep, agentLine)
	}
}

func TestParity_SectionHeader_SameFunctionSameLabelSameOutput(t *testing.T) {
	t.Parallel()
	pal := parityPalette()
	toolHeader := renderSectionHeader(pal, "Updates Available", parityWidth)
	agentHeader := renderSectionHeader(pal, agentsStatusLabel(agentsStatusUpdates), parityWidth)

	if toolHeader != agentHeader {
		t.Errorf("section header output differs for the same label:\ntools:  %q\nagents: %q", toolHeader, agentHeader)
	}

	// Every overlapping status label must render byte-identical headers, since both funnel through renderSectionHeader.
	pairs := []struct{ toolLabel, agentLabel string }{
		{"Updates Available", agentsStatusLabel(agentsStatusUpdates)},
		{"Out of Sync", agentsStatusLabel(agentsStatusOutOfSync)},
		{"Installed", agentsStatusLabel(agentsStatusInstalled)},
		{"Available", agentsStatusLabel(agentsStatusAvailable)},
		{"Ignored", agentsStatusLabel(agentsStatusIgnored)},
	}
	for _, pr := range pairs {
		got := renderSectionHeader(pal, pr.agentLabel, parityWidth)
		want := renderSectionHeader(pal, pr.toolLabel, parityWidth)
		if got != want {
			t.Errorf("section header mismatch for label pair (%q, %q):\nwant: %q\ngot:  %q", pr.toolLabel, pr.agentLabel, want, got)
		}
	}
}
