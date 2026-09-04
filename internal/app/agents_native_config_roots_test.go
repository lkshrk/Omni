package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListNativeMCPHonorsClaudeConfigDir(t *testing.T) {
	a, _ := newNativeInventoryApp(t, map[string]bool{"claude": true})
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"from-home":{"command":"home"}}}`)
	configDir := filepath.Join(home, "elsewhere")
	writeFile(t, filepath.Join(configDir, ".claude.json"), `{"mcpServers":{"from-config-dir":{"command":"configured"}}}`)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	servers, err := a.listNativeMCP(t.Context(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "from-config-dir" {
		t.Fatalf("servers = %#v", servers)
	}
}
