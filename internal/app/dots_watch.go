package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

const (
	dotsWatchServiceName      = "omni-dots-watch"
	dotsWatchLaunchLabel      = "com.lkshrk.omni.dots-watch"
	defaultDotsWatchDebounce  = 5 * time.Second
	minDotsWatchDebounce      = 500 * time.Millisecond
	dotsWatchRestartDelaySecs = 10
)

var dotsWatchDebounceChoices = []time.Duration{
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

// DefaultDotsWatchDebounce is the debounce used when no watcher debounce is
// specified.
func DefaultDotsWatchDebounce() time.Duration {
	return defaultDotsWatchDebounce
}

// DotsWatchDebounceChoices returns preset debounce values for watch controls.
func DotsWatchDebounceChoices() []time.Duration {
	return append([]time.Duration(nil), dotsWatchDebounceChoices...)
}

func DotsWatchDebounce(service *DotsWatchService) time.Duration {
	if service != nil && service.Debounce > 0 {
		return service.Debounce
	}
	return DefaultDotsWatchDebounce()
}

// DotsWatchOptions configures the long-running dotfile watcher.
type DotsWatchOptions struct {
	Debounce time.Duration
	OnStart  func(DotsWatchStart)
	OnEvent  func(DotsWatchEvent)
	OnSync   func(DotsWatchSyncResult)
}

// DotsWatchStart reports the initial watcher state.
type DotsWatchStart struct {
	WatchedPaths int
	Paths        []string
}

// DotsWatchEvent reports a filesystem change that scheduled a sync.
type DotsWatchEvent struct {
	Path string
	Op   string
}

// DotsWatchSyncResult reports one debounced sync attempt. Err is included for
// logging; the watch loop keeps running after sync failures.
type DotsWatchSyncResult struct {
	Event        DotsWatchEvent
	Ops          []dots.Op
	Err          error
	WatchedPaths int
}

// DotsWatchInstallOptions controls native user-service installation.
type DotsWatchInstallOptions struct {
	Debounce   time.Duration
	Executable string
	Activate   bool
}

// DotsWatchService describes generated watch service files.
type DotsWatchService struct {
	Platform  string        `json:"platform"`
	Debounce  time.Duration `json:"debounce"`
	Files     []string      `json:"files"`
	Installed bool          `json:"installed"`
}

// DotsWatch runs a kernel-backed filesystem watcher over configured dotfile
// source and target paths. On debounced changes it delegates to DotsSyncContext.
func (a *App) DotsWatch(ctx context.Context, opts DotsWatchOptions) error {
	debounce, err := normalizeDotsWatchDebounce(opts.Debounce)
	if err != nil {
		return err
	}
	if err := a.requireStow(ctx); err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("dots watch: create watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	paths, err := a.dotsWatchPaths()
	if err != nil {
		return fmt.Errorf("dots watch: resolve paths: %w", err)
	}
	watched, err := setDotsWatchPaths(watcher, nil, paths)
	if err != nil {
		return err
	}
	if opts.OnStart != nil {
		opts.OnStart(DotsWatchStart{WatchedPaths: len(watched), Paths: sortedWatchPaths(watched)})
	}

	var (
		pending DotsWatchEvent
		timer   *time.Timer
		timerC  <-chan time.Time
	)
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerC = nil
	}
	defer stopTimer()
	schedule := func(event DotsWatchEvent) {
		pending = event
		if timer == nil {
			timer = time.NewTimer(debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
		}
		timerC = timer.C
	}

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !dotsWatchEventRelevant(event) {
				continue
			}
			change := DotsWatchEvent{Path: event.Name, Op: event.Op.String()}
			if opts.OnEvent != nil {
				opts.OnEvent(change)
			}
			schedule(change)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			if err != nil {
				return fmt.Errorf("dots watch: watcher: %w", err)
			}
		case <-timerC:
			timerC = nil
			ops, syncErr := a.DotsSyncContext(ctx, dots.SyncOptions{})
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					return nil
				}
				return ctx.Err()
			}
			paths, pathErr := a.dotsWatchPaths()
			if pathErr == nil {
				watched, pathErr = setDotsWatchPaths(watcher, watched, paths)
			}
			if opts.OnSync != nil {
				opts.OnSync(DotsWatchSyncResult{
					Event:        pending,
					Ops:          ops,
					Err:          syncErr,
					WatchedPaths: len(watched),
				})
			}
			pending = DotsWatchEvent{}
			if pathErr != nil {
				return pathErr
			}
		}
	}
}

