package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/provider"
)

// ─── Profile helpers ──────────────────────────────────────────────────────────

// ActiveProfileInfo returns the name and explicit persisted group base-names of
// the active profile (determined by hostname mapping). The machine group is
// runtime-only and is not included here. Returns ("", nil, false) when no
// profile matches the current hostname.
func (a *App) ActiveProfileInfo() (name string, groups []string, ok bool) {
	cfg, err := a.loadConfig()
	if err != nil {
		return "", nil, false
	}
	hostname := currentHostname()
	profileName, found := cfg.ActiveProfile(hostname)
	if !found {
		return "", nil, false
	}
	prof, exists := cfg.Profiles[profileName]
	if !exists {
		return "", nil, false
	}
	return profileName, prof.Groups, true
}

// syncOrphansToMachineGroup discovers locally installed tools that are not
// covered by any of activeGroups and appends them to the machine group,
// creating it if necessary.
func (a *App) syncOrphansToMachineGroup(ctx context.Context, activeGroups []*config.GroupConfig) error {
	// Build reverse ecosystem map (concrete → ecosystem) so orphans are written
	// with the ecosystem provider name when the concrete PM is the active
	// delegate for an ecosystem provider. ResolvedEcosystemProviders is read-only
	// and does not require the config lock.
	revEcosystem := make(map[string]string)
	for eco, concrete := range a.ResolvedEcosystemProviders(ctx) {
		revEcosystem[concrete] = eco
	}

	covered := make(map[string]struct{})
	coveredNames := make(map[string]struct{}) // name-only dedup across providers
	for _, g := range activeGroups {
		for _, t := range g.Tools {
			coveredNames[t.Name] = struct{}{}
		}
	}

	machineGroup := currentMachineGroupName()

	return a.withConfig(func(cfg *config.RootConfig) error {
		// Also mark tools already in the machine group as covered.
		if hg := findGroupInConfig(cfg, machineGroup); hg != nil {
			for _, t := range hg.Tools {
				coveredNames[t.Name] = struct{}{}
			}
		}

		var orphans []config.ToolEntry
		// Closure always returns nil; per-provider errors are skipped intentionally so
		// orphan discovery is best-effort across available providers.
		_ = a.forEachAvailable(ctx, func(p provider.Provider) error {
			if a.registry.ImportSkipsProvider(p.Name()) {
				return nil // skip ecosystem providers whose concrete delegates are already iterated
			}
			installed, err := p.ListInstalled(ctx)
			if err != nil {
				return nil
			}
			for _, t := range installed {
				// Use ecosystem provider name in config when this concrete PM is its delegate.
				configProvider := p.Name()
				if eco, ok := revEcosystem[p.Name()]; ok {
					configProvider = eco
				}
				key := configProvider + "\x00" + t.Name
				if _, ok := covered[key]; ok {
					continue
				}
				if _, ok := coveredNames[t.Name]; ok {
					continue // already covered under a different provider name
				}
				if cfg.Tools == nil {
					cfg.Tools = make(map[string]config.ToolSpec)
				}
				spec := cfg.Tools[t.Name]
				spec.Provider = configProvider
				cfg.Tools[t.Name] = spec
				orphans = append(orphans, config.ToolEntry{Name: t.Name})
				covered[key] = struct{}{} // dedup within this run
				coveredNames[t.Name] = struct{}{}
			}
			return nil
		})

		if len(orphans) == 0 {
			return errSkipSave
		}

		hg := ensureGroupInConfig(cfg, machineGroup)
		hg.Tools = append(hg.Tools, orphans...)
		return nil
	})
}

// CheckSatisfiedGroups returns the base-names of groups that are NOT in
// activeGroupNames but whose every tool is recorded as installed in the DB.
// Empty groups are skipped.
func (a *App) CheckSatisfiedGroups(ctx context.Context, activeGroupNames []string) ([]string, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}

	// Load the entire cache once and build a lookup map to avoid N+1 DB queries.
	cached, err := a.readDB().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading tool cache: %w", err)
	}
	installedSet := make(map[string]struct{}, len(cached))
	for _, c := range cached {
		if c.Installed {
			installedSet[c.Name+"\x00"+c.Provider] = struct{}{}
			installedSet[NewToolKey(c.Name, c.Provider, c.Package).String()] = struct{}{}
		}
	}

	activeSet := make(map[string]struct{}, len(activeGroupNames))
	for _, n := range activeGroupNames {
		activeSet[n] = struct{}{}
	}

	var satisfied []string
	for _, g := range cfg.Groups {
		if _, active := activeSet[g.BaseName()]; active {
			continue
		}
		if len(g.Tools) == 0 {
			continue
		}
		tools, _ := a.resolvedToolEntries(ctx, cfg, []*config.GroupConfig{g})
		allInstalled := true
		for _, t := range tools {
			if _, ok := installedSet[resolvedToolKey(t)]; !ok {
				if _, ok := installedSet[t.Name+"\x00"+t.Provider]; ok {
					continue
				}
				allInstalled = false
				break
			}
		}
		if allInstalled {
			satisfied = append(satisfied, g.BaseName())
		}
	}
	return satisfied, nil
}

