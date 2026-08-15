package app

import (
	"context"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type unavailableMcpAdapter struct{ id string }

func (s *unavailableMcpAdapter) ID() string                                         { return s.id }
func (s *unavailableMcpAdapter) Available() bool                                    { return false }
func (s *unavailableMcpAdapter) List(context.Context) ([]InstalledMcpServer, error) { return nil, nil }
func (s *unavailableMcpAdapter) Add(context.Context, config.McpServer) error        { return nil }
func (s *unavailableMcpAdapter) Remove(context.Context, string) error               { return nil }

type unavailablePluginAdapter struct{ id string }

func (s *unavailablePluginAdapter) ID() string      { return s.id }
func (s *unavailablePluginAdapter) Available() bool { return false }
func (s *unavailablePluginAdapter) ListPlugins(context.Context) ([]InstalledPlugin, error) {
	return nil, nil
}
func (s *unavailablePluginAdapter) InstallPlugin(context.Context, config.Plugin) error { return nil }
func (s *unavailablePluginAdapter) RemovePlugin(context.Context, config.Plugin) error  { return nil }
func (s *unavailablePluginAdapter) UpdatePlugin(context.Context, string, string) error { return nil }
func (s *unavailablePluginAdapter) ListMarketplaces(context.Context) ([]InstalledMarketplace, error) {
	return nil, nil
}
func (s *unavailablePluginAdapter) AddMarketplace(context.Context, config.Marketplace) error {
	return nil
}
func (s *unavailablePluginAdapter) UpdateMarketplaces(context.Context) error { return nil }

func hasSubstring(list []string, substr string) bool {
	for _, item := range list {
		if strings.Contains(item, substr) {
			return true
		}
	}
	return false
}