// InstallDotsWatchService writes and optionally activates the native user-level
// service for continuous dotfile syncing.
func (a *App) InstallDotsWatchService(ctx context.Context, opts DotsWatchInstallOptions) (*DotsWatchService, error) {
	if err := a.requireStow(ctx); err != nil {
		return nil, err
	}
	debounce, err := normalizeDotsWatchDebounce(opts.Debounce)
	if err != nil {
		return nil, err
	}
	exe := opts.Executable
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable: %w", err)
		}
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	service, err := a.dotsWatchServiceForPlatform(runtime.GOOS, exe, debounce)
	if err != nil {
		return nil, err
	}
	for _, file := range service.files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			return nil, fmt.Errorf("create service dir %q: %w", filepath.Dir(file.path), err)
		}
		if err := os.WriteFile(file.path, []byte(file.content), 0o644); err != nil {
			return nil, fmt.Errorf("write service file %q: %w", file.path, err)
		}
	}
	info := service.info()
	if opts.Activate {
		if err := service.activate(ctx, executor.New()); err != nil {
			return info, err
		}
	}
	info.Installed = true
	return info, nil
}

// UninstallDotsWatchService disables and removes watch service files.
func (a *App) UninstallDotsWatchService(ctx context.Context) (*DotsWatchService, error) {
	service, err := a.dotsWatchServiceForPlatform(runtime.GOOS, "", defaultDotsWatchDebounce)
	if err != nil {
		return nil, err
	}
	if err := service.deactivate(ctx, executor.New()); err != nil {
		return service.info(), err
	}
	for _, file := range service.files {
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return service.info(), fmt.Errorf("remove service file %q: %w", file.path, err)
		}
	}
	return service.info(), nil
}

// DotsWatchServiceStatus reports whether watch service files exist.
func (a *App) DotsWatchServiceStatus() (*DotsWatchService, error) {
	service, err := a.dotsWatchServiceForPlatform(runtime.GOOS, "", defaultDotsWatchDebounce)
	if err != nil {
		return nil, err
	}
	info := service.info()
	info.Installed = true
	for _, file := range service.files {
		if _, err := os.Stat(file.path); err != nil {
			if os.IsNotExist(err) {
				info.Installed = false
				continue
			}
			return nil, fmt.Errorf("stat service file %q: %w", file.path, err)
		}
	}
	if info.Installed {
		if err := service.applyWatchStatus(info); err != nil {
			return nil, err
		}
	}
	return info, nil
}

type dotsWatchServiceSpec struct {
	platform string
	debounce time.Duration
	files    []dotsReminderServiceFile
}

func (s dotsWatchServiceSpec) info() *DotsWatchService {
	files := make([]string, 0, len(s.files))
	for _, file := range s.files {
		files = append(files, file.path)
	}
	return &DotsWatchService{
		Platform: s.platform,
		Debounce: s.debounce,
		Files:    files,
	}
}

