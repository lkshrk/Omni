package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

func setSkillGroupsInConfig(cfg *config.RootConfig, source string, groups map[string]struct{}) {
	for _, group := range cfg.Groups {
		if group == nil {
			continue
		}
		if _, keep := groups[group.BaseName()]; !keep {
			group.Skills = slices.DeleteFunc(group.Skills, func(s string) bool { return s == source })
			continue
		}
		if !slices.Contains(group.Skills, source) {
			group.Skills = append(group.Skills, source)
		}
	}
}

// SetSkillGroups persists group membership for a package source. createdGroups
// names brand-new groups to create first; activeHost auto-assigns new groups to
// the current host (mirrors SetToolGroups).
func (a *App) SetSkillGroups(source string, groups, createdGroups []string, activeHost string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("package source is required")
	}
	targets := membershipGroupSet(groups)
	return a.withConfig(func(cfg *config.RootConfig) error {
		if err := a.requireSkillsEnabled(cfg); err != nil {
			return err
		}
		if !slices.ContainsFunc(cfg.Agents.Packages, func(p config.SkillPackage) bool { return p.Source == source }) {
			return fmt.Errorf("skill package %q not found", source)
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
		setSkillGroupsInConfig(cfg, source, targets)
		return nil
	})
}

// SetSkillGroupsWithState wraps SetSkillGroups and returns refreshed rows.
func (a *App) SetSkillGroupsWithState(ctx context.Context, source string, groups, createdGroups []string, activeHost string) ([]SkillPackageRow, error) {
	if err := a.SetSkillGroups(source, groups, createdGroups, activeHost); err != nil {
		return nil, err
	}
	return a.SkillPackageRows(ctx)
}

// SetSkillAgents sets the per-package target agents (the skills-CLI -a install
// targets) for a package source. An empty list clears them (the package then
// falls back to the host agents_use default on restore).
func (a *App) SetSkillAgents(source string, agents []string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("package source is required")
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		for i := range cfg.Agents.Packages {
			if cfg.Agents.Packages[i].Source != source {
				continue
			}
			if len(agents) == 0 {
				cfg.Agents.Packages[i].Agents = nil
			} else {
				cfg.Agents.Packages[i].Agents = append([]string{}, agents...)
			}
			return nil
		}
		return fmt.Errorf("skill package %q not found", source)
	})
}

// SetSkillAgentsWithState wraps SetSkillAgents and returns refreshed rows.
func (a *App) SetSkillAgentsWithState(ctx context.Context, source string, agents []string) ([]SkillPackageRow, error) {
	if err := a.SetSkillAgents(source, agents); err != nil {
		return nil, err
	}
	return a.SkillPackageRows(ctx)
}

// mcpGroupsForName returns the names of every group whose McpServers list
// contains name, i.e. the reverse of setMcpGroupsInConfig's per-group write —
// so McpServerRows can join group membership back onto a row the same way
// resolveSkillPackages does for skill packages.
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

// pluginGroupsForName is mcpGroupsForName's plugin twin, for PluginRows.
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

// marketplaceGroupsForName is mcpGroupsForName's marketplace twin, for MarketplaceRows.
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

// SetMcpGroups persists group membership for an MCP server, assuming the
// target groups already exist (unlike SetSkillGroups, this does not create
// groups or auto-assign them to a host).
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

// SetPluginGroups persists group membership for a plugin, assuming the target
// groups already exist (see SetMcpGroups).
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

// SetMarketplaceGroups persists group membership for a marketplace, assuming
// the target groups already exist (see SetMcpGroups).
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
		setMarketplaceGroupsInConfig(cfg, name, targets)
		return nil
	})
}
