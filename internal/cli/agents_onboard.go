package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/app"
)

func newAgentsOnboardCmd(state *rootState) *cobra.Command {
	var planJSON, applyPlan string
	var apply, outputJSON bool
	var targetFlags, envFlags, excludeFlags, moveFlags, keepDotsFlags []string
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Preview or apply migration of Omni-managed agent state into APM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if apply || applyPlan != "" {
				if applyPlan == "" {
					return errors.New("--apply requires --apply-plan")
				}
				resolutions, parseErr := parseOnboardResolutions(targetFlags, envFlags, excludeFlags, moveFlags, keepDotsFlags)
				if parseErr != nil {
					return parseErr
				}
				result, err := state.app.AgentsOnboardApplyResolved(cmd.Context(), applyPlan, resolutions)
				if err == nil || outputJSON {
					printOnboardJSON(cmd, result)
				}
				return err
			}
			result, err := state.app.AgentsOnboardPlan(cmd.Context(), app.AgentsOnboardOptions{PlanJSON: planJSON})
			if err == nil || outputJSON {
				printOnboardJSON(cmd, result)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&planJSON, "plan-json", "", "Persist the reviewed plan to an absolute path")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply the reviewed plan")
	cmd.Flags().StringVar(&applyPlan, "apply-plan", "", "Absolute reviewed plan path")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Print the onboarding result as JSON")
	cmd.Flags().StringArrayVar(&targetFlags, "approve-targets", nil, "Resolve item targets as ITEM=target,target (repeatable)")
	cmd.Flags().StringArrayVar(&envFlags, "map-secret", nil, "Map secret field as ITEM:FIELD=ENV_VAR (repeatable)")
	cmd.Flags().StringArrayVar(&excludeFlags, "exclude", nil, "Keep item unmanaged (durable, repeatable)")
	cmd.Flags().StringArrayVar(&moveFlags, "move-to-apm", nil, "Remove item from dots and migrate it to APM (repeatable)")
	cmd.Flags().StringArrayVar(&keepDotsFlags, "keep-in-dots", nil, "Keep item managed by dots (repeatable)")
	cmd.AddCommand(newAgentsOnboardStatusCmd(state), newAgentsOnboardResumeCmd(state), newAgentsOnboardRollbackCmd(state), newAgentsOnboardCleanupCmd(state))
	return cmd
}

func newAgentsOnboardRollbackCmd(state *rootState) *cobra.Command {
	var operation string
	cmd := &cobra.Command{Use: "rollback", Short: "Rollback an incomplete onboarding migration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := state.app.AgentsOnboardRollback(cmd.Context(), operation)
		if err == nil {
			printOnboardJSON(cmd, result)
		}
		return err
	}}
	cmd.Flags().StringVar(&operation, "operation", "", "Onboarding operation ID")
	_ = cmd.MarkFlagRequired("operation")
	return cmd
}

func parseOnboardResolutions(targets, bindings, exclusions, moves, keepDots []string) (app.AgentsOnboardResolutions, error) {
	out := app.AgentsOnboardResolutions{ApprovedTargets: map[string][]string{}, EnvBindings: map[string]map[string]string{}, Excluded: map[string]bool{}, MoveToAPM: map[string]bool{}, KeepInDots: map[string]bool{}}
	for _, value := range targets {
		item, list, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(item) == "" {
			return out, fmt.Errorf("invalid --approve-targets %q", value)
		}
		for _, target := range strings.Split(list, ",") {
			target = strings.TrimSpace(target)
			if target == "" {
				return out, fmt.Errorf("invalid target %q", target)
			}
			out.ApprovedTargets[item] = append(out.ApprovedTargets[item], target)
		}
	}
	for _, value := range bindings {
		left, env, ok := strings.Cut(value, "=")
		item, field, hasField := strings.Cut(left, ":")
		if !ok || !hasField || item == "" || field == "" || !validEnvName(env) {
			return out, fmt.Errorf("invalid --map-secret %q", value)
		}
		if out.EnvBindings[item] == nil {
			out.EnvBindings[item] = map[string]string{}
		}
		out.EnvBindings[item][field] = env
	}
	for _, item := range exclusions {
		if strings.TrimSpace(item) == "" {
			return out, errors.New("--exclude item is empty")
		}
		out.Excluded[item] = true
	}
	for _, item := range moves {
		if strings.TrimSpace(item) == "" {
			return out, errors.New("--move-to-apm item is empty")
		}
		out.MoveToAPM[item] = true
	}
	for _, item := range keepDots {
		if strings.TrimSpace(item) == "" {
			return out, errors.New("--keep-in-dots item is empty")
		}
		out.KeepInDots[item] = true
	}
	return out, nil
}

func validEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func newAgentsOnboardStatusCmd(state *rootState) *cobra.Command {
	var operation string
	cmd := &cobra.Command{Use: "status", Short: "Show local onboarding recovery status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if operation == "" {
			return errors.New("--operation is required")
		}
		result, err := state.app.AgentsOnboardStatus(cmd.Context(), operation)
		if err == nil {
			printOnboardJSON(cmd, result)
		}
		return err
	}}
	cmd.Flags().StringVar(&operation, "operation", "", "Onboarding operation ID")
	_ = cmd.MarkFlagRequired("operation")
	return cmd
}

func newAgentsOnboardResumeCmd(state *rootState) *cobra.Command {
	var operation string
	cmd := &cobra.Command{Use: "resume", Short: "Resume an interrupted onboarding operation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := state.app.AgentsOnboardResume(cmd.Context(), operation)
		if err == nil {
			printOnboardJSON(cmd, result)
		}
		return err
	}}
	cmd.Flags().StringVar(&operation, "operation", "", "Onboarding operation ID")
	_ = cmd.MarkFlagRequired("operation")
	return cmd
}

func newAgentsOnboardCleanupCmd(state *rootState) *cobra.Command {
	var operation string
	var confirm bool
	cmd := &cobra.Command{Use: "cleanup", Short: "Delete backups for a completed onboarding operation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		preview, err := state.app.AgentsOnboardCleanup(cmd.Context(), operation, false)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmdOut(cmd), "Cleanup will remove %d path(s):\n", preview.Count)
		for _, path := range preview.Paths {
			fmt.Fprintln(cmdOut(cmd), "  "+path)
		}
		if preview.AlreadyClean {
			fmt.Fprintln(cmdOut(cmd), "onboarding backup cleanup already complete")
			return nil
		}
		if !confirm {
			return errors.New("cleanup requires --confirm")
		}
		if _, err := state.app.AgentsOnboardCleanup(cmd.Context(), operation, true); err != nil {
			return err
		}
		fmt.Fprintln(cmdOut(cmd), "onboarding backup cleanup complete")
		return nil
	}}
	cmd.Flags().StringVar(&operation, "operation", "", "Onboarding operation ID")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm permanent backup deletion")
	_ = cmd.MarkFlagRequired("operation")
	return cmd
}

func printOnboardJSON(cmd *cobra.Command, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		fmt.Fprintln(cmdOut(cmd), string(data))
	}
}
