package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lkshrk/omni/internal/app"
)

func TestSkills_MainTabsContainsSkills(t *testing.T) {
	t.Parallel()
	tabs := mainTabs()
	for _, tab := range tabs {
		if tab.mode == viewSkills && tab.label == "Agents" {
			return
		}
	}
	t.Error("mainTabs() does not contain an Agents tab")
}

func TestSkills_ViewBodyEmptyManifest(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillsRowsKnown = true
	out := m.viewSkillsBody()
	if !strings.Contains(out, "No agent skills tracked yet.") {
		t.Errorf("viewSkillsBody() with empty manifest missing 'No agent skills tracked yet.', got:\n%s", out)
	}
	if !strings.Contains(out, "[i] import") {
		t.Errorf("viewSkillsBody() missing [i] import line, got:\n%s", out)
	}
}

func TestSkills_ViewBodyRowsNotYetKnown(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	out := m.viewSkillsBody()
	if strings.Contains(out, "No agent skills tracked yet.") {
		t.Errorf("viewSkillsBody() with rows not yet known must not contain 'No agent skills tracked yet.', got:\n%s", out)
	}
	if strings.Contains(out, "[i] import") {
		t.Errorf("viewSkillsBody() with rows not yet known must not contain '[i] import', got:\n%s", out)
	}
}

func TestSkills_ViewBodyWithSkills(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "github.com/foo/caveman", Source: "github.com/foo/caveman", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "github.com/bar/review", Source: "github.com/bar/review", Ref: "v2", PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
	}
	m.skillsCursor = 0
	out := stripANSIEscapeSequences(m.viewSkillsBody())
	if !strings.Contains(out, "github.com/foo/caveman") {
		t.Errorf("viewSkillsBody() missing row 'github.com/foo/caveman', got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/bar/review") {
		t.Errorf("viewSkillsBody() missing row 'github.com/bar/review', got:\n%s", out)
	}
	// Hints are eligibility-driven via agentsRowHints (an installed row is update-eligible); format is key then desc, no brackets.
	if !strings.Contains(out, "u upgrade") {
		t.Errorf("viewSkillsBody() missing per-row hint 'u upgrade' for selected row, got:\n%s", out)
	}
}

func TestSkills_ViewBodySectionHeaders(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "github.com/foo/caveman", Source: "github.com/foo/caveman", Updated: "2026-06-01", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "github.com/bar/review", Source: "github.com/bar/review", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
	}
	out := m.viewSkillsBody()
	if !strings.Contains(out, "Installed") {
		t.Errorf("viewSkillsBody() missing 'Installed' section header, got:\n%s", out)
	}
	if !strings.Contains(out, "Out of Sync") {
		t.Errorf("viewSkillsBody() missing 'Out of Sync' section header, got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/foo/caveman") {
		t.Errorf("viewSkillsBody() missing row 'github.com/foo/caveman', got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/bar/review") {
		t.Errorf("viewSkillsBody() missing row 'github.com/bar/review', got:\n%s", out)
	}
}

func TestSkills_ViewBodyInstalledIcon(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "installed-skill", Source: "gh/x", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "missing-skill", Source: "gh/y", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
	}
	out := m.viewSkillsBody()
	if !strings.Contains(out, "✓") {
		t.Errorf("viewSkillsBody() missing ✓ for installed row, got:\n%s", out)
	}
	if !strings.Contains(out, "✗") {
		t.Errorf("viewSkillsBody() missing ✗ for non-installed row, got:\n%s", out)
	}
}

func TestSkills_ViewBodyFooterUpdate(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
	}
	m.skillsCursor = 0
	out := stripANSIEscapeSequences(m.viewSkillsBody())
	// Hint format is key then desc, no brackets (e.g. "u upgrade"), under the selected row rather than a static footer.
	if !strings.Contains(out, "u upgrade") {
		t.Errorf("viewSkillsBody() missing per-row hint 'u upgrade' for selected row, got:\n%s", out)
	}
}

