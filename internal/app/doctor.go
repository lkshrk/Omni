package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// DoctorStatus is the severity of one diagnostic check.
type DoctorStatus string

const (
	DoctorStatusOK   DoctorStatus = "ok"
	DoctorStatusWarn DoctorStatus = "warn"
	DoctorStatusFail DoctorStatus = "fail"
)

// DoctorDetailGroup is a header with nested detail items, used when a single
// check reports findings across multiple entries (e.g. per-dot ignore audits).
type DoctorDetailGroup struct {
	Header string   `json:"header"`
	Items  []string `json:"items,omitempty"`
}

// DoctorCheck describes one read-only diagnostic result.
type DoctorCheck struct {
	ID      string              `json:"id"`
	Label   string              `json:"label"`
	Status  DoctorStatus        `json:"status"`
	Message string              `json:"message"`
	Details []string            `json:"details,omitempty"`
	Groups  []DoctorDetailGroup `json:"groups,omitempty"`
}

// DoctorSummary counts diagnostic severities.
type DoctorSummary struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

// DoctorResult is the complete diagnostic snapshot.
type DoctorResult struct {
	Checks  []DoctorCheck `json:"checks"`
	Summary DoctorSummary `json:"summary"`
}

// HasFailures reports whether any check failed.
func (r *DoctorResult) HasFailures() bool {
	return r != nil && r.Summary.Fail > 0
}

// Doctor runs read-only health checks for config, host activation, providers,
// dotfiles, native services, and local cache state.
func (a *App) Doctor(ctx context.Context) (*DoctorResult, error) {
	result := &DoctorResult{}
	cfg, configOK := a.doctorConfig(result)
	a.doctorCache(result)
	if configOK {
		a.doctorHost(result, cfg)
		a.doctorProviders(ctx, result)
		a.doctorDrift(ctx, result, cfg)
		a.doctorDots(ctx, result, cfg)
		a.doctorDotsIgnorePatterns(result, cfg)
		a.doctorAgents(result, cfg)
	}
	result.Summary = doctorSummary(result.Checks)
	return result, nil
}

func (a *App) doctorConfig(result *DoctorResult) (*config.RootConfig, bool) {
	path := strings.TrimSpace(a.ConfigPath)
	if path == "" {
		result.addCheck("config", "Config", DoctorStatusFail, "config path is empty")
		return nil, false
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			result.addCheck("config", "Config", DoctorStatusWarn, "settings.json is missing", path)
			return nil, false
		}
		result.addCheck("config", "Config", DoctorStatusFail, "settings.json cannot be inspected", err.Error(), path)
		return nil, false
	}
	cfg, err := config.Load(path)
	if err != nil {
		result.addCheck("config", "Config", DoctorStatusFail, "settings.json cannot be loaded", err.Error(), path)
		return nil, false
	}
	if errs := config.ValidateRoot(cfg, a.providerValidation()); len(errs) > 0 {
		details := make([]string, 0, len(errs)+1)
		details = append(details, path)
		for _, err := range errs {
			details = append(details, err.Error())
		}
		result.addCheck("config", "Config", DoctorStatusFail, "settings.json has validation errors", details...)
		return cfg, false
	}
	if len(cfg.MergeNotices) > 0 {
		details := append([]string{path}, cfg.MergeNotices...)
		result.addCheck("config", "Config", DoctorStatusWarn, "settings.json has duplicate definitions across $include fragments", details...)
		return cfg, true
	}
	result.addCheck("config", "Config", DoctorStatusOK, "settings.json is valid", path, fmt.Sprintf("version %d", cfg.Version))
	return cfg, true
}

func (a *App) doctorCache(result *DoctorResult) {
	path := strings.TrimSpace(a.DBPath)
	if path == "" {
		result.addCheck("cache", "Cache", DoctorStatusWarn, "cache database path is empty")
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.addCheck("cache", "Cache", DoctorStatusWarn, "cache database has not been created yet", path)
			return
		}
		result.addCheck("cache", "Cache", DoctorStatusFail, "cache database cannot be inspected", err.Error(), path)
		return
	}
	if info.IsDir() {
		result.addCheck("cache", "Cache", DoctorStatusFail, "cache database path is a directory", path)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		result.addCheck("cache", "Cache", DoctorStatusFail, "cache database is not readable", err.Error(), path)
		return
	}
	if err := file.Close(); err != nil {
		result.addCheck("cache", "Cache", DoctorStatusFail, "cache database could not be closed after reading", err.Error(), path)
		return
	}
	result.addCheck("cache", "Cache", DoctorStatusOK, "cache database is readable", path)
}

func (a *App) doctorHost(result *DoctorResult, cfg *config.RootConfig) {
	host := currentMachineGroupName()
	if host == "" {
		result.addCheck("host", "Host", DoctorStatusFail, "current hostname is empty")
		return
	}
	groups, ok := activeHostGroupNames(cfg, host)
	if !ok {
		result.addCheck("host", "Host", DoctorStatusWarn, "current host is not configured", "host "+host, "run 'omni bootstrap'")
		return
	}
	details := []string{"host " + host}
	if len(groups) > 0 {
		details = append(details, "groups "+strings.Join(groups, ", "))
	}
	result.addCheck("host", "Host", DoctorStatusOK, "current host is configured", details...)
}

