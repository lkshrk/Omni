package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	textutil "github.com/lkshrk/omni/internal/text"
)

// One Scanner for the whole package: multiple Scanners on os.Stdin fight over the same buffered stream.
var stdinScanner = bufio.NewScanner(os.Stdin)

var stdinIsTerminal = defaultStdinIsTerminal

// Scan errors go to stderr rather than staying silent; callers still see ok=false and substitute their default.
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
	// term.IsTerminal, not ModeCharDevice: /dev/null is a char device but can never answer a prompt.
	return term.IsTerminal(os.Stdin.Fd())
}

// Reads a single keypress without Enter, falling back to line-based reading when stdin is not a terminal.
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

func promptReassignClaimedTools(state *rootState, claimedNames []string) {
	if len(claimedNames) == 0 {
		return
	}
	if (state != nil && state.yes) || !stdinIsTerminal() {
		// --yes runs are unattended even with a PTY attached, where a dangling prompt only reads as a hang.
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
