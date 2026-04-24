package app_test

import "github.com/lkshrk/omni/internal/config"

func testSettingsWithManager(ecosystem, manager string) config.Settings {
	var s config.Settings
	if manager != "" {
		s.SetEcosystemManager(ecosystem, manager)
	}
	return s
}

func testSettingsWithNodePython(nodeManager, pythonManager string) config.Settings {
	var s config.Settings
	if nodeManager != "" {
		s.SetEcosystemManager("node", nodeManager)
	}
	if pythonManager != "" {
		s.SetEcosystemManager("python", pythonManager)
	}
	return s
}
