package app

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDotsServicesStatus_PreservesPartialServiceResult(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		t.Skipf("dotfile services are not supported on %s", runtime.GOOS)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	spec, err := a.dotsReminderServiceForPlatform(runtime.GOOS, "/usr/local/bin/omni", defaultReminderInterval, true)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform: %v", err)
	}
	writeBrokenReminderService(t, spec)

	status := a.DotsServicesStatus()
	if status.Reminder != nil {
		t.Fatalf("Reminder = %+v, want nil on parse error", status.Reminder)
	}
	if !strings.Contains(status.ReminderError, "parse service file") {
		t.Fatalf("ReminderError = %q, want parse service file", status.ReminderError)
	}
	if status.Watch == nil {
		t.Fatal("Watch = nil, want partial watch status to be preserved")
	}
	if status.WatchError != "" {
		t.Fatalf("WatchError = %q, want empty", status.WatchError)
	}
	if status.Watch.Installed {
		t.Fatalf("Watch.Installed = true, want false")
	}
}

func TestDotsServiceTimingFallbacks(t *testing.T) {
	t.Parallel()
	if got := DotsReminderInterval(nil); got != DefaultDotsReminderInterval() {
		t.Fatalf("nil reminder interval = %s, want default", got)
	}
	if got := DotsReminderInterval(&DotsReminderService{Interval: 2 * time.Hour}); got != 2*time.Hour {
		t.Fatalf("reminder interval = %s, want 2h", got)
	}
	if got := DotsWatchDebounce(nil); got != DefaultDotsWatchDebounce() {
		t.Fatalf("nil watch debounce = %s, want default", got)
	}
	if got := DotsWatchDebounce(&DotsWatchService{Debounce: 3 * time.Second}); got != 3*time.Second {
		t.Fatalf("watch debounce = %s, want 3s", got)
	}
}

func TestDotsServiceTimingChoices(t *testing.T) {
	t.Parallel()
	wantReminder := []time.Duration{
		15 * time.Minute,
		30 * time.Minute,
		time.Hour,
		4 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
		48 * time.Hour,
		7 * 24 * time.Hour,
	}
	if got := DotsReminderIntervalChoices(); !reflect.DeepEqual(got, wantReminder) {
		t.Fatalf("DotsReminderIntervalChoices = %v, want %v", got, wantReminder)
	}
	gotReminder := DotsReminderIntervalChoices()
	gotReminder[0] = time.Second
	if got := DotsReminderIntervalChoices()[0]; got != 15*time.Minute {
		t.Fatalf("DotsReminderIntervalChoices returned mutable slice, first = %s", got)
	}

	wantWatch := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		30 * time.Second,
	}
	if got := DotsWatchDebounceChoices(); !reflect.DeepEqual(got, wantWatch) {
		t.Fatalf("DotsWatchDebounceChoices = %v, want %v", got, wantWatch)
	}
	gotWatch := DotsWatchDebounceChoices()
	gotWatch[0] = time.Second
	if got := DotsWatchDebounceChoices()[0]; got != 500*time.Millisecond {
		t.Fatalf("DotsWatchDebounceChoices returned mutable slice, first = %s", got)
	}
}

func TestBuildDashboardDotsAutomationStatus(t *testing.T) {
	t.Parallel()
	status := BuildDashboardDotsAutomationStatus(DashboardDotsAutomationInput{
		Services: DotsServicesStatus{
			Reminder: &DotsReminderService{Installed: true},
			Watch:    &DotsWatchService{Installed: false},
		},
		DotsRepo: "",
	})
	if !status.Known || status.Installed != 1 || !status.Blocked || !status.NeedsAttention {
		t.Fatalf("installed blocked status = %+v, want known installed=1 blocked attention", status)
	}
	if want := []string{"Blocked: dots_repo is not configured."}; !reflect.DeepEqual(status.ReadinessWarnings, want) {
		t.Fatalf("readiness warnings = %v, want %v", status.ReadinessWarnings, want)
	}

	status = BuildDashboardDotsAutomationStatus(DashboardDotsAutomationInput{
		Services: DotsServicesStatus{
			ReminderError: "parse service file",
		},
		DotsRepo: "/repo",
	})
	if !status.Known || !status.HasError || !status.NeedsAttention || status.Blocked {
		t.Fatalf("error status = %+v, want known error attention without blocked", status)
	}

	status = BuildDashboardDotsAutomationStatus(DashboardDotsAutomationInput{
		Services: DotsServicesStatus{
			Reminder: &DotsReminderService{Installed: true},
			Watch:    &DotsWatchService{Installed: true},
		},
		DotsRepo:     "/repo",
		DotsDisabled: true,
	})
	if status.Installed != 2 || !status.Blocked || !status.NeedsAttention {
		t.Fatalf("disabled status = %+v, want installed=2 blocked attention", status)
	}
	if want := []string{"Blocked: dotfile sync is disabled for this host."}; !reflect.DeepEqual(status.ReadinessWarnings, want) {
		t.Fatalf("disabled warnings = %v, want %v", status.ReadinessWarnings, want)
	}
}

func writeBrokenReminderService(t *testing.T, spec dotsReminderServiceSpec) {
	t.Helper()
	for _, file := range spec.files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			t.Fatal(err)
		}
		var content string
		switch filepath.Base(file.path) {
		case dotsReminderServiceName + ".timer":
			content = "[Timer]\nOnUnitActiveSec=not-a-duration\n"
		default:
			content = "<plist><dict><key>StartInterval</key><integer>not-a-number</integer></dict></plist>"
		}
		if err := os.WriteFile(file.path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
