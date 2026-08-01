package app

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type SkillStatus string

const (
	SkillStatusInstalled SkillStatus = "installed"
	SkillStatusMissing   SkillStatus = "missing"
	// Restore converges byte-identical copies on its own, so drift always needs a human decision.
	SkillStatusDrifted SkillStatus = "drifted"
	// Another package in Omni's store currently owns the same target path.
	SkillStatusShadowed SkillStatus = "shadowed"
)

// SkillOutdated — Unknown is honest for a source with no cheap identity (a Git subpath), unreachable, or never probed.
type SkillOutdated string

const (
	SkillOutdatedUnknown SkillOutdated = ""
	SkillOutdatedCurrent SkillOutdated = "current"
	SkillOutdatedBehind  SkillOutdated = "outdated"
)

func skillOutdatedFrom(verdict *bool) SkillOutdated {
	switch {
	case verdict == nil:
		return SkillOutdatedUnknown
	case *verdict:
		return SkillOutdatedBehind
	default:
		return SkillOutdatedCurrent
	}
}

type SkillPackageRow struct {
	Source    string
	Name      string // repo segment of Source
	Ref       string
	Groups    []string
	Agents    []string
	Skills    []string
	Updated   string // YYYY-MM-DD, latest native check time
	Installed bool
	// Recorded state only; a row build never probes, so a table redraw stays offline.
	Outdated       SkillOutdated
	PerAgentStatus map[string]SkillStatus
	Description    string // from the single skill's SKILL.md frontmatter, only when len(Skills) == 1
	// Set only for managed rows; unmanaged rows with this property are suppressed entirely.
	ShadowedByPlugin bool
	// Display paths annotate the row instead of failing, so one manifest typo cannot blank the table.
	UnknownAgents []string
	// Set when the source itself could not be read; the row carries the reason instead of the inventory.
	Error string
}

func (r SkillPackageRow) UnknownAgentsWarning() string {
	if len(r.UnknownAgents) == 0 {
		return ""
	}
	return fmt.Sprintf("%s: unknown agent target(s) %s; fix agents.packages[].agents in settings.json",
		r.Source, strings.Join(r.UnknownAgents, ", "))
}

func singleSkillDescription(home string, names []string) string {
	if len(names) != 1 {
		return ""
	}
	return skillDescription(home, names[0])
}

