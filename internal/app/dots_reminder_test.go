package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/dots"
)

func TestAnalyzeDotsReminderStatus_ClassifiesReasons(t *testing.T) {
	t.Parallel()
	result := AnalyzeDotsReminderStatus(&DotsStatusResult{
		GitStatus:       " M dotfiles/nvim/init.lua\n?? dotfiles/zsh/.zshrc",
		DiscoveredCount: 1,
		Entries: []DotStatus{
			{Name: "nvim", State: dots.StateModified},
			{Name: "gitconfig", State: dots.StateConflict},
			{Name: "kitty", State: dots.StateUntrackedLinked},
			{Name: "ghost", State: dots.StateNoSource},
			{Name: "synced", State: dots.StateSynced},
			{Name: "ignored", State: dots.StateIgnored},
		},
	})

	if !result.NeedsReminder {
		t.Fatal("NeedsReminder = false, want true")
	}
	gotKinds := make([]string, 0, len(result.Reasons))
	for _, reason := range result.Reasons {
		gotKinds = append(gotKinds, reason.Kind)
	}
	wantKinds := []string{"git", "sync", "conflict", "new", "source"}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("reason kinds = %v, want %v", gotKinds, wantKinds)
	}
	if len(result.Entries) != 4 {
		t.Fatalf("attention entries = %d, want 4", len(result.Entries))
	}
	if result.DiscoveredCount != 1 {
		t.Fatalf("DiscoveredCount = %d, want 1", result.DiscoveredCount)
	}
}

func TestAnalyzeDotsReminderStatus_Clean(t *testing.T) {
	t.Parallel()
	result := AnalyzeDotsReminderStatus(&DotsStatusResult{
		Entries: []DotStatus{{Name: "nvim", State: dots.StateSynced}},
	})

	if result.NeedsReminder {
		t.Fatalf("NeedsReminder = true, want false: %+v", result)
	}
	if len(result.Reasons) != 0 {
		t.Fatalf("Reasons = %+v, want empty", result.Reasons)
	}
}

func TestDotsReminderServiceForPlatform_Linux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	service, err := a.dotsReminderServiceForPlatform("linux", "/usr/local/bin/omni", 12*time.Hour, true)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform: %v", err)
	}
	if service.platform != dotsReminderServiceLinux {
		t.Fatalf("platform = %q, want %q", service.platform, dotsReminderServiceLinux)
	}
	if len(service.files) != 2 {
		t.Fatalf("files = %d, want 2", len(service.files))
	}
	unit := service.files[0].content
	if !strings.Contains(unit, "ExecStart=/usr/local/bin/omni --config "+a.ConfigPath+" --cache-dir "+a.CacheDir+" dots reminder run --notify") {
		t.Fatalf("service unit missing command:\n%s", unit)
	}
	timer := service.files[1].content
	if !strings.Contains(timer, "OnUnitActiveSec=43200s") {
		t.Fatalf("timer missing 12h interval:\n%s", timer)
	}
}

func TestDotsReminderServiceForPlatform_LaunchdEscapesArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a := &App{ConfigPath: filepath.Join(home, `settings "main".json`), CacheDir: filepath.Join(home, "cache")}
	service, err := a.dotsReminderServiceForPlatform("darwin", "/Applications/Omni & Tools/omni", 24*time.Hour, true)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform: %v", err)
	}
	if service.platform != dotsReminderServiceMacOS {
		t.Fatalf("platform = %q, want %q", service.platform, dotsReminderServiceMacOS)
	}
	if len(service.files) != 1 {
		t.Fatalf("files = %d, want 1", len(service.files))
	}
	plist := service.files[0].content
	for _, want := range []string{
		"<string>/Applications/Omni &amp; Tools/omni</string>",
		"<integer>86400</integer>",
		"<string>" + filepath.Join(home, "settings &#34;main&#34;.json") + "</string>",
		"<string>--notify</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
}

func TestDotsReminderCommandArgs_AbsolutizesConfigAndCache(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)

	a := &App{ConfigPath: "settings.json", CacheDir: "cache"}
	got := a.dotsReminderCommandArgs("omni", true)
	wantConfig := filepath.Join(work, "settings.json")
	wantCache := filepath.Join(work, "cache")

	if !containsAdjacentArgs(got, "--config", wantConfig) {
		t.Fatalf("args = %v, want --config %s", got, wantConfig)
	}
	if !containsAdjacentArgs(got, "--cache-dir", wantCache) {
		t.Fatalf("args = %v, want --cache-dir %s", got, wantCache)
	}
}

func TestDotsReminderServiceStatus_ParsesLinuxIntervalAndNotify(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	statusSpec, err := a.dotsReminderServiceForPlatform("linux", "/usr/local/bin/omni", defaultReminderInterval, true)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform status: %v", err)
	}
	customSpec, err := a.dotsReminderServiceForPlatform("linux", "/usr/local/bin/omni", 90*time.Minute, false)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform custom: %v", err)
	}
	for i, file := range statusSpec.files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file.path, []byte(customSpec.files[i].content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	info := statusSpec.info()
	info.Installed = true
	if err := statusSpec.applyReminderStatus(info); err != nil {
		t.Fatalf("applyReminderStatus: %v", err)
	}
	if info.Interval != 90*time.Minute {
		t.Fatalf("interval = %s, want 90m", info.Interval)
	}
	if info.Notify {
		t.Fatal("notify = true, want false")
	}
}

func TestDotsReminderServiceStatus_ParsesLaunchdIntervalAndNotify(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	statusSpec, err := a.dotsReminderServiceForPlatform("darwin", "/usr/local/bin/omni", defaultReminderInterval, true)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform status: %v", err)
	}
	customSpec, err := a.dotsReminderServiceForPlatform("darwin", "/usr/local/bin/omni", 45*time.Minute, false)
	if err != nil {
		t.Fatalf("dotsReminderServiceForPlatform custom: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statusSpec.files[0].path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusSpec.files[0].path, []byte(customSpec.files[0].content), 0o644); err != nil {
		t.Fatal(err)
	}

	info := statusSpec.info()
	info.Installed = true
	if err := statusSpec.applyReminderStatus(info); err != nil {
		t.Fatalf("applyReminderStatus: %v", err)
	}
	if info.Interval != 45*time.Minute {
		t.Fatalf("interval = %s, want 45m", info.Interval)
	}
	if info.Notify {
		t.Fatal("notify = true, want false")
	}
}

func TestValidateReminderInterval(t *testing.T) {
	t.Parallel()
	if err := validateReminderInterval(time.Minute); err != nil {
		t.Fatalf("time.Minute should be valid: %v", err)
	}
	if err := validateReminderInterval(time.Second); err == nil {
		t.Fatal("time.Second should be invalid")
	}
}

func containsAdjacentArgs(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
