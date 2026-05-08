package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func TestDotsWatchServiceForPlatform_Linux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	service, err := a.dotsWatchServiceForPlatform("linux", "/usr/local/bin/omni", 2*time.Second)
	if err != nil {
		t.Fatalf("dotsWatchServiceForPlatform: %v", err)
	}
	if service.platform != dotsReminderServiceLinux {
		t.Fatalf("platform = %q, want %q", service.platform, dotsReminderServiceLinux)
	}
	if service.debounce != 2*time.Second {
		t.Fatalf("debounce = %s, want 2s", service.debounce)
	}
	if len(service.files) != 1 {
		t.Fatalf("files = %d, want 1", len(service.files))
	}
	unit := service.files[0].content
	for _, want := range []string{
		"ExecStart=/usr/local/bin/omni --config " + a.ConfigPath + " --cache-dir " + a.CacheDir + " dots watch run --debounce 2s",
		"Restart=on-failure",
		"RestartSec=10s",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("service unit missing %q:\n%s", want, unit)
		}
	}
}

func TestDotsWatchServiceForPlatform_LaunchdEscapesArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a := &App{ConfigPath: filepath.Join(home, `settings "main".json`), CacheDir: filepath.Join(home, "cache")}
	service, err := a.dotsWatchServiceForPlatform("darwin", "/Applications/Omni & Tools/omni", 3*time.Second)
	if err != nil {
		t.Fatalf("dotsWatchServiceForPlatform: %v", err)
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
		"<string>" + filepath.Join(home, "settings &#34;main&#34;.json") + "</string>",
		"<string>--debounce</string>",
		"<string>3s</string>",
		"<key>KeepAlive</key>",
		"<true/>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "StartInterval") {
		t.Fatalf("watch plist should not include StartInterval:\n%s", plist)
	}
}

func TestDotsWatchCommandArgs_AbsolutizesConfigAndCache(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)

	a := &App{ConfigPath: "settings.json", CacheDir: "cache"}
	got := a.dotsWatchCommandArgs("omni", 7*time.Second)
	wantConfig := filepath.Join(work, "settings.json")
	wantCache := filepath.Join(work, "cache")

	if !containsAdjacentArgs(got, "--config", wantConfig) {
		t.Fatalf("args = %v, want --config %s", got, wantConfig)
	}
	if !containsAdjacentArgs(got, "--cache-dir", wantCache) {
		t.Fatalf("args = %v, want --cache-dir %s", got, wantCache)
	}
	if !containsAdjacentArgs(got, "--debounce", "7s") {
		t.Fatalf("args = %v, want --debounce 7s", got)
	}
}

func TestDotsWatchServiceStatus_ParsesLinuxDebounce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	statusSpec, err := a.dotsWatchServiceForPlatform("linux", "/usr/local/bin/omni", defaultDotsWatchDebounce)
	if err != nil {
		t.Fatalf("dotsWatchServiceForPlatform status: %v", err)
	}
	customSpec, err := a.dotsWatchServiceForPlatform("linux", "/usr/local/bin/omni", 750*time.Millisecond)
	if err != nil {
		t.Fatalf("dotsWatchServiceForPlatform custom: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statusSpec.files[0].path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusSpec.files[0].path, []byte(customSpec.files[0].content), 0o644); err != nil {
		t.Fatal(err)
	}

	info := statusSpec.info()
	info.Installed = true
	if err := statusSpec.applyWatchStatus(info); err != nil {
		t.Fatalf("applyWatchStatus: %v", err)
	}
	if info.Debounce != 750*time.Millisecond {
		t.Fatalf("debounce = %s, want 750ms", info.Debounce)
	}
}

func TestDotsWatchServiceStatus_ParsesLaunchdDebounce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	statusSpec, err := a.dotsWatchServiceForPlatform("darwin", "/usr/local/bin/omni", defaultDotsWatchDebounce)
	if err != nil {
		t.Fatalf("dotsWatchServiceForPlatform status: %v", err)
	}
	customSpec, err := a.dotsWatchServiceForPlatform("darwin", "/usr/local/bin/omni", 1500*time.Millisecond)
	if err != nil {
		t.Fatalf("dotsWatchServiceForPlatform custom: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statusSpec.files[0].path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusSpec.files[0].path, []byte(customSpec.files[0].content), 0o644); err != nil {
		t.Fatal(err)
	}

	info := statusSpec.info()
	info.Installed = true
	if err := statusSpec.applyWatchStatus(info); err != nil {
		t.Fatalf("applyWatchStatus: %v", err)
	}
	if info.Debounce != 1500*time.Millisecond {
		t.Fatalf("debounce = %s, want 1.5s", info.Debounce)
	}
}

