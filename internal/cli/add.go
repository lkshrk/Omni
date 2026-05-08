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
		Short: "Add a tool to the config",
		Long: `Add creates a logical tool spec and adds its name to a group.

Example:
  omni add ripgrep --provider system
  omni add typescript --provider node --install-with pnpm --name ts --group work
  omni add slack --provider system --install-with brew --group work`,
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
			fmt.Printf("✓ added %s (%s) to %s group\n", displayName, providerLabel, dest)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "override the tool name in config (default: package name)")
	cmd.Flags().StringVar(&group, "group", "", "group to add the tool to (default: current host)")
	addProviderFlag(cmd, &providerName, "ecosystem provider for the logical tool")
	cmd.Flags().StringVar(&installWith, "install-with", "", "concrete provider or manager to use for this tool")
	return cmd
}
