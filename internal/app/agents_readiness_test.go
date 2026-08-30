package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func newAgentsReadinessApp(t *testing.T, available bool, responses ...executor.MockCall) (*App, *availExecutor, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	configPath := filepath.Join(home, "config", "omni", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: map[string]bool{"apm": available}}
	mock.Responses = responses
	a := New(configPath)
	a.StateDir = filepath.Join(home, "state", "omni")
	a.SetFallbackExecutor(mock)
	return a, mock, home
}

func pinnedVersionResponse() executor.MockCall {
	return executor.MockCall{Stdout: "APM CLI version " + apmVersionPin + "\n"}
}

func writeAgentsWorkspaceFile(t *testing.T, home, name, body string) string {
	t.Helper()
	path := filepath.Join(home, ".apm", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentsReadinessStates(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
		want  AgentsReadinessState
	}{
		{name: "empty", want: AgentsReadinessEmpty},
		{name: "template only", setup: func(t *testing.T, _ string) {
			path, _ := AgentsTemplatePath()
			writeFile(t, path, "name: staged\n")
		}, want: AgentsReadinessTemplateOnly},
		{name: "live incomplete", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
		}, want: AgentsReadinessLiveIncomplete},
		{name: "lock only", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
		}, want: AgentsReadinessLockOnly},
		{name: "ready", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
			writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
		}, want: AgentsReadinessReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
			if test.setup != nil {
				test.setup(t, home)
			}
			got, err := a.AgentsReadiness(context.Background())
			if err != nil || got.State != test.want {
				t.Fatalf("readiness = %+v, err=%v; want %s", got, err, test.want)
			}
		})
	}
}

func TestAgentsReadinessRejectsUnsafeOrUnreadableWorkspaceFiles(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "symlink", setup: func(t *testing.T, home string) {
			target := writeAgentsWorkspaceFile(t, home, "target", "name: live\n")
			if err := os.Symlink(target, filepath.Join(home, ".apm", "apm.yml")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "directory", setup: func(t *testing.T, home string) {
			if err := os.MkdirAll(filepath.Join(home, ".apm", "apm.yml"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unreadable", setup: func(t *testing.T, home string) {
			path := writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid yaml", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "dependencies: [\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
			test.setup(t, home)
			got, err := a.AgentsReadiness(context.Background())
			if err != nil || got.State != AgentsReadinessInvalid || len(got.Details) == 0 {
				t.Fatalf("readiness = %+v, err=%v", got, err)
			}
		})
	}
}

func TestAgentsReadinessRejectsUnsafeWorkspaceDirectory(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "symlink", setup: func(t *testing.T, home string) {
			target := t.TempDir()
			if err := os.Symlink(target, filepath.Join(home, ".apm")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "non-directory", setup: func(t *testing.T, home string) {
			if err := os.WriteFile(filepath.Join(home, ".apm"), []byte("not a directory\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
			test.setup(t, home)
			got, err := a.AgentsReadiness(context.Background())
			if err != nil || got.State != AgentsReadinessInvalid || len(got.Details) == 0 {
				t.Fatalf("readiness = %+v, err=%v", got, err)
			}
		})
	}
}

func TestAPMRepairErrorsAreTypedWithoutHidingRuntimeErrors(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		a, _, _ := newAgentsReadinessApp(t, false)
		_, err := a.AgentsReadiness(context.Background())
		var repair *APMRepairError
		if !errors.As(err, &repair) || repair.Kind != APMRepairMissing {
			t.Fatalf("error = %T %v", err, err)
		}
	})
	t.Run("mismatch", func(t *testing.T) {
		a, _, _ := newAgentsReadinessApp(t, true, executor.MockCall{Stdout: "APM CLI version 0.28.0\n"})
		_, err := a.AgentsReadiness(context.Background())
		var repair *APMRepairError
		if !errors.As(err, &repair) || repair.Kind != APMRepairVersionMismatch || repair.Installed != "0.28.0" {
			t.Fatalf("error = %T %v", err, err)
		}
	})
	t.Run("unparseable", func(t *testing.T) {
		a, _, _ := newAgentsReadinessApp(t, true, executor.MockCall{Stdout: "unknown\n"})
		_, err := a.AgentsReadiness(context.Background())
		var repair *APMRepairError
		if !errors.As(err, &repair) || repair.Kind != APMRepairVersionUnparseable {
			t.Fatalf("error = %T %v", err, err)
		}
	})
	t.Run("executor", func(t *testing.T) {
		permissionErr := os.ErrPermission
		a, _, _ := newAgentsReadinessApp(t, true, executor.MockCall{Err: permissionErr})
		_, err := a.AgentsReadiness(context.Background())
		var repair *APMRepairError
		if !errors.Is(err, permissionErr) || errors.As(err, &repair) {
			t.Fatalf("error = %T %v", err, err)
		}
	})
	t.Run("context", func(t *testing.T) {
		a, _, _ := newAgentsReadinessApp(t, true, executor.MockCall{Err: context.Canceled})
		_, err := a.AgentsReadiness(context.Background())
		var repair *APMRepairError
		if !errors.Is(err, context.Canceled) || errors.As(err, &repair) {
			t.Fatalf("error = %T %v", err, err)
		}
	})
}

func TestAgentsOutdatedDoesNotInvokeAPMWithoutCompleteWorkspace(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "missing"},
		{name: "live incomplete", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
		}},
		{name: "lock only", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
		}},
		{name: "invalid", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "dependencies: [\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, mock, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
			if test.setup != nil {
				test.setup(t, home)
			}
			if _, err := a.AgentsOutdated(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(mock.Calls) != 1 || strings.Join(mock.Calls[0].Args, " ") != "--version" {
				t.Fatalf("calls = %+v", mock.Calls)
			}
		})
	}
}