func TestSkills_ViewBodyError(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.skillsErr = errors.New("manifest not found")
	out := m.viewSkillsBody()
	if !strings.Contains(out, "error: manifest not found") {
		t.Errorf("viewSkillsBody() missing error line, got:\n%s", out)
	}
}

func TestSkills_ViewBodyAllChipShowsSectionError(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipAll
	m.mcpErr = errors.New("mcp registry unreachable")
	out := m.viewSkillsBody()
	if !strings.Contains(out, "error: mcp registry unreachable") {
		t.Errorf("viewSkillsBody() all chip missing mcp error line, got:\n%s", out)
	}
}

func TestSkills_ViewBodyMcpChipError(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipMcp
	m.mcpErr = errors.New("mcp registry unreachable")
	out := m.viewSkillsBody()
	if !strings.Contains(out, "error: mcp registry unreachable") {
		t.Errorf("viewSkillsBody() mcp chip missing error line, got:\n%s", out)
	}
}

func TestSkills_RestoredMsgPopulatesResult(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	res := app.RestoreSkillsResult{Installed: []string{"caveman", "review"}}
	m = drive(m, skillsRestoredMsg{res: res})

	if !m.skillsRunning {
		t.Error("skillsRunning should stay true after successful skillsRestoredMsg until the manifest reload lands")
	}
	if m.skillsResult == nil {
		t.Fatal("skillsResult should be set after skillsRestoredMsg")
	}
	if len(m.skillsResult.Installed) != 2 {
		t.Errorf("skillsResult.Installed = %v, want 2 entries", m.skillsResult.Installed)
	}

	m = drive(m, skillsManifestLoadedMsg{})
	if m.skillsRunning {
		t.Error("skillsRunning should be false once skillsManifestLoadedMsg lands")
	}
}

func TestSkills_RestoredMsgWithErrorSetsErr(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	m = drive(m, skillsRestoredMsg{err: errors.New("restore failed")})

	if m.skillsRunning {
		t.Error("skillsRunning should be false after error skillsRestoredMsg")
	}
	if m.skillsResult != nil {
		t.Error("skillsResult should remain nil on error")
	}
	if m.skillsErr == nil || m.skillsErr.Error() != "restore failed" {
		t.Errorf("skillsErr = %v, want 'restore failed'", m.skillsErr)
	}
}

func TestSkills_ImportedMsgPopulatesDiff(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	diff := app.ImportDiff{Added: []string{"new-skill"}, Unchanged: []string{"old-skill"}}
	m = drive(m, skillsImportedMsg{diff: diff})

	if !m.skillsRunning {
		t.Error("skillsRunning should stay true after successful skillsImportedMsg until the manifest reload lands")
	}
	if m.skillsImport == nil {
		t.Fatal("skillsImport should be set after skillsImportedMsg")
	}
	if len(m.skillsImport.Added) != 1 || m.skillsImport.Added[0] != "new-skill" {
		t.Errorf("skillsImport.Added = %v, want [new-skill]", m.skillsImport.Added)
	}

	m = drive(m, skillsManifestLoadedMsg{})
	if m.skillsRunning {
		t.Error("skillsRunning should be false once skillsManifestLoadedMsg lands")
	}
}

func TestSkills_ImportedMsgWithErrorSetsErr(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	m = drive(m, skillsImportedMsg{err: errors.New("import failed")})

	if m.skillsRunning {
		t.Error("skillsRunning should be false after error skillsImportedMsg")
	}
	if m.skillsImport != nil {
		t.Error("skillsImport should remain nil on error")
	}
	if m.skillsErr == nil || m.skillsErr.Error() != "import failed" {
		t.Errorf("skillsErr = %v, want 'import failed'", m.skillsErr)
	}
}

func TestSkills_RKeyStartsRestore(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, pressRune('r'))

	if !m.skillsRunning {
		t.Error("skillsRunning should be true after pressing 'r' in viewSkills")
	}
}

