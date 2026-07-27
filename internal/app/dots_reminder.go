package app

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

const (
	dotsReminderServiceName  = "omni-dots-reminder"
	dotsReminderLaunchLabel  = "com.lkshrk.omni.dots-reminder"
	defaultReminderInterval  = 24 * time.Hour
	minReminderInterval      = time.Minute
	dotsReminderServiceLinux = "systemd"
	dotsReminderServiceMacOS = "launchd"
)

var dotsReminderIntervalChoices = []time.Duration{
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
	4 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
	48 * time.Hour,
	7 * 24 * time.Hour,
}

func DefaultDotsReminderInterval() time.Duration {
	return defaultReminderInterval
}

func DotsReminderIntervalChoices() []time.Duration {
	return append([]time.Duration(nil), dotsReminderIntervalChoices...)
}

func DotsReminderInterval(service *DotsReminderService) time.Duration {
	if service != nil && service.Interval > 0 {
		return service.Interval
	}
	return DefaultDotsReminderInterval()
}

type DotsReminderReason struct {
	Kind    string   `json:"kind"`
	Count   int      `json:"count,omitempty"`
	Names   []string `json:"names,omitempty"`
	Message string   `json:"message"`
}

// DotsReminderResult — Entries contains only entries that need attention.
type DotsReminderResult struct {
	NeedsReminder   bool                 `json:"needs_reminder"`
	Disabled        bool                 `json:"disabled,omitempty"`
	GitStatus       string               `json:"git_status,omitempty"`
	DiscoveredCount int                  `json:"discovered_count,omitempty"`
	Entries         []DotStatus          `json:"entries,omitempty"`
	Reasons         []DotsReminderReason `json:"reasons,omitempty"`
}

// DotsReminder — Does not mutate config, repo, or links.
func (a *App) DotsReminder(ctx context.Context) (*DotsReminderResult, error) {
	rootCfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("dots reminder: load config: %w", err)
	}
	if err := a.requireDotsEnabled(rootCfg); err != nil {
		return &DotsReminderResult{Disabled: true}, nil
	}
	status, err := a.DiscoverDotsStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("dots reminder: %w", err)
	}
	return AnalyzeDotsReminderStatus(status), nil
}

// AnalyzeDotsReminderStatus — Separate from DotsReminder so tests can exercise the policy without a filesystem-backed repo.
func AnalyzeDotsReminderStatus(status *DotsStatusResult) *DotsReminderResult {
	if status == nil {
		return &DotsReminderResult{}
	}
	result := &DotsReminderResult{
		GitStatus:       strings.TrimSpace(status.GitStatus),
		DiscoveredCount: status.DiscoveredCount,
	}
	namesByKind := make(map[string][]string)
	seen := make(map[string]bool)
	for _, entry := range status.Entries {
		kind := dotsReminderKind(entry.State)
		if kind == "" {
			continue
		}
		key := string(entry.State) + "\x00" + entry.Name + "\x00" + entry.TargetPath
		if seen[key] {
			continue
		}
		seen[key] = true
		result.Entries = append(result.Entries, entry)
		namesByKind[kind] = append(namesByKind[kind], entry.Name)
	}
	if result.GitStatus != "" {
		lines := 0
		for _, line := range strings.Split(result.GitStatus, "\n") {
			if strings.TrimSpace(line) != "" {
				lines++
			}
		}
		result.Reasons = append(result.Reasons, DotsReminderReason{
			Kind:    "git",
			Count:   lines,
			Message: fmt.Sprintf("dotfiles repo has %d pending git change(s)", lines),
		})
	}
	for _, kind := range []string{"sync", "conflict", "new", "source"} {
		names := namesByKind[kind]
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		result.Reasons = append(result.Reasons, DotsReminderReason{
			Kind:    kind,
			Count:   len(names),
			Names:   names,
			Message: dotsReminderMessage(kind, names),
		})
	}
	result.NeedsReminder = len(result.Reasons) > 0
	return result
}

func dotsReminderKind(state dots.State) string {
	switch state {
	case dots.StateMissing, dots.StateBroken, dots.StateModified, dots.StateLocalOnly, dots.StateRepoOnly:
		return "sync"
	case dots.StateConflict, dots.StateUntrackedConflict, dots.StateAmbiguous:
		return "conflict"
	case dots.StateUntrackedLinked:
		return "new"
	case dots.StateNoSource:
		return "source"
	default:
		return ""
	}
}

