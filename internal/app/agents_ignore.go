package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

// AgentsIgnoreSet — Maps are never nil, even when the underlying list is empty.
func (a *App) AgentsIgnoreSet(cfg *config.RootConfig) (skills, mcp, plugins, marketplaces map[string]bool) {
	toSet := func(names []string) map[string]bool {
		set := make(map[string]bool, len(names))
		for _, name := range names {
			set[name] = true
		}
		return set
	}
	return toSet(cfg.Agents.Ignore.Skills), toSet(cfg.Agents.Ignore.McpServers), toSet(cfg.Agents.Ignore.Plugins), toSet(cfg.Agents.Ignore.Marketplaces)
}

func (a *App) ToggleAgentsIgnore(_ context.Context, feature string, name string) (nowIgnored bool, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("name is required")
	}
	switch feature {
	case "skills", "mcp", "plugins", "marketplaces":
	default:
		return false, fmt.Errorf("unknown feature %q; want skills, mcp, plugins, or marketplaces", feature)
	}
	err = a.withConfig(func(cfg *config.RootConfig) error {
		list := agentsIgnoreList(cfg, feature)
		if slices.Contains(*list, name) {
			*list = slices.DeleteFunc(*list, func(s string) bool { return s == name })
			nowIgnored = false
			return nil
		}
		*list = append(*list, name)
		nowIgnored = true
		return nil
	})
	return nowIgnored, err
}

func agentsIgnoreList(cfg *config.RootConfig, feature string) *[]string {
	switch feature {
	case "skills":
		return &cfg.Agents.Ignore.Skills
	case "mcp":
		return &cfg.Agents.Ignore.McpServers
	case "plugins":
		return &cfg.Agents.Ignore.Plugins
	case "marketplaces":
		return &cfg.Agents.Ignore.Marketplaces
	default:
		return nil
	}
}
