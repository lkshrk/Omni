package actions

import (
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/testflow"
)

func TestFlowCatalogMatchesActionRegistryAndStaticEvidence(t *testing.T) {
	root := filepath.Join("..", "..")
	catalog, err := testflow.Load(filepath.Join(root, "test", "flows.json"))
	if err != nil {
		t.Fatal(err)
	}
	all := All()
	actions := make([]testflow.ActionSurface, 0, len(all))
	for _, action := range all {
		commands := make([]testflow.CLICommandSurface, 0, len(action.CLI))
		for _, binding := range action.CLI {
			commands = append(commands, testflow.CLICommandSurface{Command: binding.Command, RequiredFlags: binding.RequiredFlags})
		}
		actions = append(actions, testflow.ActionSurface{
			ID:          string(action.ID),
			CLI:         len(action.CLI) > 0,
			CLICommands: commands,
			TUI:         action.TUI != nil || action.Palette != nil,
			Mutates:     action.Mutates,
		})
	}
	if err := testflow.Validate(catalog, actions, root); err != nil {
		t.Fatal(err)
	}
	gapCeiling := map[testflow.Level]int{
		testflow.LevelIntegration: 0,
		testflow.LevelCLIBlackBox: 0,
		testflow.LevelTUIBlackBox: 19,
		testflow.LevelParity:      26,
	}
	gaps := make(map[testflow.Level]int)
	for _, flow := range catalog.Flows {
		for _, requirement := range flow.Requirements {
			if requirement.Status == testflow.StatusGap {
				gaps[requirement.Level]++
			}
		}
	}
	for level, ceiling := range gapCeiling {
		if gaps[level] > ceiling {
			t.Errorf("%s flow gaps increased from %d to %d", level, ceiling, gaps[level])
		}
	}
}
