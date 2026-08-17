package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func writeLedgerFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLedgerFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

func newLedgerApp(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	a := New(filepath.Join(t.TempDir(), "settings.json"), WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	return a, home
}

func TestAPMLedgerRevertsOnlyWhatTheBatchAdded(t *testing.T) {
	a, home := newLedgerApp(t)
	kept := filepath.Join(home, ".claude", "skills", "kept", "SKILL.md")
	settings := filepath.Join(home, ".claude", "settings.json")
	manifest := filepath.Join(home, ".apm", "apm.yml")
	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	writeLedgerFile(t, kept, "kept before\n")
	writeLedgerFile(t, settings, `{"hooks":{}}`)
	writeLedgerFile(t, manifest, "name: omni-host\nversion: 0.0.0\n")

	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	added := filepath.Join(home, ".claude", "skills", "orphan", "SKILL.md")
	agent := filepath.Join(home, ".claude", "agents", "orphan.md")
	writeLedgerFile(t, added, "deployed\n")
	writeLedgerFile(t, agent, "deployed\n")
	writeLedgerFile(t, settings, `{"hooks":{"PreToolUse":[]}}`)
	writeLedgerFile(t, manifest, "name: rewritten\nversion: 9.9.9\n")
	writeLedgerFile(t, lock, "lockfile_version: '2'\n")
	writeLedgerFile(t, kept, "rewritten by the install\n")

	actions := ledger.revert()
	if len(actions) == 0 {
		t.Fatal("revert reported nothing")
	}
	for _, path := range []string{added, agent, lock, filepath.Join(home, ".claude", "skills", "orphan"), filepath.Join(home, ".claude", "agents")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the reversal: %v", path, err)
		}
	}
	if got := readLedgerFile(t, settings); got != `{"hooks":{}}` {
		t.Fatalf("settings.json = %q", got)
	}
	// The manifest is omni's declarative intent, not an APM side effect: the surface that failed reinstalls from it.
	if got := readLedgerFile(t, manifest); got != "name: rewritten\nversion: 9.9.9\n" {
		t.Fatalf("manifest = %q, want the reversal to leave it alone", got)
	}
	if got := readLedgerFile(t, kept); got != "kept before\n" {
		t.Fatalf("an in-place rewrite was not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "kept")); err != nil {
		t.Fatalf("pre-existing bundle removed: %v", err)
	}
	joined := strings.Join(actions, "\n")
	for _, want := range []string{added, agent, "restored " + kept, "restored " + settings, "removed " + lock} {
		if !strings.Contains(joined, want) {
			t.Fatalf("actions %q lack %q", joined, want)
		}
	}
	if strings.Contains(joined, manifest) {
		t.Fatalf("actions %q touched the manifest", joined)
	}
}

func TestAPMLedgerRemovesSettingsFileTheBatchCreated(t *testing.T) {
	a, home := newLedgerApp(t)
	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	writeLedgerFile(t, settings, `{"hooks":{"PreToolUse":[]}}`)

	ledger.revert()
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("settings.json created by the batch survived: %v", err)
	}
}

// A codex batch deploys nowhere near claude's roots: reversing it must reach .agents and .codex, and must
// leave claude's own state alone even when the batch failed.
func TestAPMLedgerReversesANonClaudeTarget(t *testing.T) {
	a, home := newLedgerApp(t)
	claudeSkill := filepath.Join(home, ".claude", "skills", "unrelated", "SKILL.md")
	codexHooks := filepath.Join(home, ".codex", "hooks.json")
	writeLedgerFile(t, claudeSkill, "unrelated\n")
	writeLedgerFile(t, codexHooks, `{"hooks":{}}`)

	ledger, err := a.snapshotAPMLedger([]string{"codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	shared := filepath.Join(home, ".agents", "skills", "orphan", "SKILL.md")
	codexAgent := filepath.Join(home, ".codex", "agents", "orphan.toml")
	writeLedgerFile(t, shared, "deployed\n")
	writeLedgerFile(t, codexAgent, "deployed\n")
	writeLedgerFile(t, codexHooks, `{"hooks":{"PreToolUse":[]}}`)
	writeLedgerFile(t, claudeSkill, "rewritten by something else\n")

	actions := ledger.revert()
	for _, path := range []string{shared, codexAgent} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived the reversal: %v", path, err)
		}
	}
	if got := readLedgerFile(t, codexHooks); got != `{"hooks":{}}` {
		t.Fatalf("codex hooks.json = %q", got)
	}
	if got := readLedgerFile(t, claudeSkill); got != "rewritten by something else\n" {
		t.Fatalf("a root outside the batch's targets was reverted: %q", got)
	}
	if !strings.Contains(strings.Join(actions, "\n"), shared) {
		t.Fatalf("actions %v lack the shared-root reversal", actions)
	}
}

