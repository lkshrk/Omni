package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
				if !commandHasFlag(cmd, flagName) {
					t.Fatalf("%s references missing flag --%s on %q", action.ID, flagName, strings.Join(binding.Command, " "))
				}
			}
		}
	}
}

func TestRunnableCLICommandsHaveCatalogCoverageOrExplicitExemption(t *testing.T) {
	root := NewRootCmd()
	for _, path := range discoverRunnableCLICommands(root) {
		if catalogHasCLICommand(path) || uncatalogedRunnableCLICommandAllowed(path) {
			continue
		}
		t.Fatalf("runnable CLI command %q needs an action catalog binding or an explicit read-only/utility exemption", strings.Join(path, " "))
	}
}

func TestMutatingCLICommandsAreCataloged(t *testing.T) {
	root := NewRootCmd()
	expected := [][]string{
		{"reconcile"},
		{"bootstrap"},
		{"tools", "sync"},
		{"tools", "add"},
		{"tools", "install"},
		{"tools", "remove"},
		{"tools", "upgrade"},
		{"tools", "reinstall"},
		{"tools", "migrate-nvm"},
		{"tools", "import"},
		{"tools", "refresh"},
		{"tools", "consolidate"},
		{"tools", "set"},
		{"tools", "delete-spec"},
		{"tools", "ignore"},
		{"tools", "unignore"},
		{"tools", "normalize"},
		{"groups", "create"},
		{"groups", "rename"},
		{"groups", "delete"},
		{"groups", "move-tool"},
		{"groups", "remove-tool"},
		{"groups", "ignore-tool"},
		{"groups", "unignore-tool"},
		{"hosts", "ensure"},
		{"hosts", "set-groups"},
		{"hosts", "add-group"},
		{"hosts", "remove-group"},
		{"hosts", "copy"},
		{"hosts", "remove"},
		{"settings", "set"},
		{"settings", "disable-provider"},
		{"settings", "enable-provider"},
		{"settings", "reset"},
		{"settings", "reset-cache"},
		{"settings", "migrate-host-overrides"},
		{"agents", "sync"},
		{"agents", "add"},
		{"agents", "remove"},
		{"agents", "update"},
		{"dots", "sync"},
		{"dots", "add"},
		{"dots", "groups"},
		{"dots", "variant", "add"},
		{"dots", "variant", "remove"},
		{"dots", "remove"},
		{"dots", "resolve"},
		{"dots", "ignore"},
		{"dots", "unignore"},
		{"dots", "enable"},
		{"dots", "disable"},
		{"dots", "pull"},
		{"dots", "commit"},
		{"dots", "push"},
		{"dots", "reminder", "install"},
		{"dots", "reminder", "uninstall"},
		{"dots", "watch", "run"},
		{"dots", "watch", "install"},
		{"dots", "watch", "uninstall"},
	}

	expectedByKey := make(map[string]bool, len(expected))
	for _, path := range expected {
		expectedByKey[commandKey(path)] = true
	}
	for _, path := range discoverMutatingCLICommands(root) {
		if !expectedByKey[commandKey(path)] {
			t.Fatalf("mutating CLI discovery found %q, but the action catalog coverage list does not include it", strings.Join(path, " "))
		}
	}
	for _, path := range expected {
		if findCommand(root, path) == nil {
			t.Fatalf("mutating CLI command %q is missing from Cobra", strings.Join(path, " "))
		}
		if !catalogHasCLICommand(path) {
			t.Fatalf("mutating CLI command %q is missing from action catalog", strings.Join(path, " "))
		}
	}
}

