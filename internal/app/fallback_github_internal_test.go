package app

import (
	"fmt"
	"testing"
)

func TestBestGitHubReleaseAsset_PrefersExtractableArchive(t *testing.T) {
	osName := githubOSNames()[0]
	archName := githubArchNames()[0]
	asset, ok := bestGitHubReleaseAsset([]githubAsset{
		{Name: fmt.Sprintf("gh_2.93.0_%s_%s.deb", osName, archName), BrowserDownloadURL: "https://example.test/gh.deb"},
		{Name: fmt.Sprintf("gh_2.93.0_%s_%s.rpm", osName, archName), BrowserDownloadURL: "https://example.test/gh.rpm"},
		{Name: fmt.Sprintf("gh_2.93.0_%s_%s.tar.gz", osName, archName), BrowserDownloadURL: "https://example.test/gh.tar.gz"},
	}, "gh")
	if !ok {
		t.Fatal("bestGitHubReleaseAsset returned no match")
	}
	want := fmt.Sprintf("gh_2.93.0_%s_%s.tar.gz", osName, archName)
	if asset.Name != want {
		t.Fatalf("asset = %q, want extractable tar.gz", asset.Name)
	}
}
