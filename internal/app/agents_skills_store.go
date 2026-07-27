package app

import (
	"context"
	"errors"
	"sort"

	"github.com/lkshrk/omni/internal/config"
)

type SkillStoreFixReport struct {
	Debris           []string
	DanglingLinks    []string
	OrphanedPackages []string
	RebuiltMetadata  []string
}

func (r SkillStoreFixReport) Empty() bool {
	return len(r.Debris) == 0 && len(r.DanglingLinks) == 0 &&
		len(r.OrphanedPackages) == 0 && len(r.RebuiltMetadata) == 0
}

// FixSkillStore — Convergence stays with restore; the four fixers are independent so one failure never hides the others.
func (a *App) FixSkillStore(ctx context.Context, dryRun bool) (SkillStoreFixReport, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return SkillStoreFixReport{}, err
	}
	if !a.SkillsEnabled(cfg) {
		return SkillStoreFixReport{}, nil
	}
	return a.skillStoreFix(ctx, cfg, dryRun)
}

func (a *App) skillStoreFix(ctx context.Context, cfg *config.RootConfig, dryRun bool) (SkillStoreFixReport, error) {
	service, err := a.skillService()
	if err != nil {
		return SkillStoreFixReport{}, err
	}
	var report SkillStoreFixReport
	var errs []error
	report.Debris, err = service.PruneStoreDebris(dryRun)
	errs = append(errs, err)
	report.DanglingLinks, err = service.PruneDanglingLinks(dryRun)
	errs = append(errs, err)
	report.OrphanedPackages, err = service.PruneOrphanedPackages(declaredSkillSources(cfg), dryRun)
	errs = append(errs, err)
	report.RebuiltMetadata, err = service.RebuildMetadata(ctx, cfg.Agents.Packages, dryRun)
	errs = append(errs, err)
	return report, errors.Join(errs...)
}

// ForeignSkillEntries — Visibility only: foreign entries are never touched, and their presence is not breakage.
func (a *App) ForeignSkillEntries() ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if !a.SkillsEnabled(cfg) {
		return nil, nil
	}
	service, err := a.skillService()
	if err != nil {
		return nil, err
	}
	perTarget, err := service.ForeignSkillEntries(declaredSkillPackages(cfg))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var paths []string
	for _, entries := range perTarget {
		for _, path := range entries {
			if seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// A package a group still references is declared intent, neither an orphan nor foreign.
func declaredSkillPackages(cfg *config.RootConfig) []config.SkillPackage {
	pkgs := make([]config.SkillPackage, 0, len(cfg.Agents.Packages))
	pkgs = append(pkgs, cfg.Agents.Packages...)
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		for _, source := range group.Skills {
			pkgs = append(pkgs, config.SkillPackage{Source: source})
		}
	}
	return pkgs
}

func declaredSkillSources(cfg *config.RootConfig) []string {
	pkgs := declaredSkillPackages(cfg)
	sources := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		sources = append(sources, pkg.Source)
	}
	return sources
}