// ─── Profiles ─────────────────────────────────────────────────────────────────

// ProfileInfo bundles profile data and the currently active profile name for display.
type ProfileInfo struct {
	Profiles  map[string]config.Profile
	Hostnames map[string]string
	Active    string // "" when no hostname mapping matches
}

// ProfileStatus returns all profiles, hostname mappings, and the active profile.
func (a *App) ProfileStatus() (*ProfileInfo, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	hostname := currentHostname()
	active, _ := cfg.ActiveProfile(hostname)
	return &ProfileInfo{
		Profiles:  cfg.Profiles,
		Hostnames: cfg.Hostnames,
		Active:    active,
	}, nil
}

// AddProfile creates or replaces a profile with the given group names.
// The base group is created empty in config if absent, but is not added to the
// profile automatically; profiles opt into shared groups explicitly.
// The current machine group is also created empty in config (if
// absent) so it is ready to receive orphaned tools, but it is NOT added to
// the profile's Groups list — it is injected automatically at sync time on
// whichever machine runs the sync.
func (a *App) AddProfile(name string, groups []string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Profiles == nil {
			cfg.Profiles = make(map[string]config.Profile)
		}

		ensureGroupInConfig(cfg, "base")
		for _, group := range groups {
			ensureGroupInConfig(cfg, group)
		}

		// Pre-create the machine group empty so it exists for orphan sync, but
		// do not add it to the profile — it is machine-local and injected at
		// sync time automatically.
		ensureCurrentMachineGroupInConfig(cfg)

		cfg.Profiles[name] = config.Profile{Groups: groups}
		return nil
	})
}

// DeleteProfile removes a profile. Idempotent — returns nil if not found.
func (a *App) DeleteProfile(name string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		delete(cfg.Profiles, name)
		for hostname, profile := range cfg.Hostnames {
			if profile == name {
				delete(cfg.Hostnames, hostname)
			}
		}
		return nil
	})
}

// RenameProfile renames a profile and updates hostname mappings that point at it.
func (a *App) RenameProfile(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Profiles == nil {
			return fmt.Errorf("profile %q not found", oldName)
		}
		profile, ok := cfg.Profiles[oldName]
		if !ok {
			return fmt.Errorf("profile %q not found", oldName)
		}
		if _, exists := cfg.Profiles[newName]; exists {
			return fmt.Errorf("profile %q already exists", newName)
		}
		delete(cfg.Profiles, oldName)
		cfg.Profiles[newName] = profile
		for hostname, mapped := range cfg.Hostnames {
			if mapped == oldName {
				cfg.Hostnames[hostname] = newName
			}
		}
		return nil
	})
}

// addGroupToProfileInner appends group to profile inside a caller-held configMu.
// cfg is already loaded; the caller is responsible for saving.
func addGroupToProfileInner(cfg *config.RootConfig, profile, group string) {
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]config.Profile)
	}
	p := cfg.Profiles[profile]
	if slices.Contains(p.Groups, group) {
		return
	}
	p.Groups = append(p.Groups, group)
	cfg.Profiles[profile] = p
}

// AddGroupToProfile appends a group to a profile. Creates the profile if absent.
// Idempotent — does nothing if the group is already present.
func (a *App) AddGroupToProfile(profile, group string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		addGroupToProfileInner(cfg, profile, group)
		ensureGroupInConfig(cfg, group)
		return nil
	})
}

// RemoveGroupFromProfile removes a group from a profile. Idempotent.
func (a *App) RemoveGroupFromProfile(profile, group string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		p, ok := cfg.Profiles[profile]
		if !ok {
			return errSkipSave
		}
		var filtered []string
		for _, g := range p.Groups {
			if g != group {
				filtered = append(filtered, g)
			}
		}
		p.Groups = filtered
		cfg.Profiles[profile] = p
		return nil
	})
}

