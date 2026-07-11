package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// SkillPackageRow is a display row for the agents packages table.
type SkillPackageRow struct {
	Source         string
	Name           string // repo segment of Source
	Ref            string
	Groups         []string // active group memberships (badge)
	Agents         []string // configured target agents (the -a install targets)
	Skills         []string // individual skill names in this package (from the lockfile)
	Updated        string   // YYYY-MM-DD, latest across the package's lockfile skills
	Installed      bool
	PerAgentStatus map[string]bool // agentID -> installed on disk
	Description    string          // from the single skill's SKILL.md frontmatter, only when len(Skills) == 1
	// ShadowedByPlugin is true when an installed plugin's name matches this
	// package's repo-segment name on an agent where the package is actually
	// present (see installedPluginNames/skillPackageRepoName). Set only for
	// managed rows (SkillPackageRows) — unmanaged rows with this property are
	// suppressed entirely by UnmanagedSkillPackages rather than flagged.
	ShadowedByPlugin bool
}

// singleSkillDescription returns the SKILL.md description for names[0] when
// the package contains exactly one skill; merging descriptions across
// multiple skills is out of scope.
func singleSkillDescription(home string, names []string) string {
	if len(names) != 1 {
		return ""
	}
	return skillDescription(home, names[0])
}

// packageSkills returns the lockfile skill names belonging to source, sorted.
func packageSkills(lock *config.SkillLockFile, source string) []string {
	var names []string
	for name, e := range lock.Skills {
		if e.Source == source {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// packageLockStatus reports whether any lockfile entry belongs to source and
// the latest updated date (YYYY-MM-DD) across those entries.
func packageLockStatus(lock *config.SkillLockFile, source string) (bool, string) {
	installed := false
	latest := ""
	for _, e := range lock.Skills {
		if e.Source != source {
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

// packageDisplayName is the full owner/repo source — the unambiguous package
// identity (two repos named ".../skills" would collide on the repo segment).
func packageDisplayName(source string) string {
	return source
}

// skillPackageRepoName extracts the last path segment of a package source
// (e.g. "owner/academic-research-skills" -> "academic-research-skills"), used
// only to compare a package's identity against installed plugin names: a
// plugin's Name has no owner prefix, so matching must strip Source down to
// its bare repo segment rather than comparing the full owner/repo string
// packageDisplayName returns.
func skillPackageRepoName(source string) string {
	if i := strings.LastIndexByte(source, '/'); i >= 0 {
		return source[i+1:]
	}
	return source
}

// SkillAgentRow describes one installed agent for a package's agents picker:
// Targeted = chosen as an install target (-a), Installed = physically present
// in the agent's skills dir on disk.
type SkillAgentRow struct {
	ID        string
	Display   string
	Targeted  bool
	Installed bool
}

func agentHasAnySkill(home string, a AgentInfo, names []string) bool {
	for _, dir := range agentSkillsDirs(home, a) {
		for _, name := range names {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
	}
	return false
}

// SkillAgentRows returns, for a package source, every installed agent with
// whether it is a configured install target and whether the package's skills
// are physically present in that agent's skills dir.
func (a *App) SkillAgentRows(source string) ([]SkillAgentRow, error) {
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
		return nil, err
	}
	targeted := map[string]bool{}
	for _, p := range cfg.Agents.Packages {
		if p.Source == source {
			for _, id := range p.Agents {
				targeted[id] = true
			}
			break
		}
	}
	names := packageSkills(lock, source)
	rows := make([]SkillAgentRow, 0)
	for _, ag := range InstalledAgents(home) {
		rows = append(rows, SkillAgentRow{
			ID:        ag.ID,
			Display:   ag.Display,
			Targeted:  targeted[ag.ID],
			Installed: agentHasAnySkill(home, ag, names),
		})
	}
	return rows, nil
}

// SkillPackageRows builds the agents table: packages resolved for this host,
// joined with the lockfile for install status. Packages whose repo-segment
// name matches an installed plugin's name on a targeted agent are flagged via
// ShadowedByPlugin (see installedPluginNames/skillPackageRepoName) but never
// hidden — a manifest entry is declared intent.
func (a *App) SkillPackageRows(ctx context.Context) ([]SkillPackageRow, error) {
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
		return nil, err
	}
	resolved := resolveSkillPackages(cfg, currentMachineGroupName())
	pluginNames := installedPluginNames(ctx, a)
	rows := make([]SkillPackageRow, 0, len(resolved))
	for _, p := range resolved {
		installed, updated := packageLockStatus(lock, p.Source)
		names := packageSkills(lock, p.Source)
		targets := p.Agents
		if len(targets) == 0 {
			targets = a.EnabledAgentIDs(cfg)
		}
		repoName := skillPackageRepoName(p.Source)
		perAgent := make(map[string]bool, len(targets))
		shadowed := false
		for _, id := range targets {
			ag, ok := agentInfoByID(home, id)
			if !ok {
				perAgent[id] = false
				continue
			}
			perAgent[id] = agentHasAnySkill(home, ag, names)
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
			Skills:           names,
			Updated:          updated,
			Installed:        installed,
			PerAgentStatus:   perAgent,
			Description:      singleSkillDescription(home, names),
			ShadowedByPlugin: shadowed,
		})
	}
	return rows, nil
}

// UnmanagedSkillPackages returns lockfile packages absent from the manifest:
// installed on disk (per ~/.agents/.skill-lock.json) but not tracked by any
// config.SkillPackage entry. Packages whose repo-segment name matches an
// installed plugin's name on an agent where the package is actually present
// are suppressed — the plugin manages them (see installedPluginNames), so
// offering to claim them into the manifest would create a duplicate.
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
		return nil, err
	}
	manifestSources := make(map[string]struct{}, len(cfg.Agents.Packages))
	for _, p := range cfg.Agents.Packages {
		manifestSources[p.Source] = struct{}{}
	}
	lockSources := make(map[string]struct{})
	for _, e := range lock.Skills {
		lockSources[e.Source] = struct{}{}
	}
	var sources []string
	for src := range lockSources {
		if _, managed := manifestSources[src]; !managed {
			sources = append(sources, src)
		}
	}
	sort.Strings(sources)
	installedAgents := InstalledAgents(home)
	pluginNames := installedPluginNames(ctx, a)
	rows := make([]SkillPackageRow, 0, len(sources))
	for _, src := range sources {
		names := packageSkills(lock, src)
		repoName := skillPackageRepoName(src)
		shadowed := false
		for _, ag := range installedAgents {
			if agentHasAnySkill(home, ag, names) && pluginShadowsName(pluginNames, ag.ID, repoName) {
				shadowed = true
				break
			}
		}
		if shadowed {
			continue
		}
		_, updated := packageLockStatus(lock, src)
		perAgent := make(map[string]bool, len(installedAgents))
		for _, ag := range installedAgents {
			perAgent[ag.ID] = agentHasAnySkill(home, ag, names)
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

// skillUpdatedDate trims an ISO timestamp to its date component.
func skillUpdatedDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