func TestReadmeOmniExamplesReferenceRegisteredCLI(t *testing.T) {
	root := NewRootCmd()
	readmePath := filepath.Join("..", "..", "README.md")
	body, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}
	inShellBlock := false
	for i, rawLine := range strings.Split(string(body), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(rawLine)
		switch {
		case line == "```sh":
			inShellBlock = true
			continue
		case strings.HasPrefix(line, "```"):
			inShellBlock = false
			continue
		case !inShellBlock || !strings.HasPrefix(line, "omni"):
			continue
		case strings.Contains(line, "|"):
			continue
		}
		if beforeComment, _, ok := strings.Cut(line, "#"); ok {
			line = strings.TrimSpace(beforeComment)
		}
		if line == "" {
			continue
		}
		tokens := strings.Fields(line)
		cmd, path := commandForExample(root, tokens)
		if cmd == nil {
			t.Fatalf("README.md:%d references missing command: %s", lineNo, line)
		}
		for _, token := range tokens[1:] {
			flagName, shorthand, ok := exampleFlagToken(token)
			if !ok {
				continue
			}
			if flagName == "help" || shorthand == "h" {
				continue
			}
			if shorthand != "" {
				if !commandHasShorthand(cmd, shorthand) {
					t.Fatalf("README.md:%d references missing shorthand -%s on %q: %s", lineNo, shorthand, strings.Join(path, " "), line)
				}
				continue
			}
			if !commandHasFlag(cmd, flagName) {
				t.Fatalf("README.md:%d references missing flag --%s on %q: %s", lineNo, flagName, strings.Join(path, " "), line)
			}
		}
	}
}

func TestCatalogedCLICommandFlagsAreCataloged(t *testing.T) {
	root := NewRootCmd()
	catalogFlagsByCommand := catalogCommandFlags()
	for key, flags := range catalogFlagsByCommand {
		path := strings.Split(key, " ")
		cmd := findCommand(root, path)
		if cmd == nil {
			continue
		}
		cmd.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name == "help" {
				return
			}
			if !flags[flag.Name] {
				t.Errorf("%q registers --%s but no action catalog binding for that command lists it", strings.Join(path, " "), flag.Name)
			}
		})
	}
}

func TestMutatingCLICommandFlagsAreCataloged(t *testing.T) {
	for _, spec := range []struct {
		path []string
		flag string
	}{
		{path: []string{"dots", "remove"}, flag: "--keep-local"},
		{path: []string{"dots", "remove"}, flag: "--purge"},
		{path: []string{"tools", "remove"}, flag: "--purge"},
		{path: []string{"dots", "disable"}, flag: "--overwrite"},
		{path: []string{"dots", "disable"}, flag: "--remove-local"},
		{path: []string{"dots", "ignore"}, flag: "--entry"},
		{path: []string{"dots", "ignore"}, flag: "--path"},
		{path: []string{"dots", "resolve"}, flag: "--use-repo"},
		{path: []string{"dots", "resolve"}, flag: "--use-local"},
		{path: []string{"tools", "normalize"}, flag: "--default-overrides"},
	} {
		if !catalogHasCLICommandFlag(spec.path, spec.flag) {
			t.Fatalf("mutating CLI flag %q on %q is missing from action catalog", spec.flag, strings.Join(spec.path, " "))
		}
	}
}

func TestCanonicalDestructiveCLICommands(t *testing.T) {
	for _, path := range [][]string{
		{"tools", "remove"},
		{"tools", "delete-spec"},
		{"hosts", "remove"},
		{"dots", "remove"},
		{"groups", "delete"},
	} {
		if findCommand(NewRootCmd(), path) == nil {
			t.Fatalf("missing canonical destructive command %q", strings.Join(path, " "))
		}
	}
	for _, path := range [][]string{
		{"delete"},
		{"uninstall"},
	} {
		if findCommand(NewRootCmd(), path) != nil {
			t.Fatalf("legacy destructive command %q should not be registered", strings.Join(path, " "))
		}
	}
}

// Every renamed verb keeps its old spelling registered, hidden, and reaching the canonical RunE.
func TestDeprecatedAliasesStayRunnable(t *testing.T) {
	root := NewRootCmd()
	for _, spec := range []struct {
		old   []string
		flags []string
	}{
		{old: []string{"agents", "restore"}, flags: []string{"dry-run"}},
		{old: []string{"tools", "delete"}, flags: []string{"provider", "purge"}},
		{old: []string{"dots", "delete"}, flags: []string{"keep-local", "purge"}},
	} {
		cmd := findCommand(root, spec.old)
		if cmd == nil {
			t.Fatalf("deprecated spelling %q must stay registered", strings.Join(spec.old, " "))
		}
		if !cmd.Hidden {
			t.Errorf("deprecated spelling %q should be hidden from help", strings.Join(spec.old, " "))
		}
		if cmd.Annotations[annotationDeprecatedAlias] == "" {
			t.Errorf("deprecated spelling %q is missing its alias annotation", strings.Join(spec.old, " "))
		}
		if cmd.RunE == nil {
			t.Errorf("deprecated spelling %q is not runnable", strings.Join(spec.old, " "))
		}
		for _, flagName := range spec.flags {
			if !commandHasFlag(cmd, flagName) {
				t.Errorf("deprecated spelling %q lost flag --%s", strings.Join(spec.old, " "), flagName)
			}
		}
	}
}

