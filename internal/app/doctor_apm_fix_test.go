package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

type availExecutor struct {
	executor.MockExecutor
	available map[string]bool
}

func (e *availExecutor) CommandAvailable(name string) bool {
	return e.available[name]
}

func newAPMFixApp(t *testing.T, available map[string]bool) (*App, *availExecutor) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: available}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	return a, mock
}

func TestFixMissingAPMNoopWhenInstalled(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionFloor + "\n"}}
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || !report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" {
		t.Fatalf("unexpected calls: %#v", mock.Calls)
	}
}

func TestFixMissingAPMDryRunPlansPreferredInstaller(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"uv": true, "pip3": true})
	report, err := a.FixMissingAPM(context.Background(), true)
	if err != nil || report.Planned != "uv tool install apm-cli" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("dry run executed commands: %#v", mock.Calls)
	}
}

func TestFixMissingAPMInstallsViaFallbackInstaller(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"pip3": true})
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed != "pip3 install --user apm-cli" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "pip3" {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

type flipExecutor struct {
	availExecutor
	makeAvailable string
}

func (e *flipExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	stdout, stderr, err := e.availExecutor.Run(ctx, name, args...)
	if err == nil && e.makeAvailable != "" {
		e.available[e.makeAvailable] = true
	}
	return stdout, stderr, err
}

func TestFixMissingAPMReportsResolvableInstall(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	mock := &flipExecutor{availExecutor: availExecutor{available: map[string]bool{"uv": true}}, makeAvailable: "apm"}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed != "uv tool install apm-cli" || report.NotOnPATH {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}

func TestFixMissingAPMFlagsInstallNotOnPATH(t *testing.T) {
	a, _ := newAPMFixApp(t, map[string]bool{"uv": true})
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed == "" || !report.NotOnPATH {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}

func TestFixMissingAPMRetriesExternallyManagedPip(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: map[string]bool{"pip3": true}}
	mock.Responses = []executor.MockCall{
		{Stderr: "error: externally-managed-environment\n", Err: context.DeadlineExceeded},
		{},
	}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed != "pip3 install --user apm-cli --break-system-packages" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 2 || mock.Calls[1].Args[len(mock.Calls[1].Args)-1] != "--break-system-packages" {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestFixMissingAPMDoesNotRetryOtherPipFailures(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: map[string]bool{"pip3": true}}
	mock.Responses = []executor.MockCall{{Stderr: "network unreachable\n", Err: context.DeadlineExceeded}}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	_, err := a.FixMissingAPM(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "network unreachable") {
		t.Fatalf("err = %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestFixMissingAPMErrorsWithoutInstaller(t *testing.T) {
	a, _ := newAPMFixApp(t, nil)
	_, err := a.FixMissingAPM(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "install apm-cli manually") {
		t.Fatalf("err = %v", err)
	}
}

// apm is on PATH but below the floor: the first upgrader failing means it was installed by another one, so
// the fix tries the rest before reporting the upgrade impossible.
func TestFixMissingAPMFallsThroughToTheNextUpgrader(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true, "pipx": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version 0.0.1\n"},
		{Stderr: "`apm-cli` is not installed\n", Err: errors.New("exit status 1")},
		{Stdout: "upgraded\n"},
	}

	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Upgraded != "pipx upgrade apm-cli" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 3 {
		t.Fatalf("calls = %#v, want the failed uv upgrade followed by pipx", mock.Calls)
	}
}

func TestFixMissingAPMReportsEveryFailedUpgrader(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true, "pipx": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version 0.0.1\n"},
		{Stderr: "uv refused\n", Err: errors.New("exit status 1")},
		{Stderr: "pipx refused\n", Err: errors.New("exit status 1")},
	}

	_, err := a.FixMissingAPM(context.Background(), false)
	if err == nil {
		t.Fatal("every upgrader failed but the fix reported success")
	}
	for _, want := range []string{"uv refused", "pipx refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to name %q", err, want)
		}
	}
}

func newSyncWithoutAPMApp(t *testing.T, cfg *config.RootConfig) (*App, *availExecutor, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	mock := &availExecutor{available: nil}
	a := New(configPath, WithMcpAdapters([]McpAdapter{}), WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}))
	a.SetFallbackExecutor(mock)
	return a, mock, home
}

func TestAgentsSyncAllRefusesToInstallWithoutAPM(t *testing.T) {
	a, mock, home := newSyncWithoutAPMApp(t, &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared", Agents: []string{"codex"}}},
	}})

	_, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "apm-cli") {
		t.Fatalf("error lacks install hint: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked despite missing binary: %#v", mock.Calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !os.IsNotExist(err) {
		t.Fatalf("stat ~/.apm = %v, want no manifest written for a host that cannot run APM", err)
	}
}

// Nothing declared is nothing to refuse: a host without apm-cli still syncs its other surfaces, and never
// gets a ~/.apm it did not ask for — which is a hard failure where HOME is not writable.
func TestAgentsSyncAllSucceedsWithoutAPMWhenNothingIsDeclared(t *testing.T) {
	a, mock, home := newSyncWithoutAPMApp(t, &config.RootConfig{Version: config.CurrentVersion})

	res, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err != nil {
		t.Fatalf("AgentsSyncAll = %v, want the sync to succeed", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v, want none", res.Errors)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "nothing to install") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked despite missing binary: %#v", mock.Calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !os.IsNotExist(err) {
		t.Fatalf("stat ~/.apm = %v, want the APM directory left uncreated", err)
	}
}