func TestAPMLedgerLeavesADeployedRootAlone(t *testing.T) {
	a, home := newLedgerApp(t)
	skill := filepath.Join(home, ".claude", "skills", "existing", "SKILL.md")
	writeLedgerFile(t, skill, "before\n")
	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if actions := ledger.revert(); len(actions) != 0 {
		t.Fatalf("revert touched an unchanged host: %v", actions)
	}
	if got := readLedgerFile(t, skill); got != "before\n" {
		t.Fatalf("skill = %q", got)
	}
}

type hookedExecutor struct {
	executor.MockExecutor
	before func(name string, args []string)
}

func (e *hookedExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return e.RunEnv(ctx, nil, name, args...)
}

func (e *hookedExecutor) RunEnv(ctx context.Context, env []string, name string, args ...string) (string, string, error) {
	if e.before != nil {
		e.before(name, args)
	}
	return e.MockExecutor.RunEnv(ctx, env, name, args...)
}

func TestAgentsSyncAllReversesAFailedInstallBatch(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/shared", Agents: []string{"codex"}, Ref: "v1"},
		}},
	}
	a, _, _ := newSyncApp(t, cfg, "")
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(home, ".agents", "skills", "acme-shared", "SKILL.md")
	lock := filepath.Join(home, ".apm", "apm.lock.yaml")
	hooked := &hookedExecutor{before: func(name string, _ []string) {
		if name != "apm" {
			return
		}
		writeLedgerFile(t, orphan, "deployed before the failure\n")
		writeLedgerFile(t, lock, "lockfile_version: '2'\n")
	}}
	hooked.Responses = []executor.MockCall{{Stderr: "install aborted\n", Err: errors.New("exit status 1")}}
	a.SetFallbackExecutor(hooked)

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "install aborted") {
		t.Fatalf("errors = %v, want the failed batch recorded", res.Errors)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Fatalf("orphaned skill survived: %v", statErr)
	}
	if _, statErr := os.Stat(lock); !os.IsNotExist(statErr) {
		t.Fatalf("lockfile written by the failed batch survived: %v", statErr)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), orphan) {
		t.Fatalf("reversal not surfaced: %#v", res.Warnings)
	}
}

func TestAgentsSyncAllKeepsDeployedFilesWhenTheInstallSucceeds(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents: config.AgentsConfig{Packages: []config.SkillPackage{
			{Source: "acme/shared", Agents: []string{"codex"}},
		}},
	}
	a, _, _ := newSyncApp(t, cfg, "")
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	deployed := filepath.Join(home, ".agents", "skills", "acme-shared", "SKILL.md")
	hooked := &hookedExecutor{before: func(name string, _ []string) {
		if name == "apm" {
			writeLedgerFile(t, deployed, "deployed\n")
		}
	}}
	a.SetFallbackExecutor(hooked)

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := readLedgerFile(t, deployed); got != "deployed\n" {
		t.Fatalf("deployed skill = %q", got)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("successful install reported reversals: %#v", res.Warnings)
	}
}

func TestAgentsSyncAllReversesAFailedFrozenReplay(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
		Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/shared\n"
	a, _, _ := newSyncApp(t, cfg, manifest)
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(home, ".codex", "agents", "stray.toml")
	hooked := &hookedExecutor{before: func(name string, _ []string) {
		if name == "apm" {
			writeLedgerFile(t, orphan, "deployed before the failure\n")
		}
	}}
	hooked.Responses = []executor.MockCall{{Stderr: "replay aborted\n", Err: errors.New("exit status 1")}}
	a.SetFallbackExecutor(hooked)

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{Frozen: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "replay aborted") {
		t.Fatalf("errors = %v, want the failed replay recorded", res.Errors)
	}
	if _, statErr := os.Stat(orphan); !os.IsNotExist(statErr) {
		t.Fatalf("orphaned agent survived: %v", statErr)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), orphan) {
		t.Fatalf("reversal not surfaced: %#v", res.Warnings)
	}
}