func TestSkills_IKeyStartsImport(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, pressRune('i'))

	if !m.skillsRunning {
		t.Error("skillsRunning should be true after pressing 'i' in viewSkills")
	}
}

func TestSkills_RKeyClearsResult(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	prev := app.RestoreSkillsResult{Installed: []string{"old"}}
	m.skillsResult = &prev

	m = drive(m, pressRune('r'))

	if m.skillsResult != nil {
		t.Error("skillsResult should be cleared when 'r' is pressed")
	}
}

func TestSkills_IKeyClearsImportDiff(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	prev := app.ImportDiff{Added: []string{"x"}}
	m.skillsImport = &prev

	m = drive(m, pressRune('i'))

	if m.skillsImport != nil {
		t.Error("skillsImport should be cleared when 'i' is pressed")
	}
}

func TestSkills_UKeyStartsUpdate(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills

	m = drive(m, pressRune('u'))

	if !m.skillsRunning {
		t.Error("skillsRunning should be true after pressing 'u' in viewSkills")
	}
}

func TestSkills_UpdatedMsgClearsRunning(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	m = drive(m, skillsUpdatedMsg{})

	if !m.skillsRunning {
		t.Error("skillsRunning should stay true after successful skillsUpdatedMsg until the manifest reload lands")
	}

	m = drive(m, skillsManifestLoadedMsg{})
	if m.skillsRunning {
		t.Error("skillsRunning should be false once skillsManifestLoadedMsg lands")
	}
}

func TestSkills_UpdatedMsgWithErrorSetsErr(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	m = drive(m, skillsUpdatedMsg{err: errors.New("update failed")})

	if m.skillsRunning {
		t.Error("skillsRunning should be false after error skillsUpdatedMsg")
	}
	if m.skillsErr == nil || m.skillsErr.Error() != "update failed" {
		t.Errorf("skillsErr = %v, want 'update failed'", m.skillsErr)
	}
}

func TestSkills_PartialUpdatedMsgReloadsManifest(t *testing.T) {
	m := agentsAllProgressModel(t, nil, nil, nil)
	m.mode = viewSkills
	m.skillsRunning = true

	newModel, cmd := m.Update(skillsUpdatedMsg{updated: true, err: errors.New("one failed")})
	m = newModel.(Model)

	if m.skillsErr == nil {
		t.Fatal("partial update error was not retained")
	}
	if m.skillsLoaded {
		t.Fatal("partial update did not invalidate skills inventory")
	}
	if cmd == nil {
		t.Fatal("partial update did not queue inventory reload")
	}
}

func TestSkills_DisabledBody(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = false

	out := m.viewSkillsBody()

	if !strings.Contains(out, "disabled") {
		t.Errorf("viewSkillsBody() disabled: missing 'disabled', got:\n%s", out)
	}
	if !strings.Contains(out, "Settings") {
		t.Errorf("viewSkillsBody() disabled: missing 'Settings', got:\n%s", out)
	}
	if strings.Contains(out, "[r] restore") {
		t.Errorf("viewSkillsBody() disabled: must not contain '[r] restore', got:\n%s", out)
	}
}

func TestSkills_DisabledGatesKeys(t *testing.T) {
	t.Parallel()
	for _, key := range []rune{'r', 'i', 'u'} {
		m := baseModel(nil)
		m.mode = viewSkills
		m.agentsEnabled = false

		m = drive(m, pressRune(key))

		if m.skillsRunning {
			t.Errorf("key %q: skillsRunning should stay false when agentsEnabled=false", key)
		}
	}
}

func TestSkills_AgentsToggledMsgEnables(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = false

	m = drive(m, agentsToggledMsg{enabled: true})

	if !m.agentsEnabled {
		t.Error("agentsEnabled should be true after agentsToggledMsg{enabled: true}")
	}
}

