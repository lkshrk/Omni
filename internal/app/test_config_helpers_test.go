package app_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func saveAppConfig(t testing.TB, path string, cfg *config.RootConfig) error {
	t.Helper()
	return config.Save(path, cfg)
}

type logicalFixtureTool struct {
	Name        string
	Provider    string
	Package     string
	InstallWith string
	Options     map[string]string
	Ignore      bool
}

func logicalTool(name, providerName string) logicalFixtureTool {
	return logicalFixtureTool{Name: name, Provider: providerName}
}

func logicalToolPackage(name, providerName, packageName string) logicalFixtureTool {
	return logicalFixtureTool{Name: name, Provider: providerName, Package: packageName}
}

func logicalToolSpecs(tools ...logicalFixtureTool) map[string]config.ToolSpec {
	specs := make(map[string]config.ToolSpec, len(tools))
	for _, tool := range tools {
		providerName := tool.Provider
		installWith := tool.InstallWith
		if ecosystem := testEcosystemForConcrete(providerName); ecosystem != "" {
			providerName = ecosystem
			if installWith == "" {
				installWith = tool.Provider
			}
		}
		specs[tool.Name] = config.ToolSpec{
			Provider:    providerName,
			Package:     tool.Package,
			InstallWith: installWith,
			Options:     tool.Options,
			Ignore:      tool.Ignore,
		}
	}
	return specs
}

func groupTools(names ...string) []config.ToolEntry {
	tools := make([]config.ToolEntry, 0, len(names))
	for _, name := range names {
		tools = append(tools, config.ToolEntry{Name: name})
	}
	return tools
}
