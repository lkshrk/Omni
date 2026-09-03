//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

func TestCLIAndTUIReadOnlyQueriesProduceEquivalentSemanticObservations(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	observationPath := filepath.Join(root, "tui-observation.json")
	env := append(isolatedTUIEnv(t, home, cache), "OMNI_TEST_TUI_OBSERVATION="+observationPath)
	writeIntegrationFile(t, filepath.Join(home, ".apm", "apm.yml"), "name: parity\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm: []\n  mcp: []\n")
	writeIntegrationFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "dependencies: []\n")
	repo := filepath.Join(home, "dotfiles")
	initDotsRepo(t, repo, env)
	source := filepath.Join(repo, "dotfiles", "nvim", ".config", "nvim", "init.lua")
	target := filepath.Join(home, ".config", "nvim", "init.lua")
	writeIntegrationFile(t, source, "repo\n")
	writeIntegrationFile(t, target, "local\n")
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DotsRepo: repo, DisabledProviders: []string{"brew", "apt", "apk", "dnf", "pacman", "zypper", "node", "bun", "pnpm", "npm", "python", "uv", "pip"}},
		Tools:    map[string]config.ToolSpec{"fixture": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}}},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "fixture"}}, Dots: []config.DotEntry{{Name: "nvim", Path: filepath.Dir(target)}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	seedTUIToolCache(t, cache, &database.ToolCache{
		Name: "fixture", Provider: "brew", Package: "fixture", Installed: true, InstalledWith: "brew",
		Version: sql.NullString{String: "1.0.0", Valid: true}, Tracked: true,
	})
	writeExecutable(t, filepath.Join(home, ".test-stub-bin", "apm"), `#!/bin/sh
case "$*" in
  --version) echo 'Agent Package Manager (APM) CLI version 0.29.0' ;;
  'audit --ci --format json') echo '{"passed":true,"checks":[{"name":"lockfile-exists","passed":true,"message":"Lockfile present","details":[]}]}' ;;
  'outdated -g --parallel-checks 4') echo '[✓] All dependencies are up-to-date' ;;
  *) exit 0 ;;
esac
`)

	runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 7*time.Second, screenHas("Dashboard", "Tools"), "TUI did not start")
		waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			packet, err := readOnlyObservationPacket(observationPath)
			return err == nil && packet.Doctor != nil
		}, "TUI did not publish the accepted doctor result")
		writeTUIKeys(t, term, "\t")
		waitForRequiredScreen(t, term, 10*time.Second, func(text string) bool {
			packet, err := readOnlyObservationPacket(observationPath)
			return err == nil && len(packet.Tools) > 0 && bytes.Contains([]byte(text), []byte("fixture"))
		}, "TUI did not publish the accepted tools list")
		writeTUIKeys(t, term, "\t")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			packet, err := readOnlyObservationPacket(observationPath)
			return err == nil && packet.Dots != nil
		}, "TUI did not publish the accepted dots status")
	})

	var cliTools []readOnlyToolObservation
	decodeReadOnlyCLI(t, bin, root, env, &cliTools, "--config", configPath, "--cache-dir", cache, "tools", "list", "--format", "json")
	var cliDoctor app.DoctorResult
	decodeReadOnlyCLI(t, bin, root, env, &cliDoctor, "--config", configPath, "--cache-dir", cache, "doctor", "--format", "json")
	var cliDots readOnlyDotsObservation
	decodeReadOnlyCLI(t, bin, root, env, &cliDots, "--config", configPath, "--cache-dir", cache, "dots", "status", "--format", "json")
	packet, err := readOnlyObservationPacket(observationPath)
	if err != nil {
		t.Fatal(err)
	}
	sortReadOnlyTools(cliTools)
	sortReadOnlyTools(packet.Tools)
	if !reflect.DeepEqual(cliTools, packet.Tools) {
		t.Fatalf("tools.list semantic query differs\nCLI: %#v\nTUI: %#v", cliTools, packet.Tools)
	}
	normalizeReadOnlyDots(&cliDots)
	normalizeReadOnlyDots(packet.Dots)
	if !reflect.DeepEqual(cliDots, *packet.Dots) {
		t.Fatalf("dots.status semantic query differs\nCLI: %#v\nTUI: %#v", cliDots, *packet.Dots)
	}
	if !reflect.DeepEqual(cliDoctor, *packet.Doctor) {
		t.Fatalf("doctor semantic query differs\nCLI: %#v\nTUI: %#v", cliDoctor, *packet.Doctor)
	}
}

type readOnlyToolObservation struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Package       string `json:"package"`
	Installed     bool   `json:"installed"`
	InstalledWith string `json:"installed_with,omitempty"`
	Version       string `json:"version,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
	Tracked       bool   `json:"tracked"`
}

type readOnlyDotsObservation struct {
	Entries   []app.DotStatus `json:"entries"`
	GitStatus string          `json:"git_status"`
}

type readOnlyObservation struct {
	Tools  []readOnlyToolObservation `json:"tools"`
	Dots   *readOnlyDotsObservation  `json:"dots"`
	Doctor *app.DoctorResult         `json:"doctor"`
}

func decodeReadOnlyCLI(t *testing.T, bin, dir string, env []string, out any, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("omni %v: %v", args, ctx.Err())
	}
	if err != nil && stdout.Len() == 0 {
		t.Fatalf("omni %v: %v\n%s", args, err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		t.Fatalf("decode omni %v: %v\nstdout=%s\nstderr=%s", args, err, stdout.String(), stderr.String())
	}
}

func readOnlyObservationPacket(path string) (readOnlyObservation, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return readOnlyObservation{}, err
	}
	var packet readOnlyObservation
	err = json.Unmarshal(raw, &packet)
	return packet, err
}

func sortReadOnlyTools(tools []readOnlyToolObservation) {
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Name != tools[j].Name {
			return tools[i].Name < tools[j].Name
		}
		return tools[i].Provider < tools[j].Provider
	})
}

func normalizeReadOnlyDots(observation *readOnlyDotsObservation) {
	if observation == nil {
		return
	}
	for i := range observation.Entries {
		observation.Entries[i].LastError = ""
	}
}
