//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIBinaryAgentsIgnoreSilencesDriftAndUnignoreRestoresIt(t *testing.T) {
	root, _, cache, env := newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "ignore-bin")
	writeExecutable(t, filepath.Join(binDir, "claude"), `#!/bin/sh
case "$*" in
*marketplace*) echo '[{"name":"official","source":"github","repo":"acme/plugins"}]' ;;
*) echo '[{"id":"demo@official"}]' ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\necho '[]'\n")
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = replaceIntegrationEnv(env, "OMNI_HOSTNAME", "testhost")

	configPath := filepath.Join(root, "settings.json")
	writeIntegrationFile(t, configPath, `{"version":25,"hosts":{"testhost":["dev"]},"groups":[{"name":"testhost","special":"host"},{"name":"dev"}]}`)
	bin := buildOmniBinary(t)
	base := []string{"--config", configPath, "--cache-dir", cache}

	before := runOmniOutput(t, bin, root, env, append(base, "agents", "drift")...)
	if !strings.Contains(before, "demo@official") {
		t.Fatalf("expected the plugin to be reported before ignoring:\n%s", before)
	}

	runOmniOutput(t, bin, root, env, append(base, "agents", "ignore",
		"--host", "testhost", "--target", "claude", "--kind", "plugin",
		"--id", "demo@official", "--reason", "kept native on purpose")...)

	after := runOmniOutput(t, bin, root, env, append(base, "agents", "drift")...)
	if strings.Contains(after, "demo@official") {
		t.Fatalf("ignored plugin still reported as drift:\n%s", after)
	}
	if !strings.Contains(after, "Ignored: 1") {
		t.Fatalf("ignored count not reported:\n%s", after)
	}

	all := runOmniOutput(t, bin, root, env, append(base, "agents", "drift", "--all")...)
	if !strings.Contains(all, "kept native on purpose") {
		t.Fatalf("--all did not list the ignore reason:\n%s", all)
	}

	runOmniOutput(t, bin, root, env, append(base, "agents", "unignore",
		"--host", "testhost", "--target", "claude", "--kind", "plugin", "--id", "demo@official")...)

	restored := runOmniOutput(t, bin, root, env, append(base, "agents", "drift")...)
	if !strings.Contains(restored, "demo@official") {
		t.Fatalf("unignore did not restore the drift report:\n%s", restored)
	}
}