func (s dotsWatchServiceSpec) applyWatchStatus(info *DotsWatchService) error {
	switch s.platform {
	case dotsReminderServiceMacOS:
		if len(s.files) == 0 {
			return nil
		}
		content, err := os.ReadFile(s.files[0].path)
		if err != nil {
			return fmt.Errorf("read service file %q: %w", s.files[0].path, err)
		}
		args, ok, err := launchdPlistStringArray(string(content), "ProgramArguments")
		if err != nil {
			return fmt.Errorf("parse service file %q: %w", s.files[0].path, err)
		}
		if ok {
			if debounce, ok, err := durationFlagValue(args, "--debounce"); err != nil {
				return fmt.Errorf("parse service file %q: %w", s.files[0].path, err)
			} else if ok {
				info.Debounce = debounce
			}
		}
	case dotsReminderServiceLinux:
		if len(s.files) == 0 {
			return nil
		}
		content, err := os.ReadFile(s.files[0].path)
		if err != nil {
			return fmt.Errorf("read service file %q: %w", s.files[0].path, err)
		}
		if debounce, ok, err := durationFlagValue(systemdExecStartArgs(string(content)), "--debounce"); err != nil {
			return fmt.Errorf("parse service file %q: %w", s.files[0].path, err)
		} else if ok {
			info.Debounce = debounce
		}
	}
	return nil
}

func (a *App) dotsWatchServiceForPlatform(platform, exe string, debounce time.Duration) (dotsWatchServiceSpec, error) {
	debounce, err := normalizeDotsWatchDebounce(debounce)
	if err != nil {
		return dotsWatchServiceSpec{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dotsWatchServiceSpec{}, fmt.Errorf("resolve home directory: %w", err)
	}
	args := a.dotsWatchCommandArgs(exe, debounce)
	switch platform {
	case "darwin":
		plist := filepath.Join(home, "Library", "LaunchAgents", dotsWatchLaunchLabel+".plist")
		logPath := filepath.Join(home, "Library", "Logs", dotsWatchServiceName+".log")
		errPath := filepath.Join(home, "Library", "Logs", dotsWatchServiceName+".err.log")
		return dotsWatchServiceSpec{
			platform: dotsReminderServiceMacOS,
			debounce: debounce,
			files: []dotsReminderServiceFile{{
				path:    plist,
				content: launchdWatchPlist(dotsWatchLaunchLabel, args, logPath, errPath),
			}},
		}, nil
	case "linux":
		dir := filepath.Join(userConfigHome(home), "systemd", "user")
		servicePath := filepath.Join(dir, dotsWatchServiceName+".service")
		return dotsWatchServiceSpec{
			platform: dotsReminderServiceLinux,
			debounce: debounce,
			files: []dotsReminderServiceFile{{
				path:    servicePath,
				content: systemdWatchService(args),
			}},
		}, nil
	default:
		return dotsWatchServiceSpec{}, fmt.Errorf("dots watch services are not supported on %s yet", platform)
	}
}

func (a *App) dotsWatchCommandArgs(exe string, debounce time.Duration) []string {
	if exe == "" {
		exe = "omni"
	}
	args := []string{exe}
	if a.ConfigPath != "" {
		args = append(args, "--config", absoluteServiceCommandPath(a.ConfigPath))
	}
	if a.CacheDir != "" {
		args = append(args, "--cache-dir", absoluteServiceCommandPath(a.CacheDir))
	}
	return append(args, "dots", "watch", "run", "--debounce", debounce.String())
}

func systemdExecStartArgs(content string) []string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		value, ok := strings.CutPrefix(line, "ExecStart=")
		if ok {
			return strings.Fields(value)
		}
	}
	return nil
}

func durationFlagValue(args []string, flag string) (time.Duration, bool, error) {
	for i, arg := range args {
		arg = strings.Trim(arg, `"'`)
		if value, ok := strings.CutPrefix(arg, flag+"="); ok {
			duration, err := time.ParseDuration(strings.Trim(value, `"'`))
			if err != nil {
				return 0, false, err
			}
			return duration, true, nil
		}
		if arg != flag || i+1 >= len(args) {
			continue
		}
		duration, err := time.ParseDuration(strings.Trim(args[i+1], `"'`))
		if err != nil {
			return 0, false, err
		}
		return duration, true, nil
	}
	return 0, false, nil
}