func TestAgentsOutdatedProbesVersionBeforeWorkspace(t *testing.T) {
	a, mock, home := newAgentsReadinessApp(t, true, executor.MockCall{Stdout: "APM CLI version 0.28.0\n"})
	writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
	writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
	if _, err := a.AgentsOutdated(context.Background()); err == nil {
		t.Fatal("expected version mismatch")
	}
	if len(mock.Calls) != 1 || strings.Join(mock.Calls[0].Args, " ") != "--version" {
		t.Fatalf("calls = %+v", mock.Calls)
	}
}

func TestAgentsOutdatedInvokesAPMForReadyWorkspace(t *testing.T) {
	a, mock, home := newAgentsReadinessApp(t, true, pinnedVersionResponse(), executor.MockCall{})
	writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
	writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
	if _, err := a.AgentsOutdated(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 2 || strings.Join(mock.Calls[1].Args, " ") != "outdated -g --parallel-checks 4" {
		t.Fatalf("calls = %+v", mock.Calls)
	}
}

func writeDefaultMigrationSnapshot(t *testing.T, a *App, host string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(a.ConfigPath), ".omni-apm-migration-backup-test")
	mustWriteBundleFile(t, filepath.Join(dir, "omni-config-000.json"), `{
  "agents": {"mcp_servers": [{"name":"independent","transport":"stdio","command":"independent-mcp"}]},
  "groups": [{"name":"g","mcp_servers":["independent"]}],
  "hosts": {"`+host+`" :["g"]}
}`)
	mustWriteBundleFile(t, filepath.Join(dir, "paths.json"), `{"omni-config-000.json":"/tmp/settings.json"}`)
	return dir
}

func writeSuppressedMigrationSnapshot(t *testing.T, a *App) {
	t.Helper()
	snapshot := filepath.Join(filepath.Dir(a.ConfigPath), ".omni-apm-migration-backup-test")
	ownerRoot := filepath.Join(snapshot, "owner")
	mustWriteBundleFile(t, filepath.Join(ownerRoot, ".codex-plugin", "plugin.json"), `{"name":"bundle-a","version":"1.0.0"}`)
	mustWriteBundleFile(t, filepath.Join(ownerRoot, "mcp.json"), `{"mcpServers":{"owned":{"type":"stdio","command":"node","args":["${PLUGIN_ROOT}/server.js"]}}}`)
	mustWriteBundleFile(t, filepath.Join(ownerRoot, "server.js"), "process.exit(0)\n")
	original := filepath.Join(t.TempDir(), "bundle-a")
	mustWriteBundleFile(t, filepath.Join(snapshot, "omni-config-000.json"), `{
  "agents": {
    "plugins": [{"name":"bundle-a","path":"`+original+`"}],
    "mcp_servers": [{"name":"owned","transport":"stdio","command":"node `+original+`/server.js"}]
  },
  "groups": [{"name":"g","plugins":["bundle-a"],"mcp_servers":["owned"]}],
  "hosts": {"h":["g"]}
}`)
	paths, err := json.Marshal(map[string]string{"omni-config-000.json": "/tmp/settings.json", "owner": original})
	if err != nil {
		t.Fatal(err)
	}
	mustWriteBundleFile(t, filepath.Join(snapshot, "paths.json"), string(paths))
}

