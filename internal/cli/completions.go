package cli

import (
	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

type completionFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

func completeToolNames(state *rootState) completionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := state.app.ToolNameCandidates(cmd.Context(), toComplete)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeGroupNames(state *rootState) completionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := state.app.GroupNameCandidates(cmd.Context(), toComplete)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeHostNames(state *rootState) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := state.app.HostNameCandidates(toComplete)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeProviderNames(state *rootState) completionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := state.app.ProviderNameCandidates(cmd.Context(), toComplete)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeDotNames(state *rootState) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := state.app.DotNameCandidates(toComplete)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeSettingsKeys(_ *rootState) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return app.SettingKeyCandidates(toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}
