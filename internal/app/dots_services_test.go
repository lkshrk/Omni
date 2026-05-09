package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
