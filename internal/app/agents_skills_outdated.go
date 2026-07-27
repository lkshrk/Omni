package app

import "context"

type SkillOutdatedCheck struct {
	Source   string
	Outdated SkillOutdated
}

// RefreshSkillOutdated — Probe failures are reported as unknown: an unreachable source must never fail the run.
func (a *App) RefreshSkillOutdated(ctx context.Context, force bool) ([]SkillOutdatedCheck, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if !a.SkillsEnabled(cfg) {
		return nil, nil
	}
	service, err := a.skillService()
	if err != nil {
		return nil, err
	}
	resolved := a.resolveSkillPackages(cfg, currentMachineGroupName())
	checks := make([]SkillOutdatedCheck, 0, len(resolved))
	for _, p := range resolved {
		checks = append(checks, SkillOutdatedCheck{
			Source:   p.Source,
			Outdated: skillOutdatedFrom(service.CheckOutdated(ctx, p.SkillPackage, force)),
		})
	}
	return checks, nil
}

// Best-effort: a freshness side effect of a reconcile, not part of its contract.
func (a *App) refreshSkillOutdatedBestEffort(ctx context.Context) {
	// Documented no-op on error, per the rationale above.
	_, _ = a.RefreshSkillOutdated(ctx, false)
}
