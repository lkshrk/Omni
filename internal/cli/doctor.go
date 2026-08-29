package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
	textutil "github.com/lkshrk/omni/internal/text"
)

func newDoctorCmd(state *rootState) *cobra.Command {
	var format string
	var fix bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run read-only health checks",
		Long:  "Run read-only diagnostics for config, host setup, providers, dotfiles, native services, and local cache state. With --fix, apply safe auto-fixes (duplicate $include definitions, exact package-owned MCP/LSP duplicates, dead ignore patterns, installing or upgrading the apm CLI) first.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Fix progress is prose, so in JSON mode it goes to stderr rather than into the document stdout parsers consume.
			out := cmd.OutOrStdout()
			if format == "json" {
				out = cmd.ErrOrStderr()
			}
			if dryRun && !fix {
				return fmt.Errorf("--dry-run requires --fix")
			}
			// A failed fixer must not cost the user the health report, so diagnostics print before the error returns.
			var fixErr error
			if fix {
				fixResult := state.app.FixDoctorIssues(cmd.Context(), dryRun)
				if fixResult.OptimizeErr == nil {
					printOptimizeReport(out, fixResult.OptimizeReport, dryRun)
				}
				printAgentsOwnedChildrenFixReport(out, fixResult.OwnedChildren, dryRun)
				if dryRun && fixResult.Err() == nil {
					fmt.Fprintln(out, "dry run: no files were changed (ignore-pattern cleanup runs only on a real --fix)")
				}
				if !dryRun && len(fixResult.IgnoreModified) > 0 {
					fmt.Fprintf(out, "cleaned ignore patterns for: %s\n", strings.Join(fixResult.IgnoreModified, ", "))
				}
				printAPMInstallFixReport(out, fixResult.APMInstall)
				fixErr = fixResult.Err()
			}
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
			if fixErr != nil {
				return fmt.Errorf("applying fixes: %w", fixErr)
			}
			if result.HasFailures() {
				return fmt.Errorf("doctor found %d failing check(s)", result.Summary.Fail)
			}
			return nil
		},
	}
	addFormatFlag(cmd, &format, "text", "text", "json")
	cmd.Flags().BoolVar(&fix, "fix", false, "apply safe auto-fixes, including exact package-owned MCP/LSP duplicate repair")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --fix: show planned fixes without writing")
	return cmd
}

func printAPMInstallFixReport(out io.Writer, report app.APMInstallFixReport) {
	if report.Planned != "" {
		fmt.Fprintf(out, "dry run: would install or upgrade APM via: %s\n", report.Planned)
	}
	if report.Upgraded != "" {
		fmt.Fprintf(out, "upgraded APM via: %s\n", report.Upgraded)
	}
	if report.Installed != "" {
		fmt.Fprintf(out, "installed APM via: %s\n", report.Installed)
		if report.NotOnPATH {
			fmt.Fprintln(out, "warning: apm installed but not resolvable; add its bin directory (usually ~/.local/bin) to PATH")
		}
	}
}

func printAgentsOwnedChildrenFixReport(out io.Writer, report app.AgentsOwnedChildrenFixReport, dryRun bool) {
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, item := range report.Removed {
		fmt.Fprintf(out, "%s standalone %s %s: provided identically by %s\n", verb, item.Kind, item.Name, item.Owner)
	}
	for _, item := range report.Kept {
		reason := item.Reason
		if reason == "" && len(item.Fields) > 0 {
			reason = strings.Join(item.Fields, ", ") + " differs"
		}
		if reason == "" {
			reason = "not safe to remove automatically"
		}
		if item.Exact {
			fmt.Fprintf(out, "kept standalone %s %s: provided identically by %s but %s\n", item.Kind, item.Name, item.Owner, reason)
			continue
		}
		fmt.Fprintf(out, "kept standalone %s %s: conflicts with package %s (%s)\n", item.Kind, item.Name, item.Owner, reason)
	}
	if len(report.Unavailable) > 0 {
		fmt.Fprintf(out, "kept agent ownership unchanged: package manifests unavailable for %s\n", strings.Join(report.Unavailable, ", "))
	}
	if report.SyncRequired {
		fmt.Fprintln(out, "run 'omni agents sync' to apply the repaired agents template")
	}
}

func printOptimizeReport(out io.Writer, report *config.OptimizeReport, dryRun bool) {
	if report.Empty() {
		fmt.Fprintln(out, "no duplicate definitions across $include fragments")
		return
	}
	verb := "removed"
	if dryRun {
		verb = "would remove"
	}
	for _, r := range report.Removals {
		if r.Key == "group" {
			fmt.Fprintf(out, "%s empty group %q from %s\n", verb, r.Group, r.File)
			continue
		}
		fmt.Fprintf(out, "%s %s duplicate %s from %s (group %q): %s\n",
			verb, textutil.PluralCount(len(r.Names), "entry", "entries"), r.Key, r.File, r.Group, strings.Join(r.Names, ", "))
	}
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
