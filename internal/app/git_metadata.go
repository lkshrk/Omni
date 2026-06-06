package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type toolGitCandidate struct {
	name string
	git  string
}

func githubGitURL(owner, repo string) string {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return ""
	}
	return "https://github.com/" + owner + "/" + repo
}

func gitURLFromSourceMetadata(source provider.SourceMetadata) string {
	if strings.TrimSpace(source.Type) != provider.SourceTypeGitHub {
		return ""
	}
	if url := githubGitURL(source.Owner, source.Repo); url != "" {
		return url
	}
	return strings.TrimSpace(source.URL)
}

func gitURLFromMetadataUpdate(update database.MetadataUpdate) string {
	if strings.TrimSpace(update.SourceType) != provider.SourceTypeGitHub {
		return ""
	}
	if url := githubGitURL(update.SourceOwner, update.SourceRepo); url != "" {
		return url
	}
	return strings.TrimSpace(update.SourceURL)
}

func gitURLFromToolMetadata(meta *database.ToolMetadata) string {
	if meta == nil || strings.TrimSpace(meta.SourceType) != provider.SourceTypeGitHub {
		return ""
	}
	if url := githubGitURL(meta.SourceOwner, meta.SourceRepo); url != "" {
		return url
	}
	if meta.SourceURL.Valid {
		return strings.TrimSpace(meta.SourceURL.String)
	}
	return ""
}

func mergeToolGit(existing, candidate string) (string, bool) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return existing, false
	}
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return candidate, true
	}
	existingOwner, existingRepo, existingErr := parseGitHubRepo(existing)
	candidateOwner, candidateRepo, candidateErr := parseGitHubRepo(candidate)
	if existingErr == nil && candidateErr == nil &&
		strings.EqualFold(existingOwner, candidateOwner) &&
		strings.EqualFold(existingRepo, candidateRepo) &&
		existing != candidate {
		return candidate, true
	}
	return existing, false
}

func (a *App) enrichToolGitFromMetadataUpdates(ctx context.Context, updates []database.MetadataUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	candidates := make([]toolGitCandidate, 0, len(updates))
	for _, update := range updates {
		if git := gitURLFromMetadataUpdate(update); git != "" {
			candidates = append(candidates, toolGitCandidate{name: strings.TrimSpace(update.Name), git: git})
		}
	}
	return a.enrichToolGit(ctx, candidates)
}

func (a *App) enrichToolGitFromProviderMetadata(ctx context.Context, entry config.ToolEntry, prov provider.Provider) error {
	mbc, ok := prov.(provider.MetadataBulkChecker)
	if !ok {
		return nil
	}
	metadata, err := mbc.InstalledMetadataMap(ctx)
	if err != nil {
		return nil
	}
	source, ok := provider.LookupInstalledMetadata(metadata, toolEntryLookupKeys(entry))
	if !ok {
		return nil
	}
	git := gitURLFromSourceMetadata(source.Source)
	if git == "" {
		return nil
	}
	return a.enrichToolGit(ctx, []toolGitCandidate{{name: entry.Name, git: git}})
}

func (a *App) enrichToolGitFromInstalledProviderMetadata(ctx context.Context, entry config.ToolEntry, operationProvider provider.Provider, installedWith string) error {
	if err := a.enrichToolGitFromProviderMetadata(ctx, entry, operationProvider); err != nil {
		return err
	}
	installedWith = strings.TrimSpace(installedWith)
	if installedWith == "" || installedWith == operationProvider.Name() {
		return nil
	}
	concreteProvider, ok := a.registry.Get(installedWith)
	if !ok {
		return nil
	}
	return a.enrichToolGitFromProviderMetadata(ctx, entry, concreteProvider)
}

func (a *App) enrichToolGitFromCachedMetadata(ctx context.Context, name, providerName, pkg string) error {
	if name == "" || providerName == "" {
		return nil
	}
	if pkg == "" {
		pkg = name
	}
	meta, err := a.readDB().GetMetadata(ctx, name, providerName, pkg)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return nil
	}
	git := gitURLFromToolMetadata(meta)
	if git == "" {
		return nil
	}
	return a.enrichToolGit(ctx, []toolGitCandidate{{name: name, git: git}})
}

func (a *App) enrichToolGit(_ context.Context, candidates []toolGitCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Tools == nil {
			return errSkipSave
		}
		changed := false
		for _, candidate := range candidates {
			name := strings.TrimSpace(candidate.name)
			if name == "" {
				continue
			}
			spec, ok := cfg.Tools[name]
			if !ok {
				continue
			}
			if git, ok := mergeToolGit(spec.Git, candidate.git); ok {
				spec.Git = git
				cfg.Tools[name] = spec
				changed = true
			}
		}
		if !changed {
			return errSkipSave
		}
		return nil
	})
}
