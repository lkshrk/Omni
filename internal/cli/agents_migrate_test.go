package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsMigrateWriteAndDryRunAreMutuallyExclusive(t *testing.T) {
	cmd := newAgentsMigrateCmd(&rootState{})
	cmd.SetArgs([]string{"--host", "h", "--dry-run", "--write"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "[dry-run write]") {
		t.Fatalf("error = %v", err)
	}
}

// A fake claude on PATH keeps the preview hermetic: no real client is consulted.
func fakeNativeCLIs(t *testing.T, home string) {
	t.Helper()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"claude": "#!/bin/sh\ncase \"$*\" in\n*marketplace*) echo '[{\"name\":\"official\",\"source\":\"github\",\"repo\":\"acme/plugins\"}]' ;;\n*) echo '[{\"id\":\"demo@official\"}]' ;;\nesac\n",
		"codex":  "#!/bin/sh\necho '[]'\n",
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestAgentsMigrateWithoutSnapshotPrintsSections(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	fakeNativeCLIs(t, home)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"native":{"command":"npx","args":["native-mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(home, "settings.json")
	if err := os.WriteFile(cfgPath, []byte(`{"version":24,"hosts":{"testhost":["dev"]},"groups":[{"name":"testhost","special":"host"},{"name":"dev"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := runRootCommand(t, "--config", cfgPath, "--cache-dir", t.TempDir(), "--state-dir", t.TempDir(), "agents", "migrate", "--host", "testhost")
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, output)
	}
	for _, want := range []string{"name: demo", "name: native", "Replaced by this manifest (delete by hand after sync):", "  claude  mcp  native  ", "  claude  plugin  demo@official  "} {
		if !strings.Contains(output, want) {
			t.Fatalf("preview missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "config", "omni", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote the host template: %v", err)
	}
}
