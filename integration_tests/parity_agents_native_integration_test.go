//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

const nativeParityIdentity = "demo@official"

func TestCLIAndTUIAgentsIgnoreProduceEquivalentConfig(t *testing.T) {
	bin := batch16OmniBinary(t)
	runParityFlow(t, bin, parityFlow{
		seed:    seedAgentsNativeParity,
		runCLI:  runAgentsIgnoreParityCLI,
		runTUI:  runAgentsIgnoreParityTUI,
		observe: observeAgentsIgnoreParity,
		readTUI: readAgentsNativeThroughCLI,
	})
}

func TestTUIAgentsNativeSectionIgnoresWithoutTouchingTheTemplate(t *testing.T) {
	bin := batch16OmniBinary(t)
	sandbox := newParitySandbox(t, t.TempDir())
	seedAgentsNativeParity(t, sandbox)
	template := filepath.Join(sandbox.home, ".config", "omni", "apm.yml")

	runAgentsNativeTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Not managed by APM", nativeParityIdentity), "TUI did not render the native section")
		selectAgentsNativeRow(t, term)
		writeTUIKeys(t, term, "i")
	}, func(s *paritySandbox) bool { return len(readAgentsIgnored(t, s)) == 1 })

	if _, err := os.Stat(template); !os.IsNotExist(err) {
		t.Fatalf("ignoring wrote the host template: %v", err)
	}
}

func TestTUIAgentsNativeAdoptDeclaresTheArtifactInTheTemplate(t *testing.T) {
	bin := batch16OmniBinary(t)
	sandbox := newParitySandbox(t, t.TempDir())
	seedAgentsNativeParity(t, sandbox)
	template := filepath.Join(sandbox.home, ".config", "omni", "apm.yml")

	runAgentsNativeTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Not managed by APM", nativeParityIdentity), "TUI did not render the native section")
		selectAgentsNativeRow(t, term)
		writeTUIKeys(t, term, "D")
	}, func(s *paritySandbox) bool {
		raw, err := os.ReadFile(template)
		return err == nil && strings.Contains(string(raw), "official")
	})

	raw, err := os.ReadFile(template)
	if err != nil {
		t.Fatalf("adopt did not write the template: %v", err)
	}
	if !strings.HasPrefix(string(raw), "# omni:agents-migration:v1") {
		t.Fatalf("template is not migration-owned:\n%s", raw)
	}
}

func seedAgentsNativeParity(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	if err := config.Save(sandbox.configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Hosts:   map[string][]string{"testhost": {}},
		Groups:  []*config.GroupConfig{{Name: "testhost", Special: "host"}},
	}); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(sandbox.root, "bin")
	writeExecutable(t, filepath.Join(binDir, "claude"), `#!/bin/sh
case "$*" in
*marketplace*) echo '[{"name":"official","source":"github","repo":"acme/plugins"}]' ;;
*) echo '[{"id":"demo@official"}]' ;;
esac
`)
	writeExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\necho '[]'\n")
	sandbox.env = replaceIntegrationEnv(sandbox.env, "PATH", binDir+string(os.PathListSeparator)+integrationEnvValue(sandbox.env, "PATH"))
	sandbox.env = replaceIntegrationEnv(sandbox.env, "OMNI_HOSTNAME", "testhost")
}

func runAgentsIgnoreParityCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache,
		"agents", "ignore", "--host", "testhost", "--target", "claude",
		"--kind", "plugin", "--id", nativeParityIdentity)
}

func runAgentsIgnoreParityTUI(t *testing.T, bin string, sandbox *paritySandbox) {
	runAgentsNativeTUI(t, bin, sandbox, func(term *vttest.Terminal) {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Not managed by APM", nativeParityIdentity), "TUI did not render the native section")
		selectAgentsNativeRow(t, term)
		writeTUIKeys(t, term, "i")
	}, func(s *paritySandbox) bool { return len(readAgentsIgnored(t, s)) == 1 })
}

// The cursor starts on the first package row, so walk down until the native row is the selected one.
func selectAgentsNativeRow(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	sendAgentsActionsKeyUntil(t, term, "j", func(text string) bool {
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, ">") && strings.Contains(line, nativeParityIdentity) {
				return true
			}
		}
		return false
	}, "TUI did not select the native row")
}

func runAgentsNativeTUI(t *testing.T, bin string, sandbox *paritySandbox, act func(*vttest.Terminal), done func(*paritySandbox) bool) {
	t.Helper()
	runTUI(t, bin, sandbox.root, sandbox.env, []string{"--config", sandbox.configPath, "--cache-dir", sandbox.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 8*time.Second, screenHas("Dashboard", "Agents"), "TUI did not start")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		act(term)
		return waitForRequiredScreen(t, term, 10*time.Second, func(string) bool { return done(sandbox) }, "TUI native action did not complete")
	})
}

func readAgentsNativeThroughCLI(t *testing.T, bin string, sandbox *paritySandbox) {
	runOmniCommand(t, bin, sandbox.root, sandbox.env,
		"--config", sandbox.configPath, "--cache-dir", sandbox.cache, "agents", "drift")
}

func readAgentsIgnored(t *testing.T, sandbox *paritySandbox) []config.AgentIgnoreEntry {
	t.Helper()
	cfg, err := config.Load(sandbox.configPath)
	if err != nil || cfg == nil || cfg.Agents == nil {
		return nil
	}
	return cfg.Agents.Ignored
}

func observeAgentsIgnoreParity(t *testing.T, sandbox *paritySandbox) any {
	t.Helper()
	var out []string
	for _, e := range readAgentsIgnored(t, sandbox) {
		out = append(out, strings.Join([]string{e.Host, e.Target, e.Kind, e.ID}, "/"))
	}
	return strings.Join(out, ",")
}
