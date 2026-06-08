package app_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

func saveAppConfig(t testing.TB, path string, cfg *config.RootConfig) error {
	t.Helper()
	normalizeTestRootConfig(cfg)
	return config.Save(path, cfg)
}

func normalizeTestRootConfig(cfg *config.RootConfig) {
	if cfg == nil {
		return
	}
	host := testShortHostname()
	for _, group := range cfg.Groups {
		if group != nil && group.Name == "" {
			group.Name = host
			group.Special = "host"
		} else if group != nil && group.Name == host {
			group.Special = "host"
		}
	}
	if cfg.Hosts == nil {
		cfg.Hosts = map[string][]string{}
	}
	for _, group := range cfg.Groups {
		if group != nil && group.IsHost() {
			if _, ok := cfg.Hosts[group.Name]; !ok {
				cfg.Hosts[group.Name] = []string{}
			}
		}
	}
	byName := make(map[string]*config.GroupConfig, len(cfg.Groups))
	merged := make([]*config.GroupConfig, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if existing := byName[group.BaseName()]; existing != nil {
			existing.Tools = append(existing.Tools, group.Tools...)
			existing.Dots = append(existing.Dots, group.Dots...)
			existing.Taps = append(existing.Taps, group.Taps...)
			if group.Special != "" {
				existing.Special = group.Special
			}
			continue
		}
		byName[group.BaseName()] = group
		merged = append(merged, group)
	}
	cfg.Groups = merged
}

func testHostGroup() *config.GroupConfig {
	return &config.GroupConfig{Name: testShortHostname(), Special: "host"}
}

func testHostToolGroup(names ...string) *config.GroupConfig {
	group := testHostGroup()
	group.Tools = groupTools(names...)
	return group
}

func testShortHostname() string {
	hostname := os.Getenv("OMNI_HOSTNAME")
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	// Mirror production shortHostname: hostnames are always lower-cased.
	hostname = strings.ToLower(hostname)
	if idx := strings.IndexByte(hostname, '.'); idx != -1 {
		return hostname[:idx]
	}
	if hostname == "" {
		return "localhost"
	}
	return hostname
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
		if tool.InstallWith != "" {
			providerName = tool.InstallWith
		}
		switch providerName {
		case "system":
			providerName = "brew"
		case "node":
			providerName = "npm"
		case "python":
			providerName = "pip"
		}
		if providerName == "pip3" {
			providerName = "pip"
		}
		specs[tool.Name] = config.ToolSpec{
			Providers: []config.ToolInstallSpec{{
				Provider: providerName,
				Package:  tool.Package,
				Options:  tool.Options,
			}},
			Ignore: tool.Ignore,
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

// installTracker wraps stubProvider and records Install calls.
type installTracker struct {
	stubProvider
	installed     []provider.InstalledTool
	installCalled []string
}

func (s *installTracker) Install(_ context.Context, t provider.Tool) error {
	s.installCalled = append(s.installCalled, t.Name)
	s.installed = append(s.installed, provider.InstalledTool{Tool: t})
	return nil
}

func (s *installTracker) IsInstalled(ctx context.Context, t provider.Tool) (bool, string, error) {
	for _, installed := range s.installed {
		if installed.Tool.Name == t.Name && installed.Tool.Provider == t.Provider {
			return true, installed.Version, nil
		}
	}
	return s.stubProvider.IsInstalled(ctx, t)
}
