//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIBinaryAgentsDriftReportsNativeItemsWithoutWriting(t *testing.T) {
	root, home, cache, env := newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "drift-bin")
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

	out := runOmniOutput(t, buildOmniBinary(t), root, env, "--config", configPath, "--cache-dir", cache, "agents", "drift")
	for _, want := range []string{"Native items APM does not manage:", "  claude  plugin  demo@official  ", "Ignored: 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("drift report missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "omni", "apm.yml")); !os.IsNotExist(err) {
		t.Fatalf("drift wrote the host template: %v", err)
	}
}
