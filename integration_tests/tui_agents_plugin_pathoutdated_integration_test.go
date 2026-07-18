//go:build integration

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vttest"

	"github.com/lkshrk/omni/internal/config"
)

// TestTUIAgentsTabDetectsPathOutdatedVersionlessPlugin is a real end-to-end
// check of the claude plugin outdated-detection fix: most marketplace
// entries ship no manifest version at all (plugin_rows.go's Outdated doc
// comment), so PluginRow.Outdated() falls back to PathOutdated — comparing,
// via a REAL git subprocess against a REAL clone, the plugin's own source
// subdirectory's last-touched commit at HEAD vs. at the installed commit.
// This drives the actual omni binary, a fake `claude` binary on PATH (the
// same technique as TestTUIAgentsTabRendersFakeClaudeStub), and a real git
// repo standing in for the marketplace clone — nothing about git or the
// outdated computation is mocked.
//
// The repo history is built so a naive "installed sha == repo HEAD" check
// would get BOTH plugins wrong in opposite directions:
//   - stable-plugin: installed right after an unrelated commit landed
//     (so its installed sha is "newer" than the last commit that actually
//     touched its own subdirectory) — must NOT show as outdated.
//   - drifting-plugin: installed right after its own subdirectory's first
//     commit, then modified again by a later commit — must show as
//     outdated, even though nothing about repo-HEAD-vs-installed-sha
//     equality would prove that on its own.
func TestTUIAgentsTabDetectsPathOutdatedVersionlessPlugin(t *testing.T) {
	bin := buildOmniBinary(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cache := filepath.Join(root, "cache")
	configPath := filepath.Join(root, "settings.json")
	binDir := filepath.Join(root, "bin")
	env := isolatedTUIEnv(t, home, cache)

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("create fake ~/.claude dir: %v", err)
	}
	env = append(env, "PATH="+binDir+":/usr/bin:/bin")

	marketplaceRoot := filepath.Join(home, ".claude", "plugins", "marketplaces", "acme")
	sha1, sha2, sha3 := buildPathOutdatedFixtureRepo(t, marketplaceRoot, env)

	writeClaudeInstalledPluginsJSON(t, home, map[string]string{
		"stable-plugin@acme":   sha2,
		"drifting-plugin@acme": sha1,
	})
	writeFakePathOutdatedClaudeStub(t, binDir)

	if err := config.Save(configPath, &config.RootConfig{
		Version: config.CurrentVersion,
		Agents: config.AgentsConfig{
			Marketplaces: []config.Marketplace{{Name: "acme", Source: "acme/market"}},
			Plugins: []config.Plugin{
				{Name: "stable-plugin", Marketplace: "acme"},
				{Name: "drifting-plugin", Marketplace: "acme"},
			},
		},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	runOmniCommand(t, bin, root, env, "--config", configPath, "--cache-dir", cache, "hosts", "ensure", "testhost")

	screen := runTUI(t, bin, root, env, []string{"--config", configPath, "--cache-dir", cache}, func(term *vttest.Terminal) string {
		waitForRequiredScreen(t, term, 6*time.Second, func(text string) bool {
			return strings.Contains(text, "Dashboard") && strings.Contains(text, "Tools")
		}, "TUI did not render main tabs")
		writeTUIKeys(t, term, "\t", "\t", "\t")
		return waitForRequiredScreen(t, term, 8*time.Second, func(text string) bool {
			return strings.Contains(text, "Updates Available") && strings.Contains(text, "drifting-plugin")
		}, "TUI did not render drifting-plugin under Updates Available")
	})
	if strings.Contains(strings.ToLower(screen), "error") {
		t.Fatalf("TUI showed an error while rendering agents tab; screen:\n%s", screen)
	}

	updatesIdx := strings.Index(screen, "Updates Available")
	installedIdx := strings.Index(screen, "Installed")
	if updatesIdx < 0 || installedIdx < 0 || installedIdx < updatesIdx {
		t.Fatalf("expected 'Updates Available' section before 'Installed' section, screen:\n%s", screen)
	}
	updatesSection := screen[updatesIdx:installedIdx]
	if !strings.Contains(updatesSection, "drifting-plugin") {
		t.Fatalf("expected drifting-plugin under Updates Available, screen:\n%s", screen)
	}
	if strings.Contains(updatesSection, "stable-plugin") {
		t.Fatalf("stable-plugin (unchanged since install, only an unrelated commit landed after) must NOT show under Updates Available — a naive installed-sha-vs-repo-HEAD comparison would wrongly flag it; screen:\n%s", screen)
	}
	if !strings.Contains(screen[installedIdx:], "stable-plugin") {
		t.Fatalf("expected stable-plugin under Installed, screen:\n%s", screen)
	}
	_ = sha3 // referenced only to document the fixture's final HEAD in test failure messages if needed
}

// buildPathOutdatedFixtureRepo creates a real git repo at root standing in
// for a marketplace clone, with the exact three-commit history the test's
// doc comment describes, and returns the three commit shas in order.
func buildPathOutdatedFixtureRepo(t *testing.T, root string, env []string) (sha1, sha2, sha3 string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create marketplace repo root: %v", err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(append([]string{}, env...),
			"GIT_AUTHOR_NAME=omni-test", "GIT_AUTHOR_EMAIL=omni-test@example.com",
			"GIT_COMMITTER_NAME=omni-test", "GIT_COMMITTER_EMAIL=omni-test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	writeFile := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.email", "omni-test@example.com")
	run("config", "user.name", "omni-test")

	writeFile("plugins/stable-plugin/SKILL.md", "stable v1\n")
	writeFile("plugins/drifting-plugin/SKILL.md", "drifting v1\n")
	run("add", "-A")
	run("commit", "-q", "-m", "add stable-plugin and drifting-plugin")
	sha1 = run("rev-parse", "HEAD")

	writeFile("README.md", "unrelated repo-level change\n")
	run("add", "-A")
	run("commit", "-q", "-m", "unrelated change, touches neither plugin dir")
	sha2 = run("rev-parse", "HEAD")

	writeFile("plugins/drifting-plugin/SKILL.md", "drifting v2\n")
	run("add", "-A")
	run("commit", "-q", "-m", "update drifting-plugin")
	sha3 = run("rev-parse", "HEAD")

	return sha1, sha2, sha3
}

// writeClaudeInstalledPluginsJSON writes ~/.claude/plugins/installed_plugins.json
// with one gitCommitSha entry per identity ("name@marketplace" → sha),
// mirroring the real file's shape (see readClaudeInstalledPluginShas).
func writeClaudeInstalledPluginsJSON(t *testing.T, home string, shaByIdentity map[string]string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	var b strings.Builder
	b.WriteString(`{"version":2,"plugins":{`)
	first := true
	for identity, sha := range shaByIdentity {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(`"` + identity + `":[{"scope":"user","installPath":"/x","version":"unknown","gitCommitSha":"` + sha + `"}]`)
	}
	b.WriteString(`}}`)
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write installed_plugins.json: %v", err)
	}
}

// writeFakePathOutdatedClaudeStub writes a fake `claude` binary reporting
// both fixture plugins as installed, with no version field on either (the
// common real-world case per claudeAvailableEntry's doc comment) — only a
// source path, so the outdated computation must fall back to PathOutdated.
func writeFakePathOutdatedClaudeStub(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/bin/sh
set -eu
case "$*" in
"plugins list --json --available")
  printf '{"installed":[{"id":"stable-plugin@acme","version":"unknown","scope":"user","enabled":true},{"id":"drifting-plugin@acme","version":"unknown","scope":"user","enabled":true}],"available":[{"name":"stable-plugin","marketplaceName":"acme","source":{"path":"plugins/stable-plugin"}},{"name":"drifting-plugin","marketplaceName":"acme","source":{"path":"plugins/drifting-plugin"}}]}\n'
  exit 0
  ;;
"mcp list")
  printf 'Checking MCP server health…\n'
  printf '\n'
  exit 0
  ;;
esac
exit 1
`
	path := filepath.Join(binDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude stub: %v", err)
	}
}
