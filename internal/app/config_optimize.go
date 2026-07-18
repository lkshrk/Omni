package app

import (
	"errors"

	"github.com/lkshrk/omni/internal/config"
)

type DoctorFixResult struct {
	OptimizeReport *config.OptimizeReport
	IgnoreModified []string
	OptimizeErr    error
	IgnoreErr      error
}

func (r DoctorFixResult) Err() error {
	return errors.Join(r.OptimizeErr, r.IgnoreErr)
}

func runDoctorFixers(
	dryRun bool,
	optimize func(bool) (*config.OptimizeReport, error),
	fixIgnore func() ([]string, error),
) DoctorFixResult {
	result := DoctorFixResult{}
	result.OptimizeReport, result.OptimizeErr = optimize(dryRun)
	if !dryRun {
		result.IgnoreModified, result.IgnoreErr = fixIgnore()
	}
	return result
}

// OptimizeConfigIncludes removes duplicate definitions across the settings
// $include chain. See config.OptimizeIncludeChain for guarantees.
func (a *App) OptimizeConfigIncludes(dryRun bool) (*config.OptimizeReport, error) {
	return config.OptimizeIncludeChain(a.ConfigPath, dryRun)
}

func (a *App) FixDoctorIssues(dryRun bool) DoctorFixResult {
	return runDoctorFixers(dryRun, a.OptimizeConfigIncludes, a.DotsFixIgnorePatterns)
}
