package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/flock"
)

func setupGuardEnv(t *testing.T, manifest, lock string) (*App, *executor.MockExecutor) { //nolint:unparam
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), manifest)
	if lock != "" {
		writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), lock)
	}
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(home, "settings.json"))
	a.StateDir = t.TempDir()
	a.SetFallbackExecutor(mock)
	return a, mock
}

const guardLSPManifest = `name: host
targets:
- claude
dependencies:
  lsp:
  - name: gopls
    command: gopls
`

const guardCodexOnlyManifest = `name: host
targets:
- codex
dependencies:
  mcp:
  - name: probe
    registry: false
    transport: stdio
    command: probe
  lsp:
  - name: gopls
    command: gopls
`

func TestAgentsSyncAllRefusesFrozenSyncWithUnlockedLSP(t *testing.T) {
	for name, dryRun := range map[string]bool{"install": false, "dry run": true} {
		t.Run(name, func(t *testing.T) {
			a, mock := setupGuardEnv(t, guardLSPManifest, "dependencies: []\n")
			_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{Frozen: true, DryRun: dryRun})
			if err == nil || !strings.Contains(err.Error(), `lsp server "gopls" is not in the lockfile`) {
				t.Fatalf("err = %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsSyncAllRefusesLSPWithoutASupportedTarget(t *testing.T) {
	for name, dryRun := range map[string]bool{"install": false, "dry run": true} {
		t.Run(name, func(t *testing.T) {
			a, mock := setupGuardEnv(t, guardCodexOnlyManifest, "")
			_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{DryRun: dryRun})
			if err == nil || !strings.Contains(err.Error(), "declare target claude or copilot") {
				t.Fatalf("err = %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsSyncAllGuardsAllowValidManifests(t *testing.T) {
	for name, tc := range map[string]struct{ manifest, lock string }{
		"frozen with locked lsp": {guardLSPManifest, "dependencies: []\nlsp_servers:\n- gopls\n"},
		"no targets declared":    {"name: host\ndependencies:\n  lsp:\n  - name: gopls\n    command: gopls\n", "dependencies: []\nlsp_servers:\n- gopls\n"},
		"no lsp entries":         {"name: host\ntargets:\n- codex\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			setupGuardEnv(t, tc.manifest, tc.lock)
			if err := checkAgentsLSPHazards(AgentsSyncAllOptions{Frozen: true}); err != nil {
				t.Fatalf("guard rejected a valid manifest: %v", err)
			}
		})
	}
}

func TestAgentsSyncAllGuardFailurePreservesTemplateWarning(t *testing.T) {
	a, mock := setupGuardEnv(t, guardCodexOnlyManifest, "")
	writeFile(t, filepath.Join(agentsTemplateDirForTest(t), "apm.yml"), "name: template\n")

	res, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil {
		t.Fatal("guard did not fire")
	}
	if !strings.Contains(res.Warning, "force-template") {
		t.Fatalf("materialization warning lost: %q", res.Warning)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func agentsTemplateDirForTest(t *testing.T) string {
	t.Helper()
	path, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(path)
}

func TestAgentsSyncAllGuardsSkipUnparseableManifest(t *testing.T) {
	setupGuardEnv(t, "not-a-mapping\n", "")
	if err := checkAgentsLSPHazards(AgentsSyncAllOptions{Frozen: true}); err != nil {
		t.Fatalf("guard claimed an apm validation error: %v", err)
	}
}

const guardLockedLSPManifest = `name: host
targets:
- claude
dependencies:
  lsp:
  - name: gopls
    command: gopls
`

func corruptGuardLock(t *testing.T, mode os.FileMode) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".apm", "apm.lock.yaml")
	writeFile(t, path, "dependencies: [\n")
	if mode != 0 {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	}
}

func TestAgentsSyncAllTargetGuardSurvivesABrokenLockfile(t *testing.T) {
	for name, mode := range map[string]os.FileMode{"corrupt": 0, "unreadable": 0o000} {
		t.Run(name, func(t *testing.T) {
			if mode == 0o000 && os.Geteuid() == 0 {
				t.Skip("root bypasses file permissions")
			}
			a, mock := setupGuardEnv(t, guardCodexOnlyManifest, "")
			corruptGuardLock(t, mode)
			_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
			if err == nil || !strings.Contains(err.Error(), "declare target claude or copilot") {
				t.Fatalf("err = %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsSyncAllFrozenFailsClosedOnABrokenLockfile(t *testing.T) {
	a, mock := setupGuardEnv(t, guardLockedLSPManifest, "")
	corruptGuardLock(t, 0)
	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{Frozen: true})
	if err == nil || !strings.Contains(err.Error(), "frozen sync: cannot verify lockfile") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllFrozenWithAbsentLockfileReportsUnlockedLSP(t *testing.T) {
	a, mock := setupGuardEnv(t, guardLockedLSPManifest, "")
	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{Frozen: true})
	if err == nil || !strings.Contains(err.Error(), `lsp server "gopls" is not in the lockfile`) {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesMissingMigrationWrapperBeforeAPM(t *testing.T) {
	state := t.TempDir()
	missing := filepath.Join(state, "agents-migration", "bundles", strings.Repeat("a", 64))
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(missing, ""))

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "missing migration wrapper") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesCorruptMigrationWrapperBeforeAPM(t *testing.T) {
	state := t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, state, "owner", "")
	writeFile(t, filepath.Join(wrapper, "apm.yml"), "corrupt\n")
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(wrapper, ""))

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "corrupt migration wrapper") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesDuplicateMigrationOwner(t *testing.T) {
	state := t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, state, "owner", "")
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(wrapper, wrapper))

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "duplicate migration owner") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesDuplicateMigrationChild(t *testing.T) {
	state := t.TempDir()
	first := writeMigrationSyncWrapper(t, state, "first", "shared")
	second := writeMigrationSyncWrapper(t, state, "second", "shared")
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(first, second))

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), `duplicate migration child mcp "shared"`) {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllAllowsValidMigrationWrapper(t *testing.T) {
	state := t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, state, "owner", "owned")
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(wrapper, ""))
	mock.Responses = []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n"},
	}

	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil || result.Output != "installed\n" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(mock.Calls) != 2 || mock.Calls[1].Name != "apm" || !slices.Equal(mock.Calls[1].Args, []string{"install", "-g", "--trust-transitive-mcp"}) {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesUnreadableManifestBeforeAPM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".apm", "apm.yml"), 0o700); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(home, "settings.json"))
	a.SetFallbackExecutor(mock)

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "read APM manifest") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesMigrationWrapperThroughSymlinkedAncestor(t *testing.T) {
	state, outside := t.TempDir(), t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, outside, "owner", "")
	if err := os.Symlink(filepath.Join(outside, "agents-migration"), filepath.Join(state, "agents-migration")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	escaped := filepath.Join(state, "agents-migration", "bundles", filepath.Base(wrapper))
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(escaped, ""))

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "symlinked migration wrapper path") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllRefusesOutsideAliasIntoMigrationWrapperRoot(t *testing.T) {
	state := t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, state, "owner", "")
	outside := t.TempDir()
	alias := filepath.Join(outside, "bundles")
	if err := os.Symlink(filepath.Join(state, "agents-migration", "bundles"), alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	a, mock := setupMigrationSyncGuard(t, state, migrationGuardManifest(filepath.Join(alias, filepath.Base(wrapper)), ""))

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "noncanonical migration wrapper path") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func TestAgentsSyncAllCanonicalizesMigrationOwnerIdentities(t *testing.T) {
	local := t.TempDir()
	for name, dependencies := range map[string]string{
		"local path": "  - path: " + local + "\n  - path: " + local + string(filepath.Separator) + ".\n",
		"git":        "  - git: https://GitHub.com/Org/Repo.git/\n  - git: org/repo\n",
		"marketplace": "  - name: Plugin\n    marketplace: Team\n" +
			"  - name: plugin\n    marketplace: team\n",
	} {
		t.Run(name, func(t *testing.T) {
			a, mock := setupMigrationSyncGuard(t, t.TempDir(), agentsMigrationMarker+"\nname: omni-migrated\ndependencies:\n  apm:\n"+dependencies)
			_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
			if err == nil || !strings.Contains(err.Error(), "duplicate migration owner") {
				t.Fatalf("err = %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsSyncAllRedactsMigrationManifestYAMLError(t *testing.T) {
	const secret = "TOP_SECRET_VALUE"
	a, mock := setupMigrationSyncGuard(t, t.TempDir(), agentsMigrationMarker+"\nname: omni-migrated\ndependencies: "+secret+"\n")

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid migration-managed manifest") || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

type blockingMigrationSyncExecutor struct {
	*executor.MockExecutor
	started chan struct{}
	release chan struct{}
}

func (e *blockingMigrationSyncExecutor) RunDirEnv(ctx context.Context, dir string, env []string, name string, args ...string) (string, string, error) {
	if name == "apm" && len(args) > 0 && args[0] == "install" {
		close(e.started)
		<-e.release
	}
	return e.MockExecutor.RunDirEnv(ctx, dir, env, name, args...)
}

func TestAgentsSyncAllHoldsMigrationLockThroughAPMInstall(t *testing.T) {
	state := t.TempDir()
	wrapper := writeMigrationSyncWrapper(t, state, "owner", "")
	a, _ := setupMigrationSyncGuard(t, state, migrationGuardManifest(wrapper, ""))
	exec := &blockingMigrationSyncExecutor{
		MockExecutor: &executor.MockExecutor{Responses: []executor.MockCall{
			{Stdout: "APM CLI version " + apmVersionPin + "\n"},
			{Stdout: "installed\n"},
		}},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	a.SetFallbackExecutor(exec)
	done := make(chan error, 1)
	go func() {
		_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
		done <- err
	}()
	<-exec.started
	template, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(template), 0o700); err != nil {
		t.Fatal(err)
	}
	lockFile, err := os.OpenFile(filepath.Join(filepath.Dir(template), ".omni-config.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	locked, lockErr := flock.TryLock(lockFile)
	close(exec.release)
	syncErr := <-done
	if locked {
		_ = flock.Unlock(lockFile)
	}
	if lockErr != nil || locked || syncErr != nil {
		t.Fatalf("try lock = %v/%v, sync err = %v", locked, lockErr, syncErr)
	}
	locked, lockErr = flock.TryLock(lockFile)
	if lockErr != nil || !locked {
		t.Fatalf("lock remained held after sync: %v/%v", locked, lockErr)
	}
	_ = flock.Unlock(lockFile)
}

func setupMigrationSyncGuard(t *testing.T, state, manifest string) (*App, *executor.MockExecutor) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, ".apm", "apm.yml"), manifest)
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(home, "settings.json"))
	a.StateDir = state
	a.SetFallbackExecutor(mock)
	return a, mock
}

func writeMigrationSyncWrapper(t *testing.T, state, owner, mcp string) string {
	t.Helper()
	children := map[string]agentBundleChild{}
	if mcp != "" {
		dep := apmMCPDep{Name: mcp, Transport: "stdio", Command: "echo"}
		children[childKey("mcp", mcp)] = agentBundleChild{Kind: "mcp", ID: mcp, MCP: &dep}
	}
	wrapper, err := buildBundleWrapper(agentBundleOwner{Name: owner, Children: children}, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := materializeAgentBundleWrappers(agentBundlePlan{Wrappers: []agentBundleWrapper{wrapper}}); err != nil {
		t.Fatal(err)
	}
	return wrapper.Path
}

func migrationGuardManifest(first, second string) string {
	manifest := agentsMigrationMarker + "\nname: omni-migrated\nversion: 1.0.0\ndependencies:\n  apm:\n  - path: " + first + "\n"
	if second != "" {
		manifest += "  - path: " + second + "\n"
	}
	return manifest
}
