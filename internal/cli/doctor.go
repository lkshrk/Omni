package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

func newDoctorCmd(state *rootState) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run read-only health checks",
		Long:  "Run read-only diagnostics for config, host setup, providers, dotfiles, native services, and local cache state.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := state.app.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if format == "json" {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else {
				if err := validateFormat(format, "text", "json"); err != nil {
					return err
				}
				printDoctorResult(cmd, result)
			}
			if result.HasFailures() {
				return fmt.Errorf("doctor found %d failing check(s)", result.Summary.Fail)
			}
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	return cmd
}

func printDoctorResult(cmd *cobra.Command, result *app.DoctorResult) {
	fmt.Fprintln(cmd.OutOrStdout(), "Omni doctor")
	for _, check := range result.Checks {
		fmt.Fprintf(cmd.OutOrStdout(), "[%-4s] %-10s %s\n", check.Status, check.Label+":", check.Message)
		for _, detail := range check.Details {
			fmt.Fprintf(cmd.OutOrStdout(), "       %s\n", strings.TrimSpace(detail))
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Summary: %d ok, %d warn, %d fail\n", result.Summary.OK, result.Summary.Warn, result.Summary.Fail)
}
