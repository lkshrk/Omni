package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsSyncAllRunsOneGlobalAPMInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n", Stderr: "warning\n"},
	}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	var gotStdout, gotStderr string
	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{
		Frozen: true,
		Output: func(stdout, stderr string) { gotStdout, gotStderr = stdout, stderr },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "installed\n" || result.Stderr != "warning\n" || gotStdout != result.Output || gotStderr != result.Stderr {
		t.Fatalf("result = %#v, callback = (%q, %q)", result, gotStdout, gotStderr)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %d, want version + install", len(mock.Calls))
	}
	call := mock.Calls[1]
	wantArgs := []string{"install", "-g", "--frozen"}
	if call.Dir != filepath.Join(home, ".apm") || call.Name != "apm" || !reflect.DeepEqual(call.Args, wantArgs) {
		t.Fatalf("call = dir %q %s %v, want dir %q apm %v", call.Dir, call.Name, call.Args, filepath.Join(home, ".apm"), wantArgs)
	}
}

func TestAgentsSyncAllWithoutManifestIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil || !reflect.DeepEqual(result, AgentsSyncAllResult{}) {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %#v, want none without a manifest", mock.Calls)
	}
}

func TestAgentsSyncAllDoesNotHideInvalidWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(home, ".apm"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, want invalid workspace error", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("calls = %#v, want none for invalid workspace", mock.Calls)
	}
}

func TestRunAPMPreservesNonzeroResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sentinel := errors.New("exit status 9")
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "partial\n", Stderr: "failed\n", Err: sentinel},
	}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result, err := a.RunAPM(t.Context(), "outdated", "-g")
	if result.Stdout != "partial\n" || result.Stderr != "failed\n" || !errors.Is(err, sentinel) {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestRunAPMForwardsStableTargets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n"},
	}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result, err := a.RunAPM(t.Context(), "install", "-g", "org/pkg", "--target", "antigravity,hermes")
	if err != nil || result.Stdout != "installed\n" || len(mock.Calls) != 2 || mock.Calls[1].Args[0] != "install" {
		t.Fatalf("result = %#v, error = %v, calls = %#v", result, err, mock.Calls)
	}
}

func TestRunAPMRejectsUnpinnedVersionBeforeMutation(t *testing.T) {
	for _, version := range []string{"0.27.9", "0.29.1", "0.28", "0.28.0rc1", "0.28.0-dev", "0.28.0+build.1"} {
		t.Run(version, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "APM CLI version " + version + "\n"}}}
			a := New(filepath.Join(t.TempDir(), "settings.json"))
			a.SetFallbackExecutor(mock)

			_, err := a.RunAPM(t.Context(), "marketplace", "update", "team")
			if err == nil {
				t.Fatal("expected version rejection")
			}
			if len(mock.Calls) != 1 {
				t.Fatalf("calls = %#v; mutation must not run", mock.Calls)
			}
		})
	}
}

func TestFirstSyncFromTemplateWithoutLockfileSucceeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configHome := filepath.Join(home, "config")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	template, err := AgentsTemplatePath()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, template, `name: omni-apm-coder
version: 1.0.0
targets: [claude, codex]
dependencies:
  apm:
    - git: https://github.com/acme/one.git
    - git: https://github.com/acme/two.git
    - git: https://github.com/acme/three.git
  mcp:
    - name: context-mode
      command: npx
      args: [context-mode]
  lsp:
    - name: gopls
      command: gopls
`)
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "[✓] installed\n"},
	}}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.StateDir = filepath.Join(home, "state", "omni")
	a.SetFallbackExecutor(mock)

	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if len(result.Notices) != 1 || !strings.Contains(result.Notices[0], "not installed yet") {
		t.Fatalf("notices = %q", result.Notices)
	}
	if len(mock.Calls) != 2 || mock.Calls[1].Name != "apm" || !reflect.DeepEqual(mock.Calls[1].Args, []string{"install", "-g"}) {
		t.Fatalf("calls = %+v", mock.Calls)
	}
	live, err := os.ReadFile(filepath.Join(home, ".apm", "apm.yml"))
	if err != nil || !strings.Contains(string(live), "name: omni-apm-coder") {
		t.Fatalf("live manifest = %q, err=%v", live, err)
	}
}