func TestInstallAPMSurfaceSkipsTheLedgerInDryRun(t *testing.T) {
	a, home := newLedgerApp(t)
	mock := &executor.MockExecutor{}
	a.SetFallbackExecutor(mock)
	orphan := filepath.Join(home, ".agents", "skills", "preview", "SKILL.md")
	writeLedgerFile(t, orphan, "untouched\n")

	_, reverted, err := a.installAPMSurface(context.Background(), a.APMClient(apm.Global), apm.SurfacePackages, []string{"codex"}, nil, apm.InstallOptions{DryRun: true})
	if err != nil || reverted != nil {
		t.Fatalf("reverted = %v, err = %v", reverted, err)
	}
	if got := readLedgerFile(t, orphan); got != "untouched\n" {
		t.Fatalf("dry run mutated the host: %q", got)
	}
}

func TestAPMLedgerWarnsAboutAFileTooLargeToSnapshot(t *testing.T) {
	a, home := newLedgerApp(t)
	big := filepath.Join(home, ".claude", "skills", "huge", "SKILL.md")
	writeLedgerFile(t, big, strings.Repeat("x", apmLedgerFileLimit+1))

	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, big, "rewritten by the install\n")

	actions := ledger.revert()
	if !strings.Contains(strings.Join(actions, "\n"), "too large to snapshot") {
		t.Fatalf("actions %v lack the size-cap warning", actions)
	}
	if got := readLedgerFile(t, big); got != "rewritten by the install\n" {
		t.Fatalf("an unsnapshotted file was restored from nothing: %q", got)
	}
}

// A deployed path the install turned into a symlink is left alone: restoring through it would rewrite
// whatever it now points at, which is no longer a file the batch owned.
func TestAPMLedgerRefusesToRestoreThroughASymlink(t *testing.T) {
	a, home := newLedgerApp(t)
	skill := filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")
	outside := filepath.Join(home, "unrelated.md")
	writeLedgerFile(t, skill, "before\n")
	writeLedgerFile(t, outside, "someone else's file\n")

	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, skill); err != nil {
		t.Fatal(err)
	}

	actions := ledger.revert()
	if got := readLedgerFile(t, outside); got != "someone else's file\n" {
		t.Fatalf("the reversal wrote through the symlink: %q", got)
	}
	if !strings.Contains(strings.Join(actions, "\n"), "non-regular") {
		t.Fatalf("actions %v lack the refusal", actions)
	}
}

// The permissions are part of the pre-install state even when the bytes came back on their own.
func TestAPMLedgerRestoresAModeTheInstallChanged(t *testing.T) {
	a, home := newLedgerApp(t)
	skill := filepath.Join(home, ".claude", "skills", "demo", "SKILL.md")
	writeLedgerFile(t, skill, "before\n")

	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skill, 0o755); err != nil {
		t.Fatal(err)
	}

	ledger.revert()
	info, err := os.Stat(skill)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want the pre-install permissions back", info.Mode().Perm())
	}
}

// Claude rewrites ~/.claude.json while the install runs, so only its mcp registry may be reverted; a
// whole-file restore would silently drop everything the agent wrote in the meantime.
func TestAPMLedgerRevertsOnlyTheMcpEntriesOfALiveClientConfig(t *testing.T) {
	a, home := newLedgerApp(t)
	claudeConfig := filepath.Join(home, ".claude.json")
	writeLedgerFile(t, claudeConfig, `{"mcpServers":{"kept":{"url":"https://kept.example"}},"projects":{"old":1}}`)

	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, claudeConfig,
		`{"mcpServers":{"kept":{"url":"https://kept.example"},"added":{}},"projects":{"old":1,"new":2}}`)

	ledger.revert()
	var got struct {
		McpServers map[string]any `json:"mcpServers"`
		Projects   map[string]any `json:"projects"`
	}
	if err := json.Unmarshal([]byte(readLedgerFile(t, claudeConfig)), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.McpServers["added"]; ok {
		t.Fatalf("mcpServers = %v, want the server the failed install registered removed", got.McpServers)
	}
	if _, ok := got.McpServers["kept"]; !ok {
		t.Fatalf("mcpServers = %v, want the pre-install server back", got.McpServers)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("projects = %v, want the concurrent write preserved", got.Projects)
	}
}

