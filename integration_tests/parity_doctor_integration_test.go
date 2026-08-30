//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

var selectedDoctorRow = regexp.MustCompile(`(?m)^>.*Doctor`)

func TestCLIAndTUIDoctorFixProduceEquivalentSemanticState(t *testing.T) {
	bin := buildOmniBinary(t)
	cliRoot, tuiRoot := newParityTwins(t)
	seedDoctorFixParity(t, cliRoot)
	seedDoctorFixParity(t, tuiRoot)

	runOmniCommand(t, bin, cliRoot.root, cliRoot.env,
		"--config", cliRoot.configPath, "--cache-dir", cliRoot.cache, "doctor", "--fix")
	runTUI(t, bin, tuiRoot.root, tuiRoot.env, []string{"--config", tuiRoot.configPath, "--cache-dir", tuiRoot.cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
			return strings.Contains(text, "Doctor") && strings.Contains(strings.ToLower(text), "warn")
		}, "TUI doctor did not report duplicate config")
		sendTUIKey(term, uv.KeyHome)
		for range 12 {
			if selectedDoctorRow.MatchString(currentScreenText(term)) {
				break
			}
			writeTUIKeys(t, term, "j")
			if _, ok := waitForScreen(term, 300*time.Millisecond, func(text string) bool {
				return selectedDoctorRow.MatchString(text)
			}); ok {
				break
			}
		}
		if !selectedDoctorRow.MatchString(currentScreenText(term)) {
			t.Fatalf("TUI did not select Doctor row; screen:\n%s", currentScreenText(term))
		}
		writeTUIKeys(t, term, "f")
		return waitForRequiredScreen(t, term, 12*time.Second, func(string) bool {
			return doctorParityFixed(tuiRoot)
		}, "TUI doctor fix did not rewrite duplicate config")
	})

	cliState := observeDoctorFixParity(t, cliRoot)
	tuiState := observeDoctorFixParity(t, tuiRoot)
	if cliState != tuiState {
		t.Fatalf("doctor fix semantic state differs\nCLI: %#v\nTUI: %#v", cliState, tuiState)
	}
}

func seedDoctorFixParity(t *testing.T, sandbox *paritySandbox) {
	t.Helper()
	writeIntegrationFile(t, filepath.Join(sandbox.home, ".config", "omni", "apm.yml"), "name: parity\nversion: 1.0.0\ntargets: [codex]\ndependencies:\n  apm: []\n  mcp: []\n")
	writeExecutable(t, filepath.Join(sandbox.home, ".test-stub-bin", "apm"), `#!/bin/sh
case "$*" in
  --version) echo 'Agent Package Manager (APM) CLI version 0.29.0' ;;
  'audit --ci --format json') echo '{"passed":true,"checks":[{"name":"lockfile-exists","passed":true,"message":"Lockfile present","details":[]}]}' ;;
  *) exit 64 ;;
esac
`)
	writeIntegrationFile(t, sandbox.configPath, `{
  "$include":["settings.d/dots.json"],
  "settings":{"disabled_providers":["apt","apk","dnf","pacman","zypper","brew","node","bun","pnpm","npm","python","uv","pip"]},
  "hosts":{"testhost":["dev"]},
  "groups":[{"name":"testhost","special":"host"},{"name":"dev","dots":[{"name":"git","path":"~/.gitconfig"}]}]
}`)
	writeIntegrationFile(t, filepath.Join(sandbox.root, "settings.d", "dots.json"), `{"groups":[{"name":"dev","dots":[{"name":"git","path":"~/.gitconfig"}]}]}`)
}

func doctorParityFixed(sandbox *paritySandbox) bool {
	cfg, err := config.Load(sandbox.configPath)
	return err == nil && len(cfg.MergeNotices) == 0
}

type doctorFixState struct {
	Main    string
	Include string
}

func observeDoctorFixParity(t *testing.T, sandbox *paritySandbox) doctorFixState {
	t.Helper()
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	return doctorFixState{
		Main:    read(sandbox.configPath),
		Include: read(filepath.Join(sandbox.root, "settings.d", "dots.json")),
	}
}
