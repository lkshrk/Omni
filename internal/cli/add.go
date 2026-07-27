package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAddCmd(state *rootState) *cobra.Command {
	var name string
	var group string
	var providerName string
	var installWith string

	cmd := &cobra.Command{
		Use:   "add <package>",
		Short: "Declare a tool in config and install it here",
		Long: `Add records a logical tool spec in a group and installs it on this
machine, so declaration and this host's convergence happen in one step. Other
hosts pick it up on their next sync.

Example:
  omni add ripgrep --provider brew --group work
  omni add typescript --provider npm --name ts --group work
  omni add black --provider uv --group work`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg := args[0]
			if err := requireProvider(providerName); err != nil {
				return err
			}
			if !cmd.Flags().Changed("group") {
				if !stdinIsTerminal() {
					return fmt.Errorf("missing assignment target: pass --group <group> for non-interactive add")
				}
				selected, ok := promptText("Add to group?", "")
				if !ok {
					return fmt.Errorf("missing assignment target: pass --group <group>")
				}
				group = selected
			}
			if err := state.app.Add(cmd.Context(), providerName, pkg, name, group, installWith); err != nil {
				return err
			}
			displayName := name
			if displayName == "" {
				displayName = pkg
			}
			dest := "current host"
			if group != "" {
				dest = group
			}
			providerLabel := providerName
			if installWith != "" {
				providerLabel += " via " + installWith
			}
			fmt.Fprintf(cmdOut(cmd), "✓ added %s (%s) to %s group\n", displayName, providerLabel, dest)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "override the tool name in config (default: package name)")
	cmd.Flags().StringVar(&group, "group", "", "group to add the tool to (default: current host)")
	addProviderFlag(cmd, &providerName, "provider candidate for the logical tool")
	cmd.Flags().StringVar(&installWith, "install-with", "", "concrete provider or manager to use for this tool")
	cmd.ValidArgsFunction = cobra.NoFileCompletions
	_ = cmd.RegisterFlagCompletionFunc("group", completeGroupNames(state))
	_ = cmd.RegisterFlagCompletionFunc("install-with", completeProviderNames(state))
	return cmd
}
