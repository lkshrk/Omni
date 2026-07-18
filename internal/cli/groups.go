package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	textutil "github.com/lkshrk/omni/internal/text"
)

func newGroupsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "groups",
		Short: "List tool groups defined in settings.json",
		Long: `Groups lists every group defined in settings.json together with
how many tools each group contains.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groups, err := state.app.GroupSummaries(cmd.Context())
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				fmt.Fprintln(cmdOut(cmd), "No groups found. Run 'omni add' to create one.")
				return nil
			}
			for _, g := range groups {
				desc := ""
				if g.Description != "" {
					desc = "  — " + g.Description
				}
				fmt.Fprintf(cmdOut(cmd), "  %-20s %s%s\n", g.Name, textutil.PluralCount(g.ToolCount, "tool", "tools"), desc)
			}
			return nil
		},
	}
	cmd.AddCommand(
		newGroupsCreateCmd(state),
		newGroupsRenameCmd(state),
		newGroupsDeleteCmd(state),
		newGroupsMoveToolCmd(state),
		newGroupsSetToolCmd(state),
		newGroupsRemoveToolCmd(state),
		newGroupsIgnoreToolCmd(state),
		newGroupsUnignoreToolCmd(state),
	)
	return cmd
}

func newGroupsCreateCmd(state *rootState) *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create an empty tool group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.CreateGroup(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Created group %q.\n", args[0])
			return nil
		},
	}
}

func newGroupsRenameCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "Rename a tool group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RenameGroup(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Renamed group %q to %q.\n", args[0], args[1])
			return nil
		},
	}
	// first arg is the old group name; second arg (new name) is freeform
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeGroupNames(state)(c, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func newGroupsDeleteCmd(state *rootState) *cobra.Command {
	var moveTo string
	var deleteTools bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a tool group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if moveTo != "" && deleteTools {
				return fmt.Errorf("use either --move-to or --delete-tools, not both")
			}
			tools, dots, err := state.app.GroupContentCounts(args[0])
			if err != nil {
				return err
			}
			hasContent := tools > 0 || dots > 0
			if hasContent && moveTo == "" && !deleteTools {
				if !stdinIsTerminal() {
					return fmt.Errorf("groups delete requires --move-to <group> or --delete-tools")
				}
				selected, ok := promptText("Move owned tools to group, or type DELETE to delete their specs?", "")
				if !ok {
					return fmt.Errorf("groups delete requires --move-to <group> or --delete-tools")
				}
				if selected == "DELETE" {
					deleteTools = true
				} else {
					moveTo = selected
				}
			}
			action := fmt.Sprintf("Delete empty group %q?", args[0])
			if deleteTools {
				action = fmt.Sprintf("Delete group %q and delete tools with no other membership?", args[0])
			} else if moveTo != "" {
				action = fmt.Sprintf("Delete group %q and move last-membership tools to %q?", args[0], moveTo)
			}
			ok, err := confirmAction(cmd, state, action)
			if err != nil || !ok {
				return err
			}
			opts := app.DeleteGroupOptions{MoveTo: moveTo, DeleteTools: deleteTools}
			if err := state.app.DeleteGroup(cmd.Context(), args[0], opts); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Deleted group %q.\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&moveTo, "move-to", "", "move tools that have no other group membership to this group")
	cmd.Flags().BoolVar(&deleteTools, "delete-tools", false, "delete logical tool specs that have no other group membership")
	cmd.ValidArgsFunction = completeGroupNames(state)
	_ = cmd.RegisterFlagCompletionFunc("move-to", completeGroupNames(state))
	return cmd
}

func newGroupsMoveToolCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "move-tool <group> <tool>",
		Short: "Move a logical tool to a group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGroupsMoveTool(cmd, state, args)
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeGroupNames(state)(c, args, toComplete)
		}
		return completeToolNames(state)(c, args, toComplete)
	}
	return cmd
}

func runGroupsMoveTool(cmd *cobra.Command, state *rootState, args []string) error {
	if err := state.app.MoveToolToGroup(args[1], args[0]); err != nil {
		return err
	}
	fmt.Fprintf(cmdOut(cmd), "Moved %q to group %q.\n", args[1], args[0])
	return nil
}

func newGroupsSetToolCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-tool <tool> <group>...",
		Short: "Set a logical tool's full group membership",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool := args[0]
			groups := args[1:]
			if err := state.app.SetToolGroups(tool, groups, nil, ""); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Set %q groups to %s.\n", tool, strings.Join(groups, ", "))
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeToolNames(state)(c, args, toComplete)
		}
		return completeGroupNames(state)(c, args, toComplete)
	}
	return cmd
}

func newGroupsIgnoreToolCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ignore-tool <group> <tool>",
		Short: "Ignore a logical tool in one group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.SetGroupIgnore(args[0], args[1], true); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Ignored %q in group %q.\n", args[1], args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeGroupNames(state)(c, args, toComplete)
		}
		return completeToolNames(state)(c, args, toComplete)
	}
	return cmd
}

func newGroupsUnignoreToolCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unignore-tool <group> <tool>",
		Short: "Stop ignoring a logical tool in one group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.SetGroupIgnore(args[0], args[1], false); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Unignored %q in group %q.\n", args[1], args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeGroupNames(state)(c, args, toComplete)
		}
		return completeToolNames(state)(c, args, toComplete)
	}
	return cmd
}

func newGroupsRemoveToolCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-tool <group> <tool>",
		Short: "Remove a logical tool membership from a group",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := state.app.RemoveToolFromGroup(args[1], args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "Removed %q from group %q.\n", args[1], args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeGroupNames(state)(c, args, toComplete)
		}
		return completeToolNames(state)(c, args, toComplete)
	}
	return cmd
}
