package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/config"
)

func newHostsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hosts",
		Short: "Manage host group assignments",
		Long: `Hosts activate this machine's special host group plus any reusable
groups assigned to it.`,
	}
	cmd.AddCommand(
		newHostsListCmd(state),
		newHostsEnsureCmd(state),
		newHostsSetGroupsCmd(state),
		newHostsAddGroupCmd(state),
		newHostsRemoveGroupCmd(state),
		newHostsCopyCmd(state),
		newHostsRemoveCmd(state),
	)
	return cmd
}

func newHostsListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List host group assignments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.HostStatus()
			if err != nil {
				return err
			}
			if len(info.Hosts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No hosts defined. Use 'omni hosts ensure <hostname>' to create one.")
				return nil
			}
			for _, host := range sortedHostNames(info.Hosts) {
				marker := "  "
				if host == info.Active {
					marker = "* "
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%-20s %s\n", marker, host, groupList(info.Hosts[host].Groups))
			}
			return nil
		},
	}
}

func newHostsEnsureCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "ensure <hostname>",
		Short: "Create a host entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.EnsureHost(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Host %q ensured.\n", args[0])
			return nil
		},
	}
}

func newHostsSetGroupsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-groups <hostname> [group ...]",
		Short: "Replace reusable groups assigned to a host",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.SetHostGroups(args[0], args[1:]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Host %q groups set to %s.\n", args[0], groupList(args[1:]))
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeHostNames(state)(c, args, toComplete)
		}
		return completeGroupNames(state)(c, args, toComplete)
	}
	return cmd
}

func newHostsAddGroupCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-group <hostname> <group>",
		Short: "Assign a reusable group to a host",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.AddGroupToHost(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added group %q to host %q.\n", args[1], args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeHostNames(state)(c, args, toComplete)
		}
		return completeGroupNames(state)(c, args, toComplete)
	}
	return cmd
}

func newHostsRemoveGroupCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-group <hostname> <group>",
		Short: "Remove a reusable group from a host",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveGroupFromHost(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed group %q from host %q.\n", args[1], args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeHostNames(state)(c, args, toComplete)
		}
		return completeGroupNames(state)(c, args, toComplete)
	}
	return cmd
}

func newHostsCopyCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy <source-host> <target-host>",
		Short: "Copy host-scoped config to another host",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.CopyHostConfig(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Copied host config from %q to %q.\n", args[0], args[1])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeHostNames(state)
	return cmd
}

func newHostsRemoveCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <hostname>",
		Short: "Remove a host assignment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Remove host %q?", args[0]))
			if err != nil || !ok {
				return err
			}
			if err := state.app.RemoveHost(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Host %q removed.\n", args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = completeHostNames(state)
	return cmd
}

func groupList(groups []string) string {
	if len(groups) == 0 {
		return "(none)"
	}
	sorted := append([]string(nil), groups...)
	sort.Strings(sorted)
	return "[" + strings.Join(sorted, ", ") + "]"
}

func sortedHostNames(hosts map[string]config.HostAssignment) []string {
	names := make([]string, 0, len(hosts))
	for name := range hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
