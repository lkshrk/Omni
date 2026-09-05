//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIBinaryAgentsAdoptPreviewsWithoutWriting(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "adopt-bin")
	writeExecutable(t, filepath.Join(binDir, "claude"), `#!/bin/sh
case "$*" in
*marketplace*) echo '[{"name":"official","source":"github","repo":"acme/plugins"}]' ;;
*) echo '[{"id":"demo@official"}]' ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\necho '[]'\n")
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))

	configPath := filepath.Join(root, "settings.json")
	writeIntegrationFile(t, configPath, `{"version":25,"hosts":{"testhost":["dev"]},"groups":[{"name":"testhost","special":"host"},{"name":"dev"}]}`)

	binary := buildOmniBinary(t)
	out := runOmniOutput(t, binary, root, env, "--config", configPath, "--cache-dir", cache, "agents", "adopt", "--host", "testhost")
	for _, want := range []string{
		"Host: testhost",
		"(bare)",
		"Action: would write the template",
		"Manifest gains:",
		"apm  mkt:official/demo",
		"Preview only: omni agents adopt never writes.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("adopt preview missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--apply") {
		t.Fatalf("adopt advertised an apply mode:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "omni", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("adopt wrote the host template: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !os.IsNotExist(err) {
		t.Fatalf("adopt created the APM workspace: %v", err)
	}
}
