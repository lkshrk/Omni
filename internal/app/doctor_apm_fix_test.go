package app

import (
	"context"
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
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true})
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || !report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 0 {
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

func TestAgentsSyncAllRefusesToMigrateWithoutAPM(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared"}},
	}}); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: nil}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return filepath.Join(dir, "home")
		}
		return ""
	}))
	a.SetFallbackExecutor(mock)

	_, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "apm-cli") {
		t.Fatalf("error lacks install hint: %v", err)
	}
	got, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(got.Agents.Packages) != 1 {
		t.Fatalf("legacy config migrated despite missing apm: %#v", got.Agents.Packages)
	}
}

func TestAgentsUpdateAllRefusesToMigrateWithoutAPM(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion, Agents: config.AgentsConfig{
		Packages: []config.SkillPackage{{Source: "acme/shared"}},
	}}); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: nil}
	a := New(configPath, WithEnvLookup(func(name string) string {
		if name == "HOME" {
			return filepath.Join(dir, "home")
		}
		return ""
	}))
	a.SetFallbackExecutor(mock)

	_, _, err := a.AgentsUpdateAll(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "apm-cli") {
		t.Fatalf("error lacks install hint: %v", err)
	}
	got, loadErr := config.Load(configPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(got.Agents.Packages) != 1 {
		t.Fatalf("legacy config migrated despite missing apm: %#v", got.Agents.Packages)
	}
}
