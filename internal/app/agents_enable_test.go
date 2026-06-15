package app

import (
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestAgentsEnabledDefaultAndDisabled(t *testing.T) {
	a := &App{}

	if !a.AgentsEnabled(&config.RootConfig{}) {
		t.Error("agents must be enabled by default (nil agents_disabled)")
	}

	disabled := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)}}
	if a.AgentsEnabled(disabled) {
		t.Error("agents must be disabled when agents_disabled=true")
	}
	if err := a.requireAgentsEnabled(disabled); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("requireAgentsEnabled = %v, want a 'disabled' error", err)
	}

	enabled := &config.RootConfig{Settings: config.Settings{AgentsDisabled: config.BoolPtr(false)}}
	if err := a.requireAgentsEnabled(enabled); err != nil {
		t.Errorf("requireAgentsEnabled(enabled) = %v, want nil", err)
	}
}