func (s dotsWatchServiceSpec) activate(ctx context.Context, exec executor.Executor) error {
	switch s.platform {
	case dotsReminderServiceMacOS:
		plist := s.files[0].path
		domain, label, err := launchdDomainLabel(dotsWatchLaunchLabel)
		if err != nil {
			return err
		}
		_, _, _ = exec.Run(ctx, "launchctl", "bootout", domain, plist)
		if _, stderr, err := exec.Run(ctx, "launchctl", "bootstrap", domain, plist); err != nil {
			return serviceCommandError("launchctl bootstrap", err, stderr)
		}
		if _, stderr, err := exec.Run(ctx, "launchctl", "enable", label); err != nil {
			return serviceCommandError("launchctl enable", err, stderr)
		}
		if _, stderr, err := exec.Run(ctx, "launchctl", "kickstart", "-k", label); err != nil {
			return serviceCommandError("launchctl kickstart", err, stderr)
		}
	case dotsReminderServiceLinux:
		if _, stderr, err := exec.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return serviceCommandError("systemctl --user daemon-reload", err, stderr)
		}
		if _, stderr, err := exec.Run(ctx, "systemctl", "--user", "enable", "--now", dotsWatchServiceName+".service"); err != nil {
			return serviceCommandError("systemctl --user enable --now", err, stderr)
		}
	}
	return nil
}

func (s dotsWatchServiceSpec) deactivate(ctx context.Context, exec executor.Executor) error {
	switch s.platform {
	case dotsReminderServiceMacOS:
		domain, _, err := launchdDomainLabel(dotsWatchLaunchLabel)
		if err != nil {
			return err
		}
		_, _, _ = exec.Run(ctx, "launchctl", "bootout", domain, s.files[0].path)
	case dotsReminderServiceLinux:
		_, _, _ = exec.Run(ctx, "systemctl", "--user", "disable", "--now", dotsWatchServiceName+".service")
		if _, stderr, err := exec.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return serviceCommandError("systemctl --user daemon-reload", err, stderr)
		}
	}
	return nil
}

func (a *App) dotsWatchPaths() ([]string, error) {
	rawRepo, entries, err := a.dotsWatchEntries()
	if err != nil {
		return nil, err
	}
	mgr, err := dots.New(dotsContentPath(rawRepo), entries)
	if err != nil {
		return nil, fmt.Errorf("dots watch: resolve entries: %w", err)
	}
	roots := make([]string, 0, len(mgr.Entries)*2)
	for _, entry := range mgr.Entries {
		roots = append(roots, entry.SourcePath, entry.TargetPath)
	}
	watchPaths := make(map[string]struct{})
	for _, root := range roots {
		if err := collectDotsWatchPaths(root, watchPaths); err != nil {
			return nil, err
		}
	}
	return sortedWatchPaths(watchPaths), nil
}

func (a *App) dotsWatchEntries() (string, []config.DotEntry, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return "", nil, fmt.Errorf("dots watch: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return "", nil, err
	}
	repoPath, err := resolveRepoPath(a.effectiveSettings(rootCfg).DotsRepo)
	if err != nil {
		return "", nil, err
	}
	if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
		return "", nil, err
	}
	groups := rootCfg.Groups
	if effective, _, ok := effectiveHostGroups(rootCfg, groups, currentMachineGroupName()); ok {
		groups = effective
	}
	entries := collectDots(rootCfg, groups)
	entries = resolveDotEntryPackagesForCurrentHost(entries)
	entries = filterActiveDotEntries(entries)
	if err := a.requireSafeTestDotsMutation(repoPath, entries); err != nil {
		return "", nil, err
	}
	return repoPath, entries, nil
}

