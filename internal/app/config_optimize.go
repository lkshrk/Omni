package app

import "github.com/lkshrk/omni/internal/config"

// OptimizeConfigIncludes removes duplicate definitions across the settings
// $include chain. See config.OptimizeIncludeChain for guarantees.
func (a *App) OptimizeConfigIncludes(dryRun bool) (*config.OptimizeReport, error) {
	return config.OptimizeIncludeChain(a.ConfigPath, dryRun)
}