func TestSkills_AgentsToggledMsgError(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = false
	want := errors.New("save failed")

	m = drive(m, agentsToggledMsg{err: want})

	if m.skillsErr == nil || m.skillsErr.Error() != want.Error() {
		t.Errorf("skillsErr = %v, want %v", m.skillsErr, want)
	}
	if m.agentsEnabled {
		t.Error("agentsEnabled must remain false when agentsToggledMsg carries an error")
	}
}

func TestSkills_SettingsRowAgentsEnabledMeta(t *testing.T) {
	t.Parallel()
	meta := settingsRows[settingsRowAgentsEnabled]
	if meta.label != "Agent Skills" {
		t.Errorf("label = %q, want %q", meta.label, "Agent Skills")
	}
	if meta.section != "Agents" {
		t.Errorf("section = %q, want %q", meta.section, "Agents")
	}
}

func TestSkills_BodyNoRedundantTitle(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())

	if strings.Contains(out, "Agent Skills") {
		t.Errorf("viewSkillsBody() must not contain redundant title 'Agent Skills', got:\n%s", out)
	}
}

func TestSkills_StatusColumn(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "skill-present", Source: "github.com/foo/present", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "skill-absent", Source: "github.com/foo/absent", Installed: false, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusMissing}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")

	findRow := func(skillName string) string {
		for _, l := range lines {
			if strings.Contains(l, skillName) {
				return l
			}
		}
		return ""
	}

	installedRow := findRow("skill-present")
	missingRow := findRow("skill-absent")

	if installedRow == "" {
		t.Fatal("could not find row containing 'skill-present'")
	}
	if missingRow == "" {
		t.Fatal("could not find row containing 'skill-absent'")
	}

	if !strings.Contains(installedRow, iconInstalled) {
		t.Errorf("installed skill row missing mark %q: %q", iconInstalled, installedRow)
	}
	if !strings.Contains(missingRow, iconMissing) {
		t.Errorf("not-installed skill row missing mark %q: %q", iconMissing, missingRow)
	}

	installedIdx := strings.Index(out, "Installed")
	outOfSyncIdx := strings.Index(out, "Out of Sync")
	installedRowIdx := strings.Index(out, "skill-present")
	missingRowIdx := strings.Index(out, "skill-absent")
	if outOfSyncIdx < 0 || installedIdx < 0 {
		t.Fatalf("expected both Out of Sync and Installed section headers in:\n%s", out)
	}
	if missingRowIdx <= outOfSyncIdx || missingRowIdx >= installedIdx {
		t.Errorf("skill-absent should render under Out of Sync (between idx %d and %d), got row idx %d", outOfSyncIdx, installedIdx, missingRowIdx)
	}
	if installedRowIdx <= installedIdx {
		t.Errorf("skill-present should render under Installed (idx %d), got row idx %d", installedIdx, installedRowIdx)
	}
}

func TestSkills_LayoutColumnsAligned(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.agentsEnabled = true
	m.width = 120
	m.enabledAgents = []string{"claude"}
	// renderSplitRow right-aligns the whole right-side cell group via alignLR, so the agent column sits at a fixed offset from the line end regardless of name length.
	m.skillsRows = []app.SkillPackageRow{
		{Name: "cv", Source: "github.com/short/src", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
		{Name: "a-much-longer-skill-name", Source: "github.com/longer/source-path", Installed: true, PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled}},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())
	lines := strings.Split(out, "\n")

	findRow := func(name string) string {
		for _, l := range lines {
			if strings.Contains(l, name) {
				return l
			}
		}
		return ""
	}

	shortRow := findRow("cv")
	longRow := findRow("a-much-longer-skill-name")

	if shortRow == "" {
		t.Fatal("could not find row containing 'cv'")
	}
	if longRow == "" {
		t.Fatal("could not find row containing 'a-much-longer-skill-name'")
	}

	shortTypeIdx := strings.Index(shortRow, "claude")
	longTypeIdx := strings.Index(longRow, "claude")

	if shortTypeIdx < 0 {
		t.Fatalf("agent column 'claude' not found in short row: %q", shortRow)
	}
	if longTypeIdx < 0 {
		t.Fatalf("agent column 'claude' not found in long row: %q", longRow)
	}

	shortOffsetFromEnd := len([]rune(shortRow)) - len([]rune(shortRow[:shortTypeIdx]))
	longOffsetFromEnd := len([]rune(longRow)) - len([]rune(longRow[:longTypeIdx]))
	if shortOffsetFromEnd != longOffsetFromEnd {
		t.Errorf("type column not right-aligned: short row offset-from-end %d, long row offset-from-end %d\nshort: %q\nlong:  %q",
			shortOffsetFromEnd, longOffsetFromEnd, shortRow, longRow)
	}
}

