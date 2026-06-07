package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type gitMetadataProviderStub struct {
	name     string
	metadata map[string]provider.InstalledMetadata
}

func (s *gitMetadataProviderStub) Name() string { return s.name }

func (s *gitMetadataProviderStub) Description() string { return s.name }

func (s *gitMetadataProviderStub) Available(context.Context) (bool, error) { return true, nil }

func (s *gitMetadataProviderStub) Install(context.Context, provider.Tool) error { return nil }

func (s *gitMetadataProviderStub) Uninstall(context.Context, provider.Tool) error { return nil }

func (s *gitMetadataProviderStub) Upgrade(context.Context, provider.Tool) error { return nil }

func (s *gitMetadataProviderStub) IsInstalled(context.Context, provider.Tool) (bool, string, error) {
	return false, "", nil
}

func (s *gitMetadataProviderStub) ListInstalled(context.Context) ([]provider.InstalledTool, error) {
	return nil, nil
}

func (s *gitMetadataProviderStub) InstalledMetadataMap(context.Context) (map[string]provider.InstalledMetadata, error) {
	return s.metadata, nil
}

func newGitMetadataTestApp(t *testing.T, cfgPath string, providers ...provider.Provider) *App {
	t.Helper()
	a := New(cfgPath)
	if err := a.InitTestMode(context.Background(), providers...); err != nil {
		t.Fatalf("InitTestMode: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func saveGitMetadataToolConfig(t *testing.T, cfgPath string) {
	t.Helper()
	if err := config.Save(cfgPath, &config.RootConfig{
		Tools: map[string]config.ToolSpec{
			"ripgrep": {Providers: []config.ToolInstallSpec{{Provider: "brew"}}},
		},
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
}

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

func TestEnrichToolGitFromInstalledProviderMetadataUsesConcreteOwner(t *testing.T) {
	ctx := context.Background()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	brew := &gitMetadataProviderStub{name: "brew"}
	apt := &gitMetadataProviderStub{
		name: "apt",
		metadata: map[string]provider.InstalledMetadata{
			"ripgrep": {
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
				},
			},
		},
	}
	a := newGitMetadataTestApp(t, cfgPath, brew, apt)
	saveGitMetadataToolConfig(t, cfgPath)

	err := a.enrichToolGitFromInstalledProviderMetadata(ctx, config.ToolEntry{Name: "ripgrep", Provider: "brew"}, brew, "apt")
	if err != nil {
		t.Fatalf("enrichToolGitFromInstalledProviderMetadata: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if git := got.Tools["ripgrep"].Git; git != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("git = %q, want concrete owner source", git)
	}
}

func TestEnrichToolGitFromInstalledProviderMetadataUsesOperationProviderWhenOwnerSame(t *testing.T) {
	ctx := context.Background()
	cfgPath := filepath.Join(t.TempDir(), "settings.json")
	brew := &gitMetadataProviderStub{
		name: "brew",
		metadata: map[string]provider.InstalledMetadata{
			"ripgrep": {
				Source: provider.SourceMetadata{
					Type:  provider.SourceTypeGitHub,
					Owner: "BurntSushi",
					Repo:  "ripgrep",
				},
			},
		},
	}
	apt := &gitMetadataProviderStub{name: "apt"}
	a := newGitMetadataTestApp(t, cfgPath, brew, apt)
	saveGitMetadataToolConfig(t, cfgPath)

	err := a.enrichToolGitFromInstalledProviderMetadata(ctx, config.ToolEntry{Name: "ripgrep", Provider: "brew"}, brew, "brew")
	if err != nil {
		t.Fatalf("enrichToolGitFromInstalledProviderMetadata: %v", err)
	}

	got, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if git := got.Tools["ripgrep"].Git; git != "https://github.com/BurntSushi/ripgrep" {
		t.Fatalf("git = %q, want operation provider source", git)
	}
}
