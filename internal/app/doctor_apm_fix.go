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
	{"uv", "tool", "install", "apm-cli"},
	{"pipx", "install", "apm-cli"},
	{"pip3", "install", "--user", "apm-cli"},
}

var apmUpgradeCommands = [][]string{
	{"uv", "tool", "upgrade", "apm-cli"},
	{"pipx", "upgrade", "apm-cli"},
	{"pip3", "install", "--user", "--upgrade", "apm-cli"},
}

func (a *App) FixMissingAPM(ctx context.Context, dryRun bool) (APMInstallFixReport, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return APMInstallFixReport{}, err
	}
	if !a.AgentsEnabled(cfg) {
		return APMInstallFixReport{AlreadyInstalled: true}, nil
	}
	if a.APMAvailable() {
		return a.upgradeOutdatedAPM(ctx, dryRun)
	}
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
		report := APMInstallFixReport{Installed: cmdline}
		report.NotOnPATH = !a.APMAvailable()
		return report, nil
	}
	return APMInstallFixReport{}, errors.New("no supported installer found (uv, pipx, pip3); install apm-cli manually")
}

// upgradeOutdatedAPM raises an installed apm below the version floor; an unreadable version is not evidence of an old one.
func (a *App) upgradeOutdatedAPM(ctx context.Context, dryRun bool) (APMInstallFixReport, error) {
	version, err := a.APMVersion(ctx)
	if err != nil || !apmVersionBelowFloor(version) {
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
		return APMInstallFixReport{Upgraded: cmdline}, nil
	}
	if len(failures) > 0 {
		return APMInstallFixReport{}, errors.Join(failures...)
	}
	return APMInstallFixReport{}, fmt.Errorf("apm %s is older than %s and no supported upgrader (uv, pipx, pip3) is available",
		version, apmVersionFloor)
}