func collectDotsWatchPaths(root string, out map[string]struct{}) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	root = filepath.Clean(root)
	existing, exact, err := nearestExistingDotsWatchPath(root)
	if err != nil || existing == "" {
		return err
	}
	info, err := os.Lstat(existing)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("dots watch: stat %q: %w", existing, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		out[filepath.Dir(existing)] = struct{}{}
		resolved, err := filepath.EvalSymlinks(existing)
		if err != nil {
			return nil
		}
		existing = resolved
		info, err = os.Stat(existing)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("dots watch: stat %q: %w", existing, err)
		}
	}
	if !exact {
		if info.IsDir() {
			out[filepath.Clean(existing)] = struct{}{}
			return nil
		}
		out[filepath.Dir(existing)] = struct{}{}
		return nil
	}
	if !info.IsDir() {
		out[filepath.Dir(existing)] = struct{}{}
		return nil
	}
	return filepath.WalkDir(existing, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if dotsWatchSkipDir(path, d) {
			return filepath.SkipDir
		}
		out[filepath.Clean(path)] = struct{}{}
		return nil
	})
}

func nearestExistingDotsWatchPath(path string) (string, bool, error) {
	clean := filepath.Clean(path)
	for {
		if _, err := os.Lstat(clean); err == nil {
			return clean, clean == filepath.Clean(path), nil
		} else if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("dots watch: stat %q: %w", clean, err)
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			return "", false, nil
		}
		clean = parent
	}
}

func dotsWatchSkipDir(path string, d os.DirEntry) bool {
	if d.Name() == ".git" {
		return true
	}
	return dotsWatchPathHasSegment(path, ".git")
}

func dotsWatchPathHasSegment(path, segment string) bool {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == segment {
			return true
		}
	}
	return false
}

type dotsPathWatcher interface {
	Add(string) error
	Remove(string) error
}

func setDotsWatchPaths(watcher dotsPathWatcher, current map[string]struct{}, paths []string) (map[string]struct{}, error) {
	if current == nil {
		current = make(map[string]struct{})
	}
	next := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		next[path] = struct{}{}
	}
	for path := range current {
		if _, ok := next[path]; ok {
			continue
		}
		if err := watcher.Remove(path); err != nil && !os.IsNotExist(err) && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			return current, fmt.Errorf("dots watch: remove %q: %w", path, err)
		}
		delete(current, path)
	}
	for path := range next {
		if _, ok := current[path]; ok {
			continue
		}
		if err := watcher.Add(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return current, fmt.Errorf("dots watch: add %q: %w", path, err)
		}
		current[path] = struct{}{}
	}
	return current, nil
}

func sortedWatchPaths(paths map[string]struct{}) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func dotsWatchEventRelevant(event fsnotify.Event) bool {
	if event.Name == "" || dotsWatchPathHasSegment(event.Name, ".git") {
		return false
	}
	return event.Op&^fsnotify.Chmod != 0
}

func normalizeDotsWatchDebounce(debounce time.Duration) (time.Duration, error) {
	if debounce == 0 {
		debounce = defaultDotsWatchDebounce
	}
	if debounce < minDotsWatchDebounce {
		return 0, fmt.Errorf("watch debounce must be at least %s", minDotsWatchDebounce)
	}
	return debounce, nil
}

func launchdWatchPlist(label string, args []string, stdoutPath, stderrPath string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")
	writePlistKeyString(&b, "Label", label)
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range args {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xmlText(arg))
	}
	b.WriteString("  </array>\n")
	writePlistKeyBool(&b, "RunAtLoad", true)
	writePlistKeyBool(&b, "KeepAlive", true)
	writePlistKeyString(&b, "StandardOutPath", stdoutPath)
	writePlistKeyString(&b, "StandardErrorPath", stderrPath)
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func systemdWatchService(args []string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Omni dotfiles watch service",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + systemdCommand(args),
		"Restart=on-failure",
		fmt.Sprintf("RestartSec=%ds", dotsWatchRestartDelaySecs),
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n")
}
