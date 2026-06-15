package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestSkills_MainTabsContainsSkills(t *testing.T) {
	tabs := mainTabs()
	for _, tab := range tabs {
		if tab.mode == viewSkills && tab.label == "Agents" {
			return
		}
	}
	t.Error("mainTabs() does not contain an Agents tab")
}

func TestSkills_ViewBodyEmptyManifest(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	out := m.viewSkillsBody()
	if !strings.Contains(out, "No agent skills tracked yet.") {
		t.Errorf("viewSkillsBody() with empty manifest missing 'No agent skills tracked yet.', got:\n%s", out)
	}
	if !strings.Contains(out, "[i] import") {
		t.Errorf("viewSkillsBody() missing [i] import line, got:\n%s", out)
	}
}

func TestSkills_ViewBodyWithSkills(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRows = []app.SkillRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
		{Name: "review", Source: "github.com/bar/review", Ref: "v2"},
	}
	out := m.viewSkillsBody()
	if !strings.Contains(out, "caveman") {
		t.Errorf("viewSkillsBody() missing skill name 'caveman', got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/foo/caveman") {
		t.Errorf("viewSkillsBody() missing source 'github.com/foo/caveman', got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/bar/review") {
		t.Errorf("viewSkillsBody() missing source 'github.com/bar/review', got:\n%s", out)
	}
	if !strings.Contains(out, "[r] restore") {
		t.Errorf("viewSkillsBody() missing footer, got:\n%s", out)
	}
}

func TestSkills_ViewBodySectionHeaders(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRows = []app.SkillRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Agents: []string{"claude"}, Updated: "2026-06-01", Installed: true},
		{Name: "review", Source: "github.com/bar/review", Installed: false},
	}
	out := m.viewSkillsBody()
	if !strings.Contains(out, "Installed") {
		t.Errorf("viewSkillsBody() missing 'Installed' section header, got:\n%s", out)
	}
	if !strings.Contains(out, "Not Installed") {
		t.Errorf("viewSkillsBody() missing 'Not Installed' section header, got:\n%s", out)
	}
	if !strings.Contains(out, "caveman") {
		t.Errorf("viewSkillsBody() missing skill name 'caveman', got:\n%s", out)
	}
	if !strings.Contains(out, "github.com/foo/caveman") {
		t.Errorf("viewSkillsBody() missing source 'github.com/foo/caveman', got:\n%s", out)
	}
}

func TestSkills_ViewBodyInstalledIcon(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRows = []app.SkillRow{
		{Name: "installed-skill", Source: "gh/x", Installed: true},
		{Name: "missing-skill", Source: "gh/y", Installed: false},
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
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRows = []app.SkillRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
	}
	out := m.viewSkillsBody()
	if !strings.Contains(out, "[u] update") {
		t.Errorf("viewSkillsBody() missing [u] update footer, got:\n%s", out)
	}
}

func TestSkills_ViewBodyError(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsErr = errors.New("manifest not found")
	out := m.viewSkillsBody()
	if !strings.Contains(out, "error: manifest not found") {
		t.Errorf("viewSkillsBody() missing error line, got:\n%s", out)
	}
}

func TestSkills_RestoredMsgPopulatesResult(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	res := app.RestoreSkillsResult{Installed: []string{"caveman", "review"}}
	m = drive(m, skillsRestoredMsg{res: res})

	if m.skillsRunning {
		t.Error("skillsRunning should be false after skillsRestoredMsg")
	}
	if m.skillsResult == nil {
		t.Fatal("skillsResult should be set after skillsRestoredMsg")
	}
	if len(m.skillsResult.Installed) != 2 {
		t.Errorf("skillsResult.Installed = %v, want 2 entries", m.skillsResult.Installed)
	}
}

func TestSkills_RestoredMsgWithErrorSetsErr(t *testing.T) {
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
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	diff := app.ImportDiff{Added: []string{"new-skill"}, Unchanged: []string{"old-skill"}}
	m = drive(m, skillsImportedMsg{diff: diff})

	if m.skillsRunning {
		t.Error("skillsRunning should be false after skillsImportedMsg")
	}
	if m.skillsImport == nil {
		t.Fatal("skillsImport should be set after skillsImportedMsg")
	}
	if len(m.skillsImport.Added) != 1 || m.skillsImport.Added[0] != "new-skill" {
		t.Errorf("skillsImport.Added = %v, want [new-skill]", m.skillsImport.Added)
	}
}

