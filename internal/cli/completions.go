package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

type completionFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// completeToolNames completes tool names from the cached tool list.
func completeToolNames(state *rootState) completionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		tools, err := state.app.ListTools(cmd.Context(), "")
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, t := range tools {
			if strings.HasPrefix(t.Name, toComplete) {
				names = append(names, t.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeGroupNames completes group names from config.
func completeGroupNames(state *rootState) completionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		groups, err := state.app.Groups(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, g := range groups {
			name := g.GroupName()
			if strings.HasPrefix(name, toComplete) {
				names = append(names, name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeHostNames completes host names from the host status map.
func completeHostNames(state *rootState) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		info, err := state.app.HostStatus()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for host := range info.Hosts {
			if strings.HasPrefix(host, toComplete) {
				names = append(names, host)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProviderNames completes provider names from the registry.
func completeProviderNames(state *rootState) completionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		providers, err := state.app.Providers(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, p := range providers {
			if strings.HasPrefix(p.Name, toComplete) {
				names = append(names, p.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeDotNames completes dots entry names.
func completeDotNames(state *rootState) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if state.app == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		dots, err := state.app.DotsList()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, d := range dots {
			if strings.HasPrefix(d.Name, toComplete) {
				names = append(names, d.Name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// knownSettingsKeys lists the supported settings keys for completion.
var knownSettingsKeys = []string{
	"auto_import",
	"node.manager",
	"python.manager",
	"system.priority",
	"dots_repo",
	"dots_disabled",
	"dots_git.auto_commit",
	"dots_git.auto_push",
	"disabled_providers",
}

// completeSettingsKeys completes known settings keys.
func completeSettingsKeys(_ *rootState) completionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		var matches []string
		for _, key := range knownSettingsKeys {
			if strings.HasPrefix(key, toComplete) {
				matches = append(matches, key)
			}
		}
		return matches, cobra.ShellCompDirectiveNoFileComp
	}
}
