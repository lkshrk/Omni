package version_test

import (
	"testing"

	_ "github.com/lkshrk/omni/internal/testguard"
	"github.com/lkshrk/omni/internal/version"
)

// Pin extraction to real first-line banners from every script-installed tool.
func TestExtract_RealToolBanners(t *testing.T) {
	for _, tc := range []struct {
		tool string
		out  string
		want string
	}{
		{"claude", "2.1.220 (Claude Code)", "2.1.220"},
		{"uv", "uv 0.11.29 (x86_64-unknown-linux-gnu)", "0.11.29"},
		{"bun", "1.3.14", "1.3.14"},
		{"coder", "Coder v2.35.2+5c2838a Tue Jul 14 07:10:48 UTC 2026", "2.35.2"},
		{"cargo", "cargo 1.97.1 (c980f4866 2026-06-30)", "1.97.1"},
		{"task", "3.52.0", "3.52.0"},
		{"rtk", "rtk 0.43.0", "0.43.0"},
		{"just", "just 1.57.0", "1.57.0"},
		{"lazydocker", "Version: 0.25.2\nDate: 2026-04-19T02:51:21Z\nBuildSource: binaryRelease", "0.25.2"},
		{"golangci-lint", "golangci-lint has version 2.12.2 built with go1.26.2 from c0d3ddc9 on 2026-05-06T11:07:58Z", "2.12.2"},
		{"deepwiki-rs", "Litho (deepwiki-rs) 1.5.1", "1.5.1"},
		{"direnv", "2.37.1", "2.37.1"},
		{"gomplate", "gomplate version 5.2.0", "5.2.0"},
		{"yq", "yq (https://github.com/mikefarah/yq/) version v4.53.3", "4.53.3"},
		{"nvim", "NVIM v0.12.4\n\nBuild type: Release", "0.12.4"},
		{"gopls", "flag provided but not defined: -version\nUsage: gopls [flags] [command]", ""},
	} {
		if got := version.Extract(tc.out); got != tc.want {
			t.Errorf("%s: Extract(%q) = %q, want %q", tc.tool, tc.out, got, tc.want)
		}
	}
}

func TestExtract_RejectsWhatCannotBeOrdered(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want string
	}{
		{"bare integer is not a version", "built 2026", ""},
		{"no digits at all", "command not found", ""},
		{"empty", "", ""},
		{"only the first line is considered", "no version here\nactually 1.2.3", ""},
		{"five parts truncate to the four Newer accepts", "tool 1.2.3.4.5", "1.2.3.4"},
		{"prerelease suffix is dropped", "tool v1.2.3-rc1", "1.2.3"},
	} {
		if got := version.Extract(tc.out); got != tc.want {
			t.Errorf("%s: Extract(%q) = %q, want %q", tc.name, tc.out, got, tc.want)
		}
	}
}

// Dates must not outrank real versions or Newer can falsely report the tool up to date.
func TestExtract_PrefersTheMostVersionShapedToken(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		want string
	}{
		{"copyright date precedes a v-prefixed version", "Copyright (c) 2019.02.01 Acme; mytool v3.4.5", "3.4.5"},
		{"build stamp precedes a v-prefixed version", "tool build 20240115.2 (v1.0.0)", "1.0.0"},
		{"build stamp precedes a bare version", "built 2019.02.01 — mytool 3.4.5", "3.4.5"},
		{"a calendar version alone is still the version", "tool 2023.1.2", "2023.1.2"},
		{"three parts outrank two", "tool 1.2 (release 3.4.5)", "3.4.5"},
		{"a calendar version outranks a later build number", "mytool 2024.1.0 (build 3.2)", "2024.1.0"},
		{"a calendar version outranks a later revision", "myide version 2024.1.0, revision 7.1", "2024.1.0"},
	} {
		if got := version.Extract(tc.out); got != tc.want {
			t.Errorf("%s: Extract(%q) = %q, want %q", tc.name, tc.out, got, tc.want)
		}
	}
}

// Known limitation, pinned deliberately rather than fixed. A two-component calendar version loses to
// a three-component build number beside it, because it cannot earn the third-component point that
// would offset the leading-year penalty. Closing this needs a rule separating a leading 4-digit
// version component from a leading 4-digit date component, and no such rule exists: CalVer defines
// YYYY.MM, YYYY.M and YYYY.0M.0D, so "2019.02.01" and "2024.1" are both well-formed calendar
// versions and both well-formed dates. Every candidate discriminator misclassifies one standard
// CalVer family, which trades this rare wrong pick for a commoner one. Scoring here is a tiebreaker,
// not a classifier, and adding rules cannot make it one.
func TestExtract_TwoPartCalendarVersionLosesToABuildNumber(t *testing.T) {
	const banner = "mytool 2024.1 (build 3.2.1)"
	if got := version.Extract(banner); got != "3.2.1" {
		t.Fatalf("Extract(%q) = %q; behaviour changed — if this is now %q the limitation is fixed and this test should be replaced", banner, got, "2024.1")
	}
}

// Extraction that Newer cannot then order would be worse than no extraction at all.
func TestExtract_ResultIsAlwaysComparable(t *testing.T) {
	for _, out := range []string{
		"Coder v2.35.2+5c2838a Tue Jul 14 07:10:48 UTC 2026",
		"golangci-lint has version 2.12.2 built with go1.26.2",
		"yq (https://github.com/mikefarah/yq/) version v4.53.3",
		"tool v1.2.3-rc1",
	} {
		got := version.Extract(out)
		if got == "" {
			t.Fatalf("Extract(%q) = %q, want a version", out, got)
		}
		if _, ok := version.Newer("999.0.0", got); !ok {
			t.Errorf("Extract(%q) = %q, which Newer cannot order", out, got)
		}
	}
}