// CreateGroup adds an empty named group to config.
// Returns an error if name is empty, equals "base", or already exists.
func (a *App) CreateGroup(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if name == "base" {
		return fmt.Errorf(`"base" is reserved for the default group`)
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		for _, g := range cfg.Groups {
			if g.Name == name {
				return fmt.Errorf("group %q already exists", name)
			}
		}
		cfg.Groups = append(cfg.Groups, &config.GroupConfig{Name: name})
		return nil
	})
}

// RenameGroup renames a group in config and updates all profile references.
// The base group (empty name) cannot be renamed. Returns an error if newName
// already exists or if the group is not found.
func (a *App) RenameGroup(oldName, newName string) error {
	if oldName == "" {
		return fmt.Errorf("cannot rename the base group")
	}
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if oldName == newName {
		return nil // no-op
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		for _, g := range cfg.Groups {
			if g.Name == newName {
				return fmt.Errorf("group %q already exists", newName)
			}
		}
		found := false
		for _, g := range cfg.Groups {
			if g.Name == oldName {
				g.Name = newName
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("group %q not found", oldName)
		}
		// Update every profile that references the old name.
		for pname, p := range cfg.Profiles {
			for i, gname := range p.Groups {
				if gname == oldName {
					p.Groups[i] = newName
				}
			}
			cfg.Profiles[pname] = p
		}
		return nil
	})
}

type DeleteGroupOptions struct {
	MoveTo      string
	DeleteTools bool
}

// DeleteGroup removes a named group from config. Tools that still belong to
// another group only lose the deleted membership. Tools that would lose their
// last membership are moved or deleted according to opts.
func (a *App) DeleteGroup(ctx context.Context, name string, opts DeleteGroupOptions) error {
	if name == "" || name == "base" {
		return fmt.Errorf("cannot delete the base group")
	}
	if opts.MoveTo != "" && opts.DeleteTools {
		return fmt.Errorf("choose either MoveTo or DeleteTools, not both")
	}
	if opts.MoveTo == name {
		return fmt.Errorf("move target must differ from deleted group %q", name)
	}

	var deletedLogicalTools []string
	if err := a.withConfig(func(cfg *config.RootConfig) error {
		membershipCounts := make(map[string]int)
		for _, g := range cfg.Groups {
			if g.Name == name {
				continue
			}
			seen := make(map[string]struct{}, len(g.Tools))
			for _, tool := range g.Tools {
				if tool.Name == "" {
					continue
				}
				if _, ok := seen[tool.Name]; ok {
					continue
				}
				seen[tool.Name] = struct{}{}
				membershipCounts[tool.Name]++
			}
		}

		var lastMembershipTools []config.ToolEntry
		var movedDots []config.DotEntry
		found := false
		newGroups := make([]*config.GroupConfig, 0, len(cfg.Groups))
		for _, g := range cfg.Groups {
			if g.Name == name {
				found = true
				for _, tool := range g.Tools {
					if membershipCounts[tool.Name] == 0 {
						lastMembershipTools = append(lastMembershipTools, config.ToolEntry{Name: tool.Name})
					}
				}
				movedDots = append(movedDots, g.Dots...)
				continue // drop from slice
			}
			newGroups = append(newGroups, g)
		}
		if !found {
			return errSkipSave
		}
		if len(lastMembershipTools) > 0 && opts.MoveTo == "" && !opts.DeleteTools {
			return fmt.Errorf("group %q contains tools with no other membership; provide MoveTo or DeleteTools", name)
		}
		if len(movedDots) > 0 && opts.MoveTo == "" {
			return fmt.Errorf("group %q contains dots; provide MoveTo", name)
		}
		cfg.Groups = newGroups

		if opts.DeleteTools {
			for _, tool := range lastMembershipTools {
				delete(cfg.Tools, tool.Name)
				deletedLogicalTools = append(deletedLogicalTools, tool.Name)
				for _, g := range cfg.Groups {
					filterGroupIgnore(g, tool.Name)
				}
			}
		} else if len(lastMembershipTools) > 0 {
			dst := ensureGroupInConfig(cfg, opts.MoveTo)
			for _, tool := range lastMembershipTools {
				if !containsToolMembership(dst.Tools, tool.Name) {
					dst.Tools = append(dst.Tools, tool)
				}
			}
		}
		if len(movedDots) > 0 {
			dst := ensureGroupInConfig(cfg, opts.MoveTo)
			dst.Dots = append(dst.Dots, movedDots...)
		}
		// Remove from all profile references.
		for pname, p := range cfg.Profiles {
			var filtered []string
			for _, gname := range p.Groups {
				if gname != name {
					filtered = append(filtered, gname)
				}
			}
			p.Groups = filtered
			cfg.Profiles[pname] = p
		}
		return nil
	}); err != nil {
		return err
	}
	return a.deleteCachedLogicalTools(ctx, deletedLogicalTools)
}

