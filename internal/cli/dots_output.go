package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

func requireDotsConfigured(state *rootState) error {
	if !state.app.DotsConfigured() {
		return fmt.Errorf("dots_repo is not configured\n\nSet it via 'omni ui' (Dots tab) or settings.dots_repo in settings.json")
	}
	return nil
}

func printDotOps(cmd *cobra.Command, ops []dots.Op, dryRun bool) {
	report := app.DotsOperationReport(ops, dryRun)
	for _, line := range report.Stdout {
		fmt.Fprintln(cmdOut(cmd), line)
	}
	for _, line := range report.Stderr {
		fmt.Fprintln(cmdErr(cmd), line)
	}
}

func printDotsTable(cmd *cobra.Command, statuses []app.DotStatus) {
	for _, section := range app.DotStatusSections(statuses) {
		if len(section.Statuses) == 0 {
			continue
		}
		fmt.Fprintln(cmdOut(cmd), section.Title)
		printDotsSectionTable(cmd, section.Statuses)
		fmt.Fprintln(cmdOut(cmd))
	}
}

func printDotsSectionTable(cmd *cobra.Command, statuses []app.DotStatus) {
	const (
		nameW    = 20
		targetW  = 36
		stateW   = 18
		actionsW = 28
	)
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %s", nameW, "NAME", targetW, "TARGET", stateW, "STATE", "ACTIONS")
	fmt.Fprintln(cmdOut(cmd), header)
	fmt.Fprintln(cmdOut(cmd), strings.Repeat("─", len(header)))
	for _, s := range statuses {
		state := app.DotStatusState(s)
		fmt.Fprintf(cmdOut(cmd), "%-*s  %-*s  %s %-*s  %s\n",
			nameW, s.Name,
			targetW, truncateDotsTarget(s.TargetPath, targetW),
			app.DotStateIcon(state), stateW-2, state,
			truncateDotsActions(s.Actions, actionsW),
		)
	}
}

func truncateDotsTarget(path string, width int) string {
	runes := []rune(path)
	if len(runes) <= width {
		return path
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return "…" + string(runes[len(runes)-width+1:])
}

func truncateDotsActions(actions []dots.Action, width int) string {
	labels := make([]string, 0, len(actions))
	for _, action := range actions {
		labels = append(labels, string(action))
	}
	out := strings.Join(labels, ",")
	runes := []rune(out)
	if len(runes) <= width {
		return out
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
