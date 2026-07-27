package tui

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/actions"
)

func agentsPaletteIDs() []actions.ID {
	return []actions.ID{
		actions.AgentsRestore,
		actions.AgentsSyncAll,
		actions.AgentsSkillsImport,
		actions.AgentsSkillsUpdate,
	}
}

func TestPaletteListsAgentsCommandsWhenEnabled(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	for _, cmd := range buildPalette(baseModel(nil)) {
		got[cmd.name] = cmd.desc
	}
	for _, id := range agentsPaletteIDs() {
		pal := actions.MustPalette(id)
		name := paletteCommandName(pal)
		if got[name] != pal.Description {
			t.Fatalf("%s palette entry = %q, want %q for %q", id, got[name], pal.Description, name)
		}
	}
}

// "agents sync" is a prefix of "agents sync-all", so a fully typed name has to win outright or the composed sync becomes unreachable from the palette.
func TestPaletteExactNameWinsOverLongerMatch(t *testing.T) {
	t.Parallel()
	cmds := buildPalette(baseModel(nil))
	syncName := paletteCommandName(actions.MustPalette(actions.AgentsRestore))
	syncAllName := paletteCommandName(actions.MustPalette(actions.AgentsSyncAll))

	got := filterPalette(cmds, syncName)
	if len(got) != 1 || got[0].name != syncName {
		t.Fatalf("filterPalette(%q) = %v, want only %q", syncName, paletteNames(got), syncName)
	}
	if all := filterPalette(cmds, syncAllName); len(all) != 1 || all[0].name != syncAllName {
		t.Fatalf("filterPalette(%q) = %v, want only %q", syncAllName, paletteNames(all), syncAllName)
	}
}

func TestPaletteHidesAgentsCommandsWhenDisabled(t *testing.T) {
	t.Parallel()

	t.Run("agents disabled hides every agents command", func(t *testing.T) {
		t.Parallel()
		m := baseModel(nil)
		m.agentsEnabled = false
		got := paletteNames(buildPalette(m))
		for _, id := range agentsPaletteIDs() {
			if name := paletteCommandName(actions.MustPalette(id)); got[name] {
				t.Fatalf("unexpected palette command %q while agents are disabled", name)
			}
		}
	})

	t.Run("skills disabled keeps the composed runs and drops the skill commands", func(t *testing.T) {
		t.Parallel()
		m := baseModel(nil)
		m.skillsEnabled = false
		got := paletteNames(buildPalette(m))
		for _, id := range []actions.ID{actions.AgentsSkillsImport, actions.AgentsSkillsUpdate} {
			if name := paletteCommandName(actions.MustPalette(id)); got[name] {
				t.Fatalf("unexpected palette command %q while skills are disabled", name)
			}
		}
		for _, id := range []actions.ID{actions.AgentsRestore, actions.AgentsSyncAll} {
			if name := paletteCommandName(actions.MustPalette(id)); !got[name] {
				t.Fatalf("missing palette command %q: mcp and plugins still sync", name)
			}
		}
	})
}

func runPaletteCommand(t *testing.T, m Model, id actions.ID) Model {
	t.Helper()
	got := drive(m, pressRune(':'))
	for _, r := range paletteCommandName(actions.MustPalette(id)) {
		got = drive(got, pressRune(r))
	}
	if len(got.commandSuggestions) != 1 {
		t.Fatalf("suggestions for %s = %d, want exactly the one command", id, len(got.commandSuggestions))
	}
	return drive(got, pressEnter())
}

func TestPaletteAgentsCommandsDispatchToTheAgentsTab(t *testing.T) {
	t.Parallel()

	t.Run("skills import starts the skills operation", func(t *testing.T) {
		t.Parallel()
		got := runPaletteCommand(t, baseModel(nil), actions.AgentsSkillsImport)
		if got.mode != viewSkills {
			t.Fatalf("mode = %v, want viewSkills", got.mode)
		}
		if !got.skillsRunning {
			t.Fatal("skillsRunning = false, want the import operation started")
		}
	})

	t.Run("skills upgrade starts the skills operation", func(t *testing.T) {
		t.Parallel()
		got := runPaletteCommand(t, baseModel(nil), actions.AgentsSkillsUpdate)
		if got.mode != viewSkills || !got.skillsRunning {
			t.Fatalf("mode = %v, skillsRunning = %v, want viewSkills with the upgrade started", got.mode, got.skillsRunning)
		}
	})

	t.Run("sync starts the composed converge run", func(t *testing.T) {
		t.Parallel()
		got := runPaletteCommand(t, baseModel(nil), actions.AgentsRestore)
		if got.mode != viewSkills || !got.skillsRunning {
			t.Fatalf("mode = %v, skillsRunning = %v, want the converge run started on the agents tab",
				got.mode, got.skillsRunning)
		}
		if got.agentsSyncAllConfirm {
			t.Fatal("sync must not arm the sync-all confirmation")
		}
	})

	t.Run("sync-all arms the confirmation instead of claiming", func(t *testing.T) {
		t.Parallel()
		got := runPaletteCommand(t, baseModel(nil), actions.AgentsSyncAll)
		if got.mode != viewSkills {
			t.Fatalf("mode = %v, want viewSkills", got.mode)
		}
		if !got.agentsSyncAllConfirm {
			t.Fatal("agentsSyncAllConfirm = false, want the claim confirmed before it runs")
		}
		// mcp/plugin rows load on any tab switch; only skillsRunning marks the composed run itself as started.
		if got.skillsRunning {
			t.Fatal("sync-all must not start work before the confirmation")
		}
	})
}

// A palette run must not race an agents operation already in flight, matching the agents tab's own global-action guard.
func TestPaletteAgentsCommandRefusesWhileBusy(t *testing.T) {
	t.Parallel()
	m := baseModel(nil)
	m.skillsRunning = true

	got := runPaletteCommand(t, m, actions.AgentsRestore)

	if got.mcpRunning || got.pluginRunning {
		t.Fatal("a second composed run started while an agents operation was in flight")
	}
	if got.statusMsg == "" {
		t.Fatal("statusMsg = empty, want the agents-busy notice")
	}
}

// The keys above the store model only make sense once manifest, store and live are named.
func TestHelpOverlayStatesTheStoreModel(t *testing.T) {
	t.Parallel()
	for _, mode := range []viewMode{viewList, viewDots, viewSkills, viewStatus} {
		m := baseModel(nil)
		m.mode = mode
		help := stripANSIEscapeSequences(renderHelpPopupWithWidth(m, helpPopupContentWidth(m)))
		// The line wraps to the popup width, so compare on collapsed whitespace.
		flat := strings.Join(strings.Fields(help), " ")
		if want := "manifest = what you want · store = what omni holds · live = what agents/machines see"; !strings.Contains(flat, want) {
			t.Errorf("help overlay for mode %v missing the store model, got:\n%s", mode, help)
		}
	}
}
