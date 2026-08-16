package app

import (
	"context"
	"os"

	"github.com/lkshrk/omni/internal/config"
)

func (a *App) doctorAgents(ctx context.Context, result *DoctorResult, cfg *config.RootConfig) {
	_ = ctx
	if !a.AgentsEnabled(cfg) {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusOK, "disabled (agents_disabled)")
		return
	}
	manifest, err := a.globalAPMManifestPath()
	if err != nil {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, err.Error())
		return
	}
	if _, err := lookPath("apm"); err != nil {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "apm executable not found on PATH; run 'omni doctor --fix' to install it")
		return
	}
	if _, err := os.Stat(manifest); err != nil {
		if os.IsNotExist(err) {
			result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "global APM manifest not found: "+manifest)
			return
		}
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, err.Error())
		return
	}
	result.addCheck("agents", "Agent packages (APM)", DoctorStatusOK, "global manifest: "+manifest)
}
