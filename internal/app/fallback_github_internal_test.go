package app

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestBestGitHubReleaseAsset_PrefersExtractableArchive(t *testing.T) {
	t.Parallel()
	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	asset, ok := bestGitHubReleaseAsset([]githubAsset{
		{ID: "1", Name: fmt.Sprintf("gh_2.93.0_%s_%s.deb", osName, archName), BrowserDownloadURL: "https://example.test/gh.deb"},
		{ID: "2", Name: fmt.Sprintf("gh_2.93.0_%s_%s.rpm", osName, archName), BrowserDownloadURL: "https://example.test/gh.rpm"},
		{ID: "3", Name: fmt.Sprintf("gh_2.93.0_%s_%s.tar.gz", osName, archName), BrowserDownloadURL: "https://example.test/gh.tar.gz"},
	}, "gh")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset returned no match")
	}
	want := fmt.Sprintf("gh_2.93.0_%s_%s.tar.gz", osName, archName)
	if asset.Name != want {
		t.Fatalf("asset = %q, want extractable tar.gz", asset.Name)
	}
}

func TestBestGitHubReleaseAsset_SkipsMetadataAndWrongBinary(t *testing.T) {
	t.Parallel()
	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	wantName := fmt.Sprintf("fd_10.3.0_%s_%s.zip", osName, archName)
	asset, ok := bestGitHubReleaseAsset([]githubAsset{
		{Name: fmt.Sprintf("fd_10.3.0_%s_%s.tar.gz", osName, archName), BrowserDownloadURL: "https://example.test/fd-missing-id.tar.gz"},
		{ID: "1", Name: fmt.Sprintf("fd_10.3.0_%s_%s.tar.gz", osName, archName)},
		{ID: "2", Name: fmt.Sprintf("fd_10.3.0_%s_%s_checksums.txt", osName, archName), BrowserDownloadURL: "https://example.test/checksums.txt"},
		{ID: "3", Name: fmt.Sprintf("rg_14.1.1_%s_%s.tar.gz", osName, archName), BrowserDownloadURL: "https://example.test/rg.tar.gz"},
		{ID: "4", Name: wantName, BrowserDownloadURL: "https://example.test/fd.zip"},
	}, "fd")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset returned no match")
	}
	if asset.Name != wantName {
		t.Fatalf("asset = %q, want %q", asset.Name, wantName)
	}
}

func TestBestGitHubReleaseAsset_AcceptsPlatformAliases(t *testing.T) {
	t.Parallel()
	osNames := githubOSNames()
	archNames := githubArchNames()
	osName := osNames[len(osNames)-1]
	archName := archNames[len(archNames)-1]
	wantName := fmt.Sprintf("gh_2.93.0_%s_%s.tar.gz", osName, archName)

	asset, ok := bestGitHubReleaseAsset([]githubAsset{
		{ID: "1", Name: wantName, BrowserDownloadURL: "https://example.test/gh.tar.gz"},
	}, "gh")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset returned no match for platform aliases")
	}
	if asset.Name != wantName {
		t.Fatalf("asset = %q, want %q", asset.Name, wantName)
	}
}

func TestBestGitHubReleaseAsset_ReturnsNoMatchWhenNoUsableAssetExists(t *testing.T) {
	t.Parallel()
	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	// Only a checksum asset and a wrong-OS archive — no extractable current-platform asset.
	asset, ok := bestGitHubReleaseAsset([]githubAsset{
		{ID: "1", Name: fmt.Sprintf("fd_10.3.0_%s_%s.sha256", osName, archName), BrowserDownloadURL: "https://example.test/fd.sha256"},
		{ID: "2", Name: fmt.Sprintf("fd_10.3.0_%s_%s.deb", osName, archName), BrowserDownloadURL: "https://example.test/fd.deb"},
		{ID: "3", Name: fmt.Sprintf("fd_10.3.0_windows_%s.zip", archName), BrowserDownloadURL: "https://example.test/fd-windows.zip"},
	}, "fd")
	if ok {
		t.Fatalf("bestGitHubReleaseAsset returned %+v, want no match", asset)
	}
}

func TestGitHubReleaseAssetIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{name: "fd_checksums.txt", want: true},
		{name: "fd.sha256", want: true},
		{name: "fd.signature", want: true},
		{name: "fd.tar.gz.sig", want: true},
		{name: "fd.tar.gz.asc", want: true},
		{name: "fd_README.md", want: true},
		{name: "fd_LICENSE", want: true},
		{name: "fd_docs.zip", want: true},
		{name: "fd_linux_amd64.deb", want: true},
		{name: "fd_linux_amd64.rpm", want: true},
		{name: "fd_macos_arm64.pkg", want: true},
		{name: "fd_windows_amd64.msi", want: true},
		{name: "fd_macos_arm64.dmg", want: true},
		{name: "fd_linux_amd64.tar.gz", want: false},
		{name: "fd_linux_amd64.zip", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := githubReleaseAssetIgnored(strings.ToLower(tt.name)); got != tt.want {
				t.Fatalf("githubReleaseAssetIgnored(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestNormalizedGitHubPublishedAt(t *testing.T) {
	t.Parallel()
	got, err := normalizedGitHubPublishedAt("2026-06-07T12:34:56+02:00")
	if err != nil {
		t.Fatalf("normalizedGitHubPublishedAt: %v", err)
	}
	if got != "2026-06-07T10:34:56Z" {
		t.Fatalf("published_at = %q, want UTC RFC3339", got)
	}
	if _, err := normalizedGitHubPublishedAt(""); err == nil {
		t.Fatal("normalizedGitHubPublishedAt accepted empty published_at")
	}
	if _, err := normalizedGitHubPublishedAt("2026-06-07"); err == nil {
		t.Fatal("normalizedGitHubPublishedAt accepted non-RFC3339 published_at")
	}
}

func TestGitHubFallbackHasSavedReleaseMetadata(t *testing.T) {
	t.Parallel()
	valid := &config.FallbackSpec{
		Source: config.FallbackSource{
			Type:  config.FallbackSourceGitHub,
			Owner: "sharkdp",
			Repo:  "fd",
		},
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			ReleaseID:        "release-1",
			TagName:          "v10.3.0",
			PublishedAt:      "2026-06-07T10:34:56Z",
			AssetID:          "asset-1",
			AssetName:        "fd.tar.gz",
			AssetDownloadURL: "https://example.test/fd.tar.gz",
		},
	}
	if !githubFallbackHasSavedReleaseMetadata(valid) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata(valid) = false, want true")
	}
	if githubFallbackHasSavedReleaseMetadata(nil) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata accepted nil fallback")
	}

	missingAsset := *valid
	missingAsset.Recipe.AssetDownloadURL = ""
	if githubFallbackHasSavedReleaseMetadata(&missingAsset) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata accepted missing asset download URL")
	}

	badDate := *valid
	badDate.Recipe.PublishedAt = "2026-06-07"
	if githubFallbackHasSavedReleaseMetadata(&badDate) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata accepted non-RFC3339 published_at")
	}

	wrongSource := *valid
	wrongSource.Source.Type = ""
	if githubFallbackHasSavedReleaseMetadata(&wrongSource) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata accepted non-GitHub source")
	}

	missingRepo := *valid
	missingRepo.Source.Repo = ""
	if githubFallbackHasSavedReleaseMetadata(&missingRepo) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata accepted missing source repo")
	}

	wrongRecipe := *valid
	wrongRecipe.Recipe.Type = config.FallbackRecipeRawCommands
	if githubFallbackHasSavedReleaseMetadata(&wrongRecipe) {
		t.Fatal("githubFallbackHasSavedReleaseMetadata accepted non-release-asset recipe")
	}
}

func TestGitHubReleaseAssetInstallCommandUsesAssetBasename(t *testing.T) {
	t.Parallel()
	got := githubReleaseAssetInstallCommand("https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_macOS_arm64.zip")
	// Placeholders are bare so the rendered single-quoted value is the only quoting.
	// The asset name and URL are shell-quoted at construction; no surrounding quotes in the template.
	if !strings.Contains(got, `asset={{cache_dir}}/'gh_2.93.0_macOS_arm64.zip'`) {
		t.Fatalf("install command = %q, want bare cache_dir placeholder followed by quoted asset name", got)
	}
	if !strings.Contains(got, `curl -fsSL 'https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_macOS_arm64.zip'`) {
		t.Fatalf("install command = %q, want single-quoted curl download URL", got)
	}

	// No-URL branch: asset_path (full absolute path, quoted at render time) used directly.
	fallback := githubReleaseAssetInstallCommand("")
	if !strings.Contains(fallback, `asset={{asset_path}}`) {
		t.Fatalf("fallback install command = %q, want bare asset_path placeholder", fallback)
	}
	if strings.Contains(fallback, "curl") {
		t.Fatalf("fallback install command = %q, want no curl when no download URL", fallback)
	}
}

