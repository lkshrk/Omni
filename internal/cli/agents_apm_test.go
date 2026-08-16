package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsCommandsDelegateToGlobalAPM(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"sync", []string{"sync", "--frozen", "--dry-run"}, []string{"install", "--global", "--frozen", "--dry-run"}},
		{"add", []string{"add", "owner/one", "owner/two"}, []string{"install", "--global", "owner/one", "owner/two"}},
		{"remove", []string{"remove", "owner/one"}, []string{"uninstall", "--global", "owner/one"}},
		{"update", []string{"update", "owner/one", "--dry-run"}, []string{"update", "owner/one", "--dry-run", "--global"}},
		{"search", []string{"search", "security@skills"}, []string{"search", "security@skills"}},
	}

	// Failure contract: APM's streams are forwarded verbatim and the returned error repeats the detail for exit-code consumers and the TUI, which has no stream forwarding.
	t.Run("update failure forwards streams and embeds detail in error", func(t *testing.T) {
		mock := &executor.MockExecutor{Responses: []executor.MockCall{{
			Stdout: "No apm.yml found\n",
			Err:    errors.New("exit status 1"),
		}}}
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		a := app.New(home+"/settings.json", app.WithEnvLookup(func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		}))
		a.SetFallbackExecutor(mock)
		cmd := newAgentsCmd(&rootState{app: a})
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"update"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "No apm.yml found") {
			t.Fatalf("error lacks apm stdout detail: %v", err)
		}
		if !strings.Contains(stdout.String(), "No apm.yml found") {
			t.Fatalf("apm stdout not forwarded: %q", stdout.String())
		}
	})

	t.Run("update without manifest returns guidance", func(t *testing.T) {
		mock := &executor.MockExecutor{}
		home := t.TempDir()
		a := app.New(home+"/settings.json", app.WithEnvLookup(func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		}))
		a.SetFallbackExecutor(mock)
		cmd := newAgentsCmd(&rootState{app: a})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"update"})
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "omni agents sync") {
			t.Fatalf("error lacks guidance: %v", err)
		}
		if len(mock.Calls) != 0 {
			t.Fatalf("apm invoked despite missing manifest: %#v", mock.Calls)
		}
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "stdout\n", Stderr: "stderr\n"}}}
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			a := app.New(home+"/settings.json", app.WithEnvLookup(func(name string) string {
				if name == "HOME" {
					return home
				}
				return ""
			}))
			a.SetFallbackExecutor(mock)
			cmd := newAgentsCmd(&rootState{app: a})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tt.args)

			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" || !reflect.DeepEqual(mock.Calls[0].Args, tt.want) {
				t.Fatalf("calls = %#v, want apm %v", mock.Calls, tt.want)
			}
			if stdout.String() != "stdout\n" || stderr.String() != "stderr\n" {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}
