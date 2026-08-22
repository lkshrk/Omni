package app

import (
	"context"
	"errors"

	"github.com/lkshrk/omni/internal/config"
)

type DoctorFixResult struct {
	OptimizeReport *config.OptimizeReport
	IgnoreModified []string
	APMInstall     APMInstallFixReport
	OptimizeErr    error
	IgnoreErr      error
	APMInstallErr  error
}

func (r DoctorFixResult) Err() error {
	return errors.Join(r.OptimizeErr, r.IgnoreErr, r.APMInstallErr)
}

func runDoctorFixers(
	dryRun bool,
	optimize func(bool) (*config.OptimizeReport, error),
	fixIgnore func() ([]string, error),
	fixAPM func(bool) (APMInstallFixReport, error),
) DoctorFixResult {
	result := DoctorFixResult{}
	result.OptimizeReport, result.OptimizeErr = optimize(dryRun)
	if !dryRun {
		result.IgnoreModified, result.IgnoreErr = fixIgnore()
	}
	result.APMInstall, result.APMInstallErr = fixAPM(dryRun)
	return result
}

// OptimizeConfigIncludes — See config.OptimizeIncludeChain for guarantees.
func (a *App) OptimizeConfigIncludes(dryRun bool) (*config.OptimizeReport, error) {
	return config.OptimizeIncludeChain(a.ConfigPath, dryRun)
}

func (a *App) FixDoctorIssues(ctx context.Context, dryRun bool) DoctorFixResult {
	return runDoctorFixers(dryRun, a.OptimizeConfigIncludes, a.DotsFixIgnorePatterns,
		func(dryRun bool) (APMInstallFixReport, error) { return a.FixMissingAPM(ctx, dryRun) })
}
