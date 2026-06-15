package config_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestEffectiveSettings_AgentsUseInheritedWhenHostNil(t *testing.T) {
	root := &config.RootConfig{
		Settings:     config.Settings{AgentsUse: []string{"claude-code"}},
		HostSettings: map[string]config.Settings{"box": {}}, // AgentsUse == nil
	}
	got := root.EffectiveSettings("box")
	if len(got.AgentsUse) != 1 || got.AgentsUse[0] != "claude-code" {
		t.Errorf("AgentsUse = %+v, want inherited global [claude-code]", got.AgentsUse)
	}
}

func TestEffectiveSettings_HostAgentsUseReplaceGlobal(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		HostSettings: map[string]config.Settings{
			"box": {AgentsUse: []string{"codex", "cursor"}},
		},
	}
	got := root.EffectiveSettings("box")
	if len(got.AgentsUse) != 2 || got.AgentsUse[0] != "codex" || got.AgentsUse[1] != "cursor" {
		t.Errorf("AgentsUse = %+v, want host-replaced [codex cursor]", got.AgentsUse)
	}
}

func TestEffectiveSettings_EmptyHostAgentsUseClearGlobal(t *testing.T) {
	root := &config.RootConfig{
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		HostSettings: map[string]config.Settings{
			"box": {AgentsUse: []string{}},
		},
	}
	got := root.EffectiveSettings("box")
	if got.AgentsUse == nil || len(got.AgentsUse) != 0 {
		t.Errorf("AgentsUse = %+v, want explicit empty list (non-nil) clearing global", got.AgentsUse)
	}
}
