package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type SkillEntryStatus struct {
	Agent  string
	Path   string
	Skill  string
	State  string
	Detail string
	Hint   string
}

type SkillLockAttribution struct {
	Skill     string
	Ref       string
	UpdatedAt string
}

type SkillPackageStatus struct {
	Source            string
	Skill             string
	Ref               string
	Managed           bool
	Groups            []string
	Agents            []string
	Targets           []string
	Selectors         []string
	Skills            []string
	PackageDir        string
	ContentHash       string
	Installed         bool
	ShadowedByPlugin  bool
	Outdated          SkillOutdated
	OutdatedCheckedAt time.Time
	Updated           string
	Lockfile          []SkillLockAttribution
	Entries           []SkillEntryStatus
	Warnings          []string
	Hints             []string
}

// SkillPackageStatus — input may carry an @skill selector, which narrows the entry table to that skill.
func (a *App) SkillPackageStatus(ctx context.Context, input string) (SkillPackageStatus, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return SkillPackageStatus{}, err
	}
	if err := a.requireSkillsEnabled(cfg); err != nil {
		return SkillPackageStatus{}, err
	}
	parsed, err := a.parseSkillPackage(strings.TrimSpace(input))
	if err != nil {
		return SkillPackageStatus{}, err
	}
	skill := ""
	if len(parsed.Skills) > 0 {
		skill = parsed.Skills[0]
	}
	service, err := a.skillService()
	if err != nil {
		return SkillPackageStatus{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return SkillPackageStatus{}, fmt.Errorf("resolving home dir: %w", err)
	}

	// The store keys packages by the manifest's spelling; the normalized identity only matches typed input.
	pkg, managed := a.resolvedSkillPackage(cfg, parsed.Source)
	source := parsed.Source
	if managed {
		source = pkg.Source
	}
	out := SkillPackageStatus{Source: source, Skill: skill, Managed: managed}
	if managed {
		out.Ref = pkg.Ref
		out.Groups = pkg.Groups
		out.Agents = pkg.Agents
		out.Selectors = pkg.Skills
		out.Targets = a.restoreTargetsFor(cfg, pkg)
	} else {
		out.Targets = a.EnabledAgentIDs(cfg)
	}

	packageDir, contentHash, err := service.PackageStore(source)
	if err != nil {
		return SkillPackageStatus{}, err
	}
	out.PackageDir = packageDir
	out.ContentHash = contentHash

	inventory, err := service.Inventory(ctx, source, out.Targets)
	if err != nil {
		return SkillPackageStatus{}, err
	}
	out.Skills = inventory.Skills
	out.Installed = inventory.Installed
	out.Updated = inventory.Updated
	out.Outdated = skillOutdatedFrom(inventory.Outdated)
	out.OutdatedCheckedAt = inventory.OutdatedCheckedAt
	if skill != "" && len(inventory.Skills) > 0 && !slices.Contains(inventory.Skills, skill) {
		return SkillPackageStatus{}, fmt.Errorf("skill %q is not part of package %s", skill, source)
	}
	for _, id := range inventory.UnknownTargets {
		out.Warnings = append(out.Warnings, fmt.Sprintf("unknown agent target %q in this package's agents list", id))
	}

	pluginNames, warnings := installedPluginNames(ctx, a)
	out.Warnings = append(out.Warnings, warnings...)
	repoName := skillPackageRepoName(source)
	for _, id := range out.Targets {
		if pluginShadowsName(pluginNames, id, repoName) {
			out.ShadowedByPlugin = true
			break
		}
	}

	out.Lockfile = a.skillLockAttribution(home, source)

	var names []string
	if skill != "" {
		names = []string{skill}
	}
	states, err := service.EntryStates(source, out.Targets, names)
	if err != nil {
		return SkillPackageStatus{}, err
	}
	out.Entries = skillEntryStatuses(states)
	out.Hints = skillStatusHints(out)
	return out, nil
}

func (a *App) skillLockAttribution(home, source string) []SkillLockAttribution {
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return nil
	}
	identity := a.skillSourceIdentity(source)
	var out []SkillLockAttribution
	for name, entry := range lock.Skills {
		if entry.Source == "" || a.skillSourceIdentity(entry.Source) != identity {
			continue
		}
		out = append(out, SkillLockAttribution{Skill: name, Ref: entry.Ref, UpdatedAt: entry.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Skill < out[j].Skill })
	return out
}

// Each line carries its own remedy, the way the drift report does.
func skillEntryStatuses(states []agent.SkillEntryState) []SkillEntryStatus {
	out := make([]SkillEntryStatus, 0, len(states))
	for _, state := range states {
		entry := SkillEntryStatus{
			Agent: state.Target,
			Path:  state.Dir + string(os.PathSeparator) + state.Skill,
			Skill: state.Skill,
			State: string(state.Kind),
		}
		switch state.Kind {
		case agent.SkillEntryOwnedLink:
			entry.Detail = "omni's link -> " + state.Link
		case agent.SkillEntryCopy:
			entry.Detail = "byte-identical copy, not a link"
			entry.Hint = `"omni agents skills sync" converges it to a managed link`
		case agent.SkillEntryDrifted:
			entry.Detail = "content differs from the package"
			entry.Hint = "resolve with " + skillDriftRemedy
		case agent.SkillEntryForeign:
			entry.Detail = "symlink outside omni's store -> " + state.Link
			entry.Hint = "resolve with " + skillDriftRemedy
		default:
			entry.Detail = "no entry at this path"
			entry.Hint = `"omni agents skills sync" installs it`
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		if out[i].Skill != out[j].Skill {
			return out[i].Skill < out[j].Skill
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func skillStatusHints(status SkillPackageStatus) []string {
	var hints []string
	if !status.Managed {
		if len(status.Lockfile) > 0 {
			hints = append(hints, fmt.Sprintf(
				"not in the manifest; \"omni agents skills import %s\" claims it", status.Source))
		} else {
			hints = append(hints, "not in this host's manifest and not in the legacy lockfile")
		}
	}
	switch status.Outdated {
	case SkillOutdatedBehind:
		hints = append(hints, `behind its source; "omni agents skills upgrade" refreshes it`)
	case SkillOutdatedUnknown:
		hints = append(hints, `update state unknown; "omni agents skills upgrade --check" probes the source`)
	}
	if status.ShadowedByPlugin {
		hints = append(hints, "an installed plugin of the same name already provides these skills")
	}
	return hints
}
