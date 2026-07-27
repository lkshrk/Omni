package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

func (a *App) skillService() (*agent.Skills, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home dir: %w", err)
	}
	var state agent.LocalState
	if db := a.readDB(); db != nil {
		state = db
	}
	return agent.NewSkills(
		a.agentRegistry(),
		home,
		agent.DefaultSkillsDataDir(home),
		a.httpClient,
		state,
		filepath.Dir(a.ConfigPath),
	), nil
}

// Adoption is explicit to import; restore and update leave legacy-managed directories in place.
func (a *App) adoptLegacySkills(ctx context.Context, cfg *config.RootConfig, pkgs []config.SkillPackage) []string {
	if len(pkgs) == 0 {
		return nil
	}
	service, err := a.skillService()
	if err != nil {
		return []string{err.Error()}
	}
	use := a.effectiveSettings(cfg).AgentsUse
	if use == nil {
		use = a.EnabledAgentIDs(cfg)
	}
	var warnings []string
	for _, pkg := range pkgs {
		agents := effectiveSkillAgents(use, pkg)
		if len(agents) == 0 {
			continue
		}
		_, adoptWarnings, err := service.Adopt(ctx, pkg, agents)
		warnings = append(warnings, adoptWarnings...)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("adopting %s: %v", pkg.Source, err))
		}
	}
	return warnings
}

func (a *App) allAgentTargetIDs() []string {
	targets := a.agentRegistry().All()
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	return ids
}
