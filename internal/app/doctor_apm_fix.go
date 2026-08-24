package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/executor"
)

type APMInstallFixReport struct {
	AlreadyInstalled bool
	Planned          string
	Installed        string
	Upgraded         string
	// Installed succeeded but apm still unresolvable — its bin directory (usually ~/.local/bin) is not on PATH.
	NotOnPATH bool
}

var apmInstallCommands = [][]string{
	{"uv", "tool", "install", apmPackagePin},
	{"pipx", "install", apmPackagePin},
	{"pip3", "install", "--user", apmPackagePin},
}

var apmUpgradeCommands = [][]string{
	{"uv", "tool", "install", "--force", apmPackagePin},
	{"pipx", "install", "--force", apmPackagePin},
	{"pip3", "install", "--user", "--upgrade", "--force-reinstall", apmPackagePin},
}

func (a *App) FixMissingAPM(ctx context.Context, dryRun bool) (APMInstallFixReport, error) {
	if a.APMAvailable() {
		return a.upgradeOutdatedAPM(ctx, dryRun)
	}
	var failures []error
	for _, candidate := range apmInstallCommands {
		if !a.commandAvailable(candidate[0]) {
			continue
		}
		cmdline := strings.Join(candidate, " ")
		if dryRun {
			return APMInstallFixReport{Planned: cmdline}, nil
		}
		stdout, stderr, err := a.fallbackExecutor().Run(ctx, candidate[0], candidate[1:]...)
		// PEP 668: system pythons refuse even --user installs without the override; retry only for that failure.
		if err != nil && candidate[0] == "pip3" && strings.Contains(stdout+stderr, "externally-managed-environment") {
			retry := append(append([]string{}, candidate[1:]...), "--break-system-packages")
			cmdline = candidate[0] + " " + strings.Join(retry, " ")
			stdout, stderr, err = a.fallbackExecutor().Run(ctx, candidate[0], retry...)
		}
		if err != nil {
			return APMInstallFixReport{}, executor.WrapError(err, cmdline, stdout, stderr)
		}
		version, versionErr := a.APMVersion(ctx)
		if versionErr == nil && apmVersionPinned(version) {
			return APMInstallFixReport{Installed: cmdline}, nil
		}
		if versionErr != nil {
			failures = append(failures, fmt.Errorf("%s completed but pinned apm %s could not be verified: %w", cmdline, apmVersionPin, versionErr))
		} else {
			failures = append(failures, fmt.Errorf("%s completed but apm %s remains installed; want %s", cmdline, version, apmVersionPin))
		}
	}
	if len(failures) > 0 {
		return APMInstallFixReport{}, errors.Join(failures...)
	}
	return APMInstallFixReport{}, fmt.Errorf("no supported installer found (uv, pipx, pip3); install pinned APM manually from %s", apmPackagePin)
}

// upgradeOutdatedAPM restores the exact contract-tested APM release.
func (a *App) upgradeOutdatedAPM(ctx context.Context, dryRun bool) (APMInstallFixReport, error) {
	version, err := a.APMVersion(ctx)
	if err == nil && apmVersionPinned(version) {
		return APMInstallFixReport{AlreadyInstalled: true}, nil
	}
	var failures []error
	for _, candidate := range apmUpgradeCommands {
		if !a.commandAvailable(candidate[0]) {
			continue
		}
		cmdline := strings.Join(candidate, " ")
		if dryRun {
			return APMInstallFixReport{Planned: cmdline}, nil
		}
		stdout, stderr, runErr := a.fallbackExecutor().Run(ctx, candidate[0], candidate[1:]...)
		if runErr != nil {
			// apm may have been installed by a later candidate, so one upgrader refusing it says nothing about the next.
			failures = append(failures, executor.WrapError(runErr, cmdline, stdout, stderr))
			continue
		}
		installedVersion, versionErr := a.APMVersion(ctx)
		if versionErr == nil && apmVersionPinned(installedVersion) {
			return APMInstallFixReport{Upgraded: cmdline}, nil
		}
		if versionErr != nil {
			failures = append(failures, fmt.Errorf("%s completed but pinned apm %s could not be verified: %w", cmdline, apmVersionPin, versionErr))
		} else {
			failures = append(failures, fmt.Errorf("%s completed but apm %s remains installed; want %s", cmdline, installedVersion, apmVersionPin))
		}
	}
	if len(failures) > 0 {
		return APMInstallFixReport{}, errors.Join(failures...)
	}
	return APMInstallFixReport{}, fmt.Errorf("apm %s does not match pinned %s and no supported installer (uv, pipx, pip3) is available; install pinned APM manually from %s",
		version, apmVersionPin, apmPackagePin)
}
