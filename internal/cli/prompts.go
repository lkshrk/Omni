package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	textutil "github.com/lkshrk/omni/internal/text"
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
			fmt.Fprintf(stdErr(), "stdin read error: %v\n", err)
		}
		return "", false
	}
	return stdinScanner.Text(), true
}

func defaultStdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// readYesNo reads a single y/n keypress without requiring Enter.
// Returns (answer, ok). ok is false on read error or non-terminal stdin,
// in which case the caller should fall back to defaultVal.
// When stdin is not a terminal, falls back to line-based reading.
func readYesNo(defaultVal bool) (bool, bool) {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) {
		line, ok := scanLine()
		if !ok {
			return defaultVal, false
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			return defaultVal, true
		}
		return answer == "y" || answer == "yes", true
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Can't enter raw mode; fall back to line-based input.
		line, ok := scanLine()
		if !ok {
			return defaultVal, false
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			return defaultVal, true
		}
		return answer == "y" || answer == "yes", true
	}
	defer func() { _ = term.Restore(fd, oldState) }() // best-effort cleanup

	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil || n == 0 {
		return defaultVal, false
	}
	ch := buf[0]
	switch {
	case ch == 3: // Ctrl+C
		return false, false
	case ch == '\r' || ch == '\n':
		return defaultVal, true
	default:
		return ch == 'y' || ch == 'Y', true
	}
}

func promptText(question, defaultVal string) (string, bool) {
	if defaultVal != "" {
		fmt.Fprintf(stdOut(), "%s [%s] ", question, defaultVal)
	} else {
		fmt.Fprintf(stdOut(), "%s ", question)
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

// promptYesNo prints question to stdout and reads a single y/n keypress.
// Returns defaultVal when the user presses Enter without typing anything.
// When state.yes is set the prompt is skipped and answered "yes".
func promptYesNo(state *rootState, question string, defaultVal bool) bool {
	if state != nil && state.yes {
		return true
	}
	hint := "[y/N]"
	if defaultVal {
		hint = "[Y/n]"
	}
	fmt.Fprintf(stdOut(), "%s %s ", question, hint)
	yes, ok := readYesNo(defaultVal)
	if !ok {
		fmt.Fprintln(stdOut())
		return defaultVal
	}
	if yes {
		fmt.Fprintln(stdOut(), "y")
	} else {
		fmt.Fprintln(stdOut(), "n")
	}
	return yes
}

// promptReassignClaimedTools offers to move freshly claimed tools out of the
// machine hostname group. The user can move all to one group, choose per tool,
// or skip (keep in machine group).
func promptReassignClaimedTools(state *rootState, claimedNames []string) {
	if len(claimedNames) == 0 {
		return
	}
	fmt.Fprintf(stdOut(), "\n%s added to machine group. Move to a different group?\n", textutil.PluralCount(len(claimedNames), "tool", "tools"))
	fmt.Fprint(stdOut(), "  [a]ll to same group / [i]ndividual / [s]kip: ")
	line, ok := scanLine()
	if !ok {
		return
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	switch {
	case answer == "a" || answer == "all":
		group, ok := promptText("Move all to group?", "")
		if !ok || group == "" {
			return
		}
		for _, name := range claimedNames {
			if err := state.app.MoveToolToGroup(name, group); err != nil {
				fmt.Fprintf(stdErr(), "  warning: could not move %s: %v\n", name, err)
			} else {
				fmt.Fprintf(stdOut(), "  ✓ %s → %s\n", name, group)
			}
		}
	case answer == "i" || answer == "individual":
		var lastGroup string
		for _, name := range claimedNames {
			prompt := fmt.Sprintf("  %s → group?", name)
			if lastGroup != "" {
				prompt += fmt.Sprintf(" [%s]", lastGroup)
			}
			prompt += " "
			fmt.Fprint(stdOut(), prompt)
			l, ok := scanLine()
			if !ok {
				return
			}
			g := strings.TrimSpace(l)
			if g == "" {
				g = lastGroup
			}
			if g == "" {
				continue
			}
			lastGroup = g
			if err := state.app.MoveToolToGroup(name, g); err != nil {
				fmt.Fprintf(stdErr(), "  warning: could not move %s: %v\n", name, err)
			} else {
				fmt.Fprintf(stdOut(), "  ✓ %s → %s\n", name, g)
			}
		}
	}
	// "s", "skip", or anything else: keep in machine group.
}

// promptSatisfiedGroups checks result.SatisfiedGroups and for each one asks the
// user whether to assign it to the active host.
func promptSatisfiedGroups(state *rootState, activeHost string, satisfiedGroups []string, addGroupFn func(group string) error) {
	if activeHost == "" || len(satisfiedGroups) == 0 {
		return
	}
	for _, g := range satisfiedGroups {
		q := fmt.Sprintf("Group %q is fully installed on this machine. Add it to host %q?", g, activeHost)
		if promptYesNo(state, q, false) {
			if err := addGroupFn(g); err != nil {
				fmt.Fprintf(stdErr(), "warning: could not add group %q to host: %v\n", g, err)
			} else {
				fmt.Fprintf(stdOut(), "Added group %q to host %q.\n", g, activeHost)
			}
		}
	}
}
