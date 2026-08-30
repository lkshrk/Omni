//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIBinaryAgentsRemainingCommandsDelegateToAPM(t *testing.T) {
	t.Run("agents.prune", func(t *testing.T) { runAgentsRemainingCase(t, []string{"agents", "prune"}, "prune", true) })
	t.Run("agents.marketplace.add", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "marketplace", "add", "https://example.invalid/catalog.git", "--name", "fixture", "--ref", "main"}, "marketplace add https://example.invalid/catalog.git --name fixture --ref main", true)
	})
	t.Run("agents.marketplace.update", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "marketplace", "update", "fixture"}, "marketplace update fixture", true)
	})
	t.Run("agents.marketplace.remove", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "marketplace", "remove", "fixture"}, "marketplace remove fixture --yes", true)
	})
	t.Run("agents.audit", func(t *testing.T) { runAgentsRemainingCase(t, []string{"agents", "audit"}, "audit --ci", true) })
	t.Run("agents.deps.list", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "deps", "list"}, "deps list -g", true)
	})
	t.Run("agents.deps.why", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "deps", "why", "owner/pkg"}, "deps why -g owner/pkg", true)
	})
	t.Run("agents.deps.info", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "deps", "info", "owner/pkg"}, "view --global owner/pkg", true)
	})
	t.Run("agents.marketplaces.list", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "marketplace", "list"}, "marketplace list", true)
	})
	t.Run("agents.marketplaces.browse", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "marketplace", "browse", "fixture"}, "marketplace browse fixture", true)
	})
	t.Run("agents.marketplaces.validate", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "marketplace", "validate", "fixture"}, "marketplace validate fixture", true)
	})
	t.Run("agents.search", func(t *testing.T) {
		runAgentsRemainingCase(t, []string{"agents", "search", "fixture"}, "search fixture", false)
	})
	t.Run("agents.targets", func(t *testing.T) { runAgentsRemainingCase(t, []string{"agents", "targets"}, "targets --json", false) })
}

func runAgentsRemainingCase(t *testing.T, command []string, wantArgs string, global bool) {
	t.Helper()
	root, home, cache, env, logPath := agentsRemainingBinaryFixture(t)
	out := runOmniOutput(t, buildOmniBinary(t), root, env, append([]string{"--cache-dir", cache}, command...)...)
	if !strings.Contains(out, "delegated: "+wantArgs) {
		t.Fatalf("delegated output = %q, want %q", out, wantArgs)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	cwd, gotArgs, ok := strings.Cut(line, "|")
	if !ok || gotArgs != wantArgs {
		t.Fatalf("APM invocation = %q, want args %q", line, wantArgs)
	}
	wantDir := root
	if global {
		wantDir = filepath.Join(home, ".apm")
	}
	if cwd != wantDir {
		t.Fatalf("APM working directory = %q, want %q", cwd, wantDir)
	}
}

func agentsRemainingBinaryFixture(t *testing.T) (root, home, cache string, env []string, logPath string) {
	t.Helper()
	root, home, cache, env = newCLIBinarySandbox(t)
	binDir := filepath.Join(root, "bin")
	logPath = filepath.Join(root, "apm.log")
	writeExecutable(t, filepath.Join(binDir, "apm"), `#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then
  echo 'Agent Package Manager (APM) CLI version 0.29.0'
  exit 0
fi
printf '%s|%s\n' "$PWD" "$*" >> "$OMNI_TEST_APM_LOG"
printf 'delegated: %s\n' "$*"
`)
	env = replaceIntegrationEnv(env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(env, "PATH"))
	env = append(env, "OMNI_TEST_APM_LOG="+logPath)
	return root, home, cache, env, logPath
}
