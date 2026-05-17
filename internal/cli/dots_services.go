package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/executor"
)

// ─── dots reminder ───────────────────────────────────────────────────────────

func newDotsReminderCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reminder",
		Short: "Check or install periodic dotfile sync reminders",
	}
	cmd.AddCommand(
		newDotsReminderCheckCmd(state),
		newDotsReminderRunCmd(state),
		newDotsReminderInstallCmd(state),
		newDotsReminderUninstallCmd(state),
		newDotsReminderStatusCmd(state),
	)
	return cmd
}

func newDotsReminderCheckCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check whether dotfiles need attention",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			result, err := state.app.DotsReminder(cmd.Context())
			if err != nil {
				return err
			}
			return printDotsReminderResult(cmd, result, format)
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func newDotsReminderRunCmd(state *rootState) *cobra.Command {
	var notify bool
	var format string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one reminder check, optionally sending a desktop notification",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			result, err := state.app.DotsReminder(cmd.Context())
			if err != nil {
				return err
			}
			if notify && result.NeedsReminder {
				if err := notifyDotsReminder(cmd.Context(), result); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: notification: %v\n", err)
				}
			}
			return printDotsReminderResult(cmd, result, format)
		},
	}
	cmd.Flags().BoolVar(&notify, "notify", false, "Send a desktop notification when dotfiles need attention")
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func newDotsReminderInstallCmd(state *rootState) *cobra.Command {
	interval := app.DefaultDotsReminderInterval()
	notify := true
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install a periodic user service for dotfile reminders",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			info, err := state.app.InstallDotsReminderService(cmd.Context(), app.DotsReminderInstallOptions{
				Interval: interval,
				Notify:   notify,
				Activate: true,
			})
			if err != nil {
				return err
			}
			printDotsReminderService(cmd, "Installed dots reminder service.", info)
			return nil
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", interval, "Reminder interval, for example 12h or 24h")
	cmd.Flags().BoolVar(&notify, "notify", notify, "Send desktop notifications from the reminder service")
	return cmd
}

func newDotsReminderUninstallCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the periodic dotfile reminder service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.UninstallDotsReminderService(cmd.Context())
			if err != nil {
				return err
			}
			printDotsReminderService(cmd, "Uninstalled dots reminder service.", info)
			return nil
		},
	}
}

func newDotsReminderStatusCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show reminder service install status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.DotsReminderServiceStatus()
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			printDotsReminderService(cmd, dotsServiceStatusHeader("Dots reminder service", info.Installed, info.Platform), info)
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDotsReminderResult(cmd *cobra.Command, result *app.DotsReminderResult, format string) error {
	if format == "json" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	if err := validateFormat(format, "text", "json"); err != nil {
		return err
	}
	if result.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Dots are disabled for this host.")
		return nil
	}
	if !result.NeedsReminder {
		fmt.Fprintln(cmd.OutOrStdout(), "Dotfiles are in sync.")
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Dotfiles need attention:")
	for _, reason := range result.Reasons {
		fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", reason.Message)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nRun 'omni dots status' for details.")
	return nil
}

func printDotsReminderService(cmd *cobra.Command, header string, info *app.DotsReminderService) {
	fmt.Fprintln(cmd.OutOrStdout(), header)
	fmt.Fprintf(cmd.OutOrStdout(), "Platform: %s\n", info.Platform)
	fmt.Fprintf(cmd.OutOrStdout(), "Interval: %s\n", info.Interval)
	fmt.Fprintf(cmd.OutOrStdout(), "Notifications: %s\n", displaySettingsValue(info.Notify))
	for _, file := range info.Files {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
	}
}

