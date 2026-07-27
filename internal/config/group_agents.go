package config

import "strings"

// ExpandGroupAgentRefs — Explicit entries are preserved; expanded values dedupe while keeping first-seen order.
func ExpandGroupAgentRefs(cfg *RootConfig) bool {
	if cfg == nil {
		return false
	}
	changed := false
	packageSources := agentPackageSources(cfg)
	mcpNames := agentMcpServerNames(cfg)
	pluginNames := agentPluginNames(cfg)
	marketplaceNames := agentMarketplaceNames(cfg)
	for _, g := range cfg.Groups {
		if g == nil {
			continue
		}
		if next, ok := expandAgentRefList(g.Skills, AgentRefPackages, packageSources); ok {
			g.Skills = next
			changed = true
		}
		if next, ok := expandAgentRefList(g.McpServers, AgentRefMcpServers, mcpNames); ok {
			g.McpServers = next
			changed = true
		}
		if next, ok := expandAgentRefList(g.Plugins, AgentRefPlugins, pluginNames); ok {
			g.Plugins = next
			changed = true
		}
		if next, ok := expandAgentRefList(g.Marketplaces, AgentRefMarketplaces, marketplaceNames); ok {
			g.Marketplaces = next
			changed = true
		}
	}
	return changed
}

func expandAgentRefList(entries []string, ref string, expanded []string) ([]string, bool) {
	if len(entries) == 0 {
		return entries, false
	}
	hasRef := false
	for _, entry := range entries {
		if entry == ref {
			hasRef = true
			break
		}
	}
	if !hasRef {
		return entries, false
	}
	seen := make(map[string]struct{}, len(entries)+len(expanded))
	out := make([]string, 0, len(entries)+len(expanded))
	appendUnique := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, entry := range entries {
		if entry == ref {
			for _, value := range expanded {
				appendUnique(value)
			}
			continue
		}
		appendUnique(entry)
	}
	return out, true
}

func agentPackageSources(cfg *RootConfig) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Agents.Packages))
	for _, pkg := range cfg.Agents.Packages {
		if src := strings.TrimSpace(pkg.Source); src != "" {
			out = append(out, src)
		}
	}
	return out
}

func agentMcpServerNames(cfg *RootConfig) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Agents.McpServers))
	for _, srv := range cfg.Agents.McpServers {
		if name := strings.TrimSpace(srv.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func agentPluginNames(cfg *RootConfig) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Agents.Plugins))
	for _, plugin := range cfg.Agents.Plugins {
		if name := strings.TrimSpace(plugin.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func agentMarketplaceNames(cfg *RootConfig) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Agents.Marketplaces))
	for _, mkt := range cfg.Agents.Marketplaces {
		if name := strings.TrimSpace(mkt.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func IsAgentRef(value string) bool {
	switch strings.TrimSpace(value) {
	case AgentRefPackages, AgentRefMcpServers, AgentRefPlugins, AgentRefMarketplaces:
		return true
	default:
		return false
	}
}