func (a *App) doctorProviders(ctx context.Context, result *DoctorResult) {
	if a.registry == nil {
		result.addCheck("providers", "Providers", DoctorStatusFail, "provider registry is not initialised")
		return
	}
	var available []string
	var unavailable []string
	var errors []string
	for _, p := range a.registry.All() {
		ok, err := p.Available(ctx)
		switch {
		case err != nil:
			errors = append(errors, p.Name()+": "+err.Error())
		case ok:
			available = append(available, p.Name())
		default:
			unavailable = append(unavailable, p.Name())
		}
	}
	sort.Strings(available)
	sort.Strings(unavailable)
	sort.Strings(errors)
	details := make([]string, 0, 2+len(errors))
	if len(available) > 0 {
		details = append(details, "available "+strings.Join(available, ", "))
	}
	if len(unavailable) > 0 {
		details = append(details, "unavailable "+strings.Join(unavailable, ", "))
	}
	details = append(details, errors...)
	switch {
	case len(errors) > 0:
		result.addCheck("providers", "Providers", DoctorStatusWarn, "some provider checks failed", details...)
	case len(available) == 0:
		result.addCheck("providers", "Providers", DoctorStatusWarn, "no package managers are available", details...)
	default:
		result.addCheck("providers", "Providers", DoctorStatusOK, fmt.Sprintf("%d provider(s) available", len(available)), details...)
	}
}

func (a *App) doctorDots(ctx context.Context, result *DoctorResult, cfg *config.RootConfig) {
	settings := a.effectiveSettings(cfg)
	if strings.TrimSpace(settings.DotsRepo) == "" {
		result.addCheck("dots", "Dotfiles", DoctorStatusWarn, "dots_repo is not configured")
		a.doctorServices(result)
		return
	}
	if config.BoolVal(settings.DotsDisabled) {
		result.addCheck("dots", "Dotfiles", DoctorStatusOK, "dotfile sync is disabled for this host")
		a.doctorServices(result)
		return
	}
	repoPath, err := resolveRepoPath(settings.DotsRepo)
	if err != nil {
		result.addCheck("dots", "Dotfiles", DoctorStatusFail, "dots_repo cannot be resolved", err.Error(), settings.DotsRepo)
		a.doctorServices(result)
		return
	}
	info, err := os.Stat(repoPath)
	if err != nil {
		result.addCheck("dots", "Dotfiles", DoctorStatusFail, "dots_repo is not accessible", err.Error(), repoPath)
		a.doctorServices(result)
		return
	}
	if !info.IsDir() {
		result.addCheck("dots", "Dotfiles", DoctorStatusFail, "dots_repo is not a directory", repoPath)
		a.doctorServices(result)
		return
	}

	stowInstalled := a.DotsStowInstalled(ctx)
	reminder, reminderErr := a.DotsReminder(ctx)
	details := []string{"repo " + repoPath}
	if stowInstalled {
		details = append(details, "stow available")
	} else {
		details = append(details, "stow missing")
	}
	switch {
	case reminderErr != nil:
		result.addCheck("dots", "Dotfiles", DoctorStatusWarn, "dotfile status could not be fully checked", append(details, reminderErr.Error())...)
	case reminder != nil && reminder.NeedsReminder:
		for _, reason := range reminder.Reasons {
			details = append(details, reason.Message)
		}
		result.addCheck("dots", "Dotfiles", DoctorStatusWarn, "dotfiles need attention", details...)
	case !stowInstalled:
		result.addCheck("dots", "Dotfiles", DoctorStatusWarn, "dotfiles repo is configured but stow is missing", details...)
	default:
		result.addCheck("dots", "Dotfiles", DoctorStatusOK, "dotfiles look ready", details...)
	}
	a.doctorServices(result)
}

func (a *App) doctorServices(result *DoctorResult) {
	services := a.DotsServicesStatus()
	details := doctorServiceDetails(services)
	switch {
	case services.ReminderError != "" || services.WatchError != "":
		result.addCheck("services", "Services", DoctorStatusWarn, "optional dotfile services could not be fully inspected", details...)
	case services.Reminder != nil && services.Reminder.Installed || services.Watch != nil && services.Watch.Installed:
		result.addCheck("services", "Services", DoctorStatusOK, "optional dotfile services are installed", details...)
	default:
		result.addCheck("services", "Services", DoctorStatusOK, "optional dotfile services are not installed", details...)
	}
}

func doctorServiceDetails(services DotsServicesStatus) []string {
	details := make([]string, 0, 2)
	if services.ReminderError != "" {
		details = append(details, "reminder error: "+services.ReminderError)
	} else if services.Reminder != nil {
		status := "not installed"
		if services.Reminder.Installed {
			status = "installed"
		}
		details = append(details, fmt.Sprintf("reminder %s (%s), interval %s, notify %t", status, services.Reminder.Platform, services.Reminder.Interval, services.Reminder.Notify))
	}
	if services.WatchError != "" {
		details = append(details, "watch error: "+services.WatchError)
	} else if services.Watch != nil {
		status := "not installed"
		if services.Watch.Installed {
			status = "installed"
		}
		details = append(details, fmt.Sprintf("watch %s (%s), debounce %s", status, services.Watch.Platform, services.Watch.Debounce))
	}
	return details
}

func doctorSummary(checks []DoctorCheck) DoctorSummary {
	var summary DoctorSummary
	for _, check := range checks {
		switch check.Status {
		case DoctorStatusOK:
			summary.OK++
		case DoctorStatusWarn:
			summary.Warn++
		case DoctorStatusFail:
			summary.Fail++
		}
	}
	return summary
}

func (r *DoctorResult) addCheck(id, label string, status DoctorStatus, message string, details ...string) {
	cleanDetails := make([]string, 0, len(details))
	for _, detail := range details {
		detail = strings.TrimSpace(detail)
		if detail != "" {
			cleanDetails = append(cleanDetails, detail)
		}
	}
	r.Checks = append(r.Checks, DoctorCheck{
		ID:      id,
		Label:   label,
		Status:  status,
		Message: message,
		Details: cleanDetails,
	})
}
