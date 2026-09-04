//go:build integration

package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAgentsReadinessNeverWritesAndGuidesTheOperator(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		setup   func(t *testing.T, fixture agentsReadinessPTYFixture)
		want    []string
	}{
		{
			name:    "off-pin apm asks for doctor",
			version: "0.28.0",
			want:    []string{"doctor", "--fix"},
		},
		{
			name:    "legacy config asks for migrate",
			version: "0.29.0",
			setup: func(t *testing.T, fixture agentsReadinessPTYFixture) {
				writeIntegrationFile(t, fixture.configPath, `{"version":22,"agents":{"mcp_servers":[{"name":"legacy","transport":"stdio","command":"legacy-mcp"}]},"hosts":{"testhost":[]},"groups":[{"name":"testhost","special":"host"}]}`)
			},
			want: []string{"agents migrate", "--host"},
		},
		{
			name:    "staged template asks for sync",
			version: "0.29.0",
			setup: func(t *testing.T, fixture agentsReadinessPTYFixture) {
				writeIntegrationFile(t, filepath.Join(fixture.home, ".config", "omni", "apm.yml"), "name: staged\nversion: 1.0.0\n")
			},
			want: []string{"S sync"},
		},
		{
			name:    "live manifest without a lock asks for sync",
			version: "0.29.0",
			setup: func(t *testing.T, fixture agentsReadinessPTYFixture) {
				writeIntegrationFile(t, filepath.Join(fixture.home, ".apm", "apm.yml"), "name: live\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm:\n    - git: https://github.com/acme/tool.git\n")
			},
			want: []string{"S sync"},
		},
		{
			name:    "lock without a manifest is invalid",
			version: "0.29.0",
			setup: func(t *testing.T, fixture agentsReadinessPTYFixture) {
				writeIntegrationFile(t, filepath.Join(fixture.home, ".apm", "apm.lock.yaml"), "dependencies: []\n")
			},
			want: []string{"inspect APM files"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentsReadinessPTYFixture(t, test.version)
			if test.setup != nil {
				test.setup(t, fixture)
			}
			before := hashIntegrationTree(t, fixture.home)
			runTUI(t, fixture.bin, fixture.root, fixture.env, []string{"--config", fixture.configPath, "--cache-dir", fixture.cache}, func(term *vttest.Terminal) string {
				openAgentsReadinessTab(t, term)
				return waitForRequiredScreen(t, term, 15*time.Second, func(text string) bool {
					for _, want := range test.want {
						if !strings.Contains(text, want) {
							return false
						}
					}
					return true
				}, "TUI did not show the expected readiness guidance")
			})
			if after := hashIntegrationTree(t, fixture.home); after != before {
				t.Fatalf("readiness wrote to HOME: %s -> %s", before, after)
			}
			if raw, err := os.ReadFile(fixture.logPath); err == nil && strings.TrimSpace(string(raw)) != "" {
				t.Fatalf("readiness invoked mutating apm commands: %s", raw)
			}
			if _, err := os.Stat(fixture.repairLog); err == nil {
				t.Fatal("readiness invoked the apm repair path")
			}
		})
	}
}

type agentsReadinessPTYFixture struct {
	bin, root, home, cache, configPath, versionPath, logPath, repairLog string
	env                                                                 []string
}

func newAgentsReadinessPTYFixture(t *testing.T, version string) agentsReadinessPTYFixture {
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
	writeIntegrationFile(t, logPath, "")
	stub := filepath.Join(home, ".test-stub-bin")
	writeExecutable(t, filepath.Join(stub, "apm"), fmt.Sprintf(`#!/bin/sh
set -eu
if [ "${1:-}" = "--version" ]; then echo "APM CLI version $(cat %q)"; exit 0; fi
printf '%%s\n' "$*" >> %q
echo '[✓] done'
`, versionPath, logPath))
	writeExecutable(t, filepath.Join(stub, "uv"), fmt.Sprintf(`#!/bin/sh
set -eu
case "$*" in
  --version) echo 'uv 0.9.0' ;;
  *) echo invoked >> %q ;;
esac
`, repairLog))
	return agentsReadinessPTYFixture{bin: bin, root: root, home: home, cache: cache, configPath: configPath, versionPath: versionPath, logPath: logPath, repairLog: repairLog, env: env}
}

func openAgentsReadinessTab(t *testing.T, term *vttest.Terminal) {
	t.Helper()
	waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Agents"), "TUI did not start")
	writeTUIKeys(t, term, "\t", "\t", "\t")
}

// hashIntegrationTree fingerprints every path, mode and byte under root so a test can prove nothing was written.
// Omni's own cache and state live under HOME in this sandbox and are not agent configuration.
func hashIntegrationTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == ".cache" || rel == ".local" {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		line := rel + " " + info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}