func TestAPMLedgerWarnsAboutAClientConfigTooLargeToSnapshot(t *testing.T) {
	a, home := newLedgerApp(t)
	claudeConfig := filepath.Join(home, ".claude.json")
	writeLedgerFile(t, claudeConfig, strings.Repeat("x", apmLedgerFileLimit+1))

	ledger, err := a.snapshotAPMLedger([]string{"claude"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, claudeConfig, "rewritten by the install\n")

	actions := ledger.revert()
	if !strings.Contains(strings.Join(actions, "\n"), "too large to snapshot") {
		t.Fatalf("actions %v lack the size-cap warning", actions)
	}
	if got := readLedgerFile(t, claudeConfig); got != "rewritten by the install\n" {
		t.Fatalf("an unsnapshotted client config was restored from nothing: %q", got)
	}
}

func TestAPMLedgerRestoresPerTargetMcpConfigs(t *testing.T) {
	home := t.TempDir()
	claudeDir := t.TempDir()
	a := New(filepath.Join(t.TempDir(), "settings.json"), WithEnvLookup(func(name string) string {
		switch name {
		case "HOME":
			return home
		case "CLAUDE_CONFIG_DIR":
			return claudeDir
		}
		return ""
	}))
	claudeConfig := filepath.Join(claudeDir, ".claude.json")
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	writeLedgerFile(t, claudeConfig, `{"mcpServers":{"kept":{}}}`)

	ledger, err := a.snapshotAPMLedger([]string{"claude", "codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeLedgerFile(t, claudeConfig, `{"mcpServers":{"kept":{},"added":{}}}`)
	writeLedgerFile(t, codexConfig, "[mcp_servers.added]\n")

	ledger.revert()
	if got := readLedgerFile(t, claudeConfig); got != `{"mcpServers":{"kept":{}}}` {
		t.Fatalf("claude mcp config = %q", got)
	}
	if _, err := os.Stat(codexConfig); !os.IsNotExist(err) {
		t.Fatalf("codex config created by the failed install survived: %v", err)
	}
}

// A failed batch must leave the manifest and the ownership record at their pre-sync state, or the next sync
// sees the prune as already done and never retries it.
func TestAgentsSyncAllFailedPruneStaysRetryable(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"codex"}},
	}
	manifest := "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: acme/dropped\n      targets:\n        - codex\n"
	a, _, _ := newSyncApp(t, cfg, manifest)
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(home, ".apm", apmOwnedSidecarName)
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	writeLedgerFile(t, sidecar, `{"packages":["acme/dropped"],"mcp":[]}`)

	failing := &executor.MockExecutor{Responses: []executor.MockCall{{Stderr: "prune aborted\n", Err: errors.New("exit status 1")}}}
	a.SetFallbackExecutor(failing)
	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "prune aborted") {
		t.Fatalf("errors = %v, want the failed prune recorded", res.Errors)
	}
	if got := readLedgerFile(t, manifestPath); strings.Contains(got, "acme/dropped") {
		t.Fatalf("manifest after the failed prune = %q, want this host's intent written anyway", got)
	}
	// The record still naming the entry is what makes the next sync see a prune it owns and has not applied.
	if got := readLedgerFile(t, sidecar); !strings.Contains(got, "acme/dropped") {
		t.Fatalf("ownership record after the failed prune = %q", got)
	}

	retry := &executor.MockExecutor{}
	a.SetFallbackExecutor(retry)
	if _, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readLedgerFile(t, manifestPath); strings.Contains(got, "acme/dropped") {
		t.Fatalf("the retry did not re-attempt the prune: %q", got)
	}
	want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
	if calls := apmCalls(retry); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("retry calls = %#v, want one %v", calls, want)
	}
}

// Each surface owns its own reversal: rolling the MCP failure back to the pre-sync manifest trio would erase
// the package install that already succeeded, which on a first sync means all three files.
func TestAgentsSyncAllKeepsTheSucceededPackageSurfaceWhenMcpFails(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{
			Packages:   []config.SkillPackage{{Source: "acme/shared", Agents: []string{"claude-code"}}},
			McpServers: []config.McpServer{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"}},
		},
	}
	a, _, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{}))
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	a.SetFallbackExecutor(&executor.MockExecutor{Responses: []executor.MockCall{
		{},
		{Stderr: "mcp install aborted\n", Err: errors.New("exit status 1")},
	}})

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "mcp install aborted") {
		t.Fatalf("errors = %v, want the failed mcp install recorded", res.Errors)
	}
	if got := readLedgerFile(t, filepath.Join(home, ".apm", "apm.yml")); !strings.Contains(got, "acme/shared") {
		t.Fatalf("manifest after the failed mcp install = %q, want the package surface intact", got)
	}
	owned := readAPMOwnedIdentities(filepath.Join(home, ".apm", "apm.yml"))
	if !reflect.DeepEqual(owned.Applied.Packages, []string{"acme/shared"}) {
		t.Fatalf("package ownership = %v, want the surface that succeeded advanced", owned.Applied.Packages)
	}
	if len(owned.Applied.Mcp) != 0 {
		t.Fatalf("mcp ownership = %v, want the failed surface left where it was", owned.Applied.Mcp)
	}
	if !reflect.DeepEqual(owned.Rendered.Mcp, []string{"linear"}) {
		t.Fatalf("rendered mcp = %v, want the entry the failed install left in the manifest still owned", owned.Rendered.Mcp)
	}
}