func TestLegacyGroupAssignmentSurfacesAreRemoved(t *testing.T) {
	root := NewRootCmd()
	if findCommand(root, []string{"groups", "add-tool"}) != nil {
		t.Fatal("legacy groups add-tool command should not be registered")
	}
	dotsGroups := findCommand(root, []string{"dots", "groups"})
	if dotsGroups == nil {
		t.Fatal("missing dots groups command")
	}
	for _, flagName := range []string{"add", "set"} {
		if dotsGroups.Flags().Lookup(flagName) != nil {
			t.Fatalf("legacy dots groups --%s flag should not be registered", flagName)
		}
	}
}

func TestConfirmableToolActionsHaveYesBypass(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentFlags().Lookup("yes") == nil {
		t.Fatal("root command is missing global --yes confirmation bypass")
	}
	for _, action := range actions.All() {
		if action.RequiresConfirm && len(action.CLI) == 0 && action.TUI == nil {
			t.Fatalf("%s requires confirmation but has no runnable binding", action.ID)
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

func catalogCommandFlags() map[string]map[string]bool {
	flagsByCommand := map[string]map[string]bool{}
	for _, action := range actions.All() {
		for _, binding := range action.CLI {
			key := commandKey(binding.Command)
			flags := flagsByCommand[key]
			if flags == nil {
				flags = map[string]bool{}
				flagsByCommand[key] = flags
			}
			for _, flagName := range binding.Flags {
				flags[strings.TrimPrefix(flagName, "--")] = true
			}
		}
	}
	return flagsByCommand
}

func catalogHasCLICommandFlag(path []string, flag string) bool {
	flag = strings.TrimPrefix(flag, "--")
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
			if !matches {
				continue
			}
			for _, got := range binding.Flags {
				if strings.TrimPrefix(got, "--") == flag {
					return true
				}
			}
		}
	}
	return false
}

func discoverRunnableCLICommands(root *cobra.Command) [][]string {
	var out [][]string
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		if len(path) > 0 && (cmd.Run != nil || cmd.RunE != nil) && !isDeprecatedAlias(cmd) {
			out = append(out, append([]string(nil), path...))
		}
		for _, child := range cmd.Commands() {
			walk(child, append(path, child.Name()))
		}
	}
	walk(root, nil)
	return out
}

func uncatalogedRunnableCLICommandAllowed(path []string) bool {
	switch commandKey(path) {
	case "ui",
		"trace list",
		"tools list",
		"tools search",
		"tools providers",
		"groups",
		"hosts",
		"settings",
		"dots",
		"dots variant",
		"dots reminder",
		"dots watch",
		"dots services",
		"hosts list",
		"settings show",
		"settings get",
		"settings lint",
		"dots list",
		"dots status",
		"dots variant list",
		"agents skills sync",
		"agents skills remove",
		"agents skills group",
		"agents add",
		"agents find",
		"agents mcp list",
		"agents mcp add",
		"agents mcp remove",
		"agents mcp sync",
		"agents mcp import",
		"agents mcp group",
		"agents plugins list",
		"agents plugins add",
		"agents plugins remove",
		"agents plugins sync",
		"agents plugins import",
		"agents plugins group",
		"agents plugins marketplace list",
		"agents plugins marketplace add",
		"agents plugins marketplace remove",
		"agents plugins marketplace group":
		return true
	default:
		return false
	}
}

// The canonical verb carries the catalog binding, so alias paths are skipped by coverage discovery.
func isDeprecatedAlias(cmd *cobra.Command) bool {
	return cmd.Annotations[annotationDeprecatedAlias] != ""
}

func commandHasFlag(cmd *cobra.Command, flagName string) bool {
	flagName = strings.TrimPrefix(flagName, "--")
	return cmd.Flags().Lookup(flagName) != nil ||
		cmd.PersistentFlags().Lookup(flagName) != nil ||
		cmd.InheritedFlags().Lookup(flagName) != nil
}

