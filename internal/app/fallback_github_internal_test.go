package app

import (
	"fmt"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestBestGitHubReleaseAsset_PrefersExtractableArchive(t *testing.T) {
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
	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	wantName := fmt.Sprintf("fd_10.3.0_%s_%s.zip", osName, archName)
	asset, ok := bestGitHubReleaseAsset([]githubAsset{
		{ID: "1", Name: fmt.Sprintf("fd_10.3.0_%s_%s_checksums.txt", osName, archName), BrowserDownloadURL: "https://example.test/checksums.txt"},
		{ID: "2", Name: fmt.Sprintf("rg_14.1.1_%s_%s.tar.gz", osName, archName), BrowserDownloadURL: "https://example.test/rg.tar.gz"},
		{ID: "3", Name: wantName, BrowserDownloadURL: "https://example.test/fd.zip"},
	}, "fd")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset returned no match")
	}
	if asset.Name != wantName {
		t.Fatalf("asset = %q, want %q", asset.Name, wantName)
	}
}

func TestNormalizedGitHubPublishedAt(t *testing.T) {
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
}

func TestParseGitHubRepo(t *testing.T) {
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
