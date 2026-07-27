package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// source must be the manifest's own spelling so a group entry and its package entry stay textually aligned.
func (a *App) setSkillGroupsInConfig(cfg *config.RootConfig, source string, groups map[string]struct{}) {
	identity := a.skillSourceIdentity(source)
	matches := func(s string) bool { return a.skillSourceIdentity(s) == identity }
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			group.Skills = slices.DeleteFunc(group.Skills, matches)
			continue
		}
		if !slices.ContainsFunc(group.Skills, matches) {
			group.Skills = append(group.Skills, source)
		}
	}
}

func (a *App) SetSkillGroups(source string, groups, createdGroups []string, activeHost string) error {
	pkg, err := a.parseSkillPackage(strings.TrimSpace(source))
	if err != nil {
		return err
	}
	identity := a.skillSourceIdentity(pkg.Source)
	targets := membershipGroupSet(groups)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireSkillsEnabled(cfg); err != nil {
			return err
		}
		storedSource := ""
		for _, p := range cfg.Agents.Packages {
			if a.skillSourceIdentity(p.Source) == identity {
				storedSource = normalizeConfiguredSkillPackage(p).Source
				break
			}
		}
		if storedSource == "" {
			return fmt.Errorf("skill package %q not found", pkg.Source)
		}
		if err := createSelectedGroupsInConfig(cfg, createdGroups, targets); err != nil {
			return err
		}
		if err := ensureMembershipTargetGroups(cfg, targets); err != nil {
			return err
		}
		if err := ensureMembershipGroupsOnHostInConfig(cfg, activeHost, targets); err != nil {
			return err
		}
		a.setSkillGroupsInConfig(cfg, storedSource, targets)
		return nil
	})
}

func (a *App) SetSkillGroupsWithState(ctx context.Context, source string, groups, createdGroups []string, activeHost string) ([]SkillPackageRow, error) {
	if err := a.SetSkillGroups(source, groups, createdGroups, activeHost); err != nil {
		return nil, err
	}
	return a.SkillPackageRows(ctx)
}

// SetSkillAgents — An empty list clears them; the package then falls back to the host agents_use default.
func (a *App) SetSkillAgents(source string, agents []string) error {
	pkg, err := a.parseSkillPackage(strings.TrimSpace(source))
	if err != nil {
		return err
	}
	identity := a.skillSourceIdentity(pkg.Source)
	return a.withConfig(func(cfg *config.RootConfig) error {
		for i := range cfg.Agents.Packages {
			if a.skillSourceIdentity(cfg.Agents.Packages[i].Source) != identity {
				continue
			}
			if len(agents) == 0 {
				cfg.Agents.Packages[i].Agents = nil
			} else {
				cfg.Agents.Packages[i].Agents = append([]string{}, agents...)
			}
			return nil
		}
		return fmt.Errorf("skill package %q not found", pkg.Source)
	})
}

func (a *App) SetSkillAgentsWithState(ctx context.Context, source string, agents []string) ([]SkillPackageRow, error) {
	if err := a.SetSkillAgents(source, agents); err != nil {
		return nil, err
	}
	return a.SkillPackageRows(ctx)
}

func mcpGroupsForName(cfg *config.RootConfig, name string) []string {
	var groups []string
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if slices.Contains(group.McpServers, name) {
			groups = append(groups, group.BaseName())
		}
	}
	return groups
}

func pluginGroupsForName(cfg *config.RootConfig, name string) []string {
	var groups []string
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if slices.Contains(group.Plugins, name) {
			groups = append(groups, group.BaseName())
		}
	}
	return groups
}

func marketplaceGroupsForName(cfg *config.RootConfig, name string) []string {
	var groups []string
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if slices.Contains(group.Marketplaces, name) {
			groups = append(groups, group.BaseName())
		}
	}
	return groups
}

func setMcpGroupsInConfig(cfg *config.RootConfig, name string, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			group.McpServers = slices.DeleteFunc(group.McpServers, func(s string) bool { return s == name })
			continue
		}
		if !slices.Contains(group.McpServers, name) {
			group.McpServers = append(group.McpServers, name)
		}
	}
}

func (a *App) SetMcpGroups(ctx context.Context, name string, groups []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("mcp server name is required")
	}
	targets := membershipGroupSet(groups)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireMcpEnabled(cfg); err != nil {
			return err
		}
		if !slices.ContainsFunc(cfg.Agents.McpServers, func(s config.McpServer) bool { return s.Name == name }) {
			return fmt.Errorf("mcp server %q not found", name)
		}
		if err := ensureMembershipTargetGroups(cfg, targets); err != nil {
			return err
		}
		setMcpGroupsInConfig(cfg, name, targets)
		return nil
	})
}

func setPluginGroupsInConfig(cfg *config.RootConfig, name string, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			group.Plugins = slices.DeleteFunc(group.Plugins, func(s string) bool { return s == name })
			continue
		}
		if !slices.Contains(group.Plugins, name) {
			group.Plugins = append(group.Plugins, name)
		}
	}
}

func (a *App) SetPluginGroups(ctx context.Context, name string, groups []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}
	targets := membershipGroupSet(groups)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requirePluginsEnabled(cfg); err != nil {
			return err
		}
		if !slices.ContainsFunc(cfg.Agents.Plugins, func(p config.Plugin) bool { return p.Name == name }) {
			return fmt.Errorf("plugin %q not found", name)
		}
		if err := ensureMembershipTargetGroups(cfg, targets); err != nil {
			return err
		}
		setPluginGroupsInConfig(cfg, name, targets)
		return nil
	})
}

func setMarketplaceGroupsInConfig(cfg *config.RootConfig, name string, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			group.Marketplaces = slices.DeleteFunc(group.Marketplaces, func(s string) bool { return s == name })
			continue
		}
		if !slices.Contains(group.Marketplaces, name) {
			group.Marketplaces = append(group.Marketplaces, name)
		}
	}
}

func (a *App) SetMarketplaceGroups(ctx context.Context, name string, groups []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("marketplace name is required")
	}
	targets := membershipGroupSet(groups)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requirePluginsEnabled(cfg); err != nil {
			return err
		}
		if !slices.ContainsFunc(cfg.Agents.Marketplaces, func(mk config.Marketplace) bool { return mk.Name == name }) {
			return fmt.Errorf("marketplace %q not found", name)
		}
		if err := ensureMembershipTargetGroups(cfg, targets); err != nil {
			return err
		}
		setMarketplaceGroupsInConfig(cfg, name, targets)
		return nil
	})
}
