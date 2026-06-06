package app

import (
	"context"
	"fmt"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Import ───────────────────────────────────────────────────────────────────

// ImportOptions controls Import behaviour.
type ImportOptions struct {
	DryRun                 bool     // collect what would be added without writing the config
	Provider               string   // restrict to one provider; empty = all
	Group                  string   // destination group name; empty = current machine group
	SkipEcosystemProviders []string // ecosystem provider names whose tools are excluded (e.g. ["python"])
}

// ImportResult summarises what happened during an import.
type ImportResult struct {
	Added   []ImportedTool
	Skipped []ImportedTool // already present in config
}

type ImportedTool struct {
	Name     string
	Provider string
	Version  string
}

// Import discovers installed tools and adds them to the config.
// Tools already declared in any group are skipped.
func (a *App) Import(ctx context.Context, opts ImportOptions) (*ImportResult, error) {
	// When doing a full import (no explicit provider filter), build a reverse map
	// from concrete provider name → ecosystem provider name so that tools found
	// by e.g. "brew" are written into config as "system" when system→brew is
	// the delegation.
	resolvedEcosystems := a.ResolvedEcosystemProviders(ctx)
	var revEcosystem map[string]string
	if opts.Provider == "" {
		revEcosystem = make(map[string]string)
		for eco, concrete := range resolvedEcosystems {
			revEcosystem[concrete] = eco
		}
	}

	skipEcosystem := make(map[string]bool, len(opts.SkipEcosystemProviders))
	for _, s := range opts.SkipEcosystemProviders {
		skipEcosystem[s] = true
	}

	destGroup := opts.Group
	if destGroup == "" {
		destGroup = currentMachineGroupName()
	}

	// Collect CLI-tool sets from providers that can distinguish CLI tools from
	// library packages (e.g. pip). Non-CLI packages get Ignore:true so normal
	// tool processing can skip them. Errors are non-fatal: fall back to treating
	// all as CLI.
	cliSets := make(map[string]map[string]bool)
	for _, prov := range a.registry.All() {
		if cp, ok := prov.(provider.CLIToolProvider); ok {
			if set, err := cp.CLIToolSet(ctx); err == nil {
				cliSets[prov.Name()] = set
			}
		}
	}

	result := &ImportResult{}
	err := a.withConfig(func(cfg *config.RootConfig) error {
		// Index all existing entries across groups by both Name and EffectivePackage
		// so that tap-qualified brew entries (package = "tap/tool") are matched when
		// ListInstalled returns just the plain tool name.
		configured := make(map[string]struct{})
		configuredNames := make(map[string]struct{})
		configuredTools, _ := a.resolvedToolEntries(ctx, cfg, cfg.Groups)
		for _, t := range configuredTools {
			configured[t.Provider+"\x00"+t.Name] = struct{}{}
			if ep := t.EffectivePackage(); ep != t.Name {
				configured[t.Provider+"\x00"+ep] = struct{}{}
			}
			configuredNames[t.Name] = struct{}{}
		}
		for name := range ignoredToolSet(cfg) {
			configuredNames[name] = struct{}{}
		}

		if err := a.forEachAvailable(ctx, func(p provider.Provider) error {
			if opts.Provider == "" && a.registry.ImportSkipsProvider(p.Name()) {
				return nil
			}
			if opts.Provider != "" && p.Name() != opts.Provider {
				return nil
			}
			installed, err := p.ListInstalled(ctx)
			if err != nil {
				return fmt.Errorf("listing %s: %w", p.Name(), err)
			}
			metadata := map[string]provider.InstalledMetadata{}
			if mbc, ok := p.(provider.MetadataBulkChecker); ok {
				if m, metadataErr := mbc.InstalledMetadataMap(ctx); metadataErr == nil {
					metadata = m
				}
			}
			for _, t := range installed {
				configProvider := a.searchResultConfigProvider(p.Name())
				if revEcosystem != nil {
					if eco, ok := revEcosystem[p.Name()]; ok {
						configProvider = eco
					}
				}
				installWith := configInstallWithForConcreteProvider(configProvider, p.Name(), resolvedEcosystems)
				if skipEcosystem[configProvider] {
					continue
				}

				key := configProvider + "\x00" + t.Name
				if _, exists := configured[key]; exists {
					result.Skipped = append(result.Skipped, ImportedTool{Name: t.Name, Provider: configProvider, Version: t.Version})
					continue
				}
				if _, exists := configuredNames[t.Name]; exists {
					result.Skipped = append(result.Skipped, ImportedTool{Name: t.Name, Provider: configProvider, Version: t.Version})
					continue
				}
				result.Added = append(result.Added, ImportedTool{Name: t.Name, Provider: configProvider, Version: t.Version})
				if !opts.DryRun {
					gc, err := ensureImportDestinationGroup(cfg, destGroup)
					if err != nil {
						return err
					}
					if cfg.Tools == nil {
						cfg.Tools = make(map[string]config.ToolSpec)
					}
					spec := cfg.Tools[t.Name]
					spec.Provider = configProvider
					spec.InstallWith = installWith
					metadataEntry := config.ToolEntry{Name: t.Name, Provider: configProvider, Package: t.Package}
					if entry, ok := provider.LookupInstalledMetadata(metadata, toolEntryLookupKeys(metadataEntry)); ok {
						if git := gitURLFromSourceMetadata(entry.Source); git != "" {
							spec.Git = git
						}
					}
					if cliSet, hasCLISet := cliSets[p.Name()]; hasCLISet && !cliSet[t.Name] {
						spec.Ignore = true
					}
					cfg.Tools[t.Name] = spec
					if !containsToolMembership(gc.Tools, t.Name) {
						gc.Tools = append(gc.Tools, config.ToolEntry{Name: t.Name})
					}
					configured[key] = struct{}{}
					configuredNames[t.Name] = struct{}{}
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if opts.DryRun || len(result.Added) == 0 {
			return errSkipSave
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func ensureImportDestinationGroup(cfg *config.RootConfig, groupName string) (*config.GroupConfig, error) {
	return ensureDestinationGroupInConfig(cfg, groupName)
}
