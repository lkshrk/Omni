//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAgentsWrongVersionRepairsAutomatically(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.28.0", false)
	fixture.writeReadyWorkspace(t)
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		return waitForRequiredScreen(t, term, 15*time.Second, func(text string) bool {
			return strings.Contains(text, "1 installed") && fileContains(fixture.repairLog, "invoked") && fileContains(fixture.logPath, "outdated -g --parallel-checks 4")
		}, "TUI did not automatically repair and recheck APM")
	})
	if got := strings.TrimSpace(readFileString(t, fixture.versionPath)); got != "0.29.0" {
		t.Fatalf("repaired APM version = %q", got)
	}
}

func TestTUIAgentsMissingLockSyncsAutomatically(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.29.0", false)
	writeIntegrationFile(t, filepath.Join(fixture.home, ".apm", "apm.yml"), "name: live\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm: []\n")
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		return waitForRequiredScreen(t, term, 15*time.Second, func(string) bool {
			return fileContains(fixture.logPath, "install -g") && fileContains(fixture.logPath, "outdated -g --parallel-checks 4")
		}, "TUI did not automatically sync missing-lock state")
	})
	log := readFileString(t, fixture.logPath)
	if outdated, install := strings.Index(log, "outdated"), strings.Index(log, "install -g"); outdated >= 0 && outdated < install {
		t.Fatal("missing-lock readiness invoked outdated")
	}
}

func TestTUIAgentsSnapshotMigrationInstallsAutomatically(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.29.0", false)
	writeAgentsReadinessSnapshot(t, filepath.Dir(fixture.configPath))
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		return waitForRequiredScreen(t, term, 15*time.Second, func(string) bool {
			return fileContains(fixture.logPath, "install -g") && fileContains(fixture.logPath, "outdated -g --parallel-checks 4")
		}, "TUI did not automatically install the migrated snapshot")
	})
	if _, err := os.Stat(filepath.Join(fixture.home, ".config", "omni", "apm.yml")); err != nil {
		t.Fatalf("template was not auto-staged: %v", err)
	}
	for _, name := range []string{"apm.yml", "apm.lock.yaml"} {
		if _, err := os.Stat(filepath.Join(fixture.home, ".apm", name)); err != nil {
			t.Fatalf("automatic migration did not create live APM state %s: %v", name, err)
		}
	}
}

func TestTUIAgentsCleanEmptyConfigBecomesReadyAutomatically(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.29.0", false)
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		return waitForRequiredScreen(t, term, 15*time.Second, func(string) bool {
			return fileContains(fixture.logPath, "install -g") && fileContains(fixture.logPath, "outdated -g --parallel-checks 4")
		}, "TUI did not automatically initialize an empty APM workspace")
	})
	for _, path := range []string{
		filepath.Join(fixture.home, ".config", "omni", "apm.yml"),
		filepath.Join(fixture.home, ".apm", "apm.yml"),
		filepath.Join(fixture.home, ".apm", "apm.lock.yaml"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("automatic empty setup did not create %s: %v", path, err)
		}
	}
}

func TestTUIAgentsAutomaticRepairFailureRemainsActionable(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.28.0", true)
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		return waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
			return fileContains(fixture.repairLog, "invoked") && strings.Contains(text, "Automatic APM setup failed") && strings.Contains(text, "R retry")
		}, "TUI did not report the automatic repair failure")
	})
}

type agentsReadinessPTYFixture struct {
	bin, root, home, cache, configPath, versionPath, logPath, repairLog string
	env                                                                 []string
}

func newAgentsReadinessPTYFixture(t *testing.T, version string, repairFails bool) agentsReadinessPTYFixture {
	t.Helper()
	bin, root := batch16OmniBinary(t), t.TempDir()
	home, cache := filepath.Join(root, "home"), filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Hosts: map[string][]string{"testhost": {}}, Groups: []*config.GroupConfig{{Name: "testhost", Special: "host"}}}); err != nil {
		t.Fatal(err)
	}
	versionPath, logPath, repairLog := filepath.Join(root, "apm-version"), filepath.Join(root, "apm.log"), filepath.Join(root, "repair.log")
	writeIntegrationFile(t, versionPath, version+"\n")
	stub := filepath.Join(home, ".test-stub-bin")
	writeExecutable(t, filepath.Join(stub, "apm"), fmt.Sprintf(`#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then echo "APM CLI version $(cat %q)"; exit 0; fi
printf '%%s\n' "$*" >> %q
case "$*" in
  'outdated -g --parallel-checks 4') echo '[✓] All dependencies are up-to-date' ;;
  'install -g') mkdir -p %q; printf 'dependencies: []\n' > %q; echo '[✓] installed' ;;
  *) echo '[✓] done' ;;
esac
`, versionPath, logPath, filepath.Join(home, ".apm"), filepath.Join(home, ".apm", "apm.lock.yaml")))
	apmPath := filepath.Join(stub, "apm")
	uvBody := fmt.Sprintf(`case "$*" in
  --version) echo 'uv 0.9.0' ;;
  'tool install --force '*)
    echo invoked >> %q
cat > %q <<'PINNED_APM'
#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then echo 'APM CLI version 0.29.0'; exit 0; fi
printf '%%s\n' "$*" >> %q
case "$*" in
  'outdated -g --parallel-checks 4') echo '[✓] All dependencies are up-to-date' ;;
  *) echo '[✓] done' ;;
esac
PINNED_APM
chmod 755 %q
echo 0.29.0 > %q
    ;;
  *) exit 0 ;;
esac
`, repairLog, apmPath, logPath, apmPath, versionPath)
	if repairFails {
		uvBody = fmt.Sprintf("case \"$*\" in\n  --version) echo 'uv 0.9.0' ;;\n  'tool install --force '*) echo invoked >> %q; echo repair failed >&2; exit 1 ;;\n  *) exit 0 ;;\nesac\n", repairLog)
	}
	writeExecutable(t, filepath.Join(stub, "uv"), "#!/bin/sh\nset -eu\n"+uvBody)
	return agentsReadinessPTYFixture{bin: bin, root: root, home: home, cache: cache, configPath: configPath, versionPath: versionPath, logPath: logPath, repairLog: repairLog, env: env}
}

func (fixture agentsReadinessPTYFixture) writeReadyWorkspace(t *testing.T) {
	t.Helper()
	writeIntegrationFile(t, filepath.Join(fixture.home, ".apm", "apm.yml"), "name: live\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm:\n    - git: https://github.com/acme/tool.git\n")
	writeIntegrationFile(t, filepath.Join(fixture.home, ".apm", "apm.lock.yaml"), "dependencies:\n  - repo_url: acme/tool\n    name: tool\n    version: 1.0.0\n")
}

func openAgentsReadinessTab(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Agents"), "TUI did not start")
	writeTUIKeys(t, term, "\t", "\t", "\t")
}

func writeAgentsReadinessSnapshot(t *testing.T, configDir string) {
	t.Helper()
	dir := filepath.Join(configDir, ".omni-apm-migration-backup-test")
	writeIntegrationFile(t, filepath.Join(dir, "omni-config-000.json"), `{"agents":{"mcp_servers":[{"name":"independent","transport":"stdio","command":"independent-mcp"}]},"groups":[{"name":"g","mcp_servers":["independent"]}],"hosts":{"testhost":["g"]}}`)
	writeIntegrationFile(t, filepath.Join(dir, "paths.json"), `{"omni-config-000.json":"/tmp/settings.json"}`)
}

func fileContains(path, needle string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(raw), needle)
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
