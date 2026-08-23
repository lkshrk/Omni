package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

// APM is contract-tested as an exact dependency; newer releases require rerunning that suite.
const apmVersionPin = "0.28.0+omni.5"
const apmPackagePin = "git+https://github.com/lkshrk/apm.git@4713ef995283d935943cfe70c42b8fc67c3933eb"

const apmVersionFixHint = "run 'omni doctor --fix' to upgrade apm-cli"

var apmVersionPattern = regexp.MustCompile(`(?:^|\s)(\d+\.\d+\.\d+(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?)(?:\s|$)`)

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
	return match[1]
}

func apmVersionPinned(version string) bool { return version == apmVersionPin }

func (a *App) doctorAPMVersion(ctx context.Context, result *DoctorResult, _ *config.RootConfig) {
	if !a.APMAvailable() {
		return
	}
	version, err := a.APMVersion(ctx)
	if err != nil {
		result.addCheck("apm-version", "APM version", DoctorStatusFail,
			"apm version could not be determined", err.Error(), apmVersionFixHint)
		return
	}
	if !apmVersionPinned(version) {
		result.addCheck("apm-version", "APM version", DoctorStatusFail,
			fmt.Sprintf("apm %s is unsupported; omni requires exactly %s", version, apmVersionPin), apmVersionFixHint)
		return
	}
	result.addCheck("apm-version", "APM version", DoctorStatusOK, "apm "+version, "pinned "+apmVersionPin)
}

func (a *App) requirePinnedAPM(ctx context.Context) error {
	version, err := a.APMVersion(ctx)
	if err != nil {
		return err
	}
	if !apmVersionPinned(version) {
		return fmt.Errorf("apm %s is unsupported; omni requires exactly %s: %s", version, apmVersionPin, apmVersionFixHint)
	}
	return nil
}
