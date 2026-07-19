package provider_test

import (
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestGitHubSourceHint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   provider.SourceMetadata
	}{
		{
			name:   "https url",
			values: []string{"https://github.com/cli/cli"},
			want:   provider.SourceMetadata{Type: provider.SourceTypeGitHub, Owner: "cli", Repo: "cli", URL: "https://github.com/cli/cli"},
		},
		{
			name:   "git+ ssh scp form strips suffix",
			values: []string{"git+git@github.com:owner/repo.git"},
			want:   provider.SourceMetadata{Type: provider.SourceTypeGitHub, Owner: "owner", Repo: "repo", URL: "https://github.com/owner/repo"},
		},
		{
			name:   "www prefix",
			values: []string{"http://www.github.com/foo/bar"},
			want:   provider.SourceMetadata{Type: provider.SourceTypeGitHub, Owner: "foo", Repo: "bar", URL: "https://github.com/foo/bar"},
		},
		{
			name:   "skips blanks and non-github, returns first parseable",
			values: []string{"", "  ", "https://gitlab.com/x/y", "github.com/real/repo"},
			want:   provider.SourceMetadata{Type: provider.SourceTypeGitHub, Owner: "real", Repo: "repo", URL: "https://github.com/real/repo"},
		},
		{
			name:   "missing repo segment",
			values: []string{"https://github.com/owneronly"},
			want:   provider.SourceMetadata{},
		},
		{
			name:   "empty repo after trimming .git",
			values: []string{"github.com/owner/.git"},
			want:   provider.SourceMetadata{},
		},
		{
			name:   "no github values",
			values: []string{"npm:some-package", "https://example.com/a/b"},
			want:   provider.SourceMetadata{},
		},
		{
			name:   "no values",
			values: nil,
			want:   provider.SourceMetadata{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := provider.GitHubSourceHint(tc.values...); got != tc.want {
				t.Fatalf("GitHubSourceHint(%v) = %+v, want %+v", tc.values, got, tc.want)
			}
		})
	}
}
