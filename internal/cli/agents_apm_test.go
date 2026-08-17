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
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestAgentsCommandsDelegateToGlobalAPM(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"remove", []string{"remove", "owner/one"}, []string{"uninstall", "--global", "owner/one"}},
		{"search", []string{"search", "security@skills"}, []string{"search", "security@skills"}},
	}

	// APM refreshes a named subset only through `apm update`, which has no --only and so redeploys the mcp
	// surface into every target of the run.
	t.Run("update refuses a package argument", func(t *testing.T) {
		cmd := newAgentsCmd(&rootState{app: app.New(t.TempDir() + "/settings.json")})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"update", "owner/one"})
		if err := cmd.Execute(); err == nil {
			t.Fatal("a package argument was accepted")
		}
	})

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
		configPath := filepath.Join(home, "settings.json")
		if err := config.Save(configPath, &config.RootConfig{
			Version:  config.CurrentVersion,
			Settings: config.Settings{AgentsUse: []string{"codex"}},
			Agents:   config.AgentsConfig{Packages: []config.SkillPackage{{Source: "owner/one", Agents: []string{"codex"}}}},
		}); err != nil {
			t.Fatal(err)
		}
		a := app.New(configPath, app.WithEnvLookup(func(name string) string {
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

	// add is config authoring, not an apm.yml edit: APM's positional install would write a manifest the next
	// sync regenerates from config, dropping the package again.
	t.Run("add declares the package in config and installs the surface", func(t *testing.T) {
		mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "stdout\n"}}}
		home := t.TempDir()
		configPath := filepath.Join(home, "settings.json")
		if err := config.Save(configPath, &config.RootConfig{
			Version:  config.CurrentVersion,
			Settings: config.Settings{AgentsUse: []string{"codex"}},
		}); err != nil {
			t.Fatal(err)
		}
		a := app.New(configPath, app.WithEnvLookup(func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		}))
		a.SetFallbackExecutor(mock)
		cmd := newAgentsCmd(&rootState{app: a})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"add", "owner/one", "owner/two"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		want := []string{"install", "-g", "--only", "apm", "--target", "codex"}
		if len(mock.Calls) != 1 || !reflect.DeepEqual(mock.Calls[0].Args, want) {
			t.Fatalf("calls = %#v, want apm %v", mock.Calls, want)
		}
		got, err := config.Load(configPath)
		if err != nil || len(got.Agents.Packages) != 2 {
			t.Fatalf("packages = %+v, %v", got.Agents.Packages, err)
		}
	})

	// A recorded failure the app layer reports without returning an error still has to exit nonzero, or a
	// scripted caller chains onto an install that never happened.
	t.Run("sync exits nonzero on a recorded feature failure", func(t *testing.T) {
		mock := &executor.MockExecutor{Responses: []executor.MockCall{
			{Stderr: "mcp install aborted\n", Err: errors.New("exit status 1")},
		}}
		home := t.TempDir()
		configPath := filepath.Join(home, "settings.json")
		if err := config.Save(configPath, &config.RootConfig{
			Version:  config.CurrentVersion,
			Settings: config.Settings{AgentsUse: []string{"claude-code"}},
			Agents: config.AgentsConfig{McpServers: []config.McpServer{
				{Name: "docs", Transport: "stdio", Command: "npx -y docs-server", Env: []string{"DOCS_KEY"}},
			}},
		}); err != nil {
			t.Fatal(err)
		}
		a := app.New(configPath, app.WithEnvLookup(func(name string) string {
			if name == "HOME" {
				return home
			}
			return ""
		}))
		a.SetFallbackExecutor(mock)
		cmd := newAgentsCmd(&rootState{app: a})
		var stdout bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"sync"})

		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "agent operation(s) failed") {
			t.Fatalf("err = %v, want a nonzero exit for the recorded failure", err)
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
