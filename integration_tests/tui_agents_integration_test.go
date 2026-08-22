//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

func TestTUIAgentsTabSyncsMCPThroughRealAPM(t *testing.T) {
	apmBin, err := exec.LookPath("apm")
	if err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := append(isolatedTUIEnv(t, home, cache),
		"PATH="+filepath.Join(home, ".test-stub-bin")+string(os.PathListSeparator)+filepath.Dir(apmBin)+string(os.PathListSeparator)+minimalTestPATH,
	)

	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	apmDir := filepath.Join(home, ".apm")
	if err := os.MkdirAll(apmDir, 0o700); err != nil {
		t.Fatalf("create APM workspace: %v", err)
	}
	manifest := `name: omni-tui
version: 1.0.0
targets: [codex]
dependencies:
  apm: []
  mcp:
    - name: omni-tui
      registry: false
      transport: http
      url: https://example.invalid/mcp
`
	if err := os.WriteFile(filepath.Join(apmDir, "apm.yml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write APM manifest: %v", err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 4*time.Second, func(text string) bool {
			return strings.Contains(text, "Agent packages are managed by Microsoft APM") &&
				strings.Contains(text, "~/.apm/apm.yml")
		}, "TUI did not render the APM workspace")

		writeTUIKeys(t, term, "S")
		return waitForRequiredScreen(t, term, 7*time.Second, func(text string) bool {
			return strings.Contains(text, "apm install -g") && !strings.Contains(text, "running apm install -g")
		}, "TUI did not show APM sync success")
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while syncing MCP through APM; screen:\n%s", screen)
	}

	for path, wants := range map[string][]string{
		filepath.Join(home, ".apm", "apm.yml"):       {"omni-tui", "https://example.invalid/mcp"},
		filepath.Join(home, ".apm", "apm.lock.yaml"): {"codex", "omni-tui"},
		filepath.Join(home, ".codex", "config.toml"): {"omni-tui", "https://example.invalid/mcp"},
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read APM state %s: %v", path, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(content), want) {
				t.Fatalf("%s missing %q:\n%s", path, want, content)
			}
		}
	}
}