// The manifest is what APM installs from, so reverting it for the surface that failed leaves the next
// surface installing from a file that no longer declares anything it was asked to deploy.
func TestAgentsSyncAllInstallsMcpFromTheManifestAPackageFailureLeftBehind(t *testing.T) {
	for _, tc := range []struct{ name, manifest string }{
		{"fresh host", ""},
		{"established host", "name: test\nversion: 1.0.0\ndependencies:\n  apm:\n  - git: acme/prior\n    targets:\n    - claude\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.RootConfig{Version: config.CurrentVersion,
				Settings: config.Settings{AgentsUse: []string{"claude-code"}},
				Agents: config.AgentsConfig{
					Packages:   []config.SkillPackage{{Source: "acme/shared", Agents: []string{"claude-code"}}},
					McpServers: []config.McpServer{{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"}},
				},
			}
			a, _, _ := newSyncApp(t, cfg, tc.manifest, WithMcpAdapters([]McpAdapter{}))
			home, err := a.apmDeployRoot()
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(home, ".apm", "apm.yml")

			var atMcpInstall string
			failing := &hookedExecutor{before: func(name string, args []string) {
				if name == "apm" && slices.Contains(args, "mcp") {
					atMcpInstall = readLedgerFile(t, manifestPath)
				}
			}}
			failing.Responses = []executor.MockCall{{Stderr: "package install aborted\n", Err: errors.New("exit status 1")}}
			a.SetFallbackExecutor(failing)

			res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(syncErrorText(res), "package install aborted") {
				t.Fatalf("errors = %v, want the package failure recorded", res.Errors)
			}
			if !strings.Contains(atMcpInstall, "linear") {
				t.Fatalf("manifest at the mcp install = %q, want this sync's server declaration", atMcpInstall)
			}
			owned := readAPMOwnedIdentities(manifestPath)
			if !reflect.DeepEqual(owned.Applied.Mcp, []string{"linear"}) {
				t.Fatalf("mcp ownership = %v, want the surface that succeeded advanced", owned.Applied.Mcp)
			}
			if len(owned.Applied.Packages) != 0 {
				t.Fatalf("package ownership = %v, want the failed surface left where it was", owned.Applied.Packages)
			}
			if !reflect.DeepEqual(owned.Rendered.Packages, []string{"acme/shared"}) {
				t.Fatalf("rendered packages = %v, want the entry the failed install left in the manifest still owned", owned.Rendered.Packages)
			}
		})
	}
}

// Clearing a native registration happens before the install, so the reversal has to reach back past it.
func TestAgentsSyncAllRestoresClaudeStateWhenTheMcpInstallFails(t *testing.T) {
	cfg := &config.RootConfig{Version: config.CurrentVersion,
		Settings: config.Settings{AgentsUse: []string{"claude-code"}},
		Agents: config.AgentsConfig{McpServers: []config.McpServer{
			{Name: "linear", Transport: "http", URL: "https://linear.example/mcp"},
		}},
	}
	a, _, _ := newSyncApp(t, cfg, "", WithMcpAdapters([]McpAdapter{}))
	home, err := a.apmDeployRoot()
	if err != nil {
		t.Fatal(err)
	}
	claudeConfig := filepath.Join(home, ".claude.json")
	before := `{"mcpServers":{"linear":{"url":"https://linear.example/mcp"}}}`
	writeLedgerFile(t, claudeConfig, before)

	failing := &executor.MockExecutor{Responses: []executor.MockCall{{Stderr: "install aborted\n", Err: errors.New("exit status 1")}}}
	a.SetFallbackExecutor(failing)
	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(syncErrorText(res), "install aborted") {
		t.Fatalf("errors = %v, want the failed mcp install recorded", res.Errors)
	}
	if got := readLedgerFile(t, claudeConfig); got != before {
		t.Fatalf("claude state after the failed install = %q, want the pre-sync registration back", got)
	}
}
