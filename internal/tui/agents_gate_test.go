package tui

import (
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func agentsSkillsModel(t *testing.T) Model {
	t.Helper()
	m := baseModel(nil)
	m.app = newScanPlanTestApp(t, &scanPlanProvider{name: "brew"})
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipSkills
	m.enabledAgents = []string{"claude"}
	m.skillsRows = []app.SkillPackageRow{
		{Name: "caveman", Source: "github.com/foo/caveman", Installed: true},
	}
	return m
}

// An agents run leaves input live, so a tool refresh can start on top of it. The refresh used to take
// over the shared stream unconditionally, and the gen bump made the agents run's done message get
// dropped — its section spinners then ran for the rest of the session.
func TestAgentsRun_SurvivesAToolRefreshStartingOnTopOfIt(t *testing.T) {
	got := drive(agentsSkillsModel(t), pressRune('U'))
	if !got.agentsOpInFlight() {
		t.Fatal("U should have started an agents run")
	}
	agentsGen := got.progressGen

	got.mode = viewList
	got = drive(got, pressRune('R'))
	if len(got.scanningProviders) == 0 {
		t.Fatal("R should have started the provider scans")
	}
	if got.progressGen != agentsGen {
		t.Fatalf("the refresh took over the agents run's stream: progressGen %d → %d", agentsGen, got.progressGen)
	}

	// A failed run is the case where the done message alone clears the section flags; on success they
	// stay set until the reloaded rows land, which would hide whether the message was accepted at all.
	failed := errors.New("boom")
	got = drive(got, agentsProgressDoneMsg{
		gen:    agentsGen,
		skills: true, skillsErr: failed,
		mcp: true, mcpErr: failed,
		plugin: true, pluginErr: failed,
		marketplace: true, marketplaceErr: failed,
	})
	if got.agentsOpInFlight() {
		t.Fatalf("the agents run was stranded: %+v", got.spinnerActivityState())
	}
}

// Chip keys need the bulk/palette busy guard to prevent concurrent skill-store and lockfile mutations.
func TestAgentsSkillsChip_RejectsASecondRunWhileOneIsInFlight(t *testing.T) {
	for _, k := range []rune{'r', 'i', 'u'} {
		t.Run(string(k), func(t *testing.T) {
			got := drive(agentsSkillsModel(t), pressRune(k))
			if !got.skillsRunning {
				t.Fatalf("%q should have started a skills run", k)
			}

			got = drive(got, pressRune(k))
			if got.statusMsg != agentsBusyStatus {
				t.Fatalf("statusMsg = %q, want the agents-busy status", got.statusMsg)
			}
		})
	}
}

// baseModel must mirror New's plugin inputs or focusing the form nil-panics and invalidates its tests.
func TestAgentsPluginChip_NewFormOpensOnBaseModel(t *testing.T) {
	m := baseModel(nil)
	m.mode = viewSkills
	m.skillTypeIdx = agentsChipPlugin

	got := drive(m, pressRune('n'))

	if !got.pluginFormOpen {
		t.Fatal("n on the plugin chip should open the new-plugin form")
	}
}
