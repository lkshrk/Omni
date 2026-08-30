//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAgentsWrongVersionRepairsAndRechecksReadiness(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.28.0", false)
	fixture.writeReadyWorkspace(t)
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsFromDashboard(t, term, "Open Agents to repair")
		waitForRequiredScreen(t, term, 10*time.Second, screenHas("R repair pinned APM"), "TUI did not offer pinned APM repair")
		sendAgentsActionsKeyUntil(t, term, "R", func(string) bool {
			return fileContains(fixture.repairLog, "invoked")
		}, "TUI did not invoke pinned APM repair")
		return waitForRequiredScreen(t, term, 15*time.Second, func(text string) bool {
			return strings.Contains(text, "1 installed") && fileContains(fixture.logPath, "outdated -g --parallel-checks 4")
		}, "TUI did not repair and recheck APM")
	})
	if got := strings.TrimSpace(readFileString(t, fixture.versionPath)); got != "0.29.0" {
		t.Fatalf("repaired APM version = %q", got)
	}
}

func TestTUIAgentsMissingLockShowsSyncWithoutOutdated(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.29.0", false)
	writeIntegrationFile(t, filepath.Join(fixture.home, ".apm", "apm.yml"), "name: live\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm: []\n")
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsFromDashboard(t, term, "Open Agents to sync")
		waitForRequiredScreen(t, term, 10*time.Second, screenHas("live manifest exists without a readable lockfile", "S sync"), "TUI did not show missing-lock sync CTA")
		writeTUIKeys(t, term, "S")
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool { return fileContains(fixture.logPath, "install -g") }, "TUI did not invoke sync from readiness CTA")
	})
	log := readFileString(t, fixture.logPath)
	if outdated, install := strings.Index(log, "outdated"), strings.Index(log, "install -g"); outdated >= 0 && outdated < install {
		t.Fatal("missing-lock readiness invoked outdated")
	}
}

func TestTUIAgentsAutoStagesTemplateWithoutInvokingAPMInstall(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.29.0", false)
	writeAgentsReadinessSnapshot(t, filepath.Dir(fixture.configPath))
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		return waitForRequiredScreen(t, term, 12*time.Second, screenHas("template is staged", "S sync"), "TUI did not show staged-template sync CTA")
	})
	if _, err := os.Stat(filepath.Join(fixture.home, ".config", "omni", "apm.yml")); err != nil {
		t.Fatalf("template was not auto-staged: %v", err)
	}
	for _, name := range []string{"apm.yml", "apm.lock.yaml"} {
		if _, err := os.Stat(filepath.Join(fixture.home, ".apm", name)); !os.IsNotExist(err) {
			t.Fatalf("auto-stage created live APM state %s: %v", name, err)
		}
	}
	if fileContains(fixture.logPath, "install") || fileContains(fixture.logPath, "outdated") {
		t.Fatal("auto-stage invoked APM mutation/query")
	}
}

func TestTUIAgentsRepairFailureRemainsActionable(t *testing.T) {
	fixture := newAgentsReadinessPTYFixture(t, "0.28.0", true)
	runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
		openAgentsReadinessTab(t, term)
		waitForRequiredScreen(t, term, 10*time.Second, screenHas("R repair pinned APM"), "TUI did not offer repair")
		sendAgentsActionsKeyUntil(t, term, "R", func(string) bool {
			return fileContains(fixture.repairLog, "invoked")
		}, "TUI did not invoke failing APM repair")
		return waitForRequiredScreen(t, term, 10*time.Second, screenHas("APM repair failed"), "TUI did not report repair failure")
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
  'install -g') printf 'dependencies: []\n' > %q; echo '[✓] installed' ;;
  *) echo '[✓] done' ;;
esac
`, versionPath, logPath, filepath.Join(home, ".apm", "apm.lock.yaml")))
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

func openAgentsFromDashboard(t *testing.T, term *vttest.Terminal, guidance string) {
	t.Helper()
	waitForRequiredScreen(t, term, 10*time.Second, screenHas("Dashboard", guidance), "Dashboard did not show APM readiness guidance")
	sendTUIKey(term, uv.KeyHome)
	sendAgentsActionsKeyUntil(t, term, "j", func(text string) bool {
		return strings.Contains(text, guidance) && dashboardAgentsRowSelected(text)
	}, "Dashboard did not select Agents readiness row")
	writeTUIKeys(t, term, "\r")
	waitForRequiredScreen(t, term, 6*time.Second, screenHas("~/.apm/apm.yml"), "Dashboard Agents action did not open Agents tab")
}

func dashboardAgentsRowSelected(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, ">") && strings.Contains(line, " Agents ") && !strings.Contains(line, "Agent Updates") {
			return true
		}
	}
	return false
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