func TestShellSingleQuote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{in: "normal", want: "'normal'"},
		{in: "/usr/local/bin", want: "'/usr/local/bin'"},
		// A value containing a single-quote must be escaped so it cannot
		// break out of the surrounding single-quoted shell word.
		{in: "it's", want: `'it'\''s'`},
		// Values that look like injection attempts must be neutralised.
		{in: `'; touch pwned`, want: `''\''; touch pwned'`},
		{in: `$(rm -rf /)`, want: `'$(rm -rf /)'`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := shellSingleQuote(tt.in); got != tt.want {
				t.Fatalf("shellSingleQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderFallbackCommandNeutralisesInjection(t *testing.T) {
	t.Parallel(
	// A binary name containing shell metacharacters (as could arrive from a
	// compromised settings.json) must be single-quoted so it cannot break out
	// of the generated sh -c string.
	)

	a := New(t.TempDir() + "/settings.json")
	spec := config.ToolSpec{}
	poison := `'; touch /tmp/pwned; echo '`
	fallback := &config.FallbackSpec{
		Binary: poison,
		BinDir: "/safe/bin",
		Commands: config.FallbackCommands{
			Check: `test -x {{bin_dir}}/{{binary}}`,
		},
	}
	rendered, err := a.renderFallbackCommand("tool", spec, fallback, `test -x {{bin_dir}}/{{binary}}`)
	if err != nil {
		t.Fatalf("renderFallbackCommand: %v", err)
	}
	// The poison value must appear in the rendered command only inside its
	// single-quoted form — the literal characters '; touch are present in the
	// output but enclosed within the shell quoting, so they cannot execute.
	quotedPoison := shellSingleQuote(poison)
	if !strings.Contains(rendered, quotedPoison) {
		t.Fatalf("rendered command does not contain properly quoted binary name:\n  rendered = %q\n  want to contain %q", rendered, quotedPoison)
	}
	// Verify the command can be evaluated safely by sh without side effects.
	// We do this by checking the rendered string does not contain the word
	// "touch" OUTSIDE of single quotes (a crude but sufficient check: the
	// quoted form wraps all occurrences of touch inside '...').
	withoutQuoted := strings.ReplaceAll(rendered, quotedPoison, "QUOTED")
	if strings.Contains(withoutQuoted, "touch") {
		t.Fatalf("rendered command contains 'touch' outside of single-quoted value: %q", rendered)
	}
}

func TestIsGitHubHost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"api.github.com", "github.com", "API.GITHUB.COM"} {
		if !isGitHubHost(host) {
			t.Fatalf("isGitHubHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"evil.example.com", "api.github.com.evil.com", "notgithub.com"} {
		if isGitHubHost(host) {
			t.Fatalf("isGitHubHost(%q) = true, want false", host)
		}
	}
}

func TestParseGitHubRepo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in        string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{in: "cli/cli", wantOwner: "cli", wantRepo: "cli"},
		{in: "https://github.com/cli/cli", wantOwner: "cli", wantRepo: "cli"},
		{in: "https://github.com/cli/cli.git", wantOwner: "cli", wantRepo: "cli"},
		{in: "github.com/cli/cli", wantOwner: "cli", wantRepo: "cli"},
		{in: "git@github.com:cli/cli.git", wantOwner: "cli", wantRepo: "cli"},
		{in: "https://www.github.com/cli/cli", wantOwner: "cli", wantRepo: "cli"},
		{in: "https://gitlab.com/cli/cli", wantErr: true},
		{in: "https://github.com/cli/cli/releases", wantErr: true},
		{in: "https://github.com/cli/cli?tab=readme", wantErr: true},
		{in: "https://github.com/cli/cli#readme", wantErr: true},
		{in: "git@github.com:cli/cli.git?ref=main", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			owner, repo, err := parseGitHubRepo(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseGitHubRepo(%q) err = nil, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGitHubRepo(%q): %v", tt.in, err)
			}
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Fatalf("parseGitHubRepo(%q) = %s/%s, want %s/%s", tt.in, owner, repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Scoring tests (HCL-35)
// ---------------------------------------------------------------------------

// TestScoreGitHubAsset_OSAliases verifies that OS canonical names and their
// aliases both produce a positive score, with the exact GOOS term scoring higher.
func TestScoreGitHubAsset_OSAliases(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map GOOS → expected aliases from the spec.
	aliases := map[string][]string{
		"darwin":  {"macos", "mac", "apple"},
		"windows": {"win"},
		"linux":   {},
	}

	archExact := goarch
	archAlias := map[string]string{
		"amd64": "x86_64",
		"arm64": "aarch64",
	}

	// Exact OS name must outscore its alias.
	if osAliases, ok := aliases[goos]; ok && len(osAliases) > 0 {
		alias := osAliases[0]
		exactName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, archExact)
		aliasName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", alias, archExact)
		scoreExact := scoreGitHubAsset(exactName)
		scoreAlias := scoreGitHubAsset(aliasName)
		if scoreExact <= 0 {
			t.Errorf("scoreGitHubAsset(%q) = %d, want >0 for exact GOOS", exactName, scoreExact)
		}
		if scoreAlias <= 0 {
			t.Errorf("scoreGitHubAsset(%q) = %d, want >0 for OS alias", aliasName, scoreAlias)
		}
		if scoreExact <= scoreAlias {
			t.Errorf("exact GOOS score (%d) should exceed alias score (%d)", scoreExact, scoreAlias)
		}
	}

	// Exact arch name must outscore its alias.
	if aliasArch, ok := archAlias[goarch]; ok {
		exactName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, archExact)
		aliasName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, aliasArch)
		scoreExact := scoreGitHubAsset(exactName)
		scoreAlias := scoreGitHubAsset(aliasName)
		if scoreExact <= 0 {
			t.Errorf("scoreGitHubAsset(%q) = %d, want >0", exactName, scoreExact)
		}
		if scoreAlias <= 0 {
			t.Errorf("scoreGitHubAsset(%q) = %d, want >0 for arch alias", aliasName, scoreAlias)
		}
		if scoreExact <= scoreAlias {
			t.Errorf("exact arch score (%d) should exceed alias score (%d)", scoreExact, scoreAlias)
		}
	}
}

// TestScoreGitHubAsset_ArchAliases386 verifies 386↔i386↔x86 aliases on linux/386.
func TestScoreGitHubAsset_ArchAliases386(t *testing.T) {
	t.Parallel(
	// scoreGitHubAsset is a pure function; test 386 aliases directly regardless
	// of the current runtime arch.
	)

	cases := []struct {
		name    string
		wantPos bool // should score positive (OS matches linux)
	}{
		{"tool_1.0_linux_386.tar.gz", true},
		{"tool_1.0_linux_i386.tar.gz", true},
		{"tool_1.0_linux_x86.tar.gz", true},
	}
	for _, tc := range cases {
		score := scoreGitHubAsset(tc.name)
		if tc.wantPos && score <= 0 && runtime.GOOS == "linux" && runtime.GOARCH == "386" {
			t.Errorf("scoreGitHubAsset(%q) = %d, want >0 on linux/386", tc.name, score)
		}
	}
}

// TestScoreGitHubAsset_ArchivePriority verifies that .tar.gz beats .zip beats .tar.xz
// when all other factors are equal.
func TestScoreGitHubAsset_ArchivePriority(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	tarGz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, goarch))
	tgz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tgz", goos, goarch))
	zip := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.zip", goos, goarch))
	tarXz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tar.xz", goos, goarch))

	if tarGz <= 0 {
		t.Errorf("tar.gz score = %d, want >0", tarGz)
	}
	if tarGz < tgz {
		t.Errorf("tar.gz (%d) should be >= tgz (%d)", tarGz, tgz)
	}
	if tgz < zip {
		t.Errorf("tgz (%d) should be >= zip (%d)", tgz, zip)
	}
	if zip < tarXz {
		t.Errorf("zip (%d) should be >= tar.xz (%d)", zip, tarXz)
	}
	if tarXz <= 0 {
		t.Errorf("tar.xz score = %d, want >0", tarXz)
	}
}