func TestFeatureToggles_RowMetaLabels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		row   int
		label string
	}{
		{settingsRowSkillsEnabled, "Skills"},
		{settingsRowMcpEnabled, "MCP Servers"},
		{settingsRowPluginsEnabled, "Plugins"},
	}
	for _, tc := range cases {
		meta := settingsRows[tc.row]
		if meta.label != tc.label {
			t.Errorf("row %d label = %q, want %q", tc.row, meta.label, tc.label)
		}
		if meta.section != "Agents" {
			t.Errorf("row %d section = %q, want %q", tc.row, meta.section, "Agents")
		}
	}
}

func TestFeatureToggles_RenderShowsOnOff(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSettings
	m.width = 120
	m.skillsEnabled = true
	m.mcpEnabled = false
	m.pluginsEnabled = true

	out := stripANSIEscapeSequences(renderSettings(m))
	lines := strings.Split(out, "\n")

	findRowLine := func(label string) string {
		for _, l := range lines {
			if strings.Contains(l, label) && !strings.Contains(l, "Agent "+label) {
				return l
			}
		}
		return ""
	}

	skillsLine := findRowLine("Skills")
	if skillsLine == "" || !strings.Contains(skillsLine, "[ON]") {
		t.Errorf("Skills row = %q, want to contain 'on'", skillsLine)
	}
	mcpLine := findRowLine("MCP Servers")
	if mcpLine == "" || !strings.Contains(mcpLine, "[OFF]") {
		t.Errorf("MCP Servers row = %q, want to contain 'off'", mcpLine)
	}
	pluginsLine := findRowLine("Plugins")
	if pluginsLine == "" || !strings.Contains(pluginsLine, "[ON]") {
		t.Errorf("Plugins row = %q, want to contain 'on'", pluginsLine)
	}
}

func TestFeatureToggles_RenderShowsOffWhenDisabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSettings
	m.width = 120
	m.skillsEnabled = false
	m.mcpEnabled = false
	m.pluginsEnabled = false

	out := stripANSIEscapeSequences(renderSettings(m))
	lines := strings.Split(out, "\n")

	findRowLine := func(label string) string {
		for _, l := range lines {
			if strings.Contains(l, label) && !strings.Contains(l, "Agent "+label) {
				return l
			}
		}
		return ""
	}

	for _, label := range []string{"Skills", "MCP Servers", "Plugins"} {
		line := findRowLine(label)
		if line == "" || !strings.Contains(line, "[OFF]") {
			t.Errorf("%s row = %q, want to contain 'off'", label, line)
		}
	}
}

func TestFeatureToggles_NilAppTogglesReturnNilCmd(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)

	if cmd := m.doToggleSkillsFeature(); cmd != nil {
		t.Error("doToggleSkillsFeature() with nil app should return nil Cmd")
	}
	if cmd := m.doToggleMcpFeature(); cmd != nil {
		t.Error("doToggleMcpFeature() with nil app should return nil Cmd")
	}
	if cmd := m.doTogglePluginsFeature(); cmd != nil {
		t.Error("doTogglePluginsFeature() with nil app should return nil Cmd")
	}
}