func TestTUIAgentsOnboardingPreviewConfirmAndApply(t *testing.T) {
	apmBin, err := exec.LookPath("apm")
	if err != nil {
		t.Fatalf("integration tests require apm on PATH: %v", err)
	}
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := append(isolatedTUIEnv(t, home, cache), "APM_E2E_TESTS=1", "PATH="+filepath.Join(home, ".test-stub-bin")+string(os.PathListSeparator)+filepath.Dir(apmBin)+string(os.PathListSeparator)+minimalTestPATH)
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(home, ".agents", "skills", "tui-native")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# TUI native\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(skill, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "bin", "run"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")
	legacyTarget := createTUILegacyRepo(t, filepath.Join(root, "legacy-target"), "target")
	legacyOne := createTUILegacyRepo(t, filepath.Join(root, "legacy-one"), "one")
	legacyTwo := createTUILegacyRepo(t, filepath.Join(root, "legacy-two"), "two")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(configData, &raw); err != nil {
		t.Fatal(err)
	}
	raw["agents"] = map[string]any{"skills": []any{map[string]any{"name": "target-choice", "source": "file://" + legacyTarget}, map[string]any{"name": "collision", "source": "file://" + legacyOne, "agents": []string{"codex"}}, map[string]any{"name": "collision", "source": "file://" + legacyTwo, "agents": []string{"codex"}}}, "mcp_servers": []any{map[string]any{"name": "secret-api", "transport": "http", "url": "https://example.invalid/mcp", "env_literal": map[string]string{"TOKEN": "literal"}, "agents": []string{"codex"}}}, "ignore": map[string]any{"skills": []string{"durably-ignored"}}}
	raw["groups"] = []any{map[string]any{"name": "later", "skills": []string{"target-choice"}}}
	configData, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool { return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools") }, "TUI did not render")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		waitForRequiredScreen(t, term, 12*time.Second, func(text string) bool {
			return strings.Contains(text, "Agent packages are managed") && !strings.Contains(text, "Running doctor")
		}, "TUI did not open Agents")
		writeTUIKeys(t, term, "O")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Agent onboarding preview:")
		}, "TUI did not show onboarding plan")
		writeTUIKeys(t, term, "esc")
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool {
			return strings.Contains(text, "Agent onboarding preview:") && !strings.Contains(text, "Apply this onboarding plan? y/N")
		}, "TUI cancellation failed")
		if _, err := os.Stat(filepath.Join(home, ".apm")); !os.IsNotExist(err) {
			t.Fatalf("cancelled preview mutated APM state: %v", err)
		}
		writeTUIKeys(t, term, "O")
		waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool { return strings.Contains(text, "Agent onboarding preview:") }, "TUI did not reopen onboarding")
		for attempts := 0; attempts < 60; attempts++ {
			text := currentScreenText(term)
			if strings.Contains(text, "Apply this onboarding plan? y/N") {
				break
			}
			currentName := ""
			for _, name := range []string{"secret-api", "target-choice", "collision", "later", "tui-native", "durably-ignored"} {
				if strings.Contains(text, " "+name+" (") {
					currentName = name
					break
				}
			}
			key := "j"
			switch {
			case strings.Contains(text, "conditional-group-host"):
				key = "x"
			case strings.Contains(text, "legacy-unscoped-targets"):
				key = "c"
			case strings.Contains(text, "secret-field:"):
				key = "m"
			case strings.Contains(text, "executable:"):
				key = "E"
			case strings.Contains(text, "non-equivalent-name-collision"):
				key = "o"
			}
			if key != "j" {
				writeTUIKeys(t, term, key)
				waitForRequiredScreen(t, term, 3*time.Second, func(next string) bool { return next != text && strings.Contains(next, "decision=") }, "TUI resolution did not persist")
			}
			if strings.Contains(currentScreenText(term), "Apply this onboarding plan? y/N") {
				break
			}
			writeTUIKeys(t, term, "j")
			waitForRequiredScreen(t, term, 3*time.Second, func(next string) bool {
				return currentName == "" || !strings.Contains(next, " "+currentName+" (") || strings.Contains(next, "Apply this onboarding plan? y/N")
			}, "TUI item inspection did not advance")
		}
		waitForRequiredScreen(t, term, 3*time.Second, func(text string) bool { return strings.Contains(text, "Apply this onboarding plan? y/N") }, "TUI did not resolve all blockers")
		writeTUIKeys(t, term, "y")
		waitForRequiredScreen(t, term, 25*time.Second, func(text string) bool { return strings.Contains(text, "Agent onboarding complete.") }, "TUI did not complete onboarding")
		writeTUIKeys(t, term, "T")
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool { return strings.Contains(text, "Onboarding status: Omni=complete APM=complete") }, "TUI did not render joined status")
		writeTUIKeys(t, term, "X")
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Confirm cleanup? y/N") && strings.Contains(text, "Cleanup 2 path(s)")
		}, "TUI did not preview cleanup")
		writeTUIKeys(t, term, "y")
		return waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool { return strings.Contains(text, "Onboarding cleanup complete") }, "TUI did not complete cleanup")
	})
	if strings.Contains(screen, "onboarding apply:") {
		t.Fatalf("TUI onboarding failed:\n%s", screen)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "imported", "skill")); err != nil {
		t.Fatalf("APM did not import TUI skill: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(home, ".local", "state", "omni", "onboarding-cleanup")); err != nil || len(entries) == 0 {
		t.Fatalf("default cleanup tombstone missing: %v", err)
	}
}

func createTUILegacyRepo(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("# "+body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "apm.yml"), []byte("name: "+body+"\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	runCommand(t, path, env, "git", "init")
	runCommand(t, path, env, "git", "config", "user.email", "test@example.invalid")
	runCommand(t, path, env, "git", "config", "user.name", "Test")
	runCommand(t, path, env, "git", "add", ".")
	runCommand(t, path, env, "git", "commit", "-m", "init")
	return path
}
