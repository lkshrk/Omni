package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func driftedSkillsModel(t *testing.T) Model {
	t.Helper()
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillsEnabled = true
	m.skillsLoaded = true
	m.width = 120
	m.cursorHidden = false
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{
			Name: "github.com/a/drifted", Source: "github.com/a/drifted",
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusDrifted},
		},
		{
			Name: "github.com/b/clean", Source: "github.com/b/clean", Installed: true,
			PerAgentStatus: map[string]app.SkillStatus{"claude": app.SkillStatusInstalled},
		},
	}
	return m
}

func TestSkillDriftKeys_ArmAndConfirmUseManaged(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipAll

	armed := drive(m, pressRune('u'))
	if !armed.agentsResolveConfirm || armed.agentsResolveUseLocal {
		t.Fatalf("u on a drifted row = %+v, want an armed use-managed confirm", armed)
	}
	if armed.agentsResolveSource != "github.com/a/drifted" {
		t.Fatalf("armed source = %q", armed.agentsResolveSource)
	}
	if armed.skillsRunning {
		t.Fatal("arming must not start the resolution")
	}

	confirmed := drive(armed, pressRune('u'))
	if confirmed.agentsResolveConfirm || !confirmed.skillsRunning {
		t.Fatalf("second u = %+v, want the resolution dispatched", confirmed)
	}
}

func TestSkillDriftKeys_ArmAndCancelUseLocal(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipAll

	armed := drive(m, pressRune('l'))
	if !armed.agentsResolveConfirm || !armed.agentsResolveUseLocal {
		t.Fatalf("l on a drifted row = %+v, want an armed use-local confirm", armed)
	}
	cancelled := drive(armed, pressRune('u'))
	if cancelled.agentsResolveConfirm || cancelled.skillsRunning {
		t.Fatalf("the other side's key must cancel, got %+v", cancelled)
	}
}

// Only a drifted row spends u/l on drift resolution: elsewhere u still updates and l still switches chips.
func TestSkillDriftKeys_OnlyOnDriftedRows(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipAll
	m.agentsAllCursor = 1

	if got := drive(m, pressRune('u')); got.agentsResolveConfirm {
		t.Fatal("u armed a resolution on a clean row")
	}
	got := drive(m, pressRune('l'))
	if got.agentsResolveConfirm {
		t.Fatal("l armed a resolution on a clean row")
	}
	if got.skillTypeIdx == agentsChipAll {
		t.Fatal("l on a clean row must still switch chips")
	}
}

func TestSkillDriftKeys_SkillsChipRoutesResolve(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipSkills
	// installed rows sort first, so the drifted package is the second row
	m.skillsCursor = 1

	armed := drive(m, pressRune('l'))
	if !armed.agentsResolveConfirm || !armed.agentsResolveUseLocal {
		t.Fatalf("l on the skills chip = %+v, want an armed use-local confirm", armed)
	}
	if armed.skillTypeIdx != agentsChipSkills {
		t.Fatal("l on a drifted row must not switch chips")
	}
}

func TestSkillDriftKeys_HintsAndConfirmCopy(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipAll

	rows := agentsAllRowsList(m)
	var drifted agentsAllRow
	for _, row := range rows {
		if row.mark == agentsMarkDrifted {
			drifted = row
		}
	}
	hints := renderHintItems(m.palette, "", agentsRowHints(m, drifted))
	for _, want := range []string{"use managed", "use local"} {
		if !strings.Contains(stripANSIEscapeSequences(hints), want) {
			t.Fatalf("drifted row hints = %q, want %q", hints, want)
		}
	}
	if strings.Contains(stripANSIEscapeSequences(hints), "update") {
		t.Fatalf("drifted row still offers update: %q", hints)
	}

	armed := drive(m, pressRune('u'))
	confirmHints := stripANSIEscapeSequences(renderHintItems(armed.palette, "", agentsRowHints(armed, drifted)))
	if !strings.Contains(confirmHints, "confirm use managed") {
		t.Fatalf("confirm hints = %q, want the press-again copy", confirmHints)
	}
}

func TestSkillDriftKeys_ArmedResolveExpires(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipAll

	armed := drive(m, pressRune('u'))
	if !armed.agentsResolveConfirm {
		t.Fatal("u should arm the resolve confirm")
	}
	got := drive(armed, confirmTimeoutMsg{gen: armed.confirmGen})
	if got.agentsResolveConfirm || got.agentsResolveSource != "" || got.agentsResolveOpKey != "" {
		t.Fatalf("the armed resolve must expire with the confirmation timeout, got %+v", got)
	}
	if drive(got, pressRune('u')).skillsRunning {
		t.Fatal("a u after the arm expired should re-arm, not resolve")
	}
}

// The global bulk keys run before per-chip dispatch, so they must not fire straight through an armed row resolve.
func TestSkillDriftKeys_GlobalActionsCancelTheArmedResolve(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{'U', 'S', 'R'} {
		m := driftedSkillsModel(t)
		m.skillTypeIdx = agentsChipAll

		armed := drive(m, pressRune('u'))
		got := drive(armed, pressRune(r))
		if got.agentsResolveConfirm {
			t.Errorf("%c left the row resolve armed", r)
		}
		if got.agentsSyncAllConfirm {
			t.Errorf("%c armed sync-all through a live resolve confirm", r)
		}
		if got.skillsRunning || got.mcpRunning || got.pluginRunning || got.marketplaceRunning {
			t.Errorf("%c should only cancel the confirm, not dispatch a bulk op in the same press", r)
		}
	}
}

func TestSkillDriftKeys_ResolveIsRefusedWhileABulkOpRuns(t *testing.T) {
	t.Parallel()
	m := driftedSkillsModel(t)
	m.skillTypeIdx = agentsChipAll

	armed := drive(m, pressRune('u'))
	armed.mcpRunning = true
	got := drive(armed, pressRune('u'))
	if got.skillsRunning {
		t.Fatal("a row resolve must not dispatch alongside an in-flight bulk op")
	}
	if !strings.Contains(got.statusMsg, "agents busy") {
		t.Fatalf("status = %q, want the busy refusal", got.statusMsg)
	}
}