func TestFeatureToggles_ConfirmDispatchesNilCmdWithNilApp(t *testing.T) {
	t.Parallel()
	cases := []int{settingsRowSkillsEnabled, settingsRowMcpEnabled, settingsRowPluginsEnabled}
	for _, row := range cases {
		m := baseModel(nil)
		m.mode = viewSettings
		m.settingsCursor = row

		var tm tea.Model = m
		tm, cmd := tm.Update(pressEnter())
		_ = tm

		if cmd != nil {
			t.Errorf("row %d: expected nil Cmd (batch of nil cmds) when confirming toggle with nil app", row)
		}
	}
}

func TestFeatureToggles_SkillsReloadsWhenAgentsEnabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = true
	m.skillsLoaded = true

	got := drive(m, skillsFeatureToggledMsg{enabled: true})

	if got.skillsLoaded {
		t.Error("skillsLoaded should be reset to false to trigger reload when agentsEnabled=true")
	}
	if !got.skillsEnabled {
		t.Error("skillsEnabled should be true after skillsFeatureToggledMsg{enabled: true}")
	}
}

func TestFeatureToggles_SkillsNoReloadWhenAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = false
	m.skillsLoaded = true

	got := drive(m, skillsFeatureToggledMsg{enabled: true})

	if !got.skillsLoaded {
		t.Error("skillsLoaded should remain true (no reload) when agentsEnabled=false")
	}
	if !got.skillsEnabled {
		t.Error("skillsEnabled should still be true after skillsFeatureToggledMsg{enabled: true}")
	}
}

func TestFeatureToggles_McpReloadsWhenAgentsEnabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = true
	m.mcpLoaded = true
	m.mcpRunning = false

	got := drive(m, mcpFeatureToggledMsg{enabled: true})

	if got.mcpLoaded {
		t.Error("mcpLoaded should be reset to false when agentsEnabled=true")
	}
	if !got.mcpRunning {
		t.Error("mcpRunning should be true to signal reload in progress")
	}
	if !got.mcpEnabled {
		t.Error("mcpEnabled should be true after mcpFeatureToggledMsg{enabled: true}")
	}
}

func TestFeatureToggles_McpNoReloadWhenAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = false
	m.mcpLoaded = true
	m.mcpRunning = false

	got := drive(m, mcpFeatureToggledMsg{enabled: true})

	if !got.mcpLoaded {
		t.Error("mcpLoaded should remain true (no reload) when agentsEnabled=false")
	}
	if got.mcpRunning {
		t.Error("mcpRunning should remain false when agentsEnabled=false")
	}
	if !got.mcpEnabled {
		t.Error("mcpEnabled should still be true after mcpFeatureToggledMsg{enabled: true}")
	}
}

func TestFeatureToggles_PluginsReloadsWhenAgentsEnabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = true
	m.pluginLoaded = true
	m.pluginRunning = false

	got := drive(m, pluginsFeatureToggledMsg{enabled: true})

	if got.pluginLoaded {
		t.Error("pluginLoaded should be reset to false when agentsEnabled=true")
	}
	if !got.pluginRunning {
		t.Error("pluginRunning should be true to signal reload in progress")
	}
	if !got.pluginsEnabled {
		t.Error("pluginsEnabled should be true after pluginsFeatureToggledMsg{enabled: true}")
	}
}

func TestFeatureToggles_PluginsNoReloadWhenAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.agentsEnabled = false
	m.pluginLoaded = true
	m.pluginRunning = false

	got := drive(m, pluginsFeatureToggledMsg{enabled: true})

	if !got.pluginLoaded {
		t.Error("pluginLoaded should remain true (no reload) when agentsEnabled=false")
	}
	if got.pluginRunning {
		t.Error("pluginRunning should remain false when agentsEnabled=false")
	}
	if !got.pluginsEnabled {
		t.Error("pluginsEnabled should still be true after pluginsFeatureToggledMsg{enabled: true}")
	}
}

