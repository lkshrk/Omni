package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
)

func (a *App) DotsDelete(ctx context.Context, name string) error {
	return a.DotsDeleteWithOptions(ctx, name, DotsDeleteOptions{KeepLocal: true})
}

// DotsDeleteWithOptions deletes the dots entry named name from all group files
// and always removes its package from the dots repo. When KeepLocal is true,
// managed local symlinks are first replaced with real local copies.
func (a *App) DotsDeleteWithOptions(ctx context.Context, name string, opts DotsDeleteOptions) (err error) {
	repoPath := ""
	defer func() {
		a.recordDotsHistoryResult(ctx, "delete", name, repoPath, nil, err, false)
		a.refreshDotsStateAfterSuccess(ctx, &err, false)
	}()
	if err := dots.ValidateEntryName(name); err != nil {
		return fmt.Errorf("dots delete: %w", err)
	}
	var (
		stowPath       string
		gitCfg         config.DotsGitConfig
		found          bool
		deleteDot      *config.DotEntry
		deletedEntries []deletedDotEntry
	)
	err = a.withConfig(func(rootCfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(rootCfg); err != nil {
			return err
		}
		rawRepo := a.effectiveSettings(rootCfg).DotsRepo
		gitCfg = rootCfg.Settings.DotsGit

		var err error
		repoPath, err = resolveRepoPath(rawRepo)
		if err != nil {
			return err
		}
		if err := a.requireSafeTestDotsMutation(repoPath, nil); err != nil {
			return err
		}
		stowPath = dotsContentPath(repoPath)

		for _, g := range rootCfg.Groups {
			for i := 0; i < len(g.Dots); i++ {
				d := g.Dots[i]
				if d.Name != name {
					continue
				}
				found = true
				if deleteDot == nil {
					copyDot := d
					deleteDot = &copyDot
				}
				deletedEntries = append(deletedEntries, deletedDotEntry{group: g.Name, dot: d})
				g.Dots = append(g.Dots[:i], g.Dots[i+1:]...)
				i--
			}
		}
		if !found {
			return errSkipSave
		}
		if err := a.requireSafeTestDotsMutation(repoPath, []config.DotEntry{*deleteDot}); err != nil {
			return err
		}
		stowPath, err = existingDotsContentPath(repoPath)
		if err != nil {
			return fmt.Errorf("dots delete %q: content dir: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("dots entry %q not found", name)
	}

	if err := a.removeDeletedDotFiles(ctx, name, stowPath, deleteDot, opts); err != nil {
		if restoreErr := a.restoreDeletedDotConfig(deletedEntries); restoreErr != nil {
			return fmt.Errorf("%w (restore config failed: %v)", err, restoreErr)
		}
		return err
	}

	if gt := newGitForRepo(repoPath, a.newExecutor()); gt.IsRepo() {
		msg := "dots: delete " + name
		if gitCfg.AutoPush {
			if err := gt.Push(ctx, msg); err != nil {
				return fmt.Errorf("dots delete: auto-push: %w", err)
			}
		} else if gitCfg.AutoCommit {
			if err := gt.CommitAll(ctx, msg); err != nil {
				return fmt.Errorf("dots delete: auto-commit: %w", err)
			}
		}
	}
	return nil
}

type deletedDotEntry struct {
	group string
	dot   config.DotEntry
}

func (a *App) removeDeletedDotFiles(ctx context.Context, name, stowPath string, deleteDot *config.DotEntry, opts DotsDeleteOptions) error {
	unlinkDots := dotEntriesForAllPackages(*deleteDot)
	mgr, resolveErr := dots.New(stowPath, unlinkDots)
	if resolveErr != nil {
		return fmt.Errorf("dots delete %q: resolve entry: %w", name, resolveErr)
	}
	unlinkOpts := dots.UnlinkOptions{KeepExistingLocal: true, RemoveLocal: !opts.KeepLocal}
	if _, unlinkErr := mgr.UnlinkAll(unlinkOpts); unlinkErr != nil {
		return fmt.Errorf("dots delete %q: %w", name, unlinkErr)
	}
	for _, pkgName := range dotEntryPackages(*deleteDot) {
		if rmErr := os.RemoveAll(filepath.Join(stowPath, pkgName)); rmErr != nil {
			return fmt.Errorf("dots delete %q: remove repo package %q: %w", name, pkgName, rmErr)
		}
	}
	return nil
}

func (a *App) restoreDeletedDotConfig(entries []deletedDotEntry) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		for _, entry := range entries {
			group := ensureGroupInConfig(cfg, entry.group)
			exists := false
			for _, d := range group.Dots {
				if d.Name == entry.dot.Name {
					exists = true
					break
				}
			}
			if !exists {
				group.Dots = append(group.Dots, entry.dot)
			}
		}
		return nil
	})
}

func (a *App) DotMembershipMap(_ context.Context) (map[string][]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	return dotMembershipMapFromConfig(cfg), nil
}

func dotMembershipMapFromConfig(cfg *config.RootConfig) map[string][]string {
	memberships := make(map[string][]string)
	for _, group := range cfg.Groups {
		for _, entry := range group.Dots {
			if entry.Name == "" {
				continue
			}
			memberships[entry.Name] = append(memberships[entry.Name], group.BaseName())
		}
	}
	for name := range memberships {
		sort.Strings(memberships[name])
	}
	return memberships
}

// MoveDotToGroup makes groupName the dotfile entry's only owning group.
func (a *App) MoveDotToGroup(name, groupName string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("dots entry name is required")
	}
	groupName = compatibilityGroupName(groupName)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		return moveDotToGroupInConfig(cfg, name, groupName)
	})
}

func (a *App) RemoveDotFromGroup(name, groupName string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("dots entry name is required")
	}
	groupName = compatibilityGroupName(groupName)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireDotsEnabled(cfg); err != nil {
			return err
		}
		return removeDotFromGroupInConfig(cfg, name, groupName)
	})
}

func filterDotMemberships(group *config.GroupConfig, name string) bool {
	filtered := group.Dots[:0]
	changed := false
	for _, dot := range group.Dots {
		if dot.Name == name {
			changed = true
			continue
		}
		filtered = append(filtered, dot)
	}
	group.Dots = filtered
	return changed
}
