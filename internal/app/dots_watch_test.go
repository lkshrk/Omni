package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

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

func TestInstallDotsWatchService_AbsolutizesExecutable(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		t.Skipf("dots watch services are not supported on %s", runtime.GOOS)
	}

	work := t.TempDir()
	t.Chdir(work)
	home := filepath.Join(work, "home")
	binDir := filepath.Join(work, "bin")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "stow"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("PATH", binDir)

	a := &App{ConfigPath: filepath.Join(home, "settings.json"), CacheDir: filepath.Join(home, "cache")}
	info, err := a.InstallDotsWatchService(context.Background(), DotsWatchInstallOptions{
		Executable: filepath.Join("tools", "omni"),
		Debounce:   time.Second,
	})
	if err != nil {
		t.Fatalf("InstallDotsWatchService: %v", err)
	}
	if len(info.Files) == 0 {
		t.Fatal("InstallDotsWatchService returned no files")
	}
	content, err := os.ReadFile(info.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	wantExe, err := filepath.Abs(filepath.Join("tools", "omni"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), wantExe) {
		t.Fatalf("service file missing absolute executable %q:\n%s", wantExe, string(content))
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
	inactiveSourceDir := filepath.Join(repoDir, "dotfiles", "old-host", ".config", "old-host")
	ignoredSourceDir := filepath.Join(repoDir, "dotfiles", "ignored", ".config", "ignored")
	targetDir := filepath.Join(home, ".config", "nvim")
	ignoredTargetDir := filepath.Join(home, ".config", "ignored")
	for _, dir := range []string{
		sourceDir,
		inactiveSourceDir,
		ignoredSourceDir,
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
	if containsString(paths, inactiveSourceDir) {
		t.Fatalf("paths = %v, inactive package dir should not be watched", paths)
	}
	if containsString(paths, ignoredSourceDir) {
		t.Fatalf("paths = %v, ignored package dir should not be watched", paths)
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
	eventCh := make(chan DotsWatchEvent, 1)
	syncCh := make(chan DotsWatchSyncResult, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.DotsWatch(ctx, DotsWatchOptions{
			Debounce: minDotsWatchDebounce,
			OnStart: func(start DotsWatchStart) {
				startCh <- start
			},
			OnEvent: func(event DotsWatchEvent) {
				select {
				case eventCh <- event:
				default:
				}
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

func TestDotsWatch_SyncsAfterManagedSymlinkReplaced(t *testing.T) {
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
	sourceDir := filepath.Join(repoDir, "dotfiles", "zsh", ".config", "zsh")
	sourceFile := filepath.Join(sourceDir, "zshrc")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("setopt prompt_subst\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldSourceTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(sourceFile, oldSourceTime, oldSourceTime); err != nil {
		t.Fatalf("Chtimes source file: %v", err)
	}
	targetDir := filepath.Join(home, ".config", "zsh")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(targetDir, "zshrc")
	if err := os.Symlink(sourceFile, targetPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := config.Save(cfgPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repoDir},
		Hosts:    map[string][]string{"testhost": {}},
		Groups: []*config.GroupConfig{{
			Name:    "testhost",
			Special: "host",
			Dots:    []config.DotEntry{{Name: "zsh", Path: "~/.config/zsh/zshrc"}},
		}},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	a := &App{ConfigPath: cfgPath}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startCh := make(chan DotsWatchStart, 1)
	eventCh := make(chan DotsWatchEvent, 1)
	syncCh := make(chan DotsWatchSyncResult, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.DotsWatch(ctx, DotsWatchOptions{
			Debounce: minDotsWatchDebounce,
			OnStart: func(start DotsWatchStart) {
				startCh <- start
			},
			OnEvent: func(event DotsWatchEvent) {
				select {
				case eventCh <- event:
				default:
				}
			},
			OnSync: func(result DotsWatchSyncResult) {
				syncCh <- result
				cancel()
			},
		})
	}()

	select {
	case start := <-startCh:
		if !containsString(start.Paths, targetDir) {
			t.Fatalf("watch paths = %v, want local symlink parent %s", start.Paths, targetDir)
		}
	case err := <-errCh:
		t.Fatalf("DotsWatch exited early: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("DotsWatch did not start")
	}

	replaceCtx, stopReplacing := context.WithCancel(context.Background())
	defer stopReplacing()
	replaceErrCh := make(chan error, 1)
	go func() {
		replacementPath := filepath.Join(targetDir, "zshrc.new")
		for {
			if err := os.WriteFile(replacementPath, []byte("local replacement\n"), 0o644); err != nil {
				replaceErrCh <- err
				return
			}
			if err := os.Rename(replacementPath, targetPath); err != nil {
				replaceErrCh <- err
				return
			}
			select {
			case <-replaceCtx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()

	select {
	case event := <-eventCh:
		stopReplacing()
		if filepath.Dir(filepath.Clean(event.Path)) != targetDir {
			t.Fatalf("watch event path = %q, want event inside %q", event.Path, targetDir)
		}
	case err := <-replaceErrCh:
		t.Fatalf("replace managed symlink: %v", err)
	case err := <-errCh:
		t.Fatalf("DotsWatch exited before symlink replacement event: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("DotsWatch did not observe managed symlink replacement")
	}

	select {
	case result := <-syncCh:
		if result.Err != nil {
			t.Fatalf("watch sync error: %v", result.Err)
		}
		if filepath.Dir(filepath.Clean(result.Event.Path)) != targetDir {
			t.Fatalf("watch sync event path = %q, want event inside %q", result.Event.Path, targetDir)
		}
	case err := <-errCh:
		t.Fatalf("DotsWatch exited before symlink replacement sync: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("DotsWatch did not sync after managed symlink replacement")
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
	t.Parallel()
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

func TestCollectDotsWatchPaths_ExactSymlinkWatchesLocalParentAndResolvedTarget(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	localDir := filepath.Join(home, ".config")
	targetDir := filepath.Join(t.TempDir(), "nvim")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(localDir, "nvim")
	if err := os.Symlink(targetDir, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	resolvedTargetDir, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		t.Fatal(err)
	}

	out := make(map[string]struct{})
	if err := collectDotsWatchPaths(linkPath, out); err != nil {
		t.Fatalf("collectDotsWatchPaths: %v", err)
	}
	paths := sortedWatchPaths(out)
	if !containsString(paths, localDir) {
		t.Fatalf("paths = %v, want local symlink parent %s", paths, localDir)
	}
	if !containsString(paths, resolvedTargetDir) {
		t.Fatalf("paths = %v, want resolved target %s", paths, resolvedTargetDir)
	}
}

func TestSetDotsWatchPaths_IgnoresNonExistentWatchOnRemove(t *testing.T) {
	t.Parallel()
	watcher := &fakeDotsPathWatcher{removeErr: fsnotify.ErrNonExistentWatch}
	current := map[string]struct{}{"/tmp/gone": {}}

	watched, err := setDotsWatchPaths(watcher, current, nil)
	if err != nil {
		t.Fatalf("setDotsWatchPaths: %v", err)
	}
	if len(watched) != 0 {
		t.Fatalf("watched = %v, want removed path dropped", sortedWatchPaths(watched))
	}
	if len(watcher.removed) != 1 || watcher.removed[0] != "/tmp/gone" {
		t.Fatalf("removed = %v, want /tmp/gone", watcher.removed)
	}
}

type fakeDotsPathWatcher struct {
	addErr    error
	removeErr error
	added     []string
	removed   []string
}

func (w *fakeDotsPathWatcher) Add(path string) error {
	w.added = append(w.added, path)
	return w.addErr
}

func (w *fakeDotsPathWatcher) Remove(path string) error {
	w.removed = append(w.removed, path)
	return w.removeErr
}

func TestDotsWatchRejectsTooSmallDebounce(t *testing.T) {
	t.Parallel()
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
