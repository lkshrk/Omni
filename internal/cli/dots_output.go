package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/dots"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func requireDotsConfigured(state *rootState) error {
	if !state.app.DotsConfigured() {
		return fmt.Errorf("dots_repo is not configured\n\nSet it via 'omni ui' (Dots tab) or settings.dots_repo in settings.json")
	}
	return nil
}

func printDotOps(cmd *cobra.Command, ops []dots.Op, dryRun bool) {
	conflicts := 0
	changes := 0
	skipped := 0
	for _, op := range ops {
		switch op.Kind {
		case dots.OpSkip:
			if op.Err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "  - skipped:  %s — %v\n", op.Dst, op.Err)
				skipped++
			}
		case dots.OpLink:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ linked:   %s → %s\n", op.Dst, op.Src)
			changes++
		case dots.OpRepair:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ repaired: %s\n", op.Dst)
			changes++
		case dots.OpAdopt:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ adopted:  %s → %s\n", op.Dst, op.Src)
			changes++
		case dots.OpConflict:
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ conflict: %s — %v\n", op.Dst, op.Err)
			conflicts++
		case dots.OpUnlink:
			fmt.Fprintf(cmd.OutOrStdout(), "  ✓ unlinked: %s\n", op.Dst)
			changes++
		case dots.OpUnlinkSkip:
			fmt.Fprintf(cmd.OutOrStdout(), "  - skipped:  %s\n", op.Dst)
		case dots.OpUnlinkConflict:
			fmt.Fprintf(cmd.ErrOrStderr(), "  ✗ unlink conflict: %s\n", op.Dst)
			conflicts++
		case dots.OpDryLink:
			fmt.Fprintf(cmd.OutOrStdout(), "  → would link:   %s\n", op.Dst)
		case dots.OpDryRepair:
			fmt.Fprintf(cmd.OutOrStdout(), "  → would repair: %s\n", op.Dst)
		case dots.OpDryAdopt:
			fmt.Fprintf(cmd.OutOrStdout(), "  → would adopt:  %s\n", op.Dst)
		}
	}
	if dryRun {
		fmt.Fprintln(cmd.OutOrStdout(), "\nDry-run — no changes made.")
		return
	}
	if changes > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\n%d symlink(s) updated.\n", changes)
	}
	if conflicts > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d conflict(s). Choose use repo version or use local version before syncing.\n", conflicts)
	}
	if changes == 0 && conflicts == 0 && skipped > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No symlinks updated.")
	}
	if changes == 0 && conflicts == 0 && skipped == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "All symlinks up to date.")
	}
}

func printDotsTable(cmd *cobra.Command, statuses []app.DotStatus) {
	for _, section := range dotCLISections(statuses) {
		if len(section.statuses) == 0 {
			continue
		}
		fmt.Fprintln(cmd.OutOrStdout(), section.title)
		printDotsSectionTable(cmd, section.statuses)
		fmt.Fprintln(cmd.OutOrStdout())
	}
}

type dotCLISection struct {
	title    string
	statuses []app.DotStatus
}

func dotCLISections(statuses []app.DotStatus) []dotCLISection {
	sections := []dotCLISection{
		{title: "Conflict"},
		{title: "Out Of Sync"},
		{title: "Synced"},
		{title: "Ignored"},
	}
	for _, status := range statuses {
		state := dotStatusState(status)
		switch state {
		case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
			sections[0].statuses = append(sections[0].statuses, status)
		case app.DotStateSynced:
			sections[2].statuses = append(sections[2].statuses, status)
		case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
			sections[3].statuses = append(sections[3].statuses, status)
		default:
			sections[1].statuses = append(sections[1].statuses, status)
		}
	}
	return sections
}

func printDotsSectionTable(cmd *cobra.Command, statuses []app.DotStatus) {
	const (
		nameW    = 20
		targetW  = 36
		stateW   = 18
		actionsW = 28
	)
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %s", nameW, "NAME", targetW, "TARGET", stateW, "STATE", "ACTIONS")
	fmt.Fprintln(cmd.OutOrStdout(), header)
	fmt.Fprintln(cmd.OutOrStdout(), strings.Repeat("─", len(header)))
	for _, s := range statuses {
		state := dotStatusState(s)
		fmt.Fprintf(cmd.OutOrStdout(), "%-*s  %-*s  %s %-*s  %s\n",
			nameW, s.Name,
			targetW, truncateDotsTarget(s.TargetPath, targetW),
			dotStateIcon(state), stateW-2, state,
			truncateDotsActions(s.Actions, actionsW),
		)
	}
}

func dotStatusState(status app.DotStatus) app.DotState {
	if status.State != "" {
		return status.State
	}
	switch status.Health {
	case app.HealthOK:
		return app.DotStateSynced
	case app.HealthMissing:
		return app.DotStateMissing
	case app.HealthConflict:
		return app.DotStateConflict
	case app.HealthNoSource:
		return app.DotStateNoSource
	default:
		return app.DotState(status.Health)
	}
}

func dotStateIcon(state app.DotState) string {
	switch state {
	case app.DotStateSynced:
		return "✓"
	case app.DotStateConflict, app.DotStateUntrackedConflict, app.DotStateAmbiguous:
		return "✗"
	case app.DotStateNoSource:
		return "?"
	case app.DotStateIgnored, app.DotStateInactive, app.DotStateDisabled:
		return "·"
	default:
		return "!"
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

func truncateDotsActions(actions []app.DotAction, width int) string {
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

func healthIcon(h app.DotHealth) string {
	switch h {
	case app.HealthOK:
		return "✓"
	case app.HealthMissing:
		return "·"
	case app.HealthConflict:
		return "✗"
	case app.HealthNoSource:
		return "?"
	default:
		return " "
	}
}
