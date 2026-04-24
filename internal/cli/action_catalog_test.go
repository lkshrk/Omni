package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/lkshrk/omni/internal/actions"
)

func TestToolActionCatalogCLIBindingsExist(t *testing.T) {
	root := NewRootCmd()

	for _, action := range actions.All() {
		for _, binding := range action.CLI {
			cmd := findCommand(root, binding.Command)
			if cmd == nil {
				t.Fatalf("%s references missing CLI command %q", action.ID, strings.Join(binding.Command, " "))
			}
			for _, flagName := range binding.Flags {
				flagName = strings.TrimPrefix(flagName, "--")
				if cmd.Flags().Lookup(flagName) == nil && cmd.PersistentFlags().Lookup(flagName) == nil {
					t.Fatalf("%s references missing flag --%s on %q", action.ID, flagName, strings.Join(binding.Command, " "))
				}
			}
		}
	}
}

func TestMutatingCLICommandsAreCataloged(t *testing.T) {
	for _, path := range [][]string{
		{"init"},
		{"sync"},
		{"add"},
		{"install"},
		{"delete"},
		{"upgrade"},
		{"switch"},
		{"import"},
		{"consolidate"},
		{"tools", "set"},
		{"tools", "delete"},
		{"tools", "ignore"},
		{"tools", "unignore"},
		{"groups", "create"},
		{"groups", "rename"},
		{"groups", "delete"},
		{"groups", "add-tool"},
		{"groups", "remove-tool"},
		{"groups", "ignore-tool"},
		{"groups", "unignore-tool"},
		{"profile", "add"},
		{"profile", "rename"},
		{"profile", "delete"},
		{"profile", "add-group"},
		{"profile", "remove-group"},
		{"profile", "set-hostname"},
		{"profile", "remove-hostname"},
		{"profile", "ignore", "add"},
		{"profile", "ignore", "remove"},
		{"settings", "set"},
		{"settings", "disable-provider"},
		{"settings", "enable-provider"},
		{"settings", "reset"},
		{"settings", "reset-cache"},
		{"dots", "sync"},
		{"dots", "add"},
		{"dots", "groups"},
		{"dots", "delete"},
		{"dots", "ignore"},
		{"dots", "enable"},
		{"dots", "disable"},
		{"dots", "pull"},
		{"dots", "push"},
	} {
		if !catalogHasCLICommand(path) {
			t.Fatalf("mutating CLI command %q is missing from action catalog", strings.Join(path, " "))
		}
	}
}

func TestCanonicalDeleteCLICommands(t *testing.T) {
	for _, path := range [][]string{
		{"delete"},
		{"tools", "delete"},
		{"profile", "delete"},
		{"dots", "delete"},
		{"groups", "delete"},
	} {
		if findCommand(NewRootCmd(), path) == nil {
			t.Fatalf("missing canonical delete command %q", strings.Join(path, " "))
		}
	}
	for _, path := range [][]string{
		{"uninstall"},
		{"tools", "remove"},
		{"profile", "remove"},
		{"dots", "remove"},
	} {
		if findCommand(NewRootCmd(), path) != nil {
			t.Fatalf("legacy destructive command %q should not be registered", strings.Join(path, " "))
		}
	}
}

func TestConfirmableToolActionsHaveYesBypass(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentFlags().Lookup("yes") == nil {
		t.Fatal("root command is missing global --yes confirmation bypass")
	}
	for _, action := range actions.All() {
		if action.RequiresConfirm && len(action.CLI) == 0 {
			t.Fatalf("%s requires confirmation but has no CLI binding", action.ID)
		}
	}
}

func catalogHasCLICommand(path []string) bool {
	for _, action := range actions.All() {
		for _, binding := range action.CLI {
			if len(binding.Command) != len(path) {
				continue
			}
			matches := true
			for i := range path {
				if binding.Command[i] != path[i] {
					matches = false
					break
				}
			}
			if matches {
				return true
			}
		}
	}
	return false
}

func findCommand(root *cobra.Command, path []string) *cobra.Command {
	cmd := root
	for _, name := range path {
		var next *cobra.Command
		for _, child := range cmd.Commands() {
			if child.Name() == name {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		cmd = next
	}
	return cmd
}
