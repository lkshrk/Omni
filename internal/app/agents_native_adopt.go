package app

import (
	"context"
	"fmt"
)

// AgentsNativeAdopt declares one native artifact in the host template. It writes the manifest only:
// deploying it is `omni agents sync`, which owns the workspace lock and the divergence checks.
func (a *App) AgentsNativeAdopt(ctx context.Context, host string, row AgentsNativeRow) (string, error) {
	if row.Ignored {
		return "", fmt.Errorf("%s %s %s is ignored; unignore it before adopting", row.Target, row.Kind, row.Identity)
	}
	if !row.Adoptable {
		reason := row.Reason
		if reason == "" {
			reason = "the migration classifier does not import it"
		}
		return "", fmt.Errorf("%s %s %s cannot be adopted: %s", row.Target, row.Kind, row.Identity, reason)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return "", err
	}
	path, err := agentsAdoptTemplatePath()
	if err != nil {
		return "", err
	}
	template, err := a.inspectAgentsAdoptTemplate(cfg, host, path)
	if err != nil {
		return "", err
	}
	if template.Shape != adoptShapeBare {
		return "", fmt.Errorf("template %s is %s; adopt writes only a template omni owns (%s)", template.Path, template.Shape, template.Action)
	}
	identity, err := inspectAgentsMigrationTemplate(template.Path)
	if err != nil {
		return "", err
	}

	disposition, err := a.nativeDisposition(ctx, row)
	if err != nil {
		return "", err
	}
	candidates, err := agentsAdoptCandidates([]agentDisposition{disposition})
	if err != nil {
		return "", err
	}
	merged, report, err := mergeAgentsManifest(template.Existing, candidates)
	if err != nil {
		return "", err
	}
	if len(report.Rejected) > 0 {
		return "", fmt.Errorf("cannot adopt %s: %s", row.Identity, report.Rejected[0].Reason)
	}
	if len(report.Appended) == 0 && len(report.Unioned) == 0 {
		return fmt.Sprintf("%s %s %s is already declared; nothing to add.\n", row.Target, row.Kind, row.Identity), nil
	}

	current, err := inspectAgentsMigrationTemplate(template.Path)
	if err != nil {
		return "", err
	}
	if current != identity {
		return "", fmt.Errorf("agents template changed while adopting; retry")
	}
	if _, err := writeAgentsMigrationTemplate(template.Path, merged); err != nil {
		return "", fmt.Errorf("write agents template: %w", err)
	}
	return fmt.Sprintf("Declared %s %s %s in %s.\nNext: omni agents sync\n", row.Target, row.Kind, row.Identity, template.Path), nil
}

// nativeDisposition re-reads live state so adoption acts on what is installed now, not on a stale row.
func (a *App) nativeDisposition(ctx context.Context, row AgentsNativeRow) (agentDisposition, error) {
	managed, err := loadAPMManagedIndex()
	if err != nil {
		return agentDisposition{}, err
	}
	inventory := a.gatherNativeAgents(ctx)
	for _, d := range subtractAPMManaged(resolveAgentDispositions(inventory.Observations), managed) {
		if d.Action != agentActionImport && d.Action != agentActionSuppress {
			continue
		}
		obs := d.Observation
		if obs.Target == row.Target && obs.Kind == row.Kind && obs.Identity == row.Identity {
			return d, nil
		}
	}
	return agentDisposition{}, fmt.Errorf("%s %s %s is no longer installed", row.Target, row.Kind, row.Identity)
}
