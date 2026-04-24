package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/provider"
)

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
	return &cobra.Command{
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
}

func newSettingsSetCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set an omni setting",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := app.CanonicalSettingKey(args[0]), args[1]
			settings, err := state.app.LoadSettings()
			if err != nil {
				return err
			}
			switch key {
			case "auto_import":
				parsed, err := parseSettingBool(key, value)
				if err != nil {
					return err
				}
				settings.AutoImport = parsed
			case "node.manager":
				manager, err := parseSettingManager(state.app, provider.EcosystemNode, value)
				if err != nil {
					return err
				}
				settings.SetEcosystemManager(provider.EcosystemNode, manager)
			case "python.manager":
				manager, err := parseSettingManager(state.app, provider.EcosystemPython, value)
				if err != nil {
					return err
				}
				settings.SetEcosystemManager(provider.EcosystemPython, manager)
			case "system.priority":
				priority, err := parseSystemPriority(state.app, value)
				if err != nil {
					return err
				}
				settings.SetEcosystemPriority(provider.EcosystemSystem, priority)
			case "dots_repo":
				settings.DotsRepo = value
			case "dots_git.auto_commit":
				parsed, err := parseSettingBool(key, value)
				if err != nil {
					return err
				}
				settings.DotsGit.AutoCommit = parsed
			case "dots_git.auto_push":
				parsed, err := parseSettingBool(key, value)
				if err != nil {
					return err
				}
				settings.DotsGit.AutoPush = parsed
			default:
				return fmt.Errorf("unknown setting %q", args[0])
			}
			if err := state.app.SaveSettings(cmd.Context(), settings); err != nil {
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
}

func newSettingsDisableProviderCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "disable-provider <provider>",
		Short: "Disable an ecosystem provider on this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName := args[0]
			if err := validateEcosystemProvider(state.app, providerName); err != nil {
				return err
			}
			settings, err := state.app.LoadSettings()
			if err != nil {
				return err
			}
			if !slices.Contains(settings.DisabledProviders, providerName) {
				settings.DisabledProviders = append(settings.DisabledProviders, providerName)
			}
			if err := state.app.SaveDisabledProviders(cmd.Context(), settings.DisabledProviders); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled_providers = %s\n", displayList(settings.DisabledProviders))
			return nil
		},
	}
}

func newSettingsEnableProviderCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "enable-provider <provider>",
		Short: "Enable an ecosystem provider on this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName := args[0]
			if err := validateEcosystemProvider(state.app, providerName); err != nil {
				return err
			}
			settings, err := state.app.LoadSettings()
			if err != nil {
				return err
			}
			var enabled []string
			for _, disabled := range settings.DisabledProviders {
				if disabled != providerName {
					enabled = append(enabled, disabled)
				}
			}
			if err := state.app.SaveDisabledProviders(cmd.Context(), enabled); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled_providers = %s\n", displayList(enabled))
			return nil
		},
	}
}

func newSettingsResetCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Reset settings to defaults while preserving tools and profiles",
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
	order := []string{
		"auto_import",
		provider.EcosystemNode + ".manager",
		provider.EcosystemPython + ".manager",
		provider.EcosystemSystem + ".priority",
		"dots_repo",
		"dots_disabled",
		"dots_git.auto_commit",
		"dots_git.auto_push",
		"disabled_providers",
	}
	var keys []string
	for _, key := range order {
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

func parseSettingBool(key, value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func parseSettingManager(a *app.App, ecosystem, value string) (string, error) {
	if value == "auto" || value == "default" {
		return "", nil
	}
	if a.IsManagerForEcosystem(ecosystem, value) {
		return value, nil
	}
	return "", fmt.Errorf("%q is not a manager for ecosystem %q (supported: %s)", value, ecosystem, strings.Join(a.ManagerNames(ecosystem), ", "))
}

func parseSystemPriority(a *app.App, value string) ([]string, error) {
	raw := strings.Split(value, ",")
	priority := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, part := range raw {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("system.priority contains an empty provider")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("system.priority contains duplicate provider %q", name)
		}
		if !a.IsConcreteProviderForEcosystem(provider.EcosystemSystem, name) {
			return nil, fmt.Errorf("%q is not a concrete provider for ecosystem %q (supported: %s)", name, provider.EcosystemSystem, strings.Join(a.ConcreteProviderNamesForEcosystem(provider.EcosystemSystem), ", "))
		}
		seen[name] = struct{}{}
		priority = append(priority, name)
	}
	return priority, nil
}

func validateEcosystemProvider(a *app.App, name string) error {
	if a.IsEcosystemProvider(name) {
		return nil
	}
	return fmt.Errorf("%q is not an ecosystem provider (supported: %s)", name, strings.Join(a.EcosystemProviderNames(), ", "))
}
