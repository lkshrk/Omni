package tui

import "github.com/lkshrk/omni/internal/config"

func tuiSettingsWithManager(ecosystem, manager string) config.Settings {
	var s config.Settings
	if manager != "" {
		s.SetEcosystemManager(ecosystem, manager)
	}
	return s
}

func tuiSettingsWithPriority(priority ...string) config.Settings {
	var s config.Settings
	s.SetEcosystemPriority("system", priority)
	return s
}