func TestAgentsPrepareOnboardingAutoStagesOnlyTemplate(t *testing.T) {
	a, mock, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
	writeDefaultMigrationSnapshot(t, a, "h")
	got, err := a.AgentsPrepareOnboarding(context.Background(), "h")
	if err != nil || !got.AutoStaged || got.Readiness.State != AgentsReadinessTemplateOnly {
		t.Fatalf("result = %+v, err=%v", got, err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("APM calls = %+v", mock.Calls)
	}
	if _, err := os.Stat(got.Readiness.TemplatePath); err != nil {
		t.Fatalf("template not staged: %v", err)
	}
	for _, name := range []string{"apm.yml", "apm.lock.yaml"} {
		if _, err := os.Stat(filepath.Join(home, ".apm", name)); !os.IsNotExist(err) {
			t.Fatalf("onboarding created live APM state %s: %v", name, err)
		}
	}
}

func TestAgentsPrepareOnboardingRefusalsDoNotMutate(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *App, string)
		host  string
	}{
		{name: "no snapshot", host: "h"},
		{name: "unknown host", host: "wrong", setup: func(t *testing.T, a *App, _ string) { writeDefaultMigrationSnapshot(t, a, "h") }},
		{name: "suppressed declarations", host: "h", setup: func(t *testing.T, a *App, _ string) { writeSuppressedMigrationSnapshot(t, a) }},
		{name: "existing live state", host: "h", setup: func(t *testing.T, _ *App, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: existing\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, mock, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
			if test.setup != nil {
				test.setup(t, a, home)
			}
			got, err := a.AgentsPrepareOnboarding(context.Background(), test.host)
			if err != nil {
				t.Fatal(err)
			}
			if got.AutoStaged || len(mock.Calls) != 1 {
				t.Fatalf("result=%+v calls=%+v", got, mock.Calls)
			}
			template, _ := AgentsTemplatePath()
			if _, err := os.Stat(template); !os.IsNotExist(err) {
				t.Fatalf("refusal created template: %v", err)
			}
		})
	}
}

func preparedOnboardingMigration(t *testing.T, a *App) (agentBundlePlan, []preparedAgentBundleWrapper, string) {
	t.Helper()
	writeSuppressedMigrationSnapshot(t, a)
	snapshot, err := a.defaultSnapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	plan, rendered, err := a.planAgentsMigration("h", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareAgentBundleWrappers(plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { discardPreparedAgentBundleWrappers(prepared) })
	return plan, prepared, rendered
}

func TestCommitAgentsOnboardingLockedHonorsCancellationBeforePublish(t *testing.T) {
	a, _, _ := newAgentsReadinessApp(t, true)
	plan, prepared, rendered := preparedOnboardingMigration(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := commitAgentsOnboardingLocked(ctx, a.StateDir, plan, prepared, rendered); !errors.Is(err, context.Canceled) {
		t.Fatalf("commit error = %v", err)
	}
	template, _ := AgentsTemplatePath()
	if _, err := os.Stat(template); !os.IsNotExist(err) {
		t.Fatalf("cancelled commit published template: %v", err)
	}
	assertNoPublishedMigrationWrappers(t, a.StateDir)
}

func TestCommitAgentsOnboardingLockedRechecksStateAfterWrapperPreparation(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "live manifest", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: raced\n")
		}},
		{name: "lockfile", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
		}},
		{name: "template", setup: func(t *testing.T, _ string) {
			template, _ := AgentsTemplatePath()
			writeFile(t, template, "name: raced\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, home := newAgentsReadinessApp(t, true)
			plan, prepared, rendered := preparedOnboardingMigration(t, a)
			test.setup(t, home)
			if _, err := commitAgentsOnboardingLocked(context.Background(), a.StateDir, plan, prepared, rendered); err == nil || !strings.Contains(err.Error(), "state changed") {
				t.Fatalf("commit error = %v", err)
			}
			template, _ := AgentsTemplatePath()
			if test.name == "template" {
				if raw, err := os.ReadFile(template); err != nil || string(raw) != "name: raced\n" {
					t.Fatalf("raced template changed: %q, err=%v", raw, err)
				}
			} else if _, err := os.Stat(template); !os.IsNotExist(err) {
				t.Fatalf("raced commit published template: %v", err)
			}
			assertNoPublishedMigrationWrappers(t, a.StateDir)
		})
	}
}

func assertNoPublishedMigrationWrappers(t *testing.T, stateDir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(stateDir, "agents-migration", "bundles", strings.Repeat("?", 64)))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("published migration wrappers = %v", paths)
	}
}
