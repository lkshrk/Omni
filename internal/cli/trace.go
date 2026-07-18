package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newTraceCmd(state *rootState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "Inspect recent Omni command traces",
	}
	cmd.AddCommand(newTraceListCmd(state))
	return cmd
}

func newTraceListCmd(state *rootState) *cobra.Command {
	limit := 50
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent external commands Omni issued",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			traces, err := state.app.CommandTraces(cmd.Context(), limit)
			if err != nil {
				return err
			}
			out := cmdOut(cmd)
			if len(traces) == 0 {
				fmt.Fprintln(out, "No command traces recorded.")
				return nil
			}
			for _, trace := range traces {
				when := trace.StartedAt.Local().Format(time.RFC3339)
				duration := fmt.Sprintf("%dms", trace.DurationMS)
				exit := ""
				if trace.ExitCode != nil {
					exit = fmt.Sprintf(" exit=%d", *trace.ExitCode)
				}
				reason := strings.TrimSpace(trace.Reason)
				if reason == "" {
					reason = "external command"
				}
				fmt.Fprintf(out, "%s [%s %s%s] %s\n", when, trace.Status, duration, exit, reason)
				fmt.Fprintf(out, "  %s\n", trace.Command)
				if trace.Error != "" {
					fmt.Fprintf(out, "  error: %s\n", trace.Error)
				}
				if trace.Stderr != "" {
					fmt.Fprintf(out, "  stderr: %s\n", trace.Stderr)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum number of command traces to show")
	return cmd
}
