package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func newAgentsReadinessApp(t *testing.T, available bool, responses ...executor.MockCall) (*App, *availExecutor, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configHome := filepath.Join(home, "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
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
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\ndependencies:\n  apm:\n    - git: https://github.com/acme/tool.git\n")
		}, want: AgentsReadinessLiveIncomplete},
		{name: "empty manifest needs no lock", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\ndependencies:\n  apm: []\n  mcp: []\n")
		}, want: AgentsReadinessReady},
		{name: "lock without manifest", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")
		}, want: AgentsReadinessInvalid},
		{name: "lsp manifest still needs a lock", setup: func(t *testing.T, home string) {
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\ndependencies:\n  apm: []\n  mcp: []\n  lsp:\n    - name: gopls\n      command: gopls\n")
		}, want: AgentsReadinessLiveIncomplete},
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
			got, err := a.AgentsReadiness(context.Background(), "testhost")
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
			got, err := a.AgentsReadiness(context.Background(), "testhost")
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
			got, err := a.AgentsReadiness(context.Background(), "testhost")
			if err != nil || got.State != AgentsReadinessInvalid || len(got.Details) == 0 {
				t.Fatalf("readiness = %+v, err=%v", got, err)
			}
		})
	}
}

func TestAgentsReadinessMissingAPMIsInvalidWithFixHint(t *testing.T) {
	for _, test := range []struct {
		name      string
		available bool
		responses []executor.MockCall
	}{
		{name: "missing"},
		{name: "mismatch", available: true, responses: []executor.MockCall{{Stdout: "APM CLI version 0.28.0\n"}}},
		{name: "unparseable", available: true, responses: []executor.MockCall{{Stdout: "unknown\n"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, _, _ := newAgentsReadinessApp(t, test.available, test.responses...)
			got, err := a.AgentsReadiness(context.Background(), "testhost")
			if err != nil || got.State != AgentsReadinessInvalid {
				t.Fatalf("readiness = %+v, err=%v", got, err)
			}
			if len(got.Details) != 2 || got.Details[1] != "run omni doctor --fix" {
				t.Fatalf("details = %q", got.Details)
			}
		})
	}
}

func TestAPMRepairErrorsDoNotHideRuntimeErrors(t *testing.T) {
	t.Run("executor", func(t *testing.T) {
		permissionErr := os.ErrPermission
		a, _, _ := newAgentsReadinessApp(t, true, executor.MockCall{Err: permissionErr})
		_, err := a.AgentsReadiness(context.Background(), "testhost")
		var repair *APMRepairError
		if !errors.Is(err, permissionErr) || errors.As(err, &repair) {
			t.Fatalf("error = %T %v", err, err)
		}
	})
	t.Run("context", func(t *testing.T) {
		a, _, _ := newAgentsReadinessApp(t, true, executor.MockCall{Err: context.Canceled})
		_, err := a.AgentsReadiness(context.Background(), "testhost")
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
			writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\ndependencies:\n  apm:\n    - git: https://github.com/acme/tool.git\n")
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

func TestAgentsOutdatedSkipsLocklessReadyWorkspace(t *testing.T) {
	a, mock, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
	writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\ndependencies:\n  mcp: []\n")
	if _, err := a.AgentsOutdated(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 1 || strings.Join(mock.Calls[0].Args, " ") != "--version" {
		t.Fatalf("calls = %+v", mock.Calls)
	}
}

// hashTree fingerprints every path, mode and byte under root so a test can prove nothing was written.
func hashTree(t *testing.T, root string) string {
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

func TestAgentsReadinessNeverWrites(t *testing.T) {
	a, mock, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
	legacy := `{"agents":{"mcp_servers":[{"name":"legacy","transport":"stdio","command":"legacy-mcp"}]},"groups":[{"name":"g","mcp_servers":["legacy"]}],"hosts":{"coder":["g"]}}`
	if err := os.WriteFile(a.ConfigPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"native":{"command":"npx","args":["native-mcp"]}}}`)
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
		t.Fatal(err)
	}

	before := hashTree(t, home)
	got, err := a.AgentsReadiness(context.Background(), "coder")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != AgentsReadinessEmpty || got.CTA != AgentsCTAMigrate {
		t.Fatalf("readiness = %+v", got)
	}
	if !slices.Contains(got.Details, "run omni agents migrate --host coder") {
		t.Fatalf("details = %q", got.Details)
	}
	if after := hashTree(t, home); after != before {
		t.Fatalf("readiness wrote to HOME: %s -> %s", before, after)
	}
	if len(mock.Calls) != 1 || strings.Join(mock.Calls[0].Args, " ") != "--version" {
		t.Fatalf("calls = %+v", mock.Calls)
	}
}

func TestAgentsReadinessAsksForMigrateWithReadyWorkspace(t *testing.T) {
	a, _, home := newAgentsReadinessApp(t, true, pinnedVersionResponse())
	if err := os.WriteFile(a.ConfigPath, []byte(`{"agents":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAgentsWorkspaceFile(t, home, "apm.yml", "name: live\n")
	writeAgentsWorkspaceFile(t, home, "apm.lock.yaml", "dependencies: []\n")

	before := hashTree(t, home)
	got, err := a.AgentsReadiness(context.Background(), "coder")
	if err != nil || got.State != AgentsReadinessReady || got.CTA != AgentsCTAMigrate {
		t.Fatalf("readiness = %+v, err=%v", got, err)
	}
	if after := hashTree(t, home); after != before {
		t.Fatalf("readiness wrote to HOME: %s -> %s", before, after)
	}
}