func (a *App) packageSkills(lock *config.SkillLockFile, source string) []string {
	identity := a.skillSourceIdentity(source)
	var names []string
	for name, e := range lock.Skills {
		if a.skillSourceIdentity(e.Source) == identity {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (a *App) packageLockStatus(lock *config.SkillLockFile, source string) (bool, string) {
	identity := a.skillSourceIdentity(source)
	installed := false
	latest := ""
	for _, e := range lock.Skills {
		if a.skillSourceIdentity(e.Source) != identity {
			continue
		}
		installed = true
		d := skillUpdatedDate(e.UpdatedAt)
		if d > latest {
			latest = d
		}
	}
	return installed, latest
}

// Full owner/repo: two repos named .../skills would collide on the repo segment alone.
func packageDisplayName(source string) string {
	return source
}

// A plugin's Name has no owner prefix, so matching must compare the bare repo segment.
func skillPackageRepoName(source string) string {
	if parsed, err := agent.ParseSkillSource(source); err == nil {
		source = parsed.Source
	}
	if i := strings.LastIndexByte(source, '/'); i >= 0 {
		source = source[i+1:]
	}
	return strings.TrimSuffix(source, ".git")
}

// Drift is only meaningful for targets the inventory did not report as installed.
func skillPerAgentStatus(inventory agent.SkillInventory) map[string]SkillStatus {
	out := make(map[string]SkillStatus, len(inventory.PerTarget))
	for id, installed := range inventory.PerTarget {
		switch {
		case installed:
			out[id] = SkillStatusInstalled
		case inventory.PerTargetDrifted[id]:
			out[id] = SkillStatusDrifted
		case inventory.PerTargetShadowed[id]:
			out[id] = SkillStatusShadowed
		default:
			out[id] = SkillStatusMissing
		}
	}
	return out
}

// SkillAgentRow — Targeted and Installed are independent: a selected agent not yet restored to is targeted but not installed.
type SkillAgentRow struct {
	ID        string
	Display   string
	Targeted  bool
	Installed bool
}

func agentHasAnySkill(home string, a AgentInfo, names []string) bool {
	return a.HasAnySkill(home, names)
}

func (a *App) SkillAgentRows(source string) ([]SkillAgentRow, error) {
	parsed, err := a.parseSkillPackage(source)
	if err != nil {
		return nil, err
	}
	source = parsed.Source
	identity := a.skillSourceIdentity(source)
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	// A manifest entry with no agents list targets every enabled agent, so those rows must show selected.
	targeted := map[string]bool{}
	for _, p := range cfg.Agents.Packages {
		if a.skillSourceIdentity(p.Source) != identity {
			continue
		}
		// The store keys packages by the manifest's spelling; the normalized identity only matches typed input.
		source = p.Source
		ids := p.Agents
		if len(ids) == 0 {
			ids = a.restoreTargetsFor(cfg, resolvedPackage{SkillPackage: p})
		}
		for _, id := range ids {
			targeted[id] = true
		}
		break
	}
	installedAgents := a.installedAgents(home)
	ids := make([]string, 0, len(installedAgents))
	for _, target := range installedAgents {
		ids = append(ids, target.ID)
	}
	service, err := a.skillService()
	if err != nil {
		return nil, err
	}
	inventory, err := service.Inventory(context.Background(), source, ids)
	if err != nil {
		return nil, err
	}
	rows := make([]SkillAgentRow, 0, len(installedAgents))
	for _, ag := range installedAgents {
		rows = append(rows, SkillAgentRow{
			ID:        ag.ID,
			Display:   ag.Display,
			Targeted:  targeted[ag.ID],
			Installed: inventory.PerTarget[ag.ID],
		})
	}
	return rows, nil
}

// skillRowCounts — Disjoint buckets: a shadowed or drifted package is never also counted as missing.
type skillRowCounts struct {
	Installed int
	Missing   []string
	Drifted   []string
	Errored   []string
	Outdated  []string
}

// Sync deliberately skips a plugin-shadowed package and leaves a drifted one alone, so counting either
// as missing would report a gap no command can close. Doctor and the dashboard share this so they cannot
// disagree on identical data.
func classifySkillRows(rows []SkillPackageRow) skillRowCounts {
	var c skillRowCounts
	for _, r := range rows {
		if r.Error != "" {
			c.Errored = append(c.Errored, r.Source)
			continue
		}
		packageShadowed := skillRowShadowed(r) && !skillRowMissing(r)
		shadowed := r.ShadowedByPlugin || packageShadowed
		outdated := !shadowed && r.Outdated == SkillOutdatedBehind
		switch {
		case r.Installed || r.ShadowedByPlugin:
			c.Installed++
		case skillRowDrifted(r):
			c.Drifted = append(c.Drifted, r.Source)
		case packageShadowed:
			c.Installed++
		default:
			c.Missing = append(c.Missing, r.Source)
			outdated = false
		}
		if outdated {
			c.Outdated = append(c.Outdated, r.Source)
		}
	}
	sort.Strings(c.Missing)
	sort.Strings(c.Drifted)
	sort.Strings(c.Errored)
	sort.Strings(c.Outdated)
	return c
}

func skillRowDrifted(r SkillPackageRow) bool {
	for _, status := range r.PerAgentStatus {
		if status == SkillStatusDrifted {
			return true
		}
	}
	return false
}

func skillRowShadowed(r SkillPackageRow) bool {
	for _, status := range r.PerAgentStatus {
		if status == SkillStatusShadowed {
			return true
		}
	}
	return false
}

func skillRowMissing(r SkillPackageRow) bool {
	for _, status := range r.PerAgentStatus {
		if status == SkillStatusMissing {
			return true
		}
	}
	return false
}

// SkillPackageRows — A manifest entry is declared intent, so a plugin-shadowed package is flagged, never
// hidden, and a source whose inventory cannot be read becomes an error row rather than blanking the table.
func (a *App) SkillPackageRows(ctx context.Context) ([]SkillPackageRow, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	service, err := a.skillService()
	if err != nil {
		return nil, err
	}
	resolved := a.resolveSkillPackages(cfg, currentMachineGroupName())
	// display-only builder: shadow warnings surface via RestoreSkills
	pluginNames, _ := installedPluginNames(ctx, a)
	rows := make([]SkillPackageRow, 0, len(resolved))
	for _, p := range resolved {
		targets := a.restoreTargetsFor(cfg, p)
		inventory, err := service.Inventory(ctx, p.Source, targets)
		if err != nil {
			rows = append(rows, SkillPackageRow{
				Source: p.Source,
				Name:   packageDisplayName(p.Source),
				Ref:    p.Ref,
				Groups: p.Groups,
				Agents: append([]string(nil), p.Agents...),
				Error:  err.Error(),
			})
			continue
		}
		repoName := skillPackageRepoName(p.Source)
		shadowed := false
		for _, id := range targets {
			if pluginShadowsName(pluginNames, id, repoName) {
				shadowed = true
			}
		}
		rows = append(rows, SkillPackageRow{
			Source:           p.Source,
			Name:             packageDisplayName(p.Source),
			Ref:              p.Ref,
			Groups:           p.Groups,
			Agents:           append([]string(nil), p.Agents...),
			Skills:           inventory.Skills,
			Updated:          inventory.Updated,
			Installed:        inventory.Installed,
			Outdated:         skillOutdatedFrom(inventory.Outdated),
			PerAgentStatus:   skillPerAgentStatus(inventory),
			Description:      inventory.Description,
			ShadowedByPlugin: shadowed,
			UnknownAgents:    inventory.UnknownTargets,
		})
	}
	return rows, nil
}

// UnmanagedSkillPackages — Packages an installed plugin provides are suppressed: claiming them would create a duplicate.
func (a *App) UnmanagedSkillPackages(ctx context.Context) ([]SkillPackageRow, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	lock, err := config.LoadSkillLock(config.SkillLockPath(home))
	if err != nil {
		return nil, nil
	}
	sources := a.unmanagedLockSources(cfg, lock)
	installedAgents := a.installedAgents(home)
	// display-only builder: shadow warnings surface via RestoreSkills
	pluginNames, _ := installedPluginNames(ctx, a)
	rows := make([]SkillPackageRow, 0, len(sources))
	for _, src := range sources {
		names := a.packageSkills(lock, src)
		if _, shadowed := pluginProvidingLockPackage(home, src, names, installedAgents, pluginNames); shadowed {
			continue
		}
		_, updated := a.packageLockStatus(lock, src)
		perAgent := make(map[string]SkillStatus, len(installedAgents))
		for _, ag := range installedAgents {
			perAgent[ag.ID] = SkillStatusMissing
			if agentHasAnySkill(home, ag, names) {
				perAgent[ag.ID] = SkillStatusInstalled
			}
		}
		rows = append(rows, SkillPackageRow{
			Source:         src,
			Name:           packageDisplayName(src),
			Skills:         names,
			Updated:        updated,
			Installed:      true,
			PerAgentStatus: perAgent,
			Description:    singleSkillDescription(home, names),
		})
	}
	return rows, nil
}

func (a *App) unmanagedLockSources(cfg *config.RootConfig, lock *config.SkillLockFile) []string {
	manifestSources := make(map[string]struct{}, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		manifestSources[a.skillSourceIdentity(p.Source)] = struct{}{}
	}
	lockSources := make(map[string]struct{})
	for _, e := range lock.Skills {
		lockSources[a.skillSourceIdentity(e.Source)] = struct{}{}
	}
	var sources []string
	for src := range lockSources {
		if _, managed := manifestSources[src]; !managed {
			sources = append(sources, src)
		}
	}
	sort.Strings(sources)
	return sources
}

func skillUpdatedDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
