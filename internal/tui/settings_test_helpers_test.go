package tui

import "github.com/lkshrk/omni/internal/config"

func tuiSettingsWithPriority(priority ...string) config.Settings {
	var s config.Settings
	s.ProviderPriority = append([]string(nil), priority...)
	return s
}
