package app

import (
	"context"
	"fmt"
	"os"

	"github.com/lkshrk/omni/internal/config"
)

// AgentsEnabled reports whether the agent-skills feature is enabled for this
// host. Enabled by default: only an explicit agents_disabled=true turns it off.
func (a *App) AgentsEnabled(cfg *config.RootConfig) bool {
	return !config.BoolVal(a.effectiveSettings(cfg).AgentsDisabled)
}

func (a *App) requireAgentsEnabled(cfg *config.RootConfig) error {
	if !a.AgentsEnabled(cfg) {
		return fmt.Errorf("agent skills are disabled for this host")
	}
	return nil
}

// SaveAgentsDisabled persists the per-host agents_disabled flag.
func (a *App) SaveAgentsDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.AgentsDisabled = config.BoolPtr(disabled)
		return nil
	})
}

// AgentPickerRow is one selectable agent in the settings picker.
type AgentPickerRow struct {
	ID      string
	Display string
	Enabled bool
}

// AgentPickerRows returns the agents installed on this machine with their
// enabled state from the per-host agents_use list.
func (a *App) AgentPickerRows() ([]AgentPickerRow, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]bool)
	for _, id := range a.effectiveSettings(cfg).AgentsUse {
		enabled[id] = true
	}
	installed := InstalledAgents(home)
	rows := make([]AgentPickerRow, 0, len(installed))
	for _, ag := range installed {
		rows = append(rows, AgentPickerRow{ID: ag.ID, Display: ag.Display, Enabled: enabled[ag.ID]})
	}
	return rows, nil
}

// SaveAgentsUse persists the per-host agents_use list. A non-nil empty slice is
// stored as an explicit empty list (distinct from "inherit global").
func (a *App) SaveAgentsUse(_ context.Context, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.AgentsUse = append([]string{}, ids...)
		return nil
	})
}
