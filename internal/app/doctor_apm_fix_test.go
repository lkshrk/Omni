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
	mock.Responses = []executor.MockCall{{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"}}
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
	if err != nil || report.Planned != "uv tool install "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("dry run executed commands: %#v", mock.Calls)
	}
}

func TestFixMissingAPMInstallsViaFallbackInstaller(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"pip3": true})
	mock.Responses = []executor.MockCall{
		{},
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed != "pip3 install --user "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 2 || mock.Calls[0].Name != "pip3" || mock.Calls[1].Name != "apm" {
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
	mock.Responses = []executor.MockCall{
		{},
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed != "uv tool install "+apmPackagePin || report.NotOnPATH {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}

func TestFixMissingAPMRejectsUnverifiableInstall(t *testing.T) {
	a, _ := newAPMFixApp(t, map[string]bool{"uv": true})
	report, err := a.FixMissingAPM(context.Background(), false)
	if err == nil || report.Installed != "" || !strings.Contains(err.Error(), apmVersionPin) {
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
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Installed != "pip3 install --user "+apmPackagePin+" --break-system-packages" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 3 || mock.Calls[1].Args[len(mock.Calls[1].Args)-1] != "--break-system-packages" || mock.Calls[2].Name != "apm" {
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
	if err == nil || !strings.Contains(err.Error(), apmPackagePin) {
		t.Fatalf("err = %v", err)
	}
}

// apm is on PATH but does not match the pin: the first upgrader failing means it was installed by another one, so
// the fix tries the rest before reporting the upgrade impossible.
func TestFixMissingAPMFallsThroughToTheNextUpgrader(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true, "pipx": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version 0.0.1\n"},
		{Stderr: "`apm-cli` is not installed\n", Err: errors.New("exit status 1")},
		{Stdout: "upgraded\n"},
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}

	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Upgraded != "pipx install --force "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 4 {
		t.Fatalf("calls = %#v, want the failed uv upgrade followed by pipx", mock.Calls)
	}
}

func TestFixMissingAPMReprobesAfterSuccessfulUpgrade(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true, "pipx": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version 0.0.1\n"},
		{},
		{Stdout: "Agent Package Manager (APM) CLI version 0.0.1\n"},
		{},
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}

	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Upgraded != "pipx install --force "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	want := []string{"apm", "uv", "apm", "pipx", "apm"}
	if len(mock.Calls) != len(want) {
		t.Fatalf("calls = %#v", mock.Calls)
	}
	for i, call := range mock.Calls {
		if call.Name != want[i] {
			t.Fatalf("calls = %#v", mock.Calls)
		}
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

func newSyncWithoutAPMApp(t *testing.T) (*App, *availExecutor, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	if err := os.MkdirAll(filepath.Join(home, ".apm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".apm", "apm.yml"), []byte("name: test\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &availExecutor{available: nil}
	a := New(configPath)
	a.SetFallbackExecutor(mock)
	return a, mock, home
}

func TestAgentsSyncAllRefusesToInstallWithoutAPM(t *testing.T) {
	a, mock, home := newSyncWithoutAPMApp(t)

	_, err := a.AgentsSyncAll(context.Background(), AgentsSyncAllOptions{})
	if err == nil || !strings.Contains(err.Error(), "omni doctor --fix") {
		t.Fatalf("error lacks install hint: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm invoked despite missing binary: %#v", mock.Calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); err != nil {
		t.Fatalf("existing manifest changed: %v", err)
	}
}

// Without this every test would read the host's own apm install receipt.
func init() { apmReceiptLocator = func() string { return "" } }

func apmReceiptJSON(commit string) string {
	url, _ := parseAPMPackagePin(apmPackagePin)
	return `{"url":"` + url + `","vcs_info":{"vcs":"git","commit_id":"` + commit + `"}}`
}

func stubAPMReceipt(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "direct_url.json")
	if contents != "" {
		writeFile(t, path, contents)
	}
	previous := apmReceiptLocator
	apmReceiptLocator = func() string { return path }
	t.Cleanup(func() { apmReceiptLocator = previous })
	return path
}

// Reinstalling rewrites the receipt, so the fix's own verification observes the pinned provenance.
type receiptExecutor struct {
	availExecutor
	path    string
	receipt string
}

func (e *receiptExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	stdout, stderr, err := e.availExecutor.Run(ctx, name, args...)
	if err == nil {
		if writeErr := os.WriteFile(e.path, []byte(e.receipt), 0o600); writeErr != nil {
			return stdout, stderr, writeErr
		}
	}
	return stdout, stderr, err
}

func TestFixMissingAPMReinstallsWhenProvenanceDiffersFromThePin(t *testing.T) {
	_, ref := parseAPMPackagePin(apmPackagePin)
	path := stubAPMReceipt(t, apmReceiptJSON("0ff1ce0ff1ce"))
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	mock := &receiptExecutor{
		availExecutor: availExecutor{available: map[string]bool{"apm": true, "uv": true}},
		path:          path,
		receipt:       apmReceiptJSON(ref),
	}
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
		{},
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}
	a := New(configPath)
	a.SetFallbackExecutor(mock)

	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Upgraded != "uv tool install --force "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	want := []string{"apm", "uv", "apm"}
	if len(mock.Calls) != len(want) {
		t.Fatalf("calls = %#v, want %v", mock.Calls, want)
	}
	for i, call := range mock.Calls {
		if call.Name != want[i] {
			t.Fatalf("calls = %#v, want %v", mock.Calls, want)
		}
	}
}

func TestFixMissingAPMDryRunPlansTheProvenanceReinstall(t *testing.T) {
	stubAPMReceipt(t, apmReceiptJSON("0ff1ce0ff1ce"))
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}

	report, err := a.FixMissingAPM(context.Background(), true)
	if err != nil || report.Planned != "uv tool install --force "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" {
		t.Fatalf("dry run executed an installer: %#v", mock.Calls)
	}
}

func TestFixMissingAPMKeepsAPMInstalledFromThePinnedCommit(t *testing.T) {
	_, ref := parseAPMPackagePin(apmPackagePin)
	stubAPMReceipt(t, apmReceiptJSON(ref))
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}

	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || !report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" {
		t.Fatalf("calls = %#v, want the version probe only", mock.Calls)
	}
}

func TestFixMissingAPMKeepsAPMWithUnknownProvenance(t *testing.T) {
	stubAPMReceipt(t, "")
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"},
	}

	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || !report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].Name != "apm" {
		t.Fatalf("calls = %#v, want the version probe only", mock.Calls)
	}
}
