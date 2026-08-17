package app

import (
	"context"
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
		{"apm, version 1.2", "1.2.0"},
		{"apm 0.27.11\n", "0.27.11"},
		{"", ""},
		{"apm (no version reported)", ""},
	} {
		if got := parseAPMVersion(tt.output); got != tt.want {
			t.Fatalf("parseAPMVersion(%q) = %q, want %q", tt.output, got, tt.want)
		}
	}
}

func TestAPMVersionBelowFloor(t *testing.T) {
	for _, tt := range []struct {
		version string
		want    bool
	}{
		{"0.27.9", true},
		{"0.28.0", false},
		{"0.28.1", false},
		{"0.29.0", false},
		{"1.0.0", false},
		{"0.9.0", true},
	} {
		if got := apmVersionBelowFloor(tt.version); got != tt.want {
			t.Fatalf("apmVersionBelowFloor(%q) = %v, want %v", tt.version, got, tt.want)
		}
	}
}

func newAPMVersionApp(t *testing.T, response executor.MockCall) (*App, *executor.MockExecutor) {
	t.Helper()
	a, mock, _ := newSyncApp(t, &config.RootConfig{Version: config.CurrentVersion}, emptySyncManifest)
	mock.Responses = []executor.MockCall{response}
	return a, mock
}

func TestDoctorAPMVersionAcceptsFloor(t *testing.T) {
	a, _ := newAPMVersionApp(t, executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0\n"})
	result := &DoctorResult{}
	a.doctorAPMVersion(context.Background(), result, &config.RootConfig{})
	if len(result.Checks) != 1 || result.Checks[0].Status != DoctorStatusOK {
		t.Fatalf("checks = %+v", result.Checks)
	}
}

func TestDoctorAPMVersionFailsBelowFloor(t *testing.T) {
	a, _ := newAPMVersionApp(t, executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.27.3\n"})
	result := &DoctorResult{}
	a.doctorAPMVersion(context.Background(), result, &config.RootConfig{})
	check := result.Checks[0]
	if check.Status != DoctorStatusFail || !strings.Contains(check.Message, "0.27.3") ||
		!strings.Contains(check.Message, apmVersionFloor) {
		t.Fatalf("check = %+v", check)
	}
	if !strings.Contains(strings.Join(check.Details, " "), "doctor --fix") {
		t.Fatalf("check lacks the fix hint: %+v", check)
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

func TestDoctorAPMVersionSkippedWhenAgentsDisabled(t *testing.T) {
	a, _ := newAPMVersionApp(t, executor.MockCall{Stdout: "version 0.1.0\n"})
	result := &DoctorResult{}
	a.doctorAPMVersion(context.Background(), result, &config.RootConfig{
		Settings: config.Settings{AgentsDisabled: config.BoolPtr(true)},
	})
	if len(result.Checks) != 0 {
		t.Fatalf("checks = %+v", result.Checks)
	}
}

func TestFixMissingAPMUpgradesBelowFloor(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "uv": true})
	mock.Responses = []executor.MockCall{{Stdout: "APM CLI version 0.27.0\n"}, {}}
	report, err := a.FixMissingAPM(context.Background(), false)
	if err != nil || report.Upgraded != "uv tool upgrade apm-cli" || report.AlreadyInstalled {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 2 || mock.Calls[1].Name != "uv" {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestFixMissingAPMDryRunPlansUpgrade(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true, "pipx": true})
	mock.Responses = []executor.MockCall{{Stdout: "APM CLI version 0.27.0\n"}}
	report, err := a.FixMissingAPM(context.Background(), true)
	if err != nil || report.Planned != "pipx upgrade apm-cli" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("dry run ran the upgrade: %#v", mock.Calls)
	}
}

func TestFixMissingAPMErrorsWithoutUpgrader(t *testing.T) {
	a, mock := newAPMFixApp(t, map[string]bool{"apm": true})
	mock.Responses = []executor.MockCall{{Stdout: "APM CLI version 0.20.0\n"}}
	_, err := a.FixMissingAPM(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "no supported upgrader") {
		t.Fatalf("err = %v", err)
	}
}
