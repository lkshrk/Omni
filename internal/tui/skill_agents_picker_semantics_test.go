package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

// The checkbox is selection and the installed state belongs in its own column, so an unselected-but-installed agent renders as two independent facts.
func TestSkillAgentsPicker_SelectionAndInstalledAreSeparateColumns(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.width = 120
	m.skillAgentsPicker = true
	m.skillAgentsSource = "github.com/foo/pkg"
	m.skillAgentsRows = []app.SkillAgentRow{
		{ID: "codex", Display: "Codex", Targeted: true, Installed: true},
		{ID: "cursor", Display: "Cursor", Targeted: false, Installed: true},
		{ID: "claude-code", Display: "Claude Code", Targeted: true, Installed: false},
	}

	out := stripANSIEscapeSequences(renderSkillAgentsPicker(m))
	lines := strings.Split(out, "\n")
	byAgent := map[string]string{}
	for _, line := range lines {
		for _, name := range []string{"Codex", "Cursor", "Claude Code"} {
			if strings.Contains(line, name) {
				byAgent[name] = line
			}
		}
	}

	if !strings.Contains(byAgent["Codex"], "[x]") || !strings.Contains(byAgent["Codex"], "● installed") {
		t.Errorf("selected+installed row = %q", byAgent["Codex"])
	}
	if !strings.Contains(byAgent["Cursor"], "[ ]") || !strings.Contains(byAgent["Cursor"], "● installed") {
		t.Errorf("unselected+installed row = %q", byAgent["Cursor"])
	}
	if !strings.Contains(byAgent["Claude Code"], "[x]") || !strings.Contains(byAgent["Claude Code"], "○ not installed") {
		t.Errorf("selected+not-installed row = %q", byAgent["Claude Code"])
	}
	for _, hint := range []string{"toggle", "save", "cancel"} {
		if !strings.Contains(out, hint) {
			t.Errorf("picker footer is missing the %q affordance:\n%s", hint, out)
		}
	}
}

// Keeps "every enabled agent" expressible: confirming a fully-checked picker must not freeze the package to today's agent list.
func TestSkillAgentsSelection_AllSelectedSavesAsImplicitAll(t *testing.T) {
	t.Parallel()
	all := []app.SkillAgentRow{
		{ID: "codex", Targeted: true},
		{ID: "cursor", Targeted: true},
	}
	if got := skillAgentsSelection(all); got != nil {
		t.Errorf("selection with everything checked = %v, want nil", got)
	}

	partial := []app.SkillAgentRow{
		{ID: "codex", Targeted: true},
		{ID: "cursor", Targeted: false},
	}
	got := skillAgentsSelection(partial)
	if len(got) != 1 || got[0] != "codex" {
		t.Errorf("partial selection = %v, want [codex]", got)
	}
}
