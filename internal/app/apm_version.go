package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

// APM is contract-tested as an exact dependency; newer releases require rerunning that suite.
const apmVersionPin = "0.29.0"
const apmPackagePin = "git+https://github.com/microsoft/apm.git@b75a02b1cfab3ffa5e1952916045b6d5374090ae"

const apmVersionFixHint = "run 'omni doctor --fix' to upgrade apm-cli"

var apmVersionPattern = regexp.MustCompile(`(?:^|\s)(\d+\.\d+\.\d+(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?)(?:\s|$)`)

// APMVersion reports the installed apm-cli version as a dotted triple.
func (a *App) APMVersion(ctx context.Context) (string, error) {
	env, cleanup, err := apm.IsolatedEnv("omni-apm-version-")
	if err != nil {
		return "", err
	}
	defer cleanup()

	stdout, stderr, err := executor.RunWithEnv(ctx, a.fallbackExecutor(), env, "apm", "--version")
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
	a.seedPinnedAPM(version, err)
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

func pinnedAPMError(version string, err error) error {
	if err == nil && !apmVersionPinned(version) {
		return fmt.Errorf("apm %s is unsupported; omni requires exactly %s: %s", version, apmVersionPin, apmVersionFixHint)
	}
	return err
}

func (a *App) requirePinnedAPM(ctx context.Context) error {
	a.pinnedAPMMu.Lock()
	defer a.pinnedAPMMu.Unlock()
	if a.pinnedAPMDone {
		return a.pinnedAPMErr
	}
	version, err := a.APMVersion(ctx)
	err = pinnedAPMError(version, err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	a.pinnedAPMErr = err
	a.pinnedAPMDone = true
	return err
}

// Doctor always probes fresh, then seeds the memo so the rest of the same run needs no second spawn.
func (a *App) seedPinnedAPM(version string, err error) {
	err = pinnedAPMError(version, err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	a.pinnedAPMMu.Lock()
	defer a.pinnedAPMMu.Unlock()
	a.pinnedAPMErr, a.pinnedAPMDone = err, true
}
