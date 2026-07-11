package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// MigrateHostOverrides folds tools.*.hosts install overrides into providers[] and
// removes empty hosts maps.
func (a *App) MigrateHostOverrides(ctx context.Context) (int, error) {
	_ = ctx
	changed := 0
	err := a.withConfig(func(cfg *config.RootConfig) error {
		for name, spec := range cfg.Tools {
			if len(spec.Hosts) == 0 {
				continue
			}
			toolChanged := false
			for host, override := range spec.Hosts {
				if strings.TrimSpace(override.Provider) == "" {
					continue
				}
				if !providerCandidatePresent(spec.Providers, override) {
					spec.Providers = append(spec.Providers, override)
				}
				delete(spec.Hosts, host)
				toolChanged = true
			}
			if len(spec.Hosts) == 0 {
				spec.Hosts = nil
			}
			if toolChanged {
				cfg.Tools[name] = spec
				changed++
			}
		}
		if changed == 0 {
			return errSkipSave
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func providerCandidatePresent(candidates []config.ToolInstallSpec, want config.ToolInstallSpec) bool {
	for _, candidate := range candidates {
		if installSpecIdentity(candidate) == installSpecIdentity(want) {
			return true
		}
	}
	return false
}

func installSpecIdentity(spec config.ToolInstallSpec) string {
	pkg := strings.TrimSpace(spec.Package)
	opts := ""
	if len(spec.Options) > 0 {
		parts := make([]string, 0, len(spec.Options))
		for key, value := range spec.Options {
			parts = append(parts, key+"="+value)
		}
		opts = strings.Join(parts, ",")
	}
	return fmt.Sprintf("%s|%s|%s|%s", spec.Provider, pkg, spec.InstallWith, opts)
}
