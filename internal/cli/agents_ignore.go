package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

func agentIgnoreSelectorFlags(cmd *cobra.Command, sel *app.AgentIgnoreSelector) {
	cmd.Flags().StringVar(&sel.Host, "host", "", "Host the exception applies to")
	cmd.Flags().StringVar(&sel.Target, "target", "", "Agent client: claude or codex")
	cmd.Flags().StringVar(&sel.Kind, "kind", "", "Artifact kind: plugin, mcp or marketplace")
	cmd.Flags().StringVar(&sel.ID, "id", "", "Artifact identity as 'omni agents drift' prints it")
	for _, name := range []string{"host", "target", "kind", "id"} {
		cobra.CheckErr(cmd.MarkFlagRequired(name))
	}
}

func newAgentsIgnoreCmd(state *rootState) *cobra.Command {
	var sel app.AgentIgnoreSelector
	cmd := &cobra.Command{
		Use:   "ignore",
		Short: "Record a native agent artifact omni must leave alone",
		Long: "Add an entry to agents.ignored so 'omni agents drift' stops reporting this artifact " +
			"and adoption leaves it in place. Use it for a deliberate native install that should " +
			"not be declared in the host's APM manifest. Ignoring is not adopting: the artifact " +
			"stays native and absent from the manifest.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := state.app.AgentIgnore(sel); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "ignored %s %s %s on %s\n", sel.Target, sel.Kind, sel.ID, sel.Host)
			return nil
		},
	}
	agentIgnoreSelectorFlags(cmd, &sel)
	cmd.Flags().StringVar(&sel.Reason, "reason", "", "Why this artifact stays native")
	return cmd
}

func newAgentsUnignoreCmd(state *rootState) *cobra.Command {
	var sel app.AgentIgnoreSelector
	cmd := &cobra.Command{
		Use:   "unignore",
		Short: "Drop a recorded agent ignore entry",
		Long: "Remove an entry from agents.ignored so the artifact is reported by 'omni agents drift' " +
			"again. Removing an entry that was never recorded fails, because the caller believed it " +
			"was protected.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := state.app.AgentUnignore(sel); err != nil {
				return err
			}
			fmt.Fprintf(cmdOut(cmd), "unignored %s %s %s on %s\n", sel.Target, sel.Kind, sel.ID, sel.Host)
			return nil
		},
	}
	agentIgnoreSelectorFlags(cmd, &sel)
	return cmd
}
