package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

func newAgentsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage AI-agent resources (skills)",
	}
	cmd.AddCommand(newAgentsSkillsCmd(state))
	return cmd
}

func newAgentsSkillsCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Restore and import agent skills",
	}
	cmd.AddCommand(
		newAgentsRestoreSkillsCmd(state),
		newAgentsImportSkillsCmd(state),
		newAgentsUpdateSkillsCmd(state),
	)
	return cmd
}

func newAgentsUpdateSkillsCmd(state *rootState) *cobra.Command {
	var opts app.UpdateSkillsOptions
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update manifest skills to their latest upstream versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			output, dryRun, err := state.app.UpdateSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.DryRun {
				fmt.Fprintln(cmdOut(cmd), dryRun)
				return nil
			}
			fmt.Fprint(cmdOut(cmd), output)
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the skills update command without running it")
	return cmd
}

func newAgentsRestoreSkillsCmd(state *rootState) *cobra.Command {
	var opts app.RestoreSkillsOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Install the manifest skill set onto this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, lines, err := state.app.RestoreSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if opts.DryRun {
				for _, l := range lines {
					fmt.Fprintln(cmdOut(cmd), l)
				}
				return nil
			}
			fmt.Fprintln(cmdOut(cmd), app.RestoreSkillsSummaryText(res))
			for _, f := range res.Failed {
				fmt.Fprintf(cmdOut(cmd), "  ! %s: %s\n", f.Name, f.Message)
			}
			for _, d := range res.Drift {
				fmt.Fprintf(cmdOut(cmd), "  ~ drift: %s changed since lock\n", d)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the skills add commands without running them")
	return cmd
}

func newAgentsImportSkillsCmd(state *rootState) *cobra.Command {
	var opts app.ImportSkillsOptions
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import CLI/UI-added skills from the lockfile into the manifest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			diff, err := state.app.ImportSkills(cmd.Context(), opts)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmdOut(cmd), app.ImportDiffSummaryText(diff))
			for _, n := range diff.Added {
				fmt.Fprintf(cmdOut(cmd), "  + %s\n", n)
			}
			for _, n := range diff.Updated {
				fmt.Fprintf(cmdOut(cmd), "  ~ %s\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show the manifest diff without writing")
	return cmd
}