func notifyDotsReminder(ctx context.Context, result *app.DotsReminderResult) error {
	if result == nil || !result.NeedsReminder {
		return nil
	}
	message := "Run omni dots status for details."
	if len(result.Reasons) > 0 {
		message = result.Reasons[0].Message
		if len(result.Reasons) > 1 {
			message += fmt.Sprintf(" (+%d more)", len(result.Reasons)-1)
		}
	}
	exec := executor.New()
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification "%s" with title "Omni dotfiles"`, appleScriptString(message))
		_, stderr, err := exec.Run(ctx, "osascript", "-e", script)
		if err != nil {
			return fmt.Errorf("osascript: %w%s", err, stderrSuffix(stderr))
		}
	case "linux":
		_, stderr, err := exec.Run(ctx, "notify-send", "Omni dotfiles", message)
		if err != nil {
			return fmt.Errorf("notify-send: %w%s", err, stderrSuffix(stderr))
		}
	default:
		return fmt.Errorf("desktop notifications are not supported on %s", runtime.GOOS)
	}
	return nil
}

// ─── dots watch ──────────────────────────────────────────────────────────────

func newDotsWatchCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch dotfile paths and sync after changes",
	}
	cmd.AddCommand(
		newDotsWatchRunCmd(state),
		newDotsWatchInstallCmd(state),
		newDotsWatchUninstallCmd(state),
		newDotsWatchStatusCmd(state),
	)
	return cmd
}

func newDotsWatchRunCmd(state *rootState) *cobra.Command {
	debounce := app.DefaultDotsWatchDebounce()
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the dotfile watcher in the foreground",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			return state.app.DotsWatch(cmd.Context(), app.DotsWatchOptions{
				Debounce: debounce,
				OnStart: func(start app.DotsWatchStart) {
					fmt.Fprintf(cmd.OutOrStdout(), "Watching %d dotfile path(s).\n", start.WatchedPaths)
				},
				OnSync: func(result app.DotsWatchSyncResult) {
					printDotsWatchSyncResult(cmd, result)
				},
			})
		},
	}
	cmd.Flags().DurationVar(&debounce, "debounce", debounce, "Debounce delay after filesystem changes, for example 2s or 10s")
	return cmd
}

func newDotsWatchInstallCmd(state *rootState) *cobra.Command {
	debounce := app.DefaultDotsWatchDebounce()
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install a user service for automatic dotfile sync",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireDotsConfigured(state); err != nil {
				return err
			}
			if err := ensureDotsStowForCLI(cmd, state); err != nil {
				return err
			}
			info, err := state.app.InstallDotsWatchService(cmd.Context(), app.DotsWatchInstallOptions{
				Debounce: debounce,
				Activate: true,
			})
			if err != nil {
				return err
			}
			printDotsWatchService(cmd, "Installed dots watch service.", info)
			return nil
		},
	}
	cmd.Flags().DurationVar(&debounce, "debounce", debounce, "Debounce delay after filesystem changes, for example 2s or 10s")
	return cmd
}

func newDotsWatchUninstallCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall the automatic dotfile sync service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.UninstallDotsWatchService(cmd.Context())
			if err != nil {
				return err
			}
			printDotsWatchService(cmd, "Uninstalled dots watch service.", info)
			return nil
		},
	}
}

func newDotsWatchStatusCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show watch service install status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.DotsWatchServiceStatus()
			if err != nil {
				return err
			}
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			printDotsWatchService(cmd, dotsServiceStatusHeader("Dots watch service", info.Installed, info.Platform), info)
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDotsWatchService(cmd *cobra.Command, header string, info *app.DotsWatchService) {
	fmt.Fprintln(cmd.OutOrStdout(), header)
	fmt.Fprintf(cmd.OutOrStdout(), "Platform: %s\n", info.Platform)
	fmt.Fprintf(cmd.OutOrStdout(), "Debounce: %s\n", info.Debounce)
	for _, file := range info.Files {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
	}
}

func dotsServiceStatusHeader(name string, installed bool, platform string) string {
	status := "not installed"
	if installed {
		status = "installed"
	}
	return fmt.Sprintf("%s: %s (%s)", name, status, platform)
}

func printDotsWatchSyncResult(cmd *cobra.Command, result app.DotsWatchSyncResult) {
	event := strings.TrimSpace(result.Event.Path)
	if event == "" {
		event = "filesystem change"
	}
	changes, conflicts := countDotWatchOps(result.Ops)
	if result.Err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Dotfile sync failed after %s: %v\n", event, result.Err)
		return
	}
	switch {
	case conflicts > 0:
		fmt.Fprintf(cmd.ErrOrStderr(), "Dotfile sync found %d conflict(s) after %s.\n", conflicts, event)
	case changes > 0:
		fmt.Fprintf(cmd.OutOrStdout(), "Synced dotfiles after %s (%d change(s)).\n", event, changes)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "Checked dotfiles after %s; no changes.\n", event)
	}
}

// ─── dots services ───────────────────────────────────────────────────────────

func newDotsServicesCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "Inspect optional dotfile services",
	}
	cmd.AddCommand(newDotsServicesStatusCmd(state))
	return cmd
}

func newDotsServicesStatusCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show reminder and watch service status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			status := state.app.DotsServicesStatus()
			if format == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
			}
			if err := validateFormat(format, "text", "json"); err != nil {
				return err
			}
			printDotsServicesStatus(cmd, status)
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDotsServicesStatus(cmd *cobra.Command, status app.DotsServicesStatus) {
	fmt.Fprintln(cmd.OutOrStdout(), "Dots services")
	if status.ReminderError != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Reminder: unavailable - %s\n", status.ReminderError)
	} else if status.Reminder != nil {
		installed := "not installed"
		if status.Reminder.Installed {
			installed = "installed"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Reminder: %s (%s)\n", installed, status.Reminder.Platform)
		fmt.Fprintf(cmd.OutOrStdout(), "  Interval: %s\n", status.Reminder.Interval)
		fmt.Fprintf(cmd.OutOrStdout(), "  Notifications: %s\n", displaySettingsValue(status.Reminder.Notify))
		for _, file := range status.Reminder.Files {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
		}
	}
	if status.WatchError != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Watch: unavailable - %s\n", status.WatchError)
	} else if status.Watch != nil {
		installed := "not installed"
		if status.Watch.Installed {
			installed = "installed"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Watch: %s (%s)\n", installed, status.Watch.Platform)
		fmt.Fprintf(cmd.OutOrStdout(), "  Debounce: %s\n", status.Watch.Debounce)
		for _, file := range status.Watch.Files {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", file)
		}
	}
}

func countDotWatchOps(ops []dots.Op) (changes, conflicts int) {
	for _, op := range ops {
		switch op.Kind {
		case dots.OpLink, dots.OpRepair, dots.OpAdopt, dots.OpUnlink, dots.OpUnlinkSkip, dots.OpUnlinkConflict:
			changes++
		case dots.OpConflict:
			conflicts++
		}
	}
	return changes, conflicts
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}
