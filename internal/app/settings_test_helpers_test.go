package app_test

import "github.com/lkshrk/omni/internal/config"

func normalizeManager(mgr string) string {
	if c := config.NormalizeConcreteProvider(mgr); c != "" {
		return c
	}
	return mgr
}

func testSettingsWithManager(ecosystem, manager string) config.Settings {
	var s config.Settings
	if manager != "" {
		s.ProviderPriority = append([]string{normalizeManager(manager)}, s.ProviderPriority...)
	}
	return s
}

func testSettingsWithNodePython(nodeManager, pythonManager string) config.Settings {
	var s config.Settings
	if nodeManager != "" {
		s.ProviderPriority = append([]string{normalizeManager(nodeManager)}, s.ProviderPriority...)
	}
	if pythonManager != "" {
		s.ProviderPriority = append([]string{normalizeManager(pythonManager)}, s.ProviderPriority...)
	}
	return s
}
