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
		Long:  "Run read-only diagnostics for config, host setup, providers, dotfiles, native services, and local cache state. With --fix, apply safe auto-fixes (duplicate $include definitions, dead ignore patterns, installing or upgrading the apm CLI) first.",
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
				printSkillStoreFixReport(out, fixResult.SkillStore, dryRun)
				if dryRun && fixResult.Err() == nil {
					fmt.Fprintln(out, "dry run: no files were changed (ignore-pattern cleanup runs only on a real --fix)")
				}
				if !dryRun && len(fixResult.IgnoreModified) > 0 {
					fmt.Fprintf(out, "cleaned ignore patterns for: %s\n", strings.Join(fixResult.IgnoreModified, ", "))
				}
				if fixResult.APMInstall.Planned != "" {
					fmt.Fprintf(out, "dry run: would install or upgrade APM via: %s\n", fixResult.APMInstall.Planned)
				}
				if fixResult.APMInstall.Upgraded != "" {
					fmt.Fprintf(out, "upgraded APM via: %s\n", fixResult.APMInstall.Upgraded)
				}
				if fixResult.APMInstall.Installed != "" {
					fmt.Fprintf(out, "installed APM via: %s\n", fixResult.APMInstall.Installed)
					if fixResult.APMInstall.NotOnPATH {
						fmt.Fprintln(out, "warning: apm installed but not resolvable; add its bin directory (usually ~/.local/bin) to PATH")
					}
				}
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
	cmd.Flags().BoolVar(&fix, "fix", false, "apply safe auto-fixes before running checks")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --fix: show planned fixes without writing")
	return cmd
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

func printSkillStoreFixReport(out io.Writer, report app.SkillStoreFixReport, dryRun bool) {
	removed, rebuilt := "removed", "rebuilt"
	if dryRun {
		removed, rebuilt = "would remove", "would rebuild"
	}
	// Each debris line already carries its own verb, since the action varies per item.
	for _, line := range report.Debris {
		fmt.Fprintln(out, line)
	}
	for _, path := range report.DanglingLinks {
		fmt.Fprintf(out, "%s dangling skill link %s\n", removed, path)
	}
	for _, path := range report.OrphanedPackages {
		fmt.Fprintf(out, "%s unreferenced skill package %s\n", removed, path)
	}
	for _, source := range report.RebuiltMetadata {
		fmt.Fprintf(out, "%s local install metadata for %s\n", rebuilt, source)
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