func dotsReminderMessage(kind string, names []string) string {
	count := len(names)
	switch kind {
	case "sync":
		return fmt.Sprintf("%d dot entr%s need sync: %s", count, pluralY(count), joinLimited(names))
	case "conflict":
		return fmt.Sprintf("%d dot entr%s need conflict resolution: %s", count, pluralY(count), joinLimited(names))
	case "new":
		return fmt.Sprintf("%d untracked dot candidate(s) found: %s", count, joinLimited(names))
	case "source":
		return fmt.Sprintf("%d dot entr%s have no repo source: %s", count, pluralY(count), joinLimited(names))
	default:
		return fmt.Sprintf("%d dot entr%s need attention: %s", count, pluralY(count), joinLimited(names))
	}
}

func pluralY(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}

func joinLimited(names []string) string {
	const limit = 5
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:limit], ", ") + fmt.Sprintf(", +%d more", len(names)-limit)
}

type DotsReminderInstallOptions struct {
	Interval   time.Duration
	Notify     bool
	Executable string
	Activate   bool
}

type DotsReminderService struct {
	Platform  string        `json:"platform"`
	Interval  time.Duration `json:"interval"`
	Notify    bool          `json:"notify"`
	Files     []string      `json:"files"`
	Installed bool          `json:"installed,omitempty"`
}

func (a *App) InstallDotsReminderService(ctx context.Context, opts DotsReminderInstallOptions) (*DotsReminderService, error) {
	if opts.Interval == 0 {
		opts.Interval = defaultReminderInterval
	}
	if err := validateReminderInterval(opts.Interval); err != nil {
		return nil, err
	}
	exe := opts.Executable
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve omni executable: %w", err)
		}
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		return nil, fmt.Errorf("resolve omni executable: %w", err)
	}
	service, err := a.dotsReminderServiceForPlatform(runtime.GOOS, exe, opts.Interval, opts.Notify)
	if err != nil {
		return nil, err
	}
	for _, file := range service.files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
			return nil, fmt.Errorf("create service directory %q: %w", filepath.Dir(file.path), err)
		}
		if err := os.WriteFile(file.path, []byte(file.content), 0o644); err != nil {
			return nil, fmt.Errorf("write service file %q: %w", file.path, err)
		}
	}
	info := service.info()
	if opts.Activate {
		if err := service.activate(ctx, a.newExecutor()); err != nil {
			return info, err
		}
	}
	info.Installed = true
	return info, nil
}

func (a *App) UninstallDotsReminderService(ctx context.Context) (*DotsReminderService, error) {
	service, err := a.dotsReminderServiceForPlatform(runtime.GOOS, "", defaultReminderInterval, true)
	if err != nil {
		return nil, err
	}
	if err := service.deactivate(ctx, a.newExecutor()); err != nil {
		return service.info(), err
	}
	for _, file := range service.files {
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return service.info(), fmt.Errorf("remove service file %q: %w", file.path, err)
		}
	}
	return service.info(), nil
}

func (a *App) DotsReminderServiceStatus() (*DotsReminderService, error) {
	service, err := a.dotsReminderServiceForPlatform(runtime.GOOS, "", defaultReminderInterval, true)
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
		if err := service.applyReminderStatus(info); err != nil {
			return nil, err
		}
	}
	return info, nil
}

type dotsReminderServiceFile struct {
	path    string
	content string
}

type dotsReminderServiceSpec struct {
	platform string
	interval time.Duration
	notify   bool
	files    []dotsReminderServiceFile
}

func (s dotsReminderServiceSpec) info() *DotsReminderService {
	files := make([]string, 0, len(s.files))
	for _, file := range s.files {
		files = append(files, file.path)
	}
	return &DotsReminderService{
		Platform: s.platform,
		Interval: s.interval,
		Notify:   s.notify,
		Files:    files,
	}
}