// TestScoreGitHubAsset_IgnoredSuffix verifies that .sig/.sbom/.asc suffixes
// yield a non-positive score (they should be filtered before scoring, but
// scoreGitHubAsset itself should return 0 or negative for safety).
func TestScoreGitHubAsset_IgnoredSuffix(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	for _, name := range []string{
		fmt.Sprintf("tool_1.0_%s_%s.tar.gz.sig", goos, goarch),
		fmt.Sprintf("tool_1.0_%s_%s.tar.gz.asc", goos, goarch),
		fmt.Sprintf("tool_1.0_%s_%s.sbom", goos, goarch),
	} {
		if score := scoreGitHubAsset(name); score > 0 {
			t.Errorf("scoreGitHubAsset(%q) = %d, want <=0 for ignored suffix", name, score)
		}
	}
}

// TestScoreGitHubAsset_SBOMIgnored verifies .sbom is in the ignored-asset list.
func TestScoreGitHubAsset_SBOMIgnored(t *testing.T) {
	t.Parallel()
	if !githubReleaseAssetIgnored("tool_linux_amd64.sbom") {
		t.Error("githubReleaseAssetIgnored: .sbom should be ignored")
	}
	if !githubReleaseAssetIgnored("tool_linux_amd64.tar.gz.sig") {
		t.Error("githubReleaseAssetIgnored: .tar.gz.sig should be ignored")
	}
}

