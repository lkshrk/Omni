package app

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

// Below this release the declarative surfaces omni generates are unsafe: failed service writes exited 0 before 0.27.0, and per-dependency targets only round-trip through an install cycle from 0.28.0.
const apmVersionFloor = "0.28.0"

const apmVersionFixHint = "run 'omni doctor --fix' to upgrade apm-cli"

var apmVersionPattern = regexp.MustCompile(`(?:^|[^\d.])(\d+)\.(\d+)(?:\.(\d+))?(?:[^\d.]|$)`)

// APMVersion reports the installed apm-cli version as a dotted triple.
func (a *App) APMVersion(ctx context.Context) (string, error) {
	stdout, stderr, err := a.fallbackExecutor().Run(ctx, "apm", "--version")
	if err != nil {
		return "", executor.WrapError(err, "apm --version", stdout, stderr)
	}
	output := strings.TrimSpace(stdout + " " + stderr)
	version := parseAPMVersion(output)
	if version == "" {
		return "", fmt.Errorf("apm --version reported no recognisable version: %q", output)
	}
	return version, nil
}

func parseAPMVersion(output string) string {
	match := apmVersionPattern.FindStringSubmatch(output)
	if match == nil {
		return ""
	}
	patch := match[3]
	if patch == "" {
		patch = "0"
	}
	return match[1] + "." + match[2] + "." + patch
}

func apmVersionBelowFloor(version string) bool {
	return compareAPMVersions(version, apmVersionFloor) < 0
}

func compareAPMVersions(left, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for i := range 3 {
		l, r := apmVersionPart(leftParts, i), apmVersionPart(rightParts, i)
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

func apmVersionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
	if err != nil {
		return 0
	}
	return n
}

func (a *App) doctorAPMVersion(ctx context.Context, result *DoctorResult, cfg *config.RootConfig) {
	if !a.AgentsEnabled(cfg) || !a.APMAvailable() {
		return
	}
	version, err := a.APMVersion(ctx)
	if err != nil {
		result.addCheck("apm-version", "APM version", DoctorStatusFail,
			"apm version could not be determined", err.Error(), apmVersionFixHint)
		return
	}
	if apmVersionBelowFloor(version) {
		result.addCheck("apm-version", "APM version", DoctorStatusFail,
			fmt.Sprintf("apm %s is older than the required %s", version, apmVersionFloor), apmVersionFixHint)
		return
	}
	result.addCheck("apm-version", "APM version", DoctorStatusOK, "apm "+version, "floor "+apmVersionFloor)
}
