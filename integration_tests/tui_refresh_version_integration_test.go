//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func stubVersionedBinary(t *testing.T, home, name, banner string) string {
	t.Helper()
	dir := filepath.Join(home, ".test-stub-bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' " + shellQuote(banner) + "; exit 0; fi\nexit 0\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// These tools have no recorded version, so refresh must probe the installed binary.
func TestScriptToolReportsProbedVersion(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	stubVersionedBinary(t, home, "widget", "widget 4.5.6 (built somewhere)")

	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"node", "python", "pip"}},
		Tools: map[string]config.ToolSpec{
			"widget": {Providers: []config.ToolInstallSpec{{
				Provider: "script",
				Options: map[string]string{
					"install": "true",
					"check":   "command -v widget",
				},
			}}},
		},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "widget"}}}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "refresh")
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")

	if !strings.Contains(out, "4.5.6") {
		t.Fatalf("tools list did not report the probed version:\n%s", out)
	}
}

// A recipe's binary may differ from its logical tool name, so refresh must probe the recorded executable.
func TestRecipeToolProbesTheRecordedBinaryNotTheToolName(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	env := isolatedTUIEnv(t, home, cache)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	stubVersionedBinary(t, home, "gadget", "gadget v7.8.9")
	// The recipe's default check is `test -x <bin_dir>/<bin>`, so the binary must live there too.
	if err := os.Symlink(filepath.Join(home, ".test-stub-bin", "gadget"), filepath.Join(binDir, "gadget")); err != nil {
		t.Fatalf("link stub into bin dir: %v", err)
	}

	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatalf("create cache: %v", err)
	}
	if err := config.Save(configPath, &config.RootConfig{
		Version:  config.CurrentVersion,
		Settings: config.Settings{DisabledProviders: []string{"node", "python", "pip"}, FallbackBinDir: binDir},
		Tools: map[string]config.ToolSpec{
			"gadget-tool": {Providers: []config.ToolInstallSpec{{
				Provider: "script",
				Bin:      "gadget",
				BinDir:   binDir,
				Source:   &config.FallbackSource{Type: config.FallbackSourceGitHub, Owner: "example", Repo: "gadget"},
				Recipe: &config.FallbackRecipe{
					Type:         config.FallbackRecipeGitHubReleaseAsset,
					AssetPattern: "gadget-linux-amd64.tar.gz",
				},
			}}},
		},
		Groups: []*config.GroupConfig{{Name: "testhost", Special: "host", Tools: []config.ToolEntry{{Name: "gadget-tool"}}}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "refresh")
	out := runOmniOutput(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "tools", "list")

	if !strings.Contains(out, "7.8.9") {
		t.Fatalf("tools list did not report the version of the recipe's binary:\n%s", out)
	}
}