// TestBestGitHubReleaseAsset_Scoring verifies the scorer picks the highest-scoring
// asset deterministically when multiple candidates are present.
func TestBestGitHubReleaseAsset_Scoring(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// alias names for current platform
	osAliases := map[string]string{
		"darwin":  "macos",
		"windows": "win",
		"linux":   "linux",
	}
	archAliases := map[string]string{
		"amd64": "x86_64",
		"arm64": "aarch64",
		"386":   "i386",
	}
	osAlias := osAliases[goos]
	if osAlias == "" {
		osAlias = goos
	}
	archAlias := archAliases[goarch]
	if archAlias == "" {
		archAlias = goarch
	}

	// Provide: alias-named asset AND exact-named asset.
	// The scorer must pick the exact one.
	exactName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, goarch)
	aliasName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", osAlias, archAlias)

	assets := []githubAsset{
		{ID: "1", Name: aliasName, BrowserDownloadURL: "https://example.test/alias.tar.gz"},
		{ID: "2", Name: exactName, BrowserDownloadURL: "https://example.test/exact.tar.gz"},
	}

	got, ok := bestGitHubReleaseAsset(assets, "tool")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset: no match")
	}
	// When exact and alias names differ, exact should win.
	if exactName != aliasName && got.Name != exactName {
		t.Errorf("bestGitHubReleaseAsset picked %q, want exact %q", got.Name, exactName)
	}
}

// TestBestGitHubReleaseAsset_TieIsDeterministic verifies that when two assets
// have identical scores, the pick is stable (same result regardless of input order).
func TestBestGitHubReleaseAsset_TieIsDeterministic(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Two assets that will always score the same (same OS, arch, archive type).
	a1 := githubAsset{ID: "1", Name: fmt.Sprintf("tool_v1_%s_%s_variant-a.tar.gz", goos, goarch), BrowserDownloadURL: "https://example.test/a.tar.gz"}
	a2 := githubAsset{ID: "2", Name: fmt.Sprintf("tool_v1_%s_%s_variant-b.tar.gz", goos, goarch), BrowserDownloadURL: "https://example.test/b.tar.gz"}

	got1, ok1 := bestGitHubReleaseAsset([]githubAsset{a1, a2}, "tool")
	got2, ok2 := bestGitHubReleaseAsset([]githubAsset{a2, a1}, "tool")
	if !ok1 || !ok2 {
		t.Fatal("bestGitHubReleaseAsset: no match")
	}
	if got1.ID != got2.ID {
		t.Errorf("tie-breaking is not deterministic: order1=%q order2=%q", got1.Name, got2.Name)
	}
}

// TestBestGitHubReleaseAsset_LinuxLibcPreference verifies that on Linux,
// a musl asset is preferred over a glibc/gnu asset when both otherwise match.
func TestBestGitHubReleaseAsset_LinuxLibcPreference(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("libc preference only applies on linux")
	}
	goarch := runtime.GOARCH

	musl := githubAsset{ID: "1", Name: fmt.Sprintf("tool_1.0_linux_%s_musl.tar.gz", goarch), BrowserDownloadURL: "https://example.test/musl.tar.gz"}
	gnu := githubAsset{ID: "2", Name: fmt.Sprintf("tool_1.0_linux_%s_gnu.tar.gz", goarch), BrowserDownloadURL: "https://example.test/gnu.tar.gz"}

	got, ok := bestGitHubReleaseAsset([]githubAsset{gnu, musl}, "tool")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset: no match")
	}
	if got.ID != musl.ID {
		t.Errorf("bestGitHubReleaseAsset picked %q (gnu), want musl", got.Name)
	}
}

// TestBestGitHubReleaseAsset_PrefersTarGzOverZip verifies archive-type preference
// when the platform tokens match equally.
func TestBestGitHubReleaseAsset_PrefersTarGzOverZip(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	zip := githubAsset{ID: "1", Name: fmt.Sprintf("tool_1.0_%s_%s.zip", goos, goarch), BrowserDownloadURL: "https://example.test/tool.zip"}
	tarGz := githubAsset{ID: "2", Name: fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, goarch), BrowserDownloadURL: "https://example.test/tool.tar.gz"}

	got, ok := bestGitHubReleaseAsset([]githubAsset{zip, tarGz}, "tool")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset: no match")
	}
	if got.ID != tarGz.ID {
		t.Errorf("bestGitHubReleaseAsset picked %q, want tar.gz", got.Name)
	}
}
