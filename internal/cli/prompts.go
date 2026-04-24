package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// stdinScanner is the shared line-oriented reader for all interactive prompts.
// A single Scanner is used across the entire cli package so that buffering is
// consistent — creating multiple Scanners on os.Stdin causes them to fight over
// the same underlying stream.
var stdinScanner = bufio.NewScanner(os.Stdin)

var stdinIsTerminal = defaultStdinIsTerminal

// scanLine reads one line from the shared stdin scanner.
// Returns ("", false) on EOF or scan error. Scanner errors (e.g. line longer
// than bufio.MaxScanTokenSize) are reported to stderr so they are not silent;
// callers still see ok=false and substitute their default value.
func scanLine() (string, bool) {
	if !stdinScanner.Scan() {
		if err := stdinScanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "stdin read error: %v\n", err)
		}
		return "", false
	}
	return stdinScanner.Text(), true
}

func defaultStdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func promptText(question, defaultVal string) (string, bool) {
	if defaultVal != "" {
		fmt.Printf("%s [%s] ", question, defaultVal)
	} else {
		fmt.Printf("%s ", question)
	}
	line, ok := scanLine()
	if !ok {
		return "", false
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return defaultVal, true
	}
	return answer, true
}

// promptYesNo prints question to stdout and reads a yes/no answer from stdin.
// Returns defaultVal when the user presses enter without typing anything.
// When state.yes is set the prompt is skipped and answered "yes".
func promptYesNo(state *rootState, question string, defaultVal bool) bool {
	if state != nil && state.yes {
		return true
	}
	hint := "[y/N]"
	if defaultVal {
		hint = "[Y/n]"
	}
	fmt.Printf("%s %s ", question, hint)
	line, ok := scanLine()
	if !ok {
		return defaultVal
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	if answer == "" {
		return defaultVal
	}
	return answer == "y" || answer == "yes"
}

// promptSatisfiedGroups checks result.SatisfiedGroups and for each one
// asks the user whether to add it to result.ActiveProfile.
// Calls addGroupFn when the user answers yes. Silently skips when no profile
// is active or no satisfied groups were found.
func promptSatisfiedGroups(state *rootState, activeProfile string, satisfiedGroups []string, addGroupFn func(group string) error) {
	if activeProfile == "" || len(satisfiedGroups) == 0 {
		return
	}
	for _, g := range satisfiedGroups {
		q := fmt.Sprintf("Group %q is fully installed on this machine. Add it to profile %q?", g, activeProfile)
		if promptYesNo(state, q, false) {
			if err := addGroupFn(g); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not add group %q to profile: %v\n", g, err)
			} else {
				fmt.Printf("Added group %q to profile %q.\n", g, activeProfile)
			}
		}
	}
}