func commandHasShorthand(cmd *cobra.Command, shorthand string) bool {
	return cmd.Flags().ShorthandLookup(shorthand) != nil ||
		cmd.PersistentFlags().ShorthandLookup(shorthand) != nil ||
		cmd.InheritedFlags().ShorthandLookup(shorthand) != nil
}

func commandForExample(root *cobra.Command, tokens []string) (*cobra.Command, []string) {
	if len(tokens) == 0 || tokens[0] != "omni" {
		return nil, nil
	}
	cmd := root
	path := []string{}
	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		if flagName, _, ok := exampleFlagToken(token); ok {
			if flagName != "" {
				flag := cmd.Flags().Lookup(flagName)
				if flag == nil {
					flag = cmd.PersistentFlags().Lookup(flagName)
				}
				if flag == nil {
					flag = cmd.InheritedFlags().Lookup(flagName)
				}
				if flag != nil && flag.NoOptDefVal == "" && !strings.Contains(token, "=") && i+1 < len(tokens) {
					i++
				}
			}
			continue
		}
		var next *cobra.Command
		for _, child := range cmd.Commands() {
			if child.Name() == token {
				next = child
				break
			}
		}
		if next == nil {
			break
		}
		cmd = next
		path = append(path, token)
	}
	return cmd, path
}

func exampleFlagToken(token string) (flagName, shorthand string, ok bool) {
	switch {
	case strings.HasPrefix(token, "--"):
		name := strings.TrimPrefix(token, "--")
		if beforeValue, _, hasValue := strings.Cut(name, "="); hasValue {
			name = beforeValue
		}
		return name, "", name != ""
	case strings.HasPrefix(token, "-") && len(token) == 2:
		return "", strings.TrimPrefix(token, "-"), true
	default:
		return "", "", false
	}
}

func discoverMutatingCLICommands(root *cobra.Command) [][]string {
	var out [][]string
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		if len(path) > 0 && (cmd.Run != nil || cmd.RunE != nil) && isMutatingCLICommand(path) && !isDeprecatedAlias(cmd) {
			out = append(out, append([]string(nil), path...))
		}
		for _, child := range cmd.Commands() {
			walk(child, append(path, child.Name()))
		}
	}
	walk(root, nil)
	return out
}

func isMutatingCLICommand(path []string) bool {
	if len(path) == 0 {
		return false
	}
	if len(path) == 1 {
		switch path[0] {
		case "reconcile", "bootstrap":
			return true
		default:
			return false
		}
	}
	switch path[0] {
	case "tools":
		return len(path) == 2 && oneOf(path[1], "sync", "add", "install", "remove", "group", "delete-spec", "upgrade", "reinstall", "import", "refresh", "consolidate", "set", "ignore", "unignore", "normalize")
	case "groups":
		return len(path) == 2 && oneOf(path[1], "create", "rename", "delete", "move-tool", "remove-tool", "ignore-tool", "unignore-tool")
	case "hosts":
		return len(path) == 2 && oneOf(path[1], "ensure", "set-groups", "add-group", "remove-group", "copy", "remove")
	case "settings":
		return len(path) == 2 && oneOf(path[1], "set", "disable-provider", "enable-provider", "reset", "reset-cache")
	case "dots":
		return isMutatingDotsCLICommand(path)
	default:
		return false
	}
}

func isMutatingDotsCLICommand(path []string) bool {
	if len(path) == 2 {
		return oneOf(path[1], "sync", "add", "groups", "remove", "resolve", "ignore", "unignore", "enable", "disable", "pull", "commit", "push")
	}
	if len(path) != 3 {
		return false
	}
	switch path[1] {
	case "variant":
		return oneOf(path[2], "add", "remove")
	case "reminder":
		return oneOf(path[2], "install", "uninstall")
	case "watch":
		return oneOf(path[2], "run", "install", "uninstall")
	default:
		return false
	}
}

func oneOf(got string, wants ...string) bool {
	for _, want := range wants {
		if got == want {
			return true
		}
	}
	return false
}

func commandKey(path []string) string {
	return strings.Join(path, " ")
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