func TestDotsWatchPaths_CollectsActiveSourceAndTargetDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")

	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "settings.json")
	repoDir := t.TempDir()
	sourceDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	targetDir := filepath.Join(home, ".config", "nvim")
	ignoredTargetDir := filepath.Join(home, ".config", "ignored")
	for _, dir := range []string{
		sourceDir,
		targetDir,
		ignoredTargetDir,
		filepath.Join(repoDir, "dotfiles", ".git", "hooks"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.Save(cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots: []config.DotEntry{
				{Name: "nvim", Path: "~/.config/nvim"},
				{Name: "ignored", Path: "~/.config/ignored", Ignored: true},
			},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{ConfigPath: cfgPath}
	paths, err := a.dotsWatchPaths()
	if err != nil {
		t.Fatalf("dotsWatchPaths: %v", err)
	}
	if !containsString(paths, sourceDir) {
		t.Fatalf("paths = %v, want source dir %s", paths, sourceDir)
	}
	if !containsString(paths, targetDir) {
		t.Fatalf("paths = %v, want target dir %s", paths, targetDir)
	}
	if containsString(paths, ignoredTargetDir) {
		t.Fatalf("paths = %v, ignored target should not be watched", paths)
	}
	for _, path := range paths {
		if dotsWatchPathHasSegment(path, ".git") {
			t.Fatalf("paths = %v, .git path should not be watched", paths)
		}
	}
}

func TestDotsWatch_SyncsAfterFilesystemEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNI_HOSTNAME", "testhost")
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	if err := os.WriteFile(filepath.Join(binDir, "stow"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	repoDir := t.TempDir()
	sourceDir := filepath.Join(repoDir, "dotfiles", "nvim", ".config", "nvim")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(sourceDir, "init.lua")
	if err := os.WriteFile(sourceFile, []byte("-- before"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "nvim", Path: "~/.config/nvim"}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{ConfigPath: cfgPath}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startCh := make(chan DotsWatchStart, 1)
	syncCh := make(chan DotsWatchSyncResult, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.DotsWatch(ctx, DotsWatchOptions{
			Debounce: minDotsWatchDebounce,
			OnStart: func(start DotsWatchStart) {
				startCh <- start
			},
			OnSync: func(result DotsWatchSyncResult) {
				syncCh <- result
				cancel()
			},
		})
	}()

	select {
	case start := <-startCh:
		if start.WatchedPaths == 0 {
			t.Fatal("watcher started with no paths")
		}
	case err := <-errCh:
		t.Fatalf("DotsWatch exited early: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("DotsWatch did not start")
	}

	if err := os.WriteFile(sourceFile, []byte("-- after"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-syncCh:
		if result.Err != nil {
			t.Fatalf("watch sync error: %v", result.Err)
		}
		if result.Event.Path == "" {
			t.Fatalf("watch result missing event: %+v", result)
		}
	case err := <-errCh:
		t.Fatalf("DotsWatch exited before sync: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("DotsWatch did not sync after file change")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("DotsWatch exit error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DotsWatch did not stop after cancellation")
	}
}

func TestCollectDotsWatchPaths_MissingPathWatchesNearestParentOnly(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]struct{})
	if err := collectDotsWatchPaths(filepath.Join(configDir, "newapp", "settings.json"), out); err != nil {
		t.Fatalf("collectDotsWatchPaths: %v", err)
	}
	if !containsString(sortedWatchPaths(out), configDir) {
		t.Fatalf("paths = %v, want nearest parent %s", sortedWatchPaths(out), configDir)
	}
	if containsString(sortedWatchPaths(out), filepath.Join(configDir, "newapp")) {
		t.Fatalf("paths = %v, missing child should not be watched", sortedWatchPaths(out))
	}
}

func TestDotsWatchRejectsTooSmallDebounce(t *testing.T) {
	a := &App{}
	err := a.DotsWatch(context.Background(), DotsWatchOptions{Debounce: time.Millisecond})
	if err == nil {
		t.Fatal("DotsWatch should reject tiny debounce")
	}
	if !strings.Contains(err.Error(), "watch debounce") {
		t.Fatalf("error = %v, want debounce validation", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
