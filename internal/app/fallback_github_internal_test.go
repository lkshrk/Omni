package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

func TestResolveGitHubFallback_GeneratedRecipeUsesNativePipeline(t *testing.T) {
	t.Parallel()
	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	assetName := fmt.Sprintf("tool_1.0_%s_%s.tar.gz", osName, archName)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":1,"tag_name":"v1.0.0","published_at":"2026-07-20T00:00:00Z","assets":[{"id":2,"name":%q,"browser_download_url":"https://example.test/tool.tar.gz"}]}`, assetName)
	}))
	t.Cleanup(srv.Close)

	a := &App{}
	a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())
	fallback, resolved, err := a.resolveGitHubFallback(t.Context(), "tool", "owner", "repo")
	if err != nil {
		t.Fatalf("resolveGitHubFallback: %v", err)
	}
	if !resolved {
		t.Fatal("resolveGitHubFallback resolved = false, want true")
	}
	if !isNativeGitHubRecipe(&fallback) {
		t.Fatalf("generated fallback routed to shell commands: %+v", fallback.Commands)
	}
}

func TestIsNativeGitHubRecipe_PreservesCustomCommandOverrides(t *testing.T) {
	t.Parallel()
	const downloadURL = "https://example.test/tool.tar.gz"
	generated, err := githubReleaseAssetInstallCommand(downloadURL)
	if err != nil {
		t.Fatalf("githubReleaseAssetInstallCommand: %v", err)
	}
	base := config.FallbackSpec{
		Recipe: config.FallbackRecipe{
			Type:             config.FallbackRecipeGitHubReleaseAsset,
			AssetDownloadURL: downloadURL,
		},
		Commands: config.FallbackCommands{Install: generated, Upgrade: generated},
	}
	if !isNativeGitHubRecipe(&base) {
		t.Fatal("generated fallback must be recognized as a native recipe")
	}

	customInstall := base
	customInstall.Commands.Install = "custom install"
	if !isNativeGitHubRecipe(&customInstall) {
		t.Fatal("custom install command must not hide native recipe shape")
	}

	customUpgrade := base
	customUpgrade.Commands.Upgrade = "custom upgrade"
	if !isNativeGitHubRecipe(&customUpgrade) {
		t.Fatal("custom upgrade command must not hide native recipe shape")
	}
}

func TestRunFallbackInstall_CustomCommandsAreActionScoped(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"install", "upgrade"} {
		action := action
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			asset := []byte("native binary")
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(asset)
			}))
			t.Cleanup(srv.Close)

			dir := t.TempDir()
			a := &App{ConfigPath: filepath.Join(dir, "settings.json"), CacheDir: dir}
			a.SetGitHubFallbackAPIForTest(srv.URL, srv.Client())
			a.SetFallbackExecutor(executor.NewMatchMock().WithFallback(executor.MockCall{Err: fmt.Errorf("shell path invoked")}))
			downloadURL := srv.URL + "/tool"
			generated, err := githubReleaseAssetInstallCommand(downloadURL)
			if err != nil {
				t.Fatalf("githubReleaseAssetInstallCommand: %v", err)
			}
			fallback := &config.FallbackSpec{
				Binary: "tool",
				Recipe: config.FallbackRecipe{
					Type:             config.FallbackRecipeGitHubReleaseAsset,
					AssetName:        "tool",
					AssetDownloadURL: downloadURL,
				},
				Commands: config.FallbackCommands{Install: generated, Upgrade: generated},
			}
			command := fallback.Commands.Install
			if action == "install" {
				fallback.Commands.Upgrade = "custom upgrade"
			} else {
				fallback.Commands.Install = "custom install"
				command = fallback.Commands.Upgrade
			}

			if err := a.runFallbackInstall(t.Context(), "tool", action, config.ToolSpec{}, fallback, command); err != nil {
				t.Fatalf("runFallbackInstall: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "fallback", "bin", "tool"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(asset) {
				t.Fatalf("installed content = %q, want %q", got, asset)
			}
		})
	}
}

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
	got, err := githubReleaseAssetInstallCommand("https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_macOS_arm64.zip")
	if err != nil {
		t.Fatalf("githubReleaseAssetInstallCommand: %v", err)
	}
	if !strings.Contains(got, `asset={{cache_dir}}/'gh_2.93.0_macOS_arm64.zip'`) {
		t.Fatalf("install command = %q, want bare cache_dir placeholder followed by quoted asset name", got)
	}
	if !strings.Contains(got, `curl -fsSL --proto-redir =https 'https://github.com/cli/cli/releases/download/v2.93.0/gh_2.93.0_macOS_arm64.zip'`) {
		t.Fatalf("install command = %q, want a downgrade-refusing curl and a single-quoted download URL", got)
	}

	fallback, err := githubReleaseAssetInstallCommand("")
	if err != nil {
		t.Fatalf("githubReleaseAssetInstallCommand(\"\"): %v", err)
	}
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
		{in: "it's", want: `'it'\''s'`},
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
	t.Parallel()

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
	quotedPoison := shellSingleQuote(poison)
	if !strings.Contains(rendered, quotedPoison) {
		t.Fatalf("rendered command does not contain properly quoted binary name:\n  rendered = %q\n  want to contain %q", rendered, quotedPoison)
	}
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

func TestScoreGitHubAsset_OSAliases(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

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

func TestScoreGitHubAsset_ArchAliases386(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		wantPos bool
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

func TestScoreGitHubAsset_ArchivePriority(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	tarGz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tar.gz", goos, goarch))
	tgz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tgz", goos, goarch))
	zip := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.zip", goos, goarch))
	tarXz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tar.xz", goos, goarch))
	tarBz2 := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.tar.bz2", goos, goarch))
	gz := scoreGitHubAsset(fmt.Sprintf("tool_1.0_%s_%s.gz", goos, goarch))

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
	if tarBz2 <= 0 || gz <= 0 {
		t.Errorf("supported scores: tar.bz2=%d gz=%d, want both >0", tarBz2, gz)
	}
}

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

func TestScoreGitHubAsset_SBOMIgnored(t *testing.T) {
	t.Parallel()
	if !githubReleaseAssetIgnored("tool_linux_amd64.sbom") {
		t.Error("githubReleaseAssetIgnored: .sbom should be ignored")
	}
	if !githubReleaseAssetIgnored("tool_linux_amd64.tar.gz.sig") {
		t.Error("githubReleaseAssetIgnored: .tar.gz.sig should be ignored")
	}
}

func TestBestGitHubReleaseAsset_Scoring(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

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
	if exactName != aliasName && got.Name != exactName {
		t.Errorf("bestGitHubReleaseAsset picked %q, want exact %q", got.Name, exactName)
	}
}

func TestBestGitHubReleaseAsset_TieIsDeterministic(t *testing.T) {
	t.Parallel()
	goos := runtime.GOOS
	goarch := runtime.GOARCH

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