func (s dotsReminderServiceSpec) applyReminderStatus(info *DotsReminderService) error {
	switch s.platform {
	case dotsReminderServiceMacOS:
		if len(s.files) == 0 {
			return nil
		}
		content, err := os.ReadFile(s.files[0].path)
		if err != nil {
			return fmt.Errorf("read service file %q: %w", s.files[0].path, err)
		}
		if seconds, ok, err := launchdPlistInteger(string(content), "StartInterval"); err != nil {
			return fmt.Errorf("parse service file %q: %w", s.files[0].path, err)
		} else if ok {
			info.Interval = time.Duration(seconds) * time.Second
		}
		if args, ok, err := launchdPlistStringArray(string(content), "ProgramArguments"); err != nil {
			return fmt.Errorf("parse service file %q: %w", s.files[0].path, err)
		} else if ok {
			info.Notify = stringSliceContains(args, "--notify")
		}
	case dotsReminderServiceLinux:
		for _, file := range s.files {
			content, err := os.ReadFile(file.path)
			if err != nil {
				return fmt.Errorf("read service file %q: %w", file.path, err)
			}
			switch filepath.Base(file.path) {
			case dotsReminderServiceName + ".service":
				info.Notify = strings.Contains(string(content), "--notify")
			case dotsReminderServiceName + ".timer":
				if interval, ok, err := systemdUnitDuration(string(content), "OnUnitActiveSec"); err != nil {
					return fmt.Errorf("parse service file %q: %w", file.path, err)
				} else if ok {
					info.Interval = interval
				}
			}
		}
	}
	return nil
}

func (a *App) dotsReminderServiceForPlatform(platform, exe string, interval time.Duration, notify bool) (dotsReminderServiceSpec, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return dotsReminderServiceSpec{}, fmt.Errorf("resolve home directory: %w", err)
	}
	args := a.dotsReminderCommandArgs(exe, notify)
	switch platform {
	case "darwin":
		plist := filepath.Join(home, "Library", "LaunchAgents", dotsReminderLaunchLabel+".plist")
		logPath := filepath.Join(home, "Library", "Logs", dotsReminderServiceName+".log")
		errPath := filepath.Join(home, "Library", "Logs", dotsReminderServiceName+".err.log")
		return dotsReminderServiceSpec{
			platform: dotsReminderServiceMacOS,
			interval: interval,
			notify:   notify,
			files: []dotsReminderServiceFile{{
				path:    plist,
				content: launchdReminderPlist(dotsReminderLaunchLabel, args, int(interval.Seconds()), logPath, errPath),
			}},
		}, nil
	case "linux":
		dir := filepath.Join(userConfigHome(home), "systemd", "user")
		servicePath := filepath.Join(dir, dotsReminderServiceName+".service")
		timerPath := filepath.Join(dir, dotsReminderServiceName+".timer")
		return dotsReminderServiceSpec{
			platform: dotsReminderServiceLinux,
			interval: interval,
			notify:   notify,
			files: []dotsReminderServiceFile{
				{path: servicePath, content: systemdReminderService(args)},
				{path: timerPath, content: systemdReminderTimer(interval)},
			},
		}, nil
	default:
		return dotsReminderServiceSpec{}, fmt.Errorf("dots reminder services are not supported on %s yet", platform)
	}
}

func (a *App) dotsReminderCommandArgs(exe string, notify bool) []string {
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
	args = append(args, "dots", "reminder", "run")
	if notify {
		args = append(args, "--notify")
	}
	return args
}

func absoluteServiceCommandPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func (s dotsReminderServiceSpec) activate(ctx context.Context, exec executor.Executor) error {
	switch s.platform {
	case dotsReminderServiceMacOS:
		plist := s.files[0].path
		domain, label, err := launchdDomainLabel(dotsReminderLaunchLabel)
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
		if _, stderr, err := exec.Run(ctx, "systemctl", "--user", "enable", "--now", dotsReminderServiceName+".timer"); err != nil {
			return serviceCommandError("systemctl --user enable --now", err, stderr)
		}
	}
	return nil
}

func (s dotsReminderServiceSpec) deactivate(ctx context.Context, exec executor.Executor) error {
	switch s.platform {
	case dotsReminderServiceMacOS:
		domain, _, err := launchdDomainLabel(dotsReminderLaunchLabel)
		if err != nil {
			return err
		}
		_, _, _ = exec.Run(ctx, "launchctl", "bootout", domain, s.files[0].path)
	case dotsReminderServiceLinux:
		_, _, _ = exec.Run(ctx, "systemctl", "--user", "disable", "--now", dotsReminderServiceName+".timer")
		if _, stderr, err := exec.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return serviceCommandError("systemctl --user daemon-reload", err, stderr)
		}
	}
	return nil
}

func launchdDomainLabel(launchLabel string) (domain, label string, err error) {
	u, err := user.Current()
	if err != nil {
		return "", "", fmt.Errorf("resolve current user: %w", err)
	}
	domain = "gui/" + u.Uid
	return domain, domain + "/" + launchLabel, nil
}

func serviceCommandError(op string, err error, stderr string) error {
	if strings.TrimSpace(stderr) != "" {
		return fmt.Errorf("%s: %w\n%s", op, err, strings.TrimSpace(stderr))
	}
	return fmt.Errorf("%s: %w", op, err)
}

