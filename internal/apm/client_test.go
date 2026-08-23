package apm_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	commandexec "github.com/lkshrk/omni/internal/executor"
)

func TestClientRunPreservesCommandResult(t *testing.T) {
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{Stdout: "out\n", Stderr: "warning\n"}}}
	result, err := apm.New(mock, apm.Global).Run(t.Context(), "search", "security@skills")
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "out\n" || result.Stderr != "warning\n" {
		t.Fatalf("result = %#v", result)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, []string{"search", "security@skills"}) {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestClientRunRejectsEmptyCommand(t *testing.T) {
	mock := &commandexec.MockExecutor{}
	for _, args := range [][]string{nil, {""}, {"  "}} {
		if _, err := apm.New(mock, apm.Global).Run(t.Context(), args...); err == nil {
			t.Fatalf("Run(%q) succeeded", args)
		}
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(mock.Calls))
	}
}

func TestClientMarketplaceRemoveIsNonInteractive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{}}}
	if _, err := apm.New(mock, apm.Global).Run(t.Context(), "marketplace", "remove", "team"); err != nil {
		t.Fatal(err)
	}
	want := []string{"marketplace", "remove", "team", "--yes"}
	got := mock.Calls[0].Args
	if len(got) < len(want) || !reflect.DeepEqual(got[len(got)-len(want):], want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestClientNonzeroPreservesResultAndExitError(t *testing.T) {
	sentinel := errors.New("exit status 7")
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{Stdout: "partial\n", Stderr: "failed\n", Err: sentinel}}}
	result, err := apm.New(mock, apm.Global).Run(t.Context(), "outdated", "-g")
	if result.Stdout != "partial\n" || result.Stderr != "failed\n" {
		t.Fatalf("result = %#v", result)
	}
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "apm outdated failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestClientMapsMissingExecutable(t *testing.T) {
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{Err: &exec.Error{Name: "apm", Err: exec.ErrNotFound}}}}
	_, err := apm.New(mock, apm.Global).Run(t.Context(), "search", "x")
	if !errors.Is(err, apm.ErrNotInstalled) {
		t.Fatalf("error = %v, want ErrNotInstalled", err)
	}
	if !strings.Contains(err.Error(), "omni doctor --fix") || strings.Contains(err.Error(), "install apm-cli") {
		t.Fatalf("error = %q, want only the pinned doctor repair path", err)
	}
}

func TestClientCommandWorkingDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	home := t.TempDir()
	bin := t.TempDir()
	caller := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(caller); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	script := filepath.Join(bin, "apm")
	if err := os.WriteFile(script, []byte("#!/bin/sh\npwd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(home, ".apm")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"install short global", []string{"install", "-g"}, global},
		{"install long global", []string{"install", "--global", "org/pkg"}, global},
		{"update global", []string{"update", "-g", "--yes"}, global},
		{"update long global", []string{"update", "--global", "--yes"}, global},
		{"outdated short global", []string{"outdated", "-g"}, global},
		{"outdated global", []string{"outdated", "--global"}, global},
		{"uninstall global", []string{"uninstall", "-g", "org/pkg"}, global},
		{"uninstall long global", []string{"uninstall", "--global", "org/pkg"}, global},
		{"deps list global", []string{"deps", "list", "-g"}, global},
		{"deps why global", []string{"deps", "why", "--global", "org/pkg"}, global},
		{"view global", []string{"view", "--global", "org/pkg"}, global},
		{"marketplace add", []string{"marketplace", "add", "team", "https://example.test"}, global},
		{"marketplace list", []string{"marketplace", "list"}, global},
		{"marketplace browse", []string{"marketplace", "browse", "team"}, global},
		{"marketplace update", []string{"marketplace", "update", "team"}, global},
		{"marketplace validate", []string{"marketplace", "validate", "team"}, global},
		{"marketplace remove", []string{"marketplace", "remove", "team"}, global},
		{"audit", []string{"audit", "--ci"}, global},
		{"prune", []string{"prune"}, global},
		{"targets", []string{"targets", "--json"}, caller},
		{"project install", []string{"install"}, caller},
		{"project update", []string{"update"}, caller},
		{"project outdated", []string{"outdated"}, caller},
		{"project uninstall", []string{"uninstall", "org/pkg"}, caller},
		{"project deps", []string{"deps", "list"}, caller},
		{"project view", []string{"view", "org/pkg"}, caller},
		{"search", []string{"search", "skill"}, caller},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := apm.New(commandexec.New(), apm.Global).Run(t.Context(), tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(result.Stdout); got != tt.want {
				t.Fatalf("cwd = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientCancellationReachesAPM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := filepath.Join(bin, "apm")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := apm.New(commandexec.New(), apm.Global).Run(ctx, "search", "x")
	if err == nil || ctx.Err() != context.DeadlineExceeded || time.Since(started) > time.Second {
		t.Fatalf("error = %v, context = %v, elapsed = %s", err, ctx.Err(), time.Since(started))
	}
}

func TestLegacySurfaceInstallStillRejectsProjectScope(t *testing.T) {
	_, err := apm.New(&commandexec.MockExecutor{}, apm.Project).InstallOnly(t.Context(), apm.SurfacePackages, nil, apm.InstallOptions{})
	if !errors.Is(err, apm.ErrUnsupportedScope) {
		t.Fatalf("error = %v, want ErrUnsupportedScope", err)
	}
}

func TestDryRunOnlyUsesProjectScopeWithoutGlobalFlag(t *testing.T) {
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{}}}
	client := apm.New(mock, apm.Global).AtProjectRoot(t.TempDir())
	if _, err := client.DryRunOnly(t.Context(), apm.SurfacePackages, []string{"codex", "claude"}, []string{"TOKEN"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--dry-run", "--only", "apm", "--target", "codex,claude"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
		t.Fatalf("calls=%#v want=%v", mock.Calls, want)
	}
	if _, err := apm.New(mock, apm.Global).DryRunOnly(t.Context(), apm.SurfacePackages, nil, nil); !errors.Is(err, apm.ErrUnsupportedScope) {
		t.Fatalf("global error=%v", err)
	}
}

func TestAuditGlobalScrubsReviewedEnvironment(t *testing.T) {
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{}}}
	if _, err := apm.New(mock, apm.Global).AuditGlobal(t.Context(), []string{"TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, []string{"audit", "--ci", "--format", "json"}) || !slices.Contains(mock.Calls[0].Env, "TOKEN") {
		t.Fatalf("calls=%#v", mock.Calls)
	}
}

func TestDryRunOnlySupportsMCPSurfaceAndScrubsEnv(t *testing.T) {
	mock := &commandexec.MockExecutor{Responses: []commandexec.MockCall{{}}}
	client := apm.New(mock, apm.Global).AtProjectRoot(t.TempDir())
	if _, err := client.DryRunOnly(t.Context(), apm.SurfaceMcp, []string{"codex"}, []string{"TOKEN"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"install", "--dry-run", "--only", "mcp", "--target", "codex"}
	if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) || !reflect.DeepEqual(mock.Calls[0].Env, []string{"TOKEN"}) {
		t.Fatalf("calls=%#v want args=%v env=[TOKEN]", mock.Calls, want)
	}
}
