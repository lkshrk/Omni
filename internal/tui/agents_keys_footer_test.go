package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func agentsKeysBaseModel() Model {
	m := baseModel(nil)
	m.mode = viewSkills
	m.agentsEnabled = true
	m.skillsEnabled = true
	m.mcpEnabled = true
	m.pluginsEnabled = true
	m.enabledAgents = []string{"claude", "cursor"}
	m.skillsLoaded = true
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true, Agents: []string{"claude", "cursor"}},
	}
	return m
}

func TestAgentsKeys_BracketMovesFirstBar_AllToSkills(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipAll

	got := drive(m, pressRune(']'))
	if got.skillTypeIdx != agentsChipSkills {
		t.Fatalf("skillTypeIdx after ] = %d, want %d (skills)", got.skillTypeIdx, agentsChipSkills)
	}
}

func TestAgentsKeys_BracketMovesFirstBar_Reverse(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipSkills

	got := drive(m, pressRune('['))
	if got.skillTypeIdx != agentsChipAll {
		t.Fatalf("skillTypeIdx after [ = %d, want %d (all)", got.skillTypeIdx, agentsChipAll)
	}
}

func TestAgentsKeys_CurlyCyclesAgentFilter_OnAllChip(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipAll
	m.skillAgentIdx = 0

	got := drive(m, pressRune('}'))
	if got.skillAgentIdx != 1 {
		t.Fatalf("skillAgentIdx after } = %d, want 1", got.skillAgentIdx)
	}
	if got.skillTypeIdx != agentsChipAll {
		t.Fatalf("skillTypeIdx after } changed unexpectedly to %d", got.skillTypeIdx)
	}

	got = drive(got, pressRune('{'))
	if got.skillAgentIdx != 0 {
		t.Fatalf("skillAgentIdx after { = %d, want 0", got.skillAgentIdx)
	}
}

func TestAgentsKeys_CurlyCyclesAgentFilter_OnSkillsChip(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipSkills
	m.skillAgentIdx = 0

	got := drive(m, pressRune('}'))
	if got.skillAgentIdx != 1 {
		t.Fatalf("skillAgentIdx after } = %d, want 1", got.skillAgentIdx)
	}

	got = drive(got, pressRune('{'))
	if got.skillAgentIdx != 0 {
		t.Fatalf("skillAgentIdx after { = %d, want 0", got.skillAgentIdx)
	}
}

func TestAgentsKeys_BracketMovesFirstBar_OnSkillsChip(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipSkills

	got := drive(m, pressRune(']'))
	if got.skillTypeIdx != agentsChipMcp {
		t.Fatalf("skillTypeIdx after ] = %d, want %d (mcp)", got.skillTypeIdx, agentsChipMcp)
	}

	got = drive(got, pressRune('['))
	if got.skillTypeIdx != agentsChipSkills {
		t.Fatalf("skillTypeIdx after [ = %d, want %d (skills)", got.skillTypeIdx, agentsChipSkills)
	}
}

func TestAgentsKeys_CurlyDoesNotMoveChip(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipSkills

	got := drive(m, pressRune('}'))
	if got.skillTypeIdx != agentsChipSkills {
		t.Fatalf("skillTypeIdx after } = %d, want unchanged %d (skills)", got.skillTypeIdx, agentsChipSkills)
	}
}

func TestAgentsKeys_BracketAtBoundsClamps(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.skillTypeIdx = agentsChipMarketplace

	got := drive(m, pressRune(']'))
	if got.skillTypeIdx != agentsChipMarketplace {
		t.Fatalf("skillTypeIdx after ] at max = %d, want clamped %d (marketplace)", got.skillTypeIdx, agentsChipMarketplace)
	}
}

func TestAgentsFooter_ShowsSeparateFilterHints(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.width = 120

	bindings := tabShortHelpBindings(&m)
	var got []string
	for _, b := range bindings {
		h := b.Help()
		got = append(got, h.Key+" "+h.Desc)
	}
	joined := strings.Join(got, " · ")

	if !strings.Contains(joined, "[] filter") {
		t.Errorf("footer legend missing %q (type filter binding), got %q", "[] filter", joined)
	}
	if !strings.Contains(joined, "{} agent") {
		t.Errorf("footer legend missing %q (agent filter binding), got %q", "{} agent", joined)
	}
}

func TestAgentsFooter_RenderedStatusBarContainsFilterHint(t *testing.T) {
	t.Parallel()
	m := agentsKeysBaseModel()
	m.width = 120

	out := stripANSIEscapeSequences(renderStatusBar(m))

	if !strings.Contains(out, "[] filter") {
		t.Errorf("rendered footer missing %q, got:\n%s", "[] filter", out)
	}
	if !strings.Contains(out, "{} agent") {
		t.Errorf("rendered footer missing %q, got:\n%s", "{} agent", out)
	}
}
