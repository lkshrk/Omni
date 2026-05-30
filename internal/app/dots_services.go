package app

import "strings"

// DotsServicesStatus is a combined read-only snapshot of native dotfile
// reminder/watch service files and their parsed timing options.
type DotsServicesStatus struct {
	Reminder      *DotsReminderService `json:"reminder,omitempty"`
	ReminderError string               `json:"reminder_error,omitempty"`
	Watch         *DotsWatchService    `json:"watch,omitempty"`
	WatchError    string               `json:"watch_error,omitempty"`
}

type DashboardDotsAutomationInput struct {
	Services       DotsServicesStatus
	DotsRepo       string
	DotsConfigured bool
	DotsDisabled   bool
}

type DashboardDotsAutomationStatus struct {
	Known             bool
	Installed         int
	HasError          bool
	Blocked           bool
	NeedsAttention    bool
	ReadinessWarnings []string
}

// DotsServicesStatus reports both optional native dotfile services. Individual
// service errors are captured in the result so callers can still show the other
// service state.
func (a *App) DotsServicesStatus() DotsServicesStatus {
	var out DotsServicesStatus
	reminder, err := a.DotsReminderServiceStatus()
	if err != nil {
		out.ReminderError = err.Error()
	} else {
		out.Reminder = reminder
	}
	watch, err := a.DotsWatchServiceStatus()
	if err != nil {
		out.WatchError = err.Error()
	} else {
		out.Watch = watch
	}
	return out
}

func BuildDashboardDotsAutomationStatus(input DashboardDotsAutomationInput) DashboardDotsAutomationStatus {
	status := DashboardDotsAutomationStatus{
		Known:    dashboardDotsAutomationKnown(input.Services),
		HasError: strings.TrimSpace(input.Services.ReminderError) != "" || strings.TrimSpace(input.Services.WatchError) != "",
	}
	if input.Services.Reminder != nil && input.Services.Reminder.Installed {
		status.Installed++
	}
	if input.Services.Watch != nil && input.Services.Watch.Installed {
		status.Installed++
	}
	if !input.DotsConfigured && strings.TrimSpace(input.DotsRepo) == "" {
		status.ReadinessWarnings = append(status.ReadinessWarnings, "Blocked: dots_repo is not configured.")
	}
	if input.DotsDisabled {
		status.ReadinessWarnings = append(status.ReadinessWarnings, "Blocked: dotfile sync is disabled for this host.")
	}
	status.Blocked = status.Installed > 0 && len(status.ReadinessWarnings) > 0
	status.NeedsAttention = status.HasError || status.Blocked
	return status
}

func dashboardDotsAutomationKnown(services DotsServicesStatus) bool {
	return services.Reminder != nil ||
		services.Watch != nil ||
		strings.TrimSpace(services.ReminderError) != "" ||
		strings.TrimSpace(services.WatchError) != ""
}