func TestFeatureToggles_SkillsErrorPreservesState(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.skillsEnabled = false
	want := errors.New("save failed")

	got := drive(m, skillsFeatureToggledMsg{enabled: true, err: want})

	if got.skillsEnabled {
		t.Error("skillsEnabled must remain false when skillsFeatureToggledMsg carries an error")
	}
	if got.skillsErr == nil || got.skillsErr.Error() != want.Error() {
		t.Errorf("skillsErr = %v, want %v", got.skillsErr, want)
	}
}

func TestFeatureToggles_McpErrorPreservesState(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mcpEnabled = false
	want := errors.New("save failed")

	got := drive(m, mcpFeatureToggledMsg{enabled: true, err: want})

	if got.mcpEnabled {
		t.Error("mcpEnabled must remain false when mcpFeatureToggledMsg carries an error")
	}
	if got.mcpErr == nil || got.mcpErr.Error() != want.Error() {
		t.Errorf("mcpErr = %v, want %v", got.mcpErr, want)
	}
}

func TestFeatureToggles_PluginsErrorPreservesState(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.pluginsEnabled = false
	want := errors.New("save failed")

	got := drive(m, pluginsFeatureToggledMsg{enabled: true, err: want})

	if got.pluginsEnabled {
		t.Error("pluginsEnabled must remain false when pluginsFeatureToggledMsg carries an error")
	}
	if got.pluginErr == nil || got.pluginErr.Error() != want.Error() {
		t.Errorf("pluginErr = %v, want %v", got.pluginErr, want)
	}
}

// helpPopupTitle must return "Agents Help" for viewSkills and render the agents Row/Bulk groups, not Tools-only actions like "pin provider".
func TestHelpPopup_AgentsTab_TitleAndActions(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true

	if got := helpPopupTitle(m); got != "Agents Help" {
		t.Errorf("helpPopupTitle = %q, want %q", got, "Agents Help")
	}

	help := stripANSIEscapeSequences(renderHelpPopupWithWidth(m, helpPopupContentWidth(m)))
	for _, want := range []string{"install", "upgrade all", "sync all", "refresh"} {
		if !strings.Contains(help, want) {
			t.Errorf("agents help popup missing agents action %q, got:\n%s", want, help)
		}
	}
	if strings.Contains(help, "pin provider") {
		t.Errorf("agents help popup must not contain Tools-only action %q, got:\n%s", "pin provider", help)
	}
}

func TestDoUninstallSkillPackage_NilAppReturnsNil(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.app = nil
	if cmd := m.doUninstallSkillPackage("github.com/foo/pkg"); cmd != nil {
		t.Error("doUninstallSkillPackage with nil app should return nil cmd")
	}
}

func TestDoUninstallSkillPackage_ErrorPropagates(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.app = newBrokenConfigApp(t)

	cmd := m.doUninstallSkillPackage("github.com/foo/pkg")
	if cmd == nil {
		t.Fatal("expected a non-nil cmd with a live app")
	}
	msg := cmd()
	got, ok := msg.(skillRemovedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want skillRemovedMsg", msg)
	}
	if got.err == nil {
		t.Error("expected an error when the config path is unreadable")
	}
}

// The CLI prints ImportDiff.Warnings as "! <warning>"; the TUI must not drop them silently.
func TestSkills_ImportedMsgSurfacesWarnings(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	diff := app.ImportDiff{Added: []string{"new-skill"}, Warnings: []string{"adopting o/r: boom"}}
	m = drive(m, skillsImportedMsg{diff: diff})

	if !m.statusIsErr || !strings.Contains(m.statusMsg, "adopting o/r: boom") {
		t.Errorf("statusMsg = %q (isErr=%v), want the import warning", m.statusMsg, m.statusIsErr)
	}
}

func TestSkills_AddedMsgSurfacesWarnings(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills

	m = drive(m, skillAddedMsg{warning: "legacy CLI-managed install remains"})

	if !m.statusIsErr || !strings.Contains(m.statusMsg, "legacy CLI-managed install remains") {
		t.Errorf("statusMsg = %q (isErr=%v), want the add warning", m.statusMsg, m.statusIsErr)
	}
}
