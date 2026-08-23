package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
)

func (a *App) doctorAgents(ctx context.Context, result *DoctorResult, _ *config.RootConfig) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, err.Error())
		return
	}
	if !a.APMAvailable() {
		result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "apm executable not found on PATH; run 'omni doctor --fix' to install it")
		return
	}
	files := []struct {
		label string
		path  string
	}{
		{label: "manifest", path: filepath.Join(dir, "apm.yml")},
		{label: "lockfile", path: filepath.Join(dir, "apm.lock.yaml")},
	}
	for _, file := range files {
		if _, err := os.ReadFile(file.path); err != nil {
			if os.IsNotExist(err) {
				result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "global APM "+file.label+" not found: "+file.path)
				return
			}
			result.addCheck("agents", "Agent packages (APM)", DoctorStatusWarn, "global APM "+file.label+" is not readable: "+err.Error())
			return
		}
	}
	result.addCheck("agents", "Agent packages (APM)", DoctorStatusOK, "global manifest and lockfile are readable", files[0].path, files[1].path)

	audit, err := a.RunAPM(ctx, "audit", "--ci")
	if err != nil {
		details := []string{err.Error()}
		if output := strings.TrimSpace(audit.Stdout + "\n" + audit.Stderr); output != "" {
			details = append(details, output)
		}
		result.addCheck("apm-audit", "APM audit", DoctorStatusFail, "APM audit failed", details...)
		return
	}
	details := []string{}
	if output := strings.TrimSpace(audit.Stdout + "\n" + audit.Stderr); output != "" {
		details = append(details, output)
	}
	result.addCheck("apm-audit", "APM audit", DoctorStatusOK, "APM audit passed", details...)
}
