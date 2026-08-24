package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestParseAPMVersion(t *testing.T) {
	for _, tt := range []struct {
		output string
		want   string
	}{
		{"Agent Package Manager (APM) CLI version 0.28.0", "0.28.0"},
		{"Agent Package Manager (APM) CLI version 0.28.0+omni.3", "0.28.0+omni.3"},
		{"apm, version 1.2.0", "1.2.0"},
		{"apm 0.27.11\n", "0.27.11"},
		{"APM CLI version 0.28", ""},
		{"APM CLI version 0.28.0rc1", ""},
		{"APM CLI version 0.28.0-dev", ""},
		{"APM CLI version 0.28.0+build.1", "0.28.0+build.1"},
		{"", ""},
		{"apm (no version reported)", ""},
	} {
		if got := parseAPMVersion(tt.output); got != tt.want {
			t.Fatalf("parseAPMVersion(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestAPMVersionPin(t *testing.T) {
	if apmVersionPin != "0.28.0+omni.8" || apmPackagePin != "git+https://github.com/lkshrk/apm.git@1a87534ea6d5b28098e0b33226bb37e3157e2230" {
		t.Fatalf("unexpected APM pins: version=%q package=%q", apmVersionPin, apmPackagePin)
	}
	for _, tt := range []struct {
		version string
		want    bool
	}{
		{"0.27.9", false},
		{"0.28.0", false},
		{"0.28.0+omni.8", true},
		{"0.28.0+omni.2", false},
		{"0.28.0+build.1", false},
		{"0.28.1", false},
		{"0.29.0", false},
		{"1.0.0", false},
		{"0.9.0", false},
	} {
		if got := apmVersionPinned(tt.version); got != tt.want {
			t.Fatalf("apmVersionPinned(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func newAPMVersionApp(t *testing.T, response executor.MockCall) (*App, *availExecutor) {
	t.Helper()
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	mock := &availExecutor{available: map[string]bool{"apm": true}}
	a.SetFallbackExecutor(mock)
	mock.Responses = []executor.MockCall{response}
	return a, mock
}

func TestDoctorAPMVersionAcceptsPin(t *testing.T) {
	a, mock := newAPMVersionApp(t, executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version " + apmVersionPin + "\n"})
	result := &DoctorResult{}
	a.doctorAPMVersion(context.Background(), result, &config.RootConfig{})
	if len(result.Checks) != 1 || result.Checks[0].Status != DoctorStatusOK {
		t.Fatalf("checks = %+v", result.Checks)
	}
	env := make(map[string]string, len(mock.Calls[0].Env))
	for _, entry := range mock.Calls[0].Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("unexpected unset in environment: %q", entry)
		}
		env[key] = value
	}
	home := env["HOME"]
	if env["APM_E2E_TESTS"] != "1" || home == "" || env["USERPROFILE"] != home || env["XDG_CONFIG_HOME"] != filepath.Join(home, ".config") || env["XDG_CACHE_HOME"] != filepath.Join(home, ".cache") || env["XDG_STATE_HOME"] != filepath.Join(home, ".state") {
		t.Fatalf("environment=%v", env)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("disposable home still exists: %v", err)
	}
}

func TestDoctorAPMVersionRejectsMismatch(t *testing.T) {
	for _, version := range []string{"0.27.3", "0.28.0", "0.28.0+omni.3", "0.29.0"} {
		a, _ := newAPMVersionApp(t, executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version " + version + "\n"})
		result := &DoctorResult{}
		a.doctorAPMVersion(context.Background(), result, &config.RootConfig{})
		check := result.Checks[0]
		if check.Status != DoctorStatusFail || !strings.Contains(check.Message, version) ||
			!strings.Contains(check.Message, apmVersionPin) {
			t.Fatalf("check = %+v", check)
		}
		if !strings.Contains(strings.Join(check.Details, " "), "doctor --fix") {
			t.Fatalf("check lacks the fix hint: %+v", check)
		}
	}
}

func TestDoctorAPMVersionFailsWhenUnparseable(t *testing.T) {
	a, _ := newAPMVersionApp(t, executor.MockCall{Stdout: "apm: unknown option --version\n"})
	result := &DoctorResult{}
	a.doctorAPMVersion(context.Background(), result, &config.RootConfig{})
	if check := result.Checks[0]; check.Status != DoctorStatusFail ||
		!strings.Contains(check.Message, "could not be determined") {
		t.Fatalf("check = %+v", check)
	}
}

func TestFixMissingAPMUpgradesBelowFloor(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{
		{Stdout: "APM CLI version 0.27.0\n"},
		{},
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
	}
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Upgraded != "uv tool install --force "+apmPackagePin || report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 3 || mock.Calls[1].Name != "uv" || mock.Calls[2].Name != "apm" {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestFixMissingAPMDryRunPlansUpgrade(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "pipx": true})
	mock.Responses = []executor.MockCall{{Stdout: "APM CLI version 0.27.0\n"}}
	report, err := a.FixMissingAPM(context.Background(), true)
	if err != nil || report.Planned != "pipx install --force "+apmPackagePin {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("dry run ran the upgrade: %#v", mock.Calls)
	}
}

func TestFixMissingAPMRepairsUnparseableVersion(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{{Stdout: "APM CLI version unknown\n"}}
	report, err := a.FixMissingAPM(context.Background(), true)
	if err != nil || report.Planned != "uv tool install --force "+apmPackagePin || report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}

func TestFixMissingAPMErrorsWithoutUpgrader(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true})
	mock.Responses = []executor.MockCall{{Stdout: "APM CLI version 0.20.0\n"}}
	_, err := a.FixMissingAPM(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "no supported installer") {
		t.Fatalf("err = %v", err)
	}
}
