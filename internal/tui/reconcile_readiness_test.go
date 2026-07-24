package tui

import (
	"testing"

	"github.com/lkshrk/omni/internal/app"
)

func TestDashboardReconcilePlanSuppressesToolStepsWhileToolStateIsBusy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		busy func(*Model)
	}{
		{name: "manual install", busy: func(m *Model) {
			m.loading = true
			m.rowOpKey = toolKey("fd", "brew")
		}},
		{name: "upgrade", busy: func(m *Model) {
			m.upgradingKeys = map[string]bool{toolKey("fd", "brew"): true}
		}},
		{name: "provider scan", busy: func(m *Model) { m.scanningProviders = map[string]bool{"brew": true} }},
		{name: "provider snapshot", busy: func(m *Model) { m.providerSnapshotRefreshing = true }},
		{name: "outdated check", busy: func(m *Model) { m.outdatedProviders = map[string]bool{"brew": true} }},
		{name: "outdated snapshot", busy: func(m *Model) { m.outdatedSnapshotRefreshing = true }},
		{name: "discovery", busy: func(m *Model) { m.discoveryRefreshing = true }},
		{name: "descriptions", busy: func(m *Model) { m.descRefreshing = true }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := baseModel([]*app.ToolView{{Name: "fd", Provider: "brew", Tracked: true, Installed: false}})
			tc.busy(&m)

			if statusDashboardPlanHasStep(m, app.ReconcileStepSyncTools) {
				t.Fatal("stale sync-tools step remained actionable while tool state was busy")
			}
		})
	}
}
