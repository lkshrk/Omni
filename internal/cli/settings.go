package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// ErrLintWarnings is returned by `omni settings lint` when advisory issues are found.
var ErrLintWarnings = errors.New("settings lint found warnings")

func newSettingsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Manage omni settings",
	}
	cmd.AddCommand(
		newSettingsShowCmd(state),
		newSettingsGetCmd(state),
		newSettingsSetCmd(state),
		newSettingsDisableProviderCmd(state),
		newSettingsEnableProviderCmd(state),
		newSettingsLintCmd(state),
		newSettingsMigrateHostOverridesCmd(state),
		newSettingsResetCmd(state),
		newSettingsResetCacheCmd(state),
	)
	return cmd
}

func newSettingsShowCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "show [key]",
		Short: "Show effective settings for this host",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := state.app.QuerySettings(singleArg(args))
			if err != nil {
				return err
			}
			switch format {
			case "json":
				return json.NewEncoder(cmd.OutOrStdout()).Encode(values)
			case "text":
				for _, key := range settingsKeys(values) {
					fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", key, displaySettingsValue(values[key]))
				}
				return nil
			default:
				return validateFormat(format, "text", "json")
			}
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func newSettingsGetCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Show one effective setting for this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values, err := state.app.QuerySettings(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), displaySettingsValue(values[app.CanonicalSettingKey(args[0])]))
			return nil
		},
	}
	cmd.ValidArgsFunction = completeSettingsKeys(state)
	return cmd
}

func newSettingsSetCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set an omni setting",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := state.app.SetSetting(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			values, err := state.app.QuerySettings(key)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", key, displaySettingsValue(values[key]))
			return nil
		},
	}
	// complete the key (first arg); value (second arg) is freeform
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeSettingsKeys(state)(c, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func newSettingsDisableProviderCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "disable-provider <provider>",
		Short: "Disable a provider on this host (concrete name, e.g. brew, bun, pip)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			disabled, err := state.app.DisableProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled_providers = %s\n", displayList(disabled))
			return nil
		},
	}
}

func newSettingsEnableProviderCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "enable-provider <provider>",
		Short: "Enable a provider on this host (concrete name, e.g. brew, bun, pip)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			enabled, err := state.app.EnableProvider(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled_providers = %s\n", displayList(enabled))
			return nil
		},
	}
}

func newSettingsLintCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "lint",
		Short: "Check settings.json for common hygiene issues",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(state.app.ConfigPath)
			if err != nil {
				return err
			}
			issues := state.app.LintSettings(cfg)
			if len(issues) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no issues found")
				return nil
			}
			for _, issue := range issues {
				fmt.Fprintln(cmd.OutOrStdout(), "warning:", issue.String())
			}
			return ErrLintWarnings
		},
	}
}

func newSettingsMigrateHostOverridesCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-host-overrides",
		Short: "Fold tools.*.hosts install overrides into providers[]",
		RunE: func(cmd *cobra.Command, args []string) error {
			count, err := state.app.MigrateHostOverrides(cmd.Context())
			if err != nil {
				return err
			}
			if count == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no host install overrides to migrate")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "migrated host install overrides for %d tool(s)\n", count)
			return nil
		},
	}
}

func newSettingsResetCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset settings to defaults while preserving tools and hosts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ok, err := confirmAction(cmd, state, "Reset settings to defaults?")
			if err != nil || !ok {
				return err
			}
			if err := state.app.ResetSettings(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "settings reset")
			return nil
		},
	}
}

func newSettingsResetCacheCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "reset-cache",
		Short: "Clear and reinitialise the tool cache",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ok, err := confirmAction(cmd, state, "Clear and reinitialise the tool cache?")
			if err != nil || !ok {
				return err
			}
			if err := state.app.ResetCache(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "cache reset")
			return nil
		},
	}
}

func displaySetting(value string) string {
	if value == "" {
		return "(default)"
	}
	return value
}

func singleArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func settingsKeys(values map[string]any) []string {
	var keys []string
	for _, key := range app.SettingKeys() {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
		}
	}
	return keys
}

func displaySettingsValue(value any) string {
	switch typed := value.(type) {
	case string:
		return displaySetting(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case *bool:
		return displayBoolPtr(typed)
	case []string:
		return displayList(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func displayList(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ", ")
}

func displayBoolPtr(value *bool) string {
	if value == nil {
		return "(default)"
	}
	if *value {
		return "true"
	}
	return "false"
}