func validateReminderInterval(interval time.Duration) error {
	if interval < minReminderInterval {
		return fmt.Errorf("reminder interval must be at least %s", minReminderInterval)
	}
	return nil
}

func userConfigHome(home string) string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	return filepath.Join(home, ".config")
}

func launchdReminderPlist(label string, args []string, intervalSeconds int, stdoutPath, stderrPath string) string {
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
	writePlistKeyInteger(&b, "StartInterval", intervalSeconds)
	writePlistKeyBool(&b, "RunAtLoad", true)
	writePlistKeyString(&b, "StandardOutPath", stdoutPath)
	writePlistKeyString(&b, "StandardErrorPath", stderrPath)
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func writePlistKeyString(b *strings.Builder, key, value string) {
	fmt.Fprintf(b, "  <key>%s</key>\n", xmlText(key))
	fmt.Fprintf(b, "  <string>%s</string>\n", xmlText(value))
}

func writePlistKeyInteger(b *strings.Builder, key string, value int) {
	fmt.Fprintf(b, "  <key>%s</key>\n", xmlText(key))
	fmt.Fprintf(b, "  <integer>%d</integer>\n", value)
}

func writePlistKeyBool(b *strings.Builder, key string, value bool) {
	fmt.Fprintf(b, "  <key>%s</key>\n", xmlText(key))
	if value {
		b.WriteString("  <true/>\n")
		return
	}
	b.WriteString("  <false/>\n")
}

func xmlText(value string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(value)); err != nil {
		return value
	}
	return b.String()
}

func launchdPlistInteger(content, key string) (int, bool, error) {
	dec := xml.NewDecoder(strings.NewReader(content))
	var lastKey string
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return 0, false, nil
			}
			return 0, false, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			text, err := xmlElementText(dec, start)
			if err != nil {
				return 0, false, err
			}
			lastKey = text
		case "integer":
			text, err := xmlElementText(dec, start)
			if err != nil {
				return 0, false, err
			}
			if lastKey != key {
				lastKey = ""
				continue
			}
			value, err := strconv.Atoi(strings.TrimSpace(text))
			if err != nil {
				return 0, false, err
			}
			return value, true, nil
		}
	}
}

func launchdPlistStringArray(content, key string) ([]string, bool, error) {
	dec := xml.NewDecoder(strings.NewReader(content))
	var (
		lastKey string
		inArray bool
		values  []string
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return nil, false, nil
			}
			return nil, false, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			switch tok.Name.Local {
			case "key":
				text, err := xmlElementText(dec, tok)
				if err != nil {
					return nil, false, err
				}
				lastKey = text
			case "array":
				if lastKey == key {
					inArray = true
					lastKey = ""
					values = nil
				}
			case "string":
				text, err := xmlElementText(dec, tok)
				if err != nil {
					return nil, false, err
				}
				if inArray {
					values = append(values, text)
				}
			}
		case xml.EndElement:
			if tok.Name.Local == "array" && inArray {
				return values, true, nil
			}
		}
	}
}

func xmlElementText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var text string
	if err := dec.DecodeElement(&text, &start); err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func systemdUnitDuration(content, key string) (time.Duration, bool, error) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		value, ok := strings.CutPrefix(line, key+"=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return 0, false, nil
		}
		if allDigits(value) {
			value += "s"
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return 0, false, err
		}
		return duration, true, nil
	}
	return 0, false, nil
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func systemdReminderService(args []string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Omni dotfiles reminder",
		"",
		"[Service]",
		"Type=oneshot",
		"ExecStart=" + systemdCommand(args),
		"",
	}, "\n")
}

func systemdReminderTimer(interval time.Duration) string {
	seconds := int(interval.Seconds())
	return strings.Join([]string{
		"[Unit]",
		"Description=Run Omni dotfiles reminder periodically",
		"",
		"[Timer]",
		"OnBootSec=5min",
		fmt.Sprintf("OnUnitActiveSec=%ds", seconds),
		"Persistent=true",
		"Unit=" + dotsReminderServiceName + ".service",
		"",
		"[Install]",
		"WantedBy=timers.target",
		"",
	}, "\n")
}

func systemdCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = systemdQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func systemdQuote(arg string) string {
	if arg == "" {
		return `""`
	}
	for _, r := range arg {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("@%_+=:,./-", r) {
			continue
		}
		return strconv.Quote(arg)
	}
	return arg
}
