package app

import (
	"testing"
)

func TestFallbackTagOutdated(t *testing.T) {
	const newerPublished = "2026-05-27T17:47:41Z"
	const olderPublished = "2026-05-01T00:00:00Z"

	tests := []struct {
		name              string
		latestTag         string
		installedVersion  string
		latestPublishedAt string
		savedPublishedAt  string
		want              bool
	}{
		// Version comparison path: newer tag → outdated.
		{
			name:      "version_newer",
			latestTag: "v2.93.0", installedVersion: "2.92.0",
			latestPublishedAt: olderPublished, savedPublishedAt: newerPublished,
			want: true,
		},
		// Version comparison path: same version → not outdated.
		{
			name:      "version_same",
			latestTag: "v2.93.0", installedVersion: "2.93.0",
			latestPublishedAt: newerPublished, savedPublishedAt: olderPublished,
			want: false,
		},
		// Version comparison path: latest older → not outdated.
		{
			name:      "version_older",
			latestTag: "v2.92.0", installedVersion: "2.93.0",
			latestPublishedAt: newerPublished, savedPublishedAt: olderPublished,
			want: false,
		},
		// No installed version: falls back to published_at; latest is newer.
		{
			name:      "no_installed_version_publishedat_newer",
			latestTag: "v2.93.0", installedVersion: "",
			latestPublishedAt: newerPublished, savedPublishedAt: olderPublished,
			want: true,
		},
		// No installed version: falls back to published_at; not newer.
		{
			name:      "no_installed_version_publishedat_same",
			latestTag: "v2.93.0", installedVersion: "",
			latestPublishedAt: olderPublished, savedPublishedAt: olderPublished,
			want: false,
		},
		// Same non-semver tag: identical strings → not outdated (fast-path).
		{
			name:      "non_semver_same_tag_not_outdated",
			latestTag: "nightly", installedVersion: "nightly",
			latestPublishedAt: newerPublished, savedPublishedAt: olderPublished,
			want: false,
		},
		// Non-semver tags differ and are not parseable → falls back to published_at.
		{
			name:      "non_semver_different_tags_falls_back_to_publishedat",
			latestTag: "nightly-2", installedVersion: "nightly",
			latestPublishedAt: newerPublished, savedPublishedAt: olderPublished,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fallbackTagOutdated(tt.latestTag, tt.installedVersion, tt.latestPublishedAt, tt.savedPublishedAt)
			if got != tt.want {
				t.Errorf("fallbackTagOutdated(%q, %q, %q, %q) = %v, want %v",
					tt.latestTag, tt.installedVersion, tt.latestPublishedAt, tt.savedPublishedAt, got, tt.want)
			}
		})
	}
}
