package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
)

const (
	apmAuditDeployedFilesCheck = "deployed-files-present"
	apmAuditDriftCheck         = "drift"
	apmAuditDeployRootPrefix   = ".agents/"
	apmAuditDetailSample       = 5
)

type apmAuditCheckReport struct {
	Name    string   `json:"name"`
	Passed  bool     `json:"passed"`
	Message string   `json:"message"`
	Details []string `json:"details"`
}

type apmAuditReport struct {
	Passed bool                  `json:"passed"`
	Checks []apmAuditCheckReport `json:"checks"`
}

type apmAuditFindings struct {
	Integrity []string
	Missing   []string
	Drift     []string
}

// apm streams progress lines before the report, and --ci exits non-zero whenever a check fails.
func parseAPMAuditReport(stdout string) (apmAuditReport, error) {
	start := strings.Index(stdout, "{")
	if start < 0 {
		return apmAuditReport{}, errors.New("apm audit produced no JSON report")
	}
	var report apmAuditReport
	if err := json.NewDecoder(strings.NewReader(stdout[start:])).Decode(&report); err != nil {
		return apmAuditReport{}, fmt.Errorf("parse apm audit report: %w", err)
	}
	if len(report.Checks) == 0 {
		return apmAuditReport{}, errors.New("apm audit reported no checks")
	}
	return report, nil
}

// Vanilla apm resolves deployed and drifting paths against the process working directory and knows
// nothing about the .agents deploy root, so both path-based checks are re-evaluated against home.
func evaluateAPMAudit(report apmAuditReport, home string) apmAuditFindings {
	var findings apmAuditFindings
	for _, check := range report.Checks {
		switch check.Name {
		case apmAuditDeployedFilesCheck:
			for _, detail := range check.Details {
				if _, err := os.Lstat(filepath.Join(home, detail)); err != nil {
					findings.Missing = append(findings.Missing, detail)
				}
			}
		case apmAuditDriftCheck:
			for _, detail := range check.Details {
				if !strings.HasPrefix(apmAuditDriftPath(detail), apmAuditDeployRootPrefix) {
					findings.Drift = append(findings.Drift, detail)
				}
			}
		default:
			if !check.Passed {
				findings.Integrity = append(findings.Integrity, check.Name+": "+check.Message)
				findings.Integrity = append(findings.Integrity, check.Details...)
			}
		}
	}
	return findings
}

func apmAuditDriftPath(detail string) string {
	if _, path, found := strings.Cut(detail, ": "); found {
		return path
	}
	return detail
}

func apmAuditSample(label string, paths []string) []string {
	sample := []string{fmt.Sprintf("%s: %d", label, len(paths))}
	for _, path := range paths[:min(len(paths), apmAuditDetailSample)] {
		sample = append(sample, "  "+path)
	}
	if len(paths) > apmAuditDetailSample {
		sample = append(sample, fmt.Sprintf("  (+%d more)", len(paths)-apmAuditDetailSample))
	}
	return sample
}

func (a *App) doctorAPMAudit(ctx context.Context, result *DoctorResult) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil || !a.APMAvailable() {
		result.addCheck("apm-audit", "APM audit", DoctorStatusOK, "nothing to audit")
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "apm.yml")); err != nil {
		result.addCheck("apm-audit", "APM audit", DoctorStatusOK, "nothing to audit", filepath.Join(dir, "apm.yml"))
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		result.addCheck("apm-audit", "APM audit", DoctorStatusWarn, "deploy root could not be resolved", err.Error())
		return
	}

	audit, runErr := a.RunAPM(ctx, "audit", "--ci", "--format", "json")
	report, parseErr := parseAPMAuditReport(audit.Stdout)
	if parseErr != nil {
		details := []string{parseErr.Error()}
		if runErr != nil {
			details = append(details, runErr.Error())
		}
		result.addCheck("apm-audit", "APM audit", DoctorStatusFail, "APM audit could not be evaluated", details...)
		return
	}

	findings := evaluateAPMAudit(report, home)
	if len(findings.Integrity) == 0 && len(findings.Missing) == 0 && len(findings.Drift) == 0 {
		result.addCheck("apm-audit", "APM audit", DoctorStatusOK, "APM audit passed")
		return
	}

	var details []string
	details = append(details, findings.Integrity...)
	if len(findings.Missing) > 0 {
		details = append(details, apmAuditSample("deployed files missing", findings.Missing)...)
		details = append(details, "run 'omni agents sync' to redeploy them")
	}
	if len(findings.Drift) > 0 {
		details = append(details, apmAuditSample("deployed files hand-edited", findings.Drift)...)
	}
	if len(findings.Integrity) > 0 {
		result.addCheck("apm-audit", "APM audit", DoctorStatusFail, "APM workspace integrity is broken", details...)
		return
	}
	result.addCheck("apm-audit", "APM audit", DoctorStatusWarn,
		fmt.Sprintf("%d deployed file(s) missing, %d hand-edited", len(findings.Missing), len(findings.Drift)), details...)
}