// ClaimFromMachineGroup adds groupName to the profile and removes from the
// current machine group any tools that are now covered by that group. Use this
// instead of AddGroupToProfile when accepting a satisfied group so the machine
// inbox stays clean.
func (a *App) ClaimFromMachineGroup(profileName, groupName string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		// Add groupName to profile (inline to avoid re-locking).
		addGroupToProfileInner(cfg, profileName, groupName)

		claimedGroup := findGroupInConfig(cfg, groupName)
		if claimedGroup == nil || len(claimedGroup.Tools) == 0 {
			return nil
		}
		covered := make(map[string]struct{}, len(claimedGroup.Tools))
		for _, t := range claimedGroup.Tools {
			covered[t.Name] = struct{}{}
		}
		hg := findGroupInConfig(cfg, currentMachineGroupName())
		if hg == nil {
			return nil
		}
		var remaining []config.ToolEntry
		for _, t := range hg.Tools {
			if _, ok := covered[t.Name]; !ok {
				remaining = append(remaining, t)
			}
		}
		if len(remaining) != len(hg.Tools) {
			hg.Tools = remaining
		}
		return nil
	})
}

// ClaimFromHostnameGroup is kept for callers that have not yet moved to the
// machine-group naming. New app code should call ClaimFromMachineGroup.
func (a *App) ClaimFromHostnameGroup(profileName, groupName string) error {
	return a.ClaimFromMachineGroup(profileName, groupName)
}

// SetHostname maps a hostname to a profile in settings.json.
func (a *App) SetHostname(hostname, profile string) error {
	hostname = strings.TrimSpace(hostname)
	profile = strings.TrimSpace(profile)
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if profile == "" {
		return fmt.Errorf("profile is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Hostnames == nil {
			cfg.Hostnames = make(map[string]string)
		}
		if cfg.Profiles == nil {
			cfg.Profiles = make(map[string]config.Profile)
		}
		if _, ok := cfg.Profiles[profile]; !ok {
			cfg.Profiles[profile] = config.Profile{Groups: []string{}}
		}
		cfg.Hostnames[hostname] = profile
		return nil
	})
}

// RequireActiveProfile returns an error when no profile is mapped to the current
// machine's hostname.
func (a *App) RequireActiveProfile() error {
	cfg, err := a.loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	hostname := currentHostname()
	if _, ok := cfg.ActiveProfile(hostname); !ok {
		return fmt.Errorf("no active profile for this machine — run 'omni init' to set one up")
	}
	return nil
}

// AddIgnoreToProfile appends a tool name to the profile's ignore list.
// Idempotent — does nothing when the tool is already ignored.
func (a *App) AddIgnoreToProfile(profile, tool string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Profiles == nil {
			return fmt.Errorf("profile %q not found", profile)
		}
		p, ok := cfg.Profiles[profile]
		if !ok {
			return fmt.Errorf("profile %q not found", profile)
		}
		if slices.Contains(p.Ignore, tool) {
			return errSkipSave
		}
		p.Ignore = append(p.Ignore, tool)
		cfg.Profiles[profile] = p
		return nil
	})
}

// RemoveIgnoreFromProfile removes a tool name from the profile's ignore list. Idempotent.
func (a *App) RemoveIgnoreFromProfile(profile, tool string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		p, ok := cfg.Profiles[profile]
		if !ok {
			return errSkipSave
		}
		var filtered []string
		for _, t := range p.Ignore {
			if t != tool {
				filtered = append(filtered, t)
			}
		}
		p.Ignore = filtered
		cfg.Profiles[profile] = p
		return nil
	})
}

// RemoveHostname removes a hostname mapping. Idempotent.
func (a *App) RemoveHostname(hostname string) error {
	return a.withConfig(func(cfg *config.RootConfig) error {
		delete(cfg.Hostnames, hostname)
		return nil
	})
}
