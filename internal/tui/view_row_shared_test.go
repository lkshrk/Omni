package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/config"
)

// testHostInfoForHost builds a *app.HostInfo whose active host is host,
// assigned the given reusable groups. currentMachineGroupName() must resolve
// to host for the active-host filter to admit host itself — callers set
// OMNI_HOSTNAME accordingly.
func testHostInfoForHost(host string, reusableGroups ...string) *app.HostInfo {
	return &app.HostInfo{
		Active: host,
		Hosts: map[string]config.HostAssignment{
			host: {Groups: reusableGroups},
		},
	}
}

func TestRenderGroupPills_HostFirstAndCollapse(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop")

	p := defaultPalette()
	info := testHostInfoForHost("laptop", "work", "base")
	groups := []string{"work", "laptop", "base"}

	wide := stripANSIEscapeSequences(renderGroupPills(p, groups, info, 80, nil))
	if !strings.HasPrefix(wide, "[laptop]") {
		t.Fatalf("host pill must come first: %q", wide)
	}
	if !strings.Contains(wide, "[work]") || !strings.Contains(wide, "[base]") {
		t.Fatalf("reusable pills missing: %q", wide)
	}

	tight := stripANSIEscapeSequences(renderGroupPills(p, groups, info, 12, nil))
	if !strings.Contains(tight, "+") || !strings.HasPrefix(tight, "[laptop") {
		t.Fatalf("tight should collapse to host + count: %q", tight)
	}
}

// TestRenderGroupPills_HostPillStyledDistinctly guards the spec requirement
// (documented in renderGroupPills's doc comment) that the host group pill
// is rendered in "a visually distinct style" from reusable-group pills. It
// deliberately does not strip ANSI: it locates the escape sequence
// immediately preceding each pill's bracketed text in the raw, styled
// output and asserts the host pill's escape code differs from the reusable
// pill's escape code.
func TestRenderGroupPills_HostPillStyledDistinctly(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop")

	p := defaultPalette()
	info := testHostInfoForHost("laptop", "work")
	groups := []string{"work", "laptop"}

	out := renderGroupPills(p, groups, info, 80, nil)

	hostEscape := ansiEscapeBefore(t, out, "[laptop]")
	reusableEscape := ansiEscapeBefore(t, out, "[work]")

	if hostEscape == "" || reusableEscape == "" {
		t.Fatalf("expected both pills to carry an ANSI style prefix, got host=%q reusable=%q in %q", hostEscape, reusableEscape, out)
	}
	if hostEscape == reusableEscape {
		t.Fatalf("host pill style must differ from reusable pill style, both got %q in %q", hostEscape, out)
	}
}

// ansiEscapeBefore returns the ANSI escape sequence (e.g. "\x1b[38;5;2m")
// that immediately precedes the given bracketed substring in s.
func ansiEscapeBefore(t *testing.T, s, bracketed string) string {
	t.Helper()
	re := regexp.MustCompile(`(\x1b\[[0-9;]*m)+` + regexp.QuoteMeta(bracketed))
	loc := re.FindStringIndex(s)
	if loc == nil {
		t.Fatalf("could not find styled %q in %q", bracketed, s)
	}
	return s[loc[0] : loc[1]-len(bracketed)]
}

func TestRenderGroupPills_Empty(t *testing.T) {
	p := defaultPalette()
	if got := renderGroupPills(p, nil, nil, 80, nil); got != "" {
		t.Fatalf("renderGroupPills(nil) = %q, want empty", got)
	}
}

// TestRenderGroupPills_EmphasisApplied guards that a row's emphasis transform
// (e.g. bold when selected, muted when ignored) reaches every pill — the
// regression fixed after the multi-group pill rollout, where selected/ignored
// rows lost their group-pill emphasis.
func TestRenderGroupPills_EmphasisApplied(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop")
	p := defaultPalette()
	info := testHostInfoForHost("laptop", "work")
	groups := []string{"work", "laptop"}

	plain := renderGroupPills(p, groups, info, 80, nil)
	bold := renderGroupPills(p, groups, info, 80, func(s lipgloss.Style) lipgloss.Style {
		return s.Bold(true)
	})
	if bold == plain {
		t.Fatalf("bold emphasis should change pill styling, got identical output %q", plain)
	}
	// lipgloss merges the bold attribute (SGR 1) into each pill's combined
	// escape sequence, e.g. "\x1b[1;38;2;...m"; assert the bold parameter reached
	// the host pill rather than expecting a standalone "\x1b[1m".
	if !regexp.MustCompile(`\x1b\[1;[0-9;]*m\[laptop\]`).MatchString(bold) {
		t.Fatalf("bold emphasis should apply the bold SGR to the pill, got %q", bold)
	}
}
