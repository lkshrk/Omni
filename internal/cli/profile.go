package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

func newProfileCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage tool profiles",
		Long: `Profiles are named collections of groups. Use them to activate a
specific subset of tools depending on context (work, personal, etc.).

A profile is auto-selected during 'omni sync' when the current system
hostname matches a hostname mapping:

  omni profile set-hostname $(hostname) work`,
	}

	cmd.AddCommand(
		newProfileListCmd(state),
		newProfileAddCmd(state),
		newProfileRenameCmd(state),
		newProfileDeleteCmd(state),
		newProfileAddGroupCmd(state),
		newProfileRemoveGroupCmd(state),
		newProfileSetHostnameCmd(state),
		newProfileRemoveHostnameCmd(state),
		newProfileIgnoreCmd(state),
	)
	return cmd
}

func newProfileListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all profiles",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := state.app.ProfileStatus()
			if err != nil {
				return err
			}

			if len(info.Profiles) == 0 && len(info.Hostnames) == 0 {
				fmt.Println("No profiles defined. Use 'omni profile add <name>' to create one.")
				return nil
			}

			for _, name := range sortedProfileNames(info.Profiles) {
				p := info.Profiles[name]
				marker := "  "
				if name == info.Active {
					marker = "* "
				}
				fmt.Printf("%s%-20s %s\n", marker, name, groupList(p.Groups))
			}

			if len(info.Hostnames) > 0 {
				fmt.Println("\nHostname mappings:")
				for _, hostname := range sortedHostnameNames(info.Hostnames) {
					fmt.Printf("  %-30s → %s\n", hostname, info.Hostnames[hostname])
				}
			}
			return nil
		},
	}
}

func newProfileAddCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> [group ...]",
		Short: "Create or replace a profile",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			groups := args[1:]
			if err := state.app.AddProfile(name, groups); err != nil {
				return err
			}
			fmt.Printf("Profile %q saved with groups: %s\n", name, groupList(groups))
			return nil
		},
	}
}

func newProfileRenameCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a profile",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RenameProfile(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Profile %q renamed to %q.\n", args[0], args[1])
			return nil
		},
	}
}

func newProfileDeleteCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Delete profile %q?", args[0]))
			if err != nil || !ok {
				return err
			}
			if err := state.app.DeleteProfile(args[0]); err != nil {
				return err
			}
			fmt.Printf("Profile %q deleted.\n", args[0])
			return nil
		},
	}
}

func newProfileAddGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "add-group <profile> <group>",
		Short: "Add a group to a profile",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.AddGroupToProfile(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Added group %q to profile %q.\n", args[1], args[0])
			return nil
		},
	}
}

func newProfileRemoveGroupCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove-group <profile> <group>",
		Short: "Remove a group from a profile",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveGroupFromProfile(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Removed group %q from profile %q.\n", args[1], args[0])
			return nil
		},
	}
}

func newProfileSetHostnameCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "set-hostname <hostname> <profile>",
		Short: "Map a hostname to a profile for auto-activation",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.SetHostname(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Hostname %q → profile %q\n", args[0], args[1])
			return nil
		},
	}
}

func newProfileRemoveHostnameCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove-hostname <hostname>",
		Short: "Remove a hostname mapping",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := confirmAction(cmd, state, fmt.Sprintf("Remove hostname mapping %q?", args[0]))
			if err != nil || !ok {
				return err
			}
			if err := state.app.RemoveHostname(args[0]); err != nil {
				return err
			}
			fmt.Printf("Hostname %q mapping removed.\n", args[0])
			return nil
		},
	}
}

func newProfileIgnoreCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ignore",
		Short: "Manage the ignore list for a profile",
		Long: `The ignore list skips named tools during sync on machines using this profile.
Ignored tools are still visible in 'omni list' but are greyed out.`,
	}
	cmd.AddCommand(
		newProfileIgnoreAddCmd(state),
		newProfileIgnoreRemoveCmd(state),
		newProfileIgnoreListCmd(state),
	)
	return cmd
}

func newProfileIgnoreAddCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "add <profile> <tool>",
		Short: "Add a tool to a profile's ignore list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.AddIgnoreToProfile(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Added %q to ignore list of profile %q.\n", args[1], args[0])
			return nil
		},
	}
}

func newProfileIgnoreRemoveCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <profile> <tool>",
		Short: "Remove a tool from a profile's ignore list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveIgnoreFromProfile(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Removed %q from ignore list of profile %q.\n", args[1], args[0])
			return nil
		},
	}
}

func newProfileIgnoreListCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "list <profile>",
		Short: "List ignored tools for a profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := state.app.ProfileStatus()
			if err != nil {
				return err
			}
			p, ok := info.Profiles[args[0]]
			if !ok {
				return fmt.Errorf("profile %q not found", args[0])
			}
			if len(p.Ignore) == 0 {
				fmt.Printf("No tools ignored in profile %q.\n", args[0])
				return nil
			}
			for _, t := range p.Ignore {
				fmt.Println(t)
			}
			return nil
		},
	}
}

func groupList(groups []string) string {
	if len(groups) == 0 {
		return "(none)"
	}
	sorted := append([]string(nil), groups...)
	sort.Strings(sorted)
	return "[" + strings.Join(sorted, ", ") + "]"
}

func sortedHostnameNames(hostnames map[string]string) []string {
	names := make([]string, 0, len(hostnames))
	for name := range hostnames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
