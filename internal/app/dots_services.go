package app

// DotsServicesStatus is a combined read-only snapshot of native dotfile
// reminder/watch service files and their parsed timing options.
type DotsServicesStatus struct {
	Reminder      *DotsReminderService `json:"reminder,omitempty"`
	ReminderError string               `json:"reminder_error,omitempty"`
	Watch         *DotsWatchService    `json:"watch,omitempty"`
	WatchError    string               `json:"watch_error,omitempty"`
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
