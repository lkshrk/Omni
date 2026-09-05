package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

// Deadlines, not wall-clock bounds: a killed client holding a pipe open costs a further executor waitDelay.
const (
	agentsDriftDoctorDeadline  = 5 * time.Second
	agentsDriftDoctorPerClient = 2 * time.Second
)

// A native install is an operator's choice, so this check never fails; unreachable clients are reported as unchecked.
func (a *App) doctorAgentsDrift(ctx context.Context, result *DoctorResult, cfg *config.RootConfig) {
	if !a.commandAvailable("claude") && !a.commandAvailable("codex") {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, agentsDriftDoctorDeadline)
	defer cancel()

	report, err := a.agentsDriftReport(ctx, cfg, agentsDriftDoctorPerClient)
	if err != nil {
		addAgentsDriftUnchecked(result, err.Error())
		return
	}
	if ctx.Err() != nil {
		addAgentsDriftUnchecked(result, "timed out")
		return
	}
	if len(report.Incomplete) > 0 {
		addAgentsDriftUnchecked(result, agentsDriftUncheckedReason(report.Incomplete))
		return
	}
	if len(report.Unowned) == 0 {
		result.addCheck("agents-drift", "Native agent drift", DoctorStatusOK,
			fmt.Sprintf("no native agent items outside APM (%d ignored)", len(report.Ignored)))
		return
	}
	details := make([]string, 0, len(report.Unowned)+1)
	for _, disposition := range report.Unowned {
		details = append(details, migrationRow(disposition, ""))
	}
	details = append(details, "run 'omni agents drift' for detail")
	result.addCheck("agents-drift", "Native agent drift", DoctorStatusWarn,
		fmt.Sprintf("%d native agent item(s) outside APM (%d ignored)", len(report.Unowned), len(report.Ignored)), details...)
}

func addAgentsDriftUnchecked(result *DoctorResult, reason string) {
	result.addCheck("agents-drift", "Native agent drift", DoctorStatusOK, "native agent state not checked ("+agentsDriftReasonText(reason)+")")
}

func agentsDriftUncheckedReason(incomplete []nativeClientCoverage) string {
	reasons := make([]string, 0, len(incomplete))
	for _, coverage := range incomplete {
		reasons = append(reasons, coverage.Client+": "+driftCoverageReason(coverage))
	}
	return strings.Join(reasons, "; ")
}

func agentsDriftReasonText(reason string) string {
	reason = strings.TrimSpace(strings.ReplaceAll(reason, "\n", "; "))
	if reason == "" {
		return "no reason reported"
	}
	return reason
}
