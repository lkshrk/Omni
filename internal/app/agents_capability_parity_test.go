package app_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

func parityTestApp(t *testing.T, agents config.AgentsConfig, mcp []app.McpAdapter, plugins []app.PluginAdapter) *app.App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", "")
	return newMcpTestApp(t, agents, app.WithMcpAdapters(mcp), app.WithPluginAdapters(plugins))
}

func hasItem(items []string, substr string) bool {
	for _, item := range items {
		if strings.Contains(item, substr) {
			return true
		}
	}
	return false
}