func TestSkills_ImportedMsgWithErrorSetsErr(t *testing.T) {
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
	m := baseModel(nil)
	m.mode = viewSkills

	m = drive(m, pressRune('r'))

	if !m.skillsRunning {
		t.Error("skillsRunning should be true after pressing 'r' in viewSkills")
	}
}

func TestSkills_IKeyStartsImport(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills

	m = drive(m, pressRune('i'))

	if !m.skillsRunning {
		t.Error("skillsRunning should be true after pressing 'i' in viewSkills")
	}
}

func TestSkills_RKeyClearsResult(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	prev := app.RestoreSkillsResult{Installed: []string{"old"}}
	m.skillsResult = &prev

	m = drive(m, pressRune('r'))

	if m.skillsResult != nil {
		t.Error("skillsResult should be cleared when 'r' is pressed")
	}
}

func TestSkills_IKeyClearsImportDiff(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	prev := app.ImportDiff{Added: []string{"x"}}
	m.skillsImport = &prev

	m = drive(m, pressRune('i'))

	if m.skillsImport != nil {
		t.Error("skillsImport should be cleared when 'i' is pressed")
	}
}

func TestSkills_UKeyStartsUpdate(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills

	m = drive(m, pressRune('u'))

	if !m.skillsRunning {
		t.Error("skillsRunning should be true after pressing 'u' in viewSkills")
	}
}

func TestSkills_UpdatedMsgClearsRunning(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillsRunning = true

	m = drive(m, skillsUpdatedMsg{})

	if m.skillsRunning {
		t.Error("skillsRunning should be false after skillsUpdatedMsg")
	}
}

func TestSkills_UpdatedMsgWithErrorSetsErr(t *testing.T) {
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

func TestSkills_DisabledBody(t *testing.T) {
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
	m := baseModel(nil)
	m.agentsEnabled = false

	m = drive(m, agentsToggledMsg{enabled: true})

	if !m.agentsEnabled {
		t.Error("agentsEnabled should be true after agentsToggledMsg{enabled: true}")
	}
}

func TestSkills_AgentsToggledMsgError(t *testing.T) {
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
	meta := settingsRows[settingsRowAgentsEnabled]
	if meta.label != "Agent Skills" {
		t.Errorf("label = %q, want %q", meta.label, "Agent Skills")
	}
	if meta.section != "Agents" {
		t.Errorf("section = %q, want %q", meta.section, "Agents")
	}
}

func TestSkills_BodyNoRedundantTitle(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillsRows = []app.SkillRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
	}

	out := stripANSIEscapeSequences(m.viewSkillsBody())

	if strings.Contains(out, "Agent Skills") {
		t.Errorf("viewSkillsBody() must not contain redundant title 'Agent Skills', got:\n%s", out)
	}
}

func TestSkills_StatusColumn(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillsRows = []app.SkillRow{
		{Name: "skill-present", Source: "github.com/foo/present", Installed: true},
		{Name: "skill-absent", Source: "github.com/foo/absent", Installed: false},
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

	if !strings.Contains(installedRow, "installed") {
		t.Errorf("installed skill row missing status label 'installed': %q", installedRow)
	}
	if !strings.Contains(missingRow, "missing") {
		t.Errorf("not-installed skill row missing status label 'missing': %q", missingRow)
	}
}

func TestSkills_LayoutColumnsAligned(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillsRows = []app.SkillRow{
		{Name: "cv", Source: "github.com/short/src", Installed: true},
		{Name: "a-much-longer-skill-name", Source: "github.com/longer/source-path", Installed: true},
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

	shortRow := findRow("cv")
	longRow := findRow("a-much-longer-skill-name")

	if shortRow == "" {
		t.Fatal("could not find row containing 'cv'")
	}
	if longRow == "" {
		t.Fatal("could not find row containing 'a-much-longer-skill-name'")
	}

	shortSrcIdx := strings.Index(shortRow, "github.com/short/src")
	longSrcIdx := strings.Index(longRow, "github.com/longer/source-path")

	if shortSrcIdx < 0 {
		t.Fatalf("source not found in short row: %q", shortRow)
	}
	if longSrcIdx < 0 {
		t.Fatalf("source not found in long row: %q", longRow)
	}

	if shortSrcIdx != longSrcIdx {
		t.Errorf("source column not aligned: short row source at col %d, long row source at col %d\nshort: %q\nlong:  %q",
			shortSrcIdx, longSrcIdx, shortRow, longRow)
	}
}
