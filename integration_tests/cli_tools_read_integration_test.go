//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestCLIBinaryToolsProvidersAndSearchUseConfiguredAdapter(t *testing.T) {
	root, _, cache, env := newCLIBinarySandbox(t)
	configPath := filepath.Join(root, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"apt", "apk", "dnf", "pacman", "zypper", "node", "bun", "pnpm", "npm", "python", "uv", "pip"}},
		Hosts:    map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(binDir, "brew"), `#!/bin/sh
case "$*" in
  "--version") echo "Homebrew 6.0.0" ;;
  "search fixture") echo "fixture" ;;
  "info --json=v2 fixture") echo '{"formulae":[{"name":"fixture","full_name":"fixture","desc":"fixture package","versions":{"stable":"1.0.0"},"installed":[]}],"casks":[]}' ;;
  *) exit 64 ;;
esac
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	bin := buildOmniBinary(t)
	t.Run("tools.providers", func(t *testing.T) {
		providers := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "providers")
		if !strings.Contains(providers, "brew") || !strings.Contains(providers, "yes") {
			t.Fatalf("providers output: %s", providers)
		}
	})
	t.Run("tools.search", func(t *testing.T) {
		search := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "search", "fixture", "--provider", "brew")
		if !strings.Contains(search, "fixture") || !strings.Contains(search, "fixture package") {
			t.Fatalf("search output: %s", search)
		}
	})
}
