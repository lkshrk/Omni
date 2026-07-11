package app

import (
	"context"
	"fmt"
	"os"
	"sort"

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

func (a *App) SkillsEnabled(cfg *config.RootConfig) bool {
	s := a.effectiveSettings(cfg)
	return !config.BoolVal(s.AgentsDisabled) && !config.BoolVal(s.SkillsDisabled)
}

func (a *App) McpEnabled(cfg *config.RootConfig) bool {
	s := a.effectiveSettings(cfg)
	return !config.BoolVal(s.AgentsDisabled) && !config.BoolVal(s.McpDisabled)
}

func (a *App) PluginsEnabled(cfg *config.RootConfig) bool {
	s := a.effectiveSettings(cfg)
	return !config.BoolVal(s.AgentsDisabled) && !config.BoolVal(s.PluginsDisabled)
}

func (a *App) requireSkillsEnabled(cfg *config.RootConfig) error {
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if config.BoolVal(a.effectiveSettings(cfg).SkillsDisabled) {
		return fmt.Errorf("skills are disabled for this host")
	}
	return nil
}

func (a *App) requireMcpEnabled(cfg *config.RootConfig) error {
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if config.BoolVal(a.effectiveSettings(cfg).McpDisabled) {
		return fmt.Errorf("mcp servers are disabled for this host")
	}
	return nil
}

func (a *App) requirePluginsEnabled(cfg *config.RootConfig) error {
	if err := a.requireAgentsEnabled(cfg); err != nil {
		return err
	}
	if config.BoolVal(a.effectiveSettings(cfg).PluginsDisabled) {
		return fmt.Errorf("plugins are disabled for this host")
	}
	return nil
}

func (a *App) SaveSkillsDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.SkillsDisabled = config.BoolPtr(disabled)
		return nil
	})
}

func (a *App) SaveMcpDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.McpDisabled = config.BoolPtr(disabled)
		return nil
	})
}

func (a *App) SavePluginsDisabled(_ context.Context, disabled bool) error {
	return a.patchCurrentHostSettings(func(hs *config.Settings) error {
		hs.PluginsDisabled = config.BoolPtr(disabled)
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
	installed := InstalledAgents(home)
	use := a.effectiveSettings(cfg).AgentsUse
	enabled := make(map[string]bool)
	if use == nil {
		// nil is the documented sentinel for "all detected agents active".
		for _, ag := range installed {
			enabled[ag.ID] = true
		}
	} else {
		for _, id := range use {
			enabled[id] = true
		}
	}
	rows := make([]AgentPickerRow, 0, len(installed))
	for _, ag := range installed {
		rows = append(rows, AgentPickerRow{ID: ag.ID, Display: ag.Display, Enabled: enabled[ag.ID]})
	}
	return rows, nil
}

// EnabledAgentIDs returns the agent IDs active for this host: every installed
// agent when AgentsUse is nil (the "all detected agents active" sentinel), or
// the intersection of the configured AgentsUse list with InstalledAgents
// otherwise, so a stale AgentsUse entry for an agent no longer installed
// doesn't leak into callers. Always sorted; empty on a home-dir lookup
// failure rather than propagating an error the TUI callers aren't set up to
// handle.
func (a *App) EnabledAgentIDs(cfg *config.RootConfig) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{}
	}
	installed := InstalledAgents(home)
	use := a.effectiveSettings(cfg).AgentsUse
	var ids []string
	if use == nil {
		for _, ag := range installed {
			ids = append(ids, ag.ID)
		}
	} else {
		useSet := make(map[string]bool, len(use))
		for _, id := range use {
			useSet[id] = true
		}
		for _, ag := range installed {
			if useSet[ag.ID] {
				ids = append(ids, ag.ID)
			}
		}
	}
	sort.Strings(ids)
	return ids
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
