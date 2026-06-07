package app

import (
	"database/sql"
	"testing"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

func TestGitURLFromMetadataPrefersOwnerRepo(t *testing.T) {
	t.Run("provider source", func(t *testing.T) {
		got := gitURLFromSourceMetadata(provider.SourceMetadata{
			Type:  provider.SourceTypeGitHub,
			Owner: "cli",
			Repo:  "cli",
			URL:   "https://github.com/ignored/ignored",
		})
		if got != "https://github.com/cli/cli" {
			t.Fatalf("gitURLFromSourceMetadata = %q, want owner/repo URL", got)
		}
	})

	t.Run("database metadata", func(t *testing.T) {
		got := gitURLFromToolMetadata(&database.ToolMetadata{
			SourceType:  provider.SourceTypeGitHub,
			SourceURL:   sql.NullString{String: "https://github.com/cli/cli", Valid: true},
			SourceOwner: "cli",
			SourceRepo:  "cli",
		})
		if got != "https://github.com/cli/cli" {
			t.Fatalf("gitURLFromToolMetadata = %q, want owner/repo URL", got)
		}
	})

	t.Run("metadata update falls back to source url", func(t *testing.T) {
		got := gitURLFromMetadataUpdate(database.MetadataUpdate{
			SourceType: provider.SourceTypeGitHub,
			SourceURL:  "https://github.com/cli/cli",
		})
		if got != "https://github.com/cli/cli" {
			t.Fatalf("gitURLFromMetadataUpdate = %q, want source URL", got)
		}
	})

	t.Run("database metadata falls back to source url", func(t *testing.T) {
		got := gitURLFromToolMetadata(&database.ToolMetadata{
			SourceType: provider.SourceTypeGitHub,
			SourceURL:  sql.NullString{String: "https://github.com/cli/cli", Valid: true},
		})
		if got != "https://github.com/cli/cli" {
			t.Fatalf("gitURLFromToolMetadata = %q, want source URL", got)
		}
	})

	t.Run("non github ignored", func(t *testing.T) {
		got := gitURLFromSourceMetadata(provider.SourceMetadata{
			Type: "homepage",
			URL:  "https://example.com/cli/cli",
		})
		if got != "" {
			t.Fatalf("gitURLFromSourceMetadata = %q, want empty for non-GitHub source", got)
		}
	})

	t.Run("nil database metadata ignored", func(t *testing.T) {
		if got := gitURLFromToolMetadata(nil); got != "" {
			t.Fatalf("gitURLFromToolMetadata(nil) = %q, want empty", got)
		}
	})
}

func TestMergeToolGitPreservesUserEditedDifferentRepo(t *testing.T) {
	tests := []struct {
		name        string
		existing    string
		candidate   string
		want        string
		wantChanged bool
	}{
		{
			name:        "empty existing takes candidate",
			candidate:   " https://github.com/cli/cli ",
			want:        "https://github.com/cli/cli",
			wantChanged: true,
		},
		{
			name:        "same repo canonicalizes candidate",
			existing:    "git@github.com:cli/cli.git",
			candidate:   "https://github.com/cli/cli",
			want:        "https://github.com/cli/cli",
			wantChanged: true,
		},
		{
			name:      "different existing repo preserved",
			existing:  "https://example.com/user-edited/rg",
			candidate: "https://github.com/BurntSushi/ripgrep",
			want:      "https://example.com/user-edited/rg",
		},
		{
			name:     "empty candidate ignored",
			existing: "https://github.com/cli/cli",
			want:     "https://github.com/cli/cli",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := mergeToolGit(tt.existing, tt.candidate)
			if got != tt.want || changed != tt.wantChanged {
				t.Fatalf("mergeToolGit = %q/%v, want %q/%v", got, changed, tt.want, tt.wantChanged)
			}
		})
	}
}
